package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAgyMCPEntryMergesQmaxAndPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp_config.json")
	existing := `{
  "mcpServers": {
    "other": {"command": "echo", "args": ["hi"]}
  }
}
`
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}

	had, err := WriteAgyMCPEntry(path, map[string]string{"QMAX_LOCAL_ONLY": "1"})
	if err != nil {
		t.Fatalf("WriteAgyMCPEntry: %v", err)
	}
	if had {
		t.Fatal("fresh qmax entry should not report alreadyHad")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatal("unrelated MCP server was dropped")
	}
	qmax := servers["qmax"].(map[string]any)
	if qmax["command"] != QmaxMCPCommand {
		t.Fatalf("command = %v", qmax["command"])
	}
	had, err = WriteAgyMCPEntry(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !had {
		t.Fatal("second write should report alreadyHad")
	}
}

func TestBuildAgyArgsPutsPrintLastAndResumesConversation(t *testing.T) {
	a := NewAgyAgent("agy", "gemini-3.7-flash-high", "medium", "unattended", false, nil)
	args, err := a.buildAgyArgs("review the diff", "/tmp/repo", "055a398f-db14-4c5f-abbb-1bf03f8120a7")
	if err != nil {
		t.Fatal(err)
	}
	if len(args) < 4 {
		t.Fatalf("too few args: %v", args)
	}
	if args[len(args)-2] != "-p" || args[len(args)-1] != "review the diff" {
		t.Fatalf("-p prompt must be last, got %v", args)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--output-format stream-json",
		"--add-dir /tmp/repo",
		"--model gemini-3.7-flash-high",
		"--effort medium",
		"--dangerously-skip-permissions",
		"--conversation 055a398f-db14-4c5f-abbb-1bf03f8120a7",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, args)
		}
	}
}

func TestBuildAgyArgsRejectsBadConversationID(t *testing.T) {
	a := NewAgyAgent("agy", "", "high", "standard", false, nil)
	if _, err := a.buildAgyArgs("hi", ".", "not-a-uuid"); err == nil {
		t.Fatal("expected invalid conversation id to fail")
	}
}

func TestParseAgyStreamCapturesResultAndUsage(t *testing.T) {
	ndjson := strings.Join([]string{
		`{"event":"init","conversation_id":"c3b66b04-872b-4fbe-a3a4-058a026ef20a","init":{"cwd":"/tmp"}}`,
		`{"event":"step_update","step_update":{"conversation_id":"c3b66b04-872b-4fbe-a3a4-058a026ef20a","step_index":1,"state":"DONE","step_type":"agent_response","text_delta":"ok\n"}}`,
		`{"event":"result","result":{"conversation_id":"c3b66b04-872b-4fbe-a3a4-058a026ef20a","status":"SUCCESS","response":"ok\n","usage":{"input_tokens":10,"output_tokens":2}}}`,
	}, "\n")
	a := NewAgyAgent("agy", "", "high", "standard", false, nil)
	got := a.parseStream(strings.NewReader(ndjson), nil)
	if got != "ok\n" {
		t.Fatalf("response = %q", got)
	}
	in, out, ok := a.LastTurnStats()
	if !ok || in != 10 || out != 2 {
		t.Fatalf("usage = (%d,%d,ok=%v)", in, out, ok)
	}
	a.mu.Lock()
	id := a.conversationID
	a.mu.Unlock()
	if id != "c3b66b04-872b-4fbe-a3a4-058a026ef20a" {
		t.Fatalf("conversation_id = %q", id)
	}
}

func TestAgyResetConversationClearsResumeID(t *testing.T) {
	a := NewAgyAgent("agy", "", "high", "standard", false, nil)
	a.conversationID = "c3b66b04-872b-4fbe-a3a4-058a026ef20a"
	a.ResetConversation()
	if a.conversationID != "" {
		t.Fatal("ResetConversation left a conversation id")
	}
}

func TestWrapAgyExitPointsAtGoogleOAuth(t *testing.T) {
	a := NewAgyAgent("agy", "", "high", "standard", false, nil)
	a.lastStderr = "authentication required"
	err := a.wrapAgyExit(os.ErrPermission)
	if err == nil || !strings.Contains(err.Error(), "Run `agy`") {
		t.Fatalf("want Google OAuth hint, got %v", err)
	}
}

func TestAgyOAuthTokenPath(t *testing.T) {
	got := AgyOAuthTokenPath("/Users/ada")
	want := "/Users/ada/.gemini/antigravity-cli/antigravity-oauth-token"
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}
