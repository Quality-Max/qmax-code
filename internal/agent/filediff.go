package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/qualitymax/qmax-code/internal/tui"
)

// fileSnapshot captures a file's pre-edit content so the TUI can render a live
// diff when the tool completes — regardless of which backend made the edit.
type fileSnapshot struct {
	path string
	old  string
}

// strMapInput coerces a tool input (interface{} from content blocks) to a map.
func strMapInput(input interface{}) map[string]interface{} {
	if m, ok := input.(map[string]interface{}); ok {
		return m
	}
	return nil
}

// isFileEditTool reports whether a tool (built-in, Claude Code, or OpenCode
// naming) modifies workspace files and deserves a live diff.
func isFileEditTool(name string) bool {
	n := strings.ToLower(name)
	if i := strings.LastIndex(n, "__"); i >= 0 {
		n = n[i+2:] // strip mcp server prefixes like qmax__edit_file
	}
	return strings.Contains(n, "edit") ||
		strings.Contains(n, "write") ||
		strings.Contains(n, "patch") ||
		n == "notebook"
}

// toolPath extracts the target file path from a tool input map, tolerating the
// field names used by the built-in tools and CC/OpenCode backends.
func toolPath(input map[string]interface{}) string {
	if input == nil {
		return ""
	}
	for _, k := range []string{"path", "file_path", "filePath", "notebook_path"} {
		if v, ok := input[k].(string); ok && v != "" {
			return v
		}
	}
	// OpenCode nests edit targets under files/edits maps.
	for _, k := range []string{"files", "edits"} {
		if nested, ok := input[k].(map[string]interface{}); ok {
			if p := toolPath(nested); p != "" {
				return p
			}
		}
	}
	return ""
}

// toolPathFromRaw decodes a raw JSON tool input and extracts its file path.
func toolPathFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var input map[string]interface{}
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	return toolPath(input)
}

// takeFileSnapshot resolves the tool's target to an absolute path and captures
// its current content. Returns a zero snapshot when the tool edits nothing.
func takeFileSnapshot(name string, input map[string]interface{}) fileSnapshot {
	if !isFileEditTool(name) {
		return fileSnapshot{}
	}
	p := toolPath(input)
	if p == "" {
		return fileSnapshot{}
	}
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fileSnapshot{}
		}
		p = abs
	}
	data, _ := os.ReadFile(p)
	return fileSnapshot{path: p, old: string(data)}
}

// takeFileSnapshotRaw is takeFileSnapshot for raw JSON inputs (OpenCode events).
func takeFileSnapshotRaw(name, path string) fileSnapshot {
	if !isFileEditTool(name) || path == "" {
		return fileSnapshot{}
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return fileSnapshot{}
		}
		path = abs
	}
	data, _ := os.ReadFile(path)
	return fileSnapshot{path: path, old: string(data)}
}

// printFileDiff renders the change to the snapshot path since capture. No-op
// for identical content (e.g. a rejected or no-op edit).
func printFileDiff(term *tui.Terminal, snap fileSnapshot) {
	if term == nil || snap.path == "" {
		return
	}
	data, err := os.ReadFile(snap.path)
	if err != nil {
		return // deleted target: nothing sensible to diff against
	}
	if string(data) == snap.old {
		return
	}
	term.PrintFileDiff(displayPath(snap.path), snap.old, string(data))
}

// displayPath shortens an absolute path to cwd-relative when possible.
func displayPath(p string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}
