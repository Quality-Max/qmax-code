package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}

	res := &MaterializeResult{Backend: backend, Dir: root}
	for _, sk := range SortedCatalog() {
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
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(renderSkillMD(backend, sk)), 0600); err != nil {
		return err
	}

	// Codex consumes optional per-skill config from agents/openai.yaml: UI
	// metadata and MCP dependencies. Only emit it when there is something to say.
	if backend == BackendCodex && (len(sk.MCPDeps) > 0 || sk.ShortDescription != "") {
		agentsDir := filepath.Join(dir, "agents")
		if err := os.MkdirAll(agentsDir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(agentsDir, "openai.yaml"), []byte(renderOpenAIYAML(sk)), 0600); err != nil {
			return err
		}
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
func yamlScalar(s string) string {
	if s == "" || strings.ContainsAny(s, ":#\"'\n") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return yamlQuoted(s)
	}
	return s
}

// yamlQuoted renders a double-quoted YAML string with the minimal escaping YAML
// requires inside double quotes.
func yamlQuoted(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}
