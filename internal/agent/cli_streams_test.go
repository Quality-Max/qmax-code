package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

const agyConversation = "055a398f-db14-4c5f-abbb-1bf03f8120a7"

func agyStream() string {
	return strings.Join([]string{
		`{"event":"init","conversation_id":"` + agyConversation + `"}`,
		`not json`,
		`{"event":"step_update"}`,
		`{"event":"step_update","step_update":{"conversation_id":"` + agyConversation + `","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"Checking "}}`,
		`{"event":"step_update","step_update":{"step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"mcp__qmax__run_test","tool_info":{"name":"mcp__qmax__run_test","parameters":{"script_id":3}}}}`,
		`{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"tool","tool_info":{"name":"run_test","output":"passed","error":{"message":"flaky"}},"usage":{"input_tokens":40,"output_tokens":9}}}`,
		`{"event":"result"}`,
		`{"event":"result","result":{"conversation_id":"` + agyConversation + `","status":"ERROR","response":"Tests passed.","error":"partial","usage":{"input_tokens":80,"output_tokens":20}}}`,
	}, "\n") + "\n"
}

func TestAgyRunStreamsAndRecordsUsage(t *testing.T) {
	requirePOSIX(t)
	home := withTempHome(t)
	fixture := filepath.Join(home, "agy.ndjson")
	if err := os.WriteFile(fixture, []byte(agyStream()), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := writeStubIn(t, t.TempDir(), "agy", `cat "`+fixture+`"`)
	sctx := &api.SessionContext{ProjectID: 4, LiveFeed: true, LocalOnly: true}

	for _, verbose := range []bool{true, false} {
		a := NewAgyAgent(bin, "gemini-3", "", "unattended", verbose, "on", sctx)
		var out string
		var err error
		captureStdout(t, func() { out, err = a.Run("run the smoke test", &tui.Terminal{}) })
		if err != nil || out != "Tests passed." {
			t.Fatalf("verbose=%v: out=%q err=%v", verbose, out, err)
		}
		if in, o, ok := a.LastTurnStats(); !ok || in != 80 || o != 20 {
			t.Fatalf("stats = %d/%d/%v", in, o, ok)
		}
		if a.conversationID != agyConversation {
			t.Fatalf("conversation = %q", a.conversationID)
		}
		// The second turn resumes the stored conversation.
		captureStdout(t, func() { _, err = a.Run("again", nil) })
		if err != nil {
			t.Fatal(err)
		}
		a.SetOutputVerbose(!verbose)
		a.ResetConversation()
		a.Cancel()
		a.Cleanup()
	}
	if _, err := os.Stat(agyMCPConfigPath(home)); err != nil {
		t.Fatalf("MCP config not written: %v", err)
	}
	if !AgyFileTokenPresent() {
		t.Log("no file token (expected in tests)")
	}
}

func TestAgyRunExitErrors(t *testing.T) {
	requirePOSIX(t)
	withTempHome(t)
	dir := t.TempDir()
	cases := []struct {
		name, body, want string
		limit            bool
	}{
		{"auth", `echo "Error: authentication required" >&2; exit 1`, "not signed in with Google", false},
		{"limit", `echo "usage limit reached" >&2; exit 1`, "agy exited with error", true},
		{"long stderr", `printf '%0500d' 0 >&2; exit 2`, "agy exited with error", false},
		{"silent", `exit 3`, "agy exited with error: exit status 3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bin := writeStubIn(t, dir, "agy-"+strings.ReplaceAll(tc.name, " ", "-"), tc.body)
			a := NewAgyAgent(bin, "", "", "", false, "", nil)
			_, err := a.Run("hi", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if _, hit := a.LastPlanLimit(); hit != tc.limit {
				t.Fatalf("limit hit = %v, want %v", hit, tc.limit)
			}
		})
	}

	a := NewAgyAgent("/nonexistent/agy", "", "", "", false, "", nil)
	if _, err := a.Run("hi", nil); err == nil || !strings.Contains(err.Error(), "start agy") {
		t.Fatalf("missing binary err = %v", err)
	}
	if _, err := a.buildAgyArgs("x", "", "not-a-uuid"); err == nil {
		t.Fatal("invalid conversation ID accepted")
	}
	if agyToolName(nil) != "" {
		t.Fatal("nil step tool name")
	}
}

func TestCCParseStream(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	file := filepath.Join(dir, "app.go")
	if err := os.WriteFile(file, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"0f8fad5b-d9cb-469f-a165-70867728950e"}`,
		`garbage`,
		`{"type":"assistant"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Editing."},{"type":"tool_use","id":"tu1","name":"Edit","input":{"file_path":"` + file + `","old_string":"package app","new_string":"package app2"}}]}}`,
		`{"type":"user"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":[{"type":"text","text":"ok"}]}]}}`,
		`{"type":"user","message":{"role":"user","content":"plain echo"}}`,
		`{"type":"result","result":"Done.","is_error":true,"usage":{"input_tokens":11,"output_tokens":5}}`,
	}, "\n")

	for _, verbose := range []bool{true, false} {
		a := NewCCAgent("claude", "", "", "", verbose, "", &api.SessionContext{})
		var got string
		captureStdout(t, func() {
			_ = os.WriteFile(file, []byte("package app\n"), 0o644)
			got = a.parseStream(strings.NewReader(stream), &tui.Terminal{})
		})
		if got != "Done." {
			t.Fatalf("result = %q", got)
		}
		if in, out, ok := a.LastTurnStats(); !ok || in != 11 || out != 5 {
			t.Fatalf("stats = %d/%d/%v", in, out, ok)
		}
		if a.ccSessionID == "" {
			t.Fatal("session id not captured")
		}
	}
	if got := extractToolResultText([]byte(`"plain"`)); got != "plain" {
		t.Fatalf("string tool result = %q", got)
	}
	if got := extractToolResultText([]byte(`{"x":1}`)); got == "" {
		t.Fatal("object tool result dropped")
	}
}

func TestOpenCodeParseStream(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stream := strings.Join([]string{
		`{"type":"step_start","sessionID":"ses_0123456789abcdefABCDEF"}`,
		`{"type":"text","part":{"id":"p1","type":"text","text":"Hello"}}`,
		`{"type":"text","part":{"id":"p1","type":"text","text":"Hello world"}}`,
		`{"type":"text","part":{"id":"p1","type":"text","text":"Rewritten"}}`,
		`{"type":"text","part":{"id":"p2","type":"text","text":""}}`,
		`{"type":"text","part":{"id":"p3","type":"text","text":"Second part"}}`,
		`{"type":"tool","part":{"id":"t0","type":"tool"}}`,
		`{"type":"tool","part":{"id":"t1","type":"tool","tool":"edit","state":"running","input":{"filePath":"` + file + `"}}}`,
		`{"type":"tool","part":{"id":"t1","type":"tool","tool":"edit","state":"completed","output":"ok"}}`,
		`{"type":"tool","part":{"id":"t2","type":"tool","tool":"qmax_run_test","state":"error","error":"boom"}}`,
		`{"type":"step_finish","part":{"id":"s1","tokens":{"input":30,"output":7}}}`,
		`{"type":"step_finish","part":{"id":"s1","tokens":{"input":30,"output":7}}}`,
		`{"type":"step_finish","timestamp":5,"usage":{"prompt":3,"completion":2}}`,
		`{"type":"error","error":{"name":"APIError","data":{"message":"Rate limit exceeded","statusCode":429}}}`,
		`{"type":"error","error":{"name":"Quiet"}}`,
	}, "\n")
	for _, verbose := range []bool{true, false} {
		a := NewOpenCodeAgent("opencode", "groq/llama", "", "", verbose, api.DefaultConfig(), &api.SessionContext{})
		var got string
		captureStdout(t, func() { got = a.parseStream(strings.NewReader(stream), &tui.Terminal{}) })
		if got != "Rewritten\n\nSecond part" {
			t.Fatalf("result = %q", got)
		}
		if in, out, ok := a.LastTurnStats(); !ok || in != 33 || out != 9 {
			t.Fatalf("stats = %d/%d/%v", in, out, ok)
		}
	}
}
