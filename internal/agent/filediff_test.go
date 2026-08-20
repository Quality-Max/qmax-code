package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsFileEditTool(t *testing.T) {
	cases := map[string]bool{
		"edit_file":          true,
		"write_file":         true,
		"Edit":               true, // Claude Code
		"Write":              true,
		"NotebookEdit":       true,
		"qmax__edit_file":    true, // mcp-prefixed
		"opencode.edit":      true,
		"applypatch":         true,
		"read_file":          false,
		"bash":               false,
		"update_plan":        false,
		"webfetch":           false,
	}
	for name, want := range cases {
		if got := isFileEditTool(name); got != want {
			t.Errorf("isFileEditTool(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestToolPathFieldVariants(t *testing.T) {
	cases := []struct {
		input map[string]interface{}
		want  string
	}{
		{map[string]interface{}{"path": "a.go"}, "a.go"},
		{map[string]interface{}{"file_path": "b.go"}, "b.go"},       // CC
		{map[string]interface{}{"filePath": "c.go"}, "c.go"},        // OpenCode
		{map[string]interface{}{"notebook_path": "n.ipynb"}, "n.ipynb"},
		{map[string]interface{}{"files": map[string]interface{}{"path": "d.go"}}, "d.go"},
		{map[string]interface{}{"command": "ls"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := toolPath(c.input); got != c.want {
			t.Errorf("toolPath(%v) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestTakeFileSnapshotAndDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := takeFileSnapshot("edit_file", map[string]interface{}{"path": path})
	if snap.path == "" {
		t.Fatal("expected snapshot for edit_file on existing file")
	}
	if snap.old != "old\n" {
		t.Fatalf("snapshot content = %q", snap.old)
	}

	// No snapshot for non-edit tools or missing paths.
	if s := takeFileSnapshot("bash", map[string]interface{}{"path": path}); s.path != "" {
		t.Fatal("bash should not snapshot")
	}
	if s := takeFileSnapshot("edit_file", map[string]interface{}{"command": "x"}); s.path != "" {
		t.Fatal("edit without a path should not snapshot")
	}

	// Identical content → zero snapshot path after edit (printFileDiff no-ops).
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTakeFileSnapshotRaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("x"), 0o644)
	if s := takeFileSnapshotRaw("opencode.edit", path); s.path == "" || s.old != "x" {
		t.Fatalf("raw snapshot = %+v", s)
	}
	if s := takeFileSnapshotRaw("opencode.bash", path); s.path != "" {
		t.Fatal("non-edit tool must not snapshot")
	}
}
