package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// skillNameRe constrains a skill slug to a single path segment of lowercase
// alphanumerics, hyphens, and underscores. Since Name becomes a directory path
// under the user's home, this guards against traversal ("../") or hidden-dir
// ("." prefix) names — defense for the day the catalog is sourced externally.
var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Backend identifies which CLI's native skill format to render.
type Backend string

const (
	BackendCC    Backend = "cc"
	BackendCodex Backend = "codex"
)

// MaterializeResult reports what Materialize wrote, for display to the user.
type MaterializeResult struct {
	Backend Backend
	Dir     string   // root skills directory written into
	Written []string // skill names written or updated
}

// SkillsDir returns the user-level skills directory for a backend under home,
// e.g. ~/.claude/skills or ~/.codex/skills. home is taken as a parameter so the
// caller (and tests) control it.
func SkillsDir(backend Backend, home string) (string, error) {
	switch backend {
	case BackendCC:
		return filepath.Join(home, ".claude", "skills"), nil
	case BackendCodex:
		return filepath.Join(home, ".codex", "skills"), nil
	default:
		return "", fmt.Errorf("skills: unknown backend %q", backend)
	}
}

// Materialize writes every catalog skill into the backend's user-level skills
// directory under home, rendering the SKILL.md (and, for Codex, the
// agents/openai.yaml) appropriate to that backend. Existing qmax skill folders
// are overwritten so the catalog stays authoritative; unrelated skills the user
// installed themselves are left untouched.
func Materialize(backend Backend, home string) (*MaterializeResult, error) {
	root, err := SkillsDir(backend, home)
	if err != nil {
		return nil, err
	}
	if err := mkdirStrict(root); err != nil {
		return nil, err
	}
	// Guard against a pre-planted symlink at ~/.claude or ~/.codex redirecting
	// writes outside the user's home. Done after MkdirAll so the path exists to
	// resolve, but before any skill files are written.
	if err := ensureWithinHome(root, home); err != nil {
		return nil, err
	}

	res := &MaterializeResult{Backend: backend, Dir: root}
	for _, sk := range SortedCatalog() {
		if !skillNameRe.MatchString(sk.Name) {
			return nil, fmt.Errorf("skills: invalid skill name %q (must match %s)", sk.Name, skillNameRe)
		}
		if err := writeSkill(backend, root, sk); err != nil {
			return nil, fmt.Errorf("write skill %q: %w", sk.Name, err)
		}
		res.Written = append(res.Written, sk.Name)
	}
	return res, nil
}

// writeSkill renders one skill folder for the given backend.
func writeSkill(backend Backend, root string, sk Skill) error {
	dir := filepath.Join(root, sk.Name)
	if err := mkdirStrict(dir); err != nil {
		return err
	}

	if err := writeFileStrict(filepath.Join(dir, "SKILL.md"), []byte(renderSkillMD(backend, sk))); err != nil {
		return err
	}

	// Codex consumes optional per-skill config from agents/openai.yaml: UI
	// metadata and MCP dependencies. Only emit it when there is something to say.
	if backend == BackendCodex && (len(sk.MCPDeps) > 0 || sk.ShortDescription != "") {
		agentsDir := filepath.Join(dir, "agents")
		if err := mkdirStrict(agentsDir); err != nil {
			return err
		}
		if err := writeFileStrict(filepath.Join(agentsDir, "openai.yaml"), []byte(renderOpenAIYAML(sk))); err != nil {
			return err
		}
	}
	return nil
}

// Skill files hold no secrets, but they live among the user's CLI config, so we
// keep them owner-only and deterministic regardless of the ambient umask.
const (
	dirPerm  os.FileMode = 0700
	filePerm os.FileMode = 0600
)

// mkdirStrict creates dir (and parents) then chmods the leaf so umask can't
// leave it more permissive than dirPerm.
func mkdirStrict(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	return os.Chmod(dir, dirPerm)
}

// writeFileStrict writes data then chmods the file to filePerm, so the result
// is the same whatever the umask was.
func writeFileStrict(path string, data []byte) error {
	if err := os.WriteFile(path, data, filePerm); err != nil {
		return err
	}
	return os.Chmod(path, filePerm)
}

// ensureWithinHome resolves symlinks on root and home and verifies root is
// contained in home, so a redirected ~/.claude or ~/.codex can't escape.
func ensureWithinHome(root, home string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realHome, realRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("skills: resolved dir %q escapes home %q", realRoot, realHome)
	}
	return nil
}

// renderSkillMD builds the SKILL.md contents for a backend. The frontmatter core
// (name, description) is identical across both; only the optional enrichment
// differs.
func renderSkillMD(backend Backend, sk Skill) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + yamlScalar(sk.Name) + "\n")
	b.WriteString("description: " + yamlScalar(sk.Description) + "\n")

	switch backend {
	case BackendCC:
		// Claude Code: gate tools via allowed-tools when the skill declares them.
		if len(sk.AllowedTools) > 0 {
			b.WriteString("allowed-tools:\n")
			for _, t := range sk.AllowedTools {
				b.WriteString("  - " + yamlScalar(t) + "\n")
			}
		}
	case BackendCodex:
		// Codex: surface a short description via metadata; richer config lives in
		// agents/openai.yaml.
		if sk.ShortDescription != "" {
			b.WriteString("metadata:\n")
			b.WriteString("  short-description: " + yamlScalar(sk.ShortDescription) + "\n")
		}
	default:
		// Materialize validates the backend via SkillsDir before reaching here,
		// so an unknown value is a programming error, not a runtime condition.
		panic(fmt.Sprintf("skills: unknown backend %q", backend))
	}

	b.WriteString("---\n\n")
	b.WriteString(sk.Body())
	if !strings.HasSuffix(sk.Body(), "\n") {
		b.WriteString("\n")
	}
	// Provenance footer so a reader (or a future orch run) can tell these folders
	// are catalog-managed and will be overwritten.
	b.WriteString("\n<!-- Managed by qmax-code orch. Edit the catalog, not this file. -->\n")
	return b.String()
}

// renderOpenAIYAML builds the Codex agents/openai.yaml for a skill from its
// short description and MCP dependencies.
func renderOpenAIYAML(sk Skill) string {
	var b strings.Builder
	b.WriteString("# Managed by qmax-code orch. Edit the catalog, not this file.\n")
	if sk.ShortDescription != "" {
		b.WriteString("interface:\n")
		b.WriteString("  short_description: " + yamlQuoted(sk.ShortDescription) + "\n")
	}
	if len(sk.MCPDeps) > 0 {
		b.WriteString("dependencies:\n")
		b.WriteString("  tools:\n")
		for _, d := range sk.MCPDeps {
			b.WriteString("    - type: \"mcp\"\n")
			b.WriteString("      value: " + yamlQuoted(d.Value) + "\n")
			b.WriteString("      description: " + yamlQuoted(d.Description) + "\n")
		}
	}
	b.WriteString("policy:\n")
	b.WriteString("  allow_implicit_invocation: true\n")
	return b.String()
}

// yamlScalar renders a string as a YAML scalar, quoting only when needed so the
// common case stays readable. Used for unquoted-by-default frontmatter keys.
//
// A backslash in a plain scalar is a literal (YAML only escapes inside double
// quotes), so it does not force quoting — but control characters do, since they
// can corrupt the frontmatter. This hardening matters most when the catalog is
// ever fed from an external source rather than the in-repo definitions.
func yamlScalar(s string) string {
	if s == "" || strings.ContainsAny(s, ":#\"'\n") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") || hasControlChar(s) {
		return yamlQuoted(s)
	}
	return s
}

// hasControlChar reports whether s contains any C0 control character or DEL.
func hasControlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// yamlQuoted renders a double-quoted YAML string, escaping the metacharacters
// and control characters that are illegal or ambiguous inside double quotes.
func yamlQuoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
