package tui

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/api"
)

// captureTermOutput returns everything fn writes to stdout and stderr.
func captureTermOutput(t *testing.T, fn func()) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = w, w
	done := make(chan string)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()
	fn()
	_ = w.Close()
	os.Stdout, os.Stderr = origOut, origErr
	return <-done
}

func expectAll(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestPrintBannerMoods(t *testing.T) {
	term := &Terminal{}
	out := captureTermOutput(t, func() {
		term.PrintBanner("1.2.3", &api.SessionContext{})
		term.PrintBanner("1.2.3", &api.SessionContext{
			QMaxBin:   "/usr/bin/qmax",
			ProjectID: 9,
			Backend:   "cc",
			Auth:      &api.AuthConfig{APIKey: "k", Email: "qa@example.com"},
			GitInfo:   &api.GitInfo{Branch: "main", ChangedFiles: []string{"a", "b"}},
		})
		term.PrintBanner("1.2.3", &api.SessionContext{LocalOnly: true})
	})
	expectAll(t, out, "1.2.3", "/help")
}

func TestPrintToolResultFormats(t *testing.T) {
	term := &Terminal{}
	out := captureTermOutput(t, func() {
		term.PrintToolResult("x", `{"error":"bad thing happened"}`)
		term.PrintToolResult("check_test_status", `{"execution_id":"e1","status":"passed","message":"ok","execution_time":2.5,"screenshot_paths":["https://cdn/s1.png",""],"video_path":"v.webm"}`)
		term.PrintToolResult("run_test", `{"execution_id":"e2","status":"failed","test_errors":"assert","console_logs":[{"text":"Error: 500"},{"text":"fine"},"x"]}`)
		term.PrintToolResult("run_test", `{"execution_id":"e3","status":"queued"}`)
		term.PrintToolResult("run_test", `execution_id but not json`)
		term.PrintToolResult("list", "one\ntwo")
		term.PrintToolResult("list", "1\n2\n3\n4\n5")
		term.StopThinking()
	})
	expectAll(t, out, "✗ Error", "Execution: e1", "✓ PASSED", "Message: ok", "Duration: 2.5s", "Screenshots: 2 captured", "Video:",
		"✗ FAILED", "Errors:", "Console errors:", "Error: 500", "queued", "one", "✓ 5 lines of output")
}

func TestPrintPlanStatusCostAndTests(t *testing.T) {
	term := &Terminal{streaming: true}
	w := api.NewPlanWindow(5 * time.Hour)
	now := time.Now()
	w.Record(now.Add(-time.Hour), 100, 20)
	w.MarkExhausted(now, now.Add(time.Hour))
	idle := api.NewPlanWindow(5 * time.Hour)
	idle.Record(now, 0, 0)

	out := captureTermOutput(t, func() {
		term.PrintPlan([]PlanStep{{Title: "read", Status: "done"}, {Title: "edit", Status: "in_progress"}, {Title: "test", Status: "pending"}})
		term.StopThinking()
		term.PrintCostSummary(api.TokenUsage{InputTokens: 1000, OutputTokens: 200, Requests: 3}, api.ModelSonnet, w)
		term.PrintPlanWindow(idle)
		term.PrintPlanWindow(nil)
		term.PrintStatusInfo(&api.SessionContext{LocalOnly: true}, api.TokenUsage{}, api.ModelSonnet, nil)
		term.PrintStatusInfo(&api.SessionContext{API: &api.APIClient{}, Auth: &api.AuthConfig{APIKey: "k", Email: "me@x"}}, api.TokenUsage{}, api.ModelHaiku, w)
		cliCtx := &api.SessionContext{QMaxBin: "/bin/qmax"}
		cliCtx.QMaxCfg.Email = "cli@x"
		term.PrintStatusInfo(cliCtx, api.TokenUsage{}, "", nil)
		term.PrintStatusInfo(&api.SessionContext{}, api.TokenUsage{}, "", nil)
		term.PrintTestResults(nil)
		term.PrintTestResults([]TestResult{{Name: "ok", Passed: true, Duration: 1}, {Name: "bad", Error: strings.Repeat("e", 120), Duration: 2}})
		term.PrintTestResults([]TestResult{{Name: "ok", Passed: true}})
		term.PrintTokenUsage(api.TokenUsage{InputTokens: 1, OutputTokens: 2})
	})
	expectAll(t, out, "(1/3 done)", "Session Token Usage", "Coding-plan Usage Window", "limit reached", "backend reports no token usage",
		"Standalone local-only", "Logged in as:", "me@x", "Connected (via CLI)", "cli@x", "Not connected", "Plan window:",
		"❌ 1 FAILED", "✅ ALL PASSED", "...", "tokens: 1 in / 2 out")
}

func TestStreamingAndMarkdown(t *testing.T) {
	term := &Terminal{}
	term.setMarkdownStyle(true)
	out := captureTermOutput(t, func() {
		term.FinishMarkdown("ignored when not streaming")
		term.StreamText("# Title\n")
		term.StreamText("**bold** text")
		term.FinishMarkdown("# Title\n**bold** text")
		term.PrintAssistant("plain *answer*")
		term.PrintToolIcon("read_file")
		term.PrintToolStart("read_file", map[string]interface{}{"path": "README.md", "long": strings.Repeat("x", 60)})
		term.PrintToolIcon("unknown_tool")
		term.PrintToolStart("unknown_tool", "not a map")
		term.StreamText("partial")
		term.PrintToolIcon("run_command")
		term.StreamText("diff next")
		term.PrintFileDiff("a.go", "package a\n", "package b\n")
		term.PrintFileDiff("a.go", "same", "same")
		term.PrintSystem("system")
		term.PrintError("oops")
		term.EndLine()
		_, _ = term.Write([]byte("raw write\n"))
	})
	expectAll(t, out, "Title", "bold", "answer", "read file", "path=README.md", "...", "unknown tool", "system", "✗ oops", "raw write")

	plain := &Terminal{}
	out = captureTermOutput(t, func() {
		plain.StreamText("no renderer")
		plain.FinishMarkdown("no renderer")
		plain.PrintAssistant("no renderer either")
	})
	expectAll(t, out, "no renderer", "no renderer either")
	if TruncateStr("abcdef", 5) != "ab..." || TruncateStr("abc", 5) != "abc" {
		t.Fatal("TruncateStr")
	}
}

func TestSpinnerLifecycle(t *testing.T) {
	term := &Terminal{}
	out := captureTermOutput(t, func() {
		term.StartThinking()
		time.Sleep(200 * time.Millisecond)
		term.SetUserTyping(true)
		time.Sleep(100 * time.Millisecond)
		term.SetUserTyping(false)
		term.StartThinking() // replaces the running spinner
		term.StopThinking()
		term.StopThinking()
	})
	if !strings.Contains(out, "...") {
		t.Fatalf("spinner never rendered: %q", out)
	}
	if term.Stderr() == nil {
		t.Fatal("Stderr writer is nil")
	}
	if term.SetTurnActivity("x") {
		t.Fatal("no turn program, activity should not be accepted")
	}
	var nilTerm *Terminal
	if nilTerm.Prompt() != "" {
		t.Fatal("nil terminal prompt")
	}
}
