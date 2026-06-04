package skills

import (
	"os"
	"path/filepath"
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

func TestYAMLScalarQuoting(t *testing.T) {
	cases := map[string]string{
		"simple":          "simple",
		"has: colon":      `"has: colon"`,
		"trailing space ": `"trailing space "`,
	}
	for in, want := range cases {
		if got := yamlScalar(in); got != want {
			t.Errorf("yamlScalar(%q) = %q, want %q", in, got, want)
		}
	}
}
