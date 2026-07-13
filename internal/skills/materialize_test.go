package skills

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCatalogBodiesEmbed(t *testing.T) {
	for _, sk := range Catalog {
		if strings.TrimSpace(sk.Body()) == "" {
			t.Errorf("skill %q has empty body", sk.Name)
		}
		if sk.Name == "" || sk.Description == "" {
			t.Errorf("skill %q missing name or description", sk.Name)
		}
	}
}

func TestMaterializeCC(t *testing.T) {
	home := t.TempDir()
	res, err := Materialize(BackendCC, home)
	if err != nil {
		t.Fatalf("Materialize(cc): %v", err)
	}
	if len(res.Written) != len(Catalog) {
		t.Fatalf("wrote %d skills, want %d", len(res.Written), len(Catalog))
	}

	for _, sk := range Catalog {
		md := filepath.Join(home, ".claude", "skills", sk.Name, "SKILL.md")
		data, err := os.ReadFile(md)
		if err != nil {
			t.Fatalf("read %s: %v", md, err)
		}
		text := string(data)
		if !strings.HasPrefix(text, "---\n") {
			t.Errorf("%s: missing frontmatter open", sk.Name)
		}
		if !strings.Contains(text, "name: "+sk.Name) {
			t.Errorf("%s: frontmatter missing name", sk.Name)
		}
		if !strings.Contains(text, "description:") {
			t.Errorf("%s: frontmatter missing description", sk.Name)
		}
		// Claude backend must NOT emit Codex's openai.yaml.
		if _, err := os.Stat(filepath.Join(home, ".claude", "skills", sk.Name, "agents", "openai.yaml")); err == nil {
			t.Errorf("%s: cc backend should not write agents/openai.yaml", sk.Name)
		}
	}
}

func TestMaterializeCodexEmitsOpenAIYAML(t *testing.T) {
	home := t.TempDir()
	if _, err := Materialize(BackendCodex, home); err != nil {
		t.Fatalf("Materialize(codex): %v", err)
	}

	// migrate-to-playwright declares the qmax MCP dep → expect openai.yaml.
	yaml := filepath.Join(home, ".codex", "skills", "migrate-to-playwright", "agents", "openai.yaml")
	data, err := os.ReadFile(yaml)
	if err != nil {
		t.Fatalf("read %s: %v", yaml, err)
	}
	text := string(data)
	if !strings.Contains(text, `value: "qmax"`) {
		t.Errorf("openai.yaml missing qmax MCP dependency:\n%s", text)
	}
	if !strings.Contains(text, "allow_implicit_invocation: true") {
		t.Errorf("openai.yaml missing invocation policy:\n%s", text)
	}

	// Codex SKILL.md should carry the short-description metadata, not allowed-tools.
	md, err := os.ReadFile(filepath.Join(home, ".codex", "skills", "migrate-to-playwright", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "short-description:") {
		t.Errorf("codex SKILL.md missing short-description metadata")
	}
	if strings.Contains(string(md), "allowed-tools:") {
		t.Errorf("codex SKILL.md should not carry allowed-tools")
	}
}

func TestMaterializeOpenCode(t *testing.T) {
	home := t.TempDir()
	res, err := Materialize(BackendOpenCode, home)
	if err != nil {
		t.Fatalf("Materialize(opencode): %v", err)
	}
	if len(res.Written) != len(Catalog) {
		t.Fatalf("wrote %d skills, want %d", len(res.Written), len(Catalog))
	}

	for _, sk := range Catalog {
		md := filepath.Join(home, ".config", "opencode", "skills", sk.Name, "SKILL.md")
		data, err := os.ReadFile(md)
		if err != nil {
			t.Fatalf("read %s: %v", md, err)
		}
		text := string(data)
		if !strings.HasPrefix(text, "---\n") {
			t.Errorf("%s: missing frontmatter open", sk.Name)
		}
		if !strings.Contains(text, "name: "+sk.Name) {
			t.Errorf("%s: frontmatter missing name", sk.Name)
		}
		if !strings.Contains(text, "description:") {
			t.Errorf("%s: frontmatter missing description", sk.Name)
		}
		// opencode ignores allowed-tools; the opencode render omits it so the
		// frontmatter stays within opencode's recognized schema.
		if strings.Contains(text, "allowed-tools:") {
			t.Errorf("%s: opencode SKILL.md should not carry allowed-tools", sk.Name)
		}
		// opencode must NOT get Codex's openai.yaml sibling.
		if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", sk.Name, "agents", "openai.yaml")); err == nil {
			t.Errorf("%s: opencode backend should not write agents/openai.yaml", sk.Name)
		}
	}
}

// opencode enforces a stricter skill-name regex (^[a-z0-9]+(-[a-z0-9]+)*$ — no
// underscores). Every catalog name must satisfy it so skills load in opencode.
var opencodeNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestCatalogNamesAreOpenCodeCompatible(t *testing.T) {
	for _, sk := range Catalog {
		if !opencodeNameRe.MatchString(sk.Name) {
			t.Errorf("catalog skill name %q is not opencode-compatible (underscores/segments rejected)", sk.Name)
		}
	}
}

func TestMaterializeIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := Materialize(BackendCodex, home); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(home, ".codex", "skills", "qa-triage", "SKILL.md")
	first, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Materialize(BackendCodex, home); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("re-materializing changed the SKILL.md output")
	}
}

func TestMaterializeSetsDeterministicPerms(t *testing.T) {
	home := t.TempDir()
	if _, err := Materialize(BackendCodex, home); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".codex", "skills", "migrate-to-playwright")
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != dirPerm {
		t.Errorf("dir perms = %o, want %o", di.Mode().Perm(), dirPerm)
	}
	fi, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != filePerm {
		t.Errorf("file perms = %o, want %o", fi.Mode().Perm(), filePerm)
	}
}

func TestMaterializeRejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	evil := t.TempDir()
	// Plant ~/.codex/skills as a symlink pointing outside home.
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evil, filepath.Join(home, ".codex", "skills")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := Materialize(BackendCodex, home); err == nil {
		t.Fatal("expected error materializing into a symlink that escapes home")
	}
	// And it must not have written any skill files into the escape target.
	if _, err := os.Stat(filepath.Join(evil, "migrate-to-playwright", "SKILL.md")); err == nil {
		t.Error("skill files were written outside home via symlink")
	}
}

func TestYAMLScalarQuoting(t *testing.T) {
	cases := map[string]string{
		"simple":          "simple",
		"has: colon":      `"has: colon"`,
		"trailing space ": `"trailing space "`,
		// A backslash is literal in a plain scalar → stays unquoted.
		`a\b`: `a\b`,
		// Control characters force quoting and get escaped.
		"tab\there":  `"tab\there"`,
		"cr\rhere":   `"cr\rhere"`,
		"nul\x00bye": `"nul\x00bye"`,
		"esc\x1bseq": `"esc\x1bseq"`,
	}
	for in, want := range cases {
		if got := yamlScalar(in); got != want {
			t.Errorf("yamlScalar(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestYAMLQuotedEscapesBackslash(t *testing.T) {
	if got := yamlQuoted(`a"b\c`); got != `"a\"b\\c"` {
		t.Errorf("yamlQuoted = %q", got)
	}
}

func TestCatalogNamesAreValid(t *testing.T) {
	for _, sk := range Catalog {
		if !skillNameRe.MatchString(sk.Name) {
			t.Errorf("catalog skill name %q does not satisfy %s", sk.Name, skillNameRe)
		}
	}
}

// sast-presurgery has a ShortDescription but no MCPDeps: Codex should still get
// an openai.yaml (for the UI blurb) but without a dependencies section.
func TestMaterializeCodexShortDescWithoutMCPDeps(t *testing.T) {
	home := t.TempDir()
	if _, err := Materialize(BackendCodex, home); err != nil {
		t.Fatal(err)
	}
	yaml := filepath.Join(home, ".codex", "skills", "sast-presurgery", "agents", "openai.yaml")
	data, err := os.ReadFile(yaml)
	if err != nil {
		t.Fatalf("expected openai.yaml for short-desc-only skill: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "short_description:") {
		t.Errorf("missing short_description:\n%s", text)
	}
	if strings.Contains(text, "dependencies:") {
		t.Errorf("should not emit dependencies section when MCPDeps empty:\n%s", text)
	}
}
