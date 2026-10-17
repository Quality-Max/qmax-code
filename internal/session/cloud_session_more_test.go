package session

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Quality-Max/qmax-code/internal/api"
)

func TestPromptCloudSyncConsentPersistsAnswer(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{name: "yes enables sync", line: "y\n", want: true},
		{name: "no disables sync", line: "n\n", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			withTempHome(t)

			cfg := api.DefaultConfig()
			got := PromptCloudSyncConsent(cfg, func() (string, error) { return tt.line, nil })
			if got != tt.want {
				t.Fatalf("PromptCloudSyncConsent = %v, want %v", got, tt.want)
			}
			if cfg.CloudSync == nil || *cfg.CloudSync != tt.want {
				t.Fatalf("cfg.CloudSync = %v, want %v", cfg.CloudSync, tt.want)
			}

			loaded := api.LoadQMaxCodeConfig()
			if loaded.CloudSync == nil || *loaded.CloudSync != tt.want {
				t.Fatalf("choice not persisted; loaded CloudSync = %v, want %v", loaded.CloudSync, tt.want)
			}
		})
	}
}

func TestCloudTracker_SwitchProject_Guards(t *testing.T) {
	t.Run("nil client is not a switch", func(t *testing.T) {
		tracker := CloudSessionTracker{cloudID: "c", projectID: 1}
		if tracker.SwitchProject(nil, 2, "m", 0, nil) {
			t.Fatal("nil client must not report a project switch")
		}
	})

	t.Run("unstarted tracker opens first session without switching", func(t *testing.T) {
		creates := 0
		client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/agent-sessions" {
				creates++
				_, _ = w.Write([]byte(`{"session_id":"cloud-first"}`))
				return
			}
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			_, _ = w.Write([]byte(`{}`))
		})

		var tracker CloudSessionTracker
		msgs := []api.Message{{Role: "user", Content: "hi"}}
		if tracker.SwitchProject(client, 5, "m", 10, msgs) {
			t.Fatal("opening the first session must return false (nothing moved)")
		}
		if creates != 1 {
			t.Fatalf("creates = %d, want 1", creates)
		}
		if tracker.cloudID != "cloud-first" || tracker.projectID != 5 || tracker.historyStart != len(msgs) {
			t.Fatalf("tracker state after first open = %+v", tracker)
		}
	})

	t.Run("same project is a no-op", func(t *testing.T) {
		calls := 0
		client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			_, _ = w.Write([]byte(`{}`))
		})

		tracker := CloudSessionTracker{cloudID: "cloud-a", projectID: 5}
		if tracker.SwitchProject(client, 5, "m", 10, nil) {
			t.Fatal("switching to the current project must return false")
		}
		if calls != 0 {
			t.Fatalf("same-project switch made %d HTTP calls, want 0", calls)
		}
	})
}

func TestCloudTracker_StartWithHistoryRecordsOffsets(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"session_id":"off-1"}`))
	})

	var tracker CloudSessionTracker
	tracker.StartWithHistory(client, 9, "model", 4, 100)

	if tracker.cloudID != "off-1" || tracker.projectID != 9 {
		t.Fatalf("session not started: %+v", tracker)
	}
	if tracker.historyStart != 4 || tracker.tokenStart != 100 {
		t.Fatalf("history/token offsets not recorded: %+v", tracker)
	}
}

func TestCloudTracker_CompleteCurrent_ClampsNegativeStartAndTokenDelta(t *testing.T) {
	var patchedTokens []int
	var eventCounts []int
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			var body struct {
				TotalTokens int `json:"total_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode finalize request: %v", err)
			}
			patchedTokens = append(patchedTokens, body.TotalTokens)
			_, _ = w.Write([]byte("{}"))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/events"):
			var body struct {
				Events []json.RawMessage `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode events request: %v", err)
			}
			eventCounts = append(eventCounts, len(body.Events))
			_, _ = w.Write([]byte("{}"))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			_, _ = w.Write([]byte("{}"))
		}
	})

	// A negative historyStart (from a /load shrinking the conversation) and a
	// token delta below the session's start must both clamp to zero.
	tracker := CloudSessionTracker{cloudID: "neg", historyStart: -5, tokenStart: 500}
	msgs := []api.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	tracker.CompleteCurrent(client, 100, msgs)

	if len(patchedTokens) != 1 || patchedTokens[0] != 0 {
		t.Fatalf("patched total_tokens = %v, want [0] (negative delta clamped)", patchedTokens)
	}
	if len(eventCounts) != 1 || eventCounts[0] != 2 {
		t.Fatalf("uploaded event counts = %v, want [2] (negative start clamped to 0)", eventCounts)
	}
}
