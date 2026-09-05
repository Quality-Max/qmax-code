package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

type fableTestTransport func(*http.Request) (*http.Response, error)

func (f fableTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFable51ToolLoopDoesNotReplayIncompleteThinking(t *testing.T) {
	a := NewAgent(AgentConfig{Model: api.ModelFable51, Context: &api.SessionContext{LocalOnly: true}})
	requests := 0
	a.client.Transport = fableTestTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var req api.APIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != api.ModelFable51 {
			t.Fatal("request did not use Fable 5.1")
		}
		body := `{"content":[{"type":"thinking"},{"type":"redacted_thinking"},{"type":"tool_use","id":"test-tool-call","name":"unavailable_test_tool","input":{}}],"stop_reason":"tool_use"}`
		if requests == 2 {
			if len(req.Messages) != 3 {
				t.Fatal("tool continuation lost conversation history")
			}
			blocks, ok := req.Messages[1].Content.([]interface{})
			if !ok || len(blocks) != 1 || blocks[0].(map[string]interface{})["type"] != "tool_use" {
				t.Fatal("tool continuation replayed an incomplete thinking block")
			}
			body = `{"content":[{"type":"text","text":"Done"}],"stop_reason":"end_turn"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	got, err := a.Run("Check the repository")
	if err != nil || got != "Done" || requests != 2 {
		t.Fatalf("tool loop: response=%q, requests=%d, error=%v", got, requests, err)
	}
}
