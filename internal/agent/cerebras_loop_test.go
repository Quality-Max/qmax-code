package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/sysutil"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

func TestRunCerebrasAgentToolRoundAndSpeed(t *testing.T) {
	withTempHome(t)
	fc := newFakeCloud(t)
	replies := []string{
		`{"choices":[{"message":{"content":"Planning first.","tool_calls":[{"id":"c1","type":"function","function":{"name":"update_plan","arguments":"{\"steps\":[{\"title\":\"look\",\"status\":\"in_progress\"}]}"}},{"id":"c2","type":"function","function":{"name":"update_plan","arguments":"not json"}}]}}],"usage":{"prompt_tokens":20,"completion_tokens":5},"time_info":{"tokens_per_second":1800,"time_to_first_token_sec":0.12}}`,
		`{"choices":[{"message":{"content":"All done."}}],"usage":{"prompt_tokens":40,"completion_tokens":8},"time_info":{"tps":1500}}`,
	}
	var mu sync.Mutex
	fc.handle("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body := `{"choices":[]}`
		if len(replies) > 0 {
			body, replies = replies[0], replies[1:]
		}
		_, _ = io.WriteString(w, body)
	})
	ag := NewAgent(AgentConfig{Context: &api.SessionContext{LocalOnly: true}})
	ag.Logger = sysutil.NewLogger("cerebras-test")
	t.Cleanup(ag.Logger.Close)
	ag.Cerebras = &CerebrasClient{BaseURL: fc.srv.URL, APIKey: "csk", Model: "", HTTP: ag.client}
	ag.AppendHistory(api.Message{Role: "user", Content: "plan it"})

	var out string
	var ok bool
	printed := captureStdout(t, func() { out, ok = ag.RunCerebrasAgent(&tui.Terminal{}) })
	if !ok || out != "All done." {
		t.Fatalf("out=%q ok=%v", out, ok)
	}
	if ag.Usage.InputTokens != 60 || ag.Usage.Requests != 2 || ag.LastContextTokens != 40 {
		t.Fatalf("usage = %+v last=%d", ag.Usage, ag.LastContextTokens)
	}
	if !strings.Contains(printed, "1800 tok/s · TTFT 0.12s") || !strings.Contains(printed, "1500 tok/s") {
		t.Fatalf("speed lines missing:\n%s", printed)
	}

	// No choices is a failure.
	captureStdout(t, func() { out, ok = ag.RunCerebrasAgent(&tui.Terminal{}) })
	if ok {
		t.Fatal("empty choices reported success")
	}

	// A request error surfaces its reason.
	fc.handle("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad model", http.StatusBadRequest)
	})
	printed = captureStdout(t, func() { _, ok = ag.RunCerebrasAgent(&tui.Terminal{}) })
	if ok || !strings.Contains(printed, "bad model") {
		t.Fatalf("error path ok=%v printed=%s", ok, printed)
	}

	fc.handle("/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `not json`)
	})
	if _, err := ag.Cerebras.Chat(context.Background(), nil, nil); err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("bad json err = %v", err)
	}

	// Cancelling mid-request reports an interrupted turn without an error line.
	fc.handle("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	go func() {
		time.Sleep(100 * time.Millisecond)
		ag.CancelCurrent()
	}()
	printed = captureStdout(t, func() { _, ok = ag.RunCerebrasAgent(&tui.Terminal{}) })
	if ok || strings.Contains(printed, "Cerebras request failed") {
		t.Fatalf("interrupt ok=%v printed=%s", ok, printed)
	}

	var nilAgent Agent
	if _, ok := nilAgent.RunCerebrasAgent(&tui.Terminal{}); ok {
		t.Fatal("nil cerebras client ran")
	}
	printCerebrasSpeed(&tui.Terminal{}, nil, "")
	captureStdout(t, func() {
		printCerebrasSpeed(&tui.Terminal{}, &oaiChatResponse{TimeInfo: map[string]interface{}{"ttft_ms": 250}}, "")
	})
}

func TestDoWithRetryHonoursRetryAfter(t *testing.T) {
	fc := newFakeCloud(t)
	var mu sync.Mutex
	calls := 0
	fc.handle("/", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		c := calls
		mu.Unlock()
		if c == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	ag := NewAgent(AgentConfig{})
	newReq := func() (*http.Request, error) {
		return http.NewRequestWithContext(context.Background(), "GET", fc.srv.URL, nil)
	}
	var resp *http.Response
	var err error
	captureStdout(t, func() { resp, err = doWithRetry(context.Background(), ag.client, newReq, nil) })
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	resp.Body.Close()

	// Cancellation during the backoff returns the context error.
	calls = 0
	fc.handle("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	captureStdout(t, func() { _, err = doWithRetry(ctx, ag.client, newReq, &tui.Terminal{}) })
	if err == nil {
		t.Fatal("expected cancellation error")
	}

	if _, err := doWithRetry(context.Background(), ag.client, func() (*http.Request, error) { return nil, io.EOF }, nil); err != io.EOF {
		t.Fatalf("builder error = %v", err)
	}
}

func TestParseResetTimeHeaders(t *testing.T) {
	now := time.Now()
	if !parseResetTime(nil).IsZero() {
		t.Fatal("nil headers")
	}
	if got := parseResetTime(map[string]string{"Retry-After": "60"}); got.Before(now.Add(59*time.Second)) {
		t.Fatalf("retry-after seconds = %v", got)
	}
	date := now.Add(time.Hour).UTC().Truncate(time.Second)
	if got := parseResetTime(map[string]string{"retry-after": date.Format(http.TimeFormat)}); !got.Equal(date) {
		t.Fatalf("retry-after date = %v", got)
	}
	if got := parseResetTime(map[string]string{"x-ratelimit-reset": "1900000000"}); got.Unix() != 1900000000 {
		t.Fatalf("epoch = %v", got)
	}
	if got := parseResetTime(map[string]string{"ratelimit-reset": "30"}); got.Before(now.Add(29 * time.Second)) {
		t.Fatalf("delta = %v", got)
	}
	if got := parseResetTime(map[string]string{"anthropic-ratelimit-unified-reset": date.Format(time.RFC3339)}); !got.Equal(date) {
		t.Fatalf("rfc3339 = %v", got)
	}
	if !parseResetTime(map[string]string{"x-ratelimit-reset": "soon"}).IsZero() {
		t.Fatal("unparseable reset should be zero")
	}
}
