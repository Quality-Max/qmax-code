package session

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

// newTestClient wires an api.APIClient to a local httptest.Server so callers
// can observe outbound requests and stub responses. Mirrors the helper in
// internal/api tests; lives here because main-package tests can't reach
// across the package boundary.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*api.APIClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &api.APIClient{
		BaseURL: srv.URL,
		APIKey:  "qm-test-key",
		HTTP:    srv.Client(),
	}, srv
}

// withTempHome points $HOME at a fresh temp dir for the test and restores it
// in cleanup. Local copy of the helper in main package's config_command_test.go.
func withTempHome(t *testing.T) string {
	t.Helper()
	orig := os.Getenv("HOME")
	tmp := t.TempDir()
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", orig) })
	return tmp
}

// ---- ApplyCloudSyncChoice ----

func TestApplyCloudSyncChoice_YesVariants(t *testing.T) {
	for _, line := range []string{"y\n", "Y\n", "yes\n", "YES\n", "\n", "  \n"} {
		withTempHome(t)
		cfg := api.DefaultConfig()
		got := ApplyCloudSyncChoice(cfg, line)
		if !got {
			t.Errorf("ApplyCloudSyncChoice(%q): got false, want true", line)
		}
		if cfg.CloudSync == nil || !*cfg.CloudSync {
			t.Errorf("ApplyCloudSyncChoice(%q): cfg.CloudSync not set to true", line)
		}
	}
}

func TestApplyCloudSyncChoice_NoVariants(t *testing.T) {
	for _, line := range []string{"n\n", "N\n", "no\n", "NO\n"} {
		withTempHome(t)
		cfg := api.DefaultConfig()
		got := ApplyCloudSyncChoice(cfg, line)
		if got {
			t.Errorf("ApplyCloudSyncChoice(%q): got true, want false", line)
		}
		if cfg.CloudSync == nil || *cfg.CloudSync {
			t.Errorf("ApplyCloudSyncChoice(%q): cfg.CloudSync not set to false", line)
		}
	}
}

func TestApplyCloudSyncChoice_PersistsToDisk(t *testing.T) {
	withTempHome(t)
	cfg := api.DefaultConfig()
	ApplyCloudSyncChoice(cfg, "y\n")

	loaded := api.LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || !*loaded.CloudSync {
		t.Error("CloudSync=true not persisted to disk")
	}
}

// ---- Config.CloudSync JSON round-trip ----

func TestConfigCloudSync_NilOmittedFromJSON(t *testing.T) {
	cfg := &api.Config{}
	data, _ := json.Marshal(cfg)
	var m map[string]interface{}
	_ = json.Unmarshal(data, &m)
	if _, ok := m["cloud_sync"]; ok {
		t.Errorf("expected cloud_sync omitted when nil, got key in JSON: %s", data)
	}
}

func TestConfigCloudSync_TruePersistedAndLoaded(t *testing.T) {
	withTempHome(t)
	v := true
	cfg := api.DefaultConfig()
	cfg.CloudSync = &v
	_ = cfg.Save()

	loaded := api.LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || !*loaded.CloudSync {
		t.Error("CloudSync true did not survive Save/Load round-trip")
	}
}

func TestConfigCloudSync_FalsePersistedAndLoaded(t *testing.T) {
	withTempHome(t)
	v := false
	cfg := api.DefaultConfig()
	cfg.CloudSync = &v
	_ = cfg.Save()

	loaded := api.LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || *loaded.CloudSync {
		t.Error("CloudSync false did not survive Save/Load round-trip")
	}
}

// ---- CloudSessionTracker.Start ----

func TestCloudTracker_Start_SkipsWhenAPIIsNil(t *testing.T) {
	calls := 0
	var tracker CloudSessionTracker
	// Passing nil api — real Start must bail out before making any HTTP call.
	// We verify indirectly: cloudID stays empty.
	tracker.Start(nil, 184, "claude-sonnet-4-6")
	_ = calls // no way to get an HTTP call count here; guard is the observable
	if tracker.cloudID != "" {
		t.Errorf("cloudID should remain empty when api is nil, got %q", tracker.cloudID)
	}
}

func TestCloudTracker_Start_SkipsWhenProjectIDIsZero(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"session_id":"should-not-see-this"}`))
	})

	var tracker CloudSessionTracker
	tracker.Start(client, 0, "claude-sonnet-4-6")

	if calls != 0 {
		t.Errorf("expected no HTTP call when projectID=0, got %d", calls)
	}
	if tracker.cloudID != "" {
		t.Errorf("cloudID should remain empty, got %q", tracker.cloudID)
	}
}

func TestCloudTracker_Start_HappyPath(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"session_id":"cloud-xyz"}`))
	})

	var tracker CloudSessionTracker
	tracker.Start(client, 184, "claude-sonnet-4-6")

	if tracker.cloudID != "cloud-xyz" {
		t.Errorf("cloudID: got %q, want cloud-xyz", tracker.cloudID)
	}
}

func TestCloudTracker_Start_Idempotent(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"session_id":"cloud-first"}`))
	})

	var tracker CloudSessionTracker
	tracker.Start(client, 184, "claude-sonnet-4-6")
	tracker.Start(client, 184, "claude-sonnet-4-6") // second call — must be a no-op
	tracker.Start(client, 184, "claude-sonnet-4-6") // third call  — same

	if calls != 1 {
		t.Errorf("expected exactly 1 HTTP call, got %d", calls)
	}
	if tracker.cloudID != "cloud-first" {
		t.Errorf("cloudID should not change on repeated Start, got %q", tracker.cloudID)
	}
}

func TestCloudTracker_Start_CloudIDEmptyOnServerError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})

	var tracker CloudSessionTracker
	tracker.Start(client, 184, "claude-sonnet-4-6")

	if tracker.cloudID != "" {
		t.Errorf("cloudID should be empty after server error, got %q", tracker.cloudID)
	}
}

// ---- CloudSessionTracker.Complete ----

func TestCloudTracker_Complete_SkipsWhenCloudIDEmpty(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})

	var tracker CloudSessionTracker // cloudID == ""
	tracker.Complete(client, 500, "some summary", nil)

	if calls != 0 {
		t.Errorf("expected no HTTP call when cloudID is empty, got %d", calls)
	}
}

func TestCloudTracker_Complete_SkipsWhenAPIIsNil(t *testing.T) {
	// Manually set cloudID to simulate a started session, then pass nil api.
	tracker := CloudSessionTracker{cloudID: "cloud-abc"}
	// Should not panic.
	tracker.Complete(nil, 100, "summary", nil)
}

func TestCloudTracker_Complete_PatchesWithCloudID(t *testing.T) {
	var gotPath, gotMethod string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{}`))
	})

	tracker := CloudSessionTracker{cloudID: "cloud-abc"}
	tracker.Complete(client, 200, "did stuff  [2 turns]", nil)

	if gotMethod != "PATCH" {
		t.Errorf("method: got %q, want PATCH", gotMethod)
	}
	if gotPath != "/api/agent-sessions/cloud-abc" {
		t.Errorf("path: got %q, want /api/agent-sessions/cloud-abc", gotPath)
	}
}

func TestCloudTracker_Complete_SilentOnServerError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	})

	tracker := CloudSessionTracker{cloudID: "cloud-abc"}
	// Must not panic or block.
	tracker.Complete(client, 100, "summary", nil)
}

// ---- full Start → Complete lifecycle ----

func TestCloudTracker_StartThenComplete_UsesCorrectCloudID(t *testing.T) {
	var patchedPath string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/agent-sessions":
			_, _ = w.Write([]byte(`{"session_id":"lifecycle-id"}`))
		case r.Method == "PATCH":
			patchedPath = r.URL.Path
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})

	var tracker CloudSessionTracker
	tracker.Start(client, 184, "claude-sonnet-4-6")
	tracker.Complete(client, 750, "lifecycle test  [5 turns]", nil)

	if patchedPath != "/api/agent-sessions/lifecycle-id" {
		t.Errorf("Complete used wrong id: path %q", patchedPath)
	}
}

func TestCloudTracker_Complete_UploadsMessages(t *testing.T) {
	var paths []string
	var methods []string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		methods = append(methods, r.Method)
		_, _ = w.Write([]byte(`{}`))
	})

	msgs := []api.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}

	tracker := CloudSessionTracker{cloudID: "cloud-xyz"}
	tracker.Complete(client, 300, "test upload", msgs)

	if len(paths) != 2 {
		t.Fatalf("expected 2 HTTP calls (PATCH + POST), got %d", len(paths))
	}
	if methods[0] != "PATCH" {
		t.Errorf("first call: got %s, want PATCH", methods[0])
	}
	if methods[1] != "POST" {
		t.Errorf("second call: got %s, want POST", methods[1])
	}
	if paths[1] != "/api/agent-sessions/cloud-xyz/events" {
		t.Errorf("messages path: got %q, want /api/agent-sessions/cloud-xyz/events", paths[1])
	}
}

func TestCloudTracker_Complete_SkipsUploadWhenNoMessages(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})

	tracker := CloudSessionTracker{cloudID: "cloud-xyz"}
	tracker.Complete(client, 100, "empty session", nil)

	// Only the PATCH to complete, no POST for messages
	if calls != 1 {
		t.Errorf("expected 1 HTTP call (PATCH only), got %d", calls)
	}
}

func TestCloudTracker_CompleteCurrent_SkipsHistoryBeforeTruncatedSession(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})

	tracker := CloudSessionTracker{
		cloudID:      "cloud-xyz",
		historyStart: 5,
	}
	// A /load can replace the local conversation with a shorter one. None of
	// those loaded messages belong to the active cloud session.
	messages := []api.Message{
		{Role: "user", Content: "loaded conversation"},
		{Role: "assistant", Content: "loaded response"},
	}
	tracker.CompleteCurrent(client, 100, messages)

	// Complete may PATCH the session, but must not upload loaded messages.
	if calls != 1 {
		t.Errorf("expected 1 HTTP call (PATCH only), got %d", calls)
	}
}

func TestCloudTracker_SwitchProject_FinalizesOldSessionAndScopesNewHistory(t *testing.T) {
	var createdProjects []int
	var patchedIDs []string
	var uploadedEventCounts []int
	var patchedTokens []int
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-sessions":
			var body struct {
				ProjectID int `json:"project_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create request: %v", err)
			}
			createdProjects = append(createdProjects, body.ProjectID)
			_, _ = w.Write([]byte("{\"session_id\":\"cloud-" + string(rune('a'+len(createdProjects)-1)) + "\"}"))
		case r.Method == http.MethodPatch:
			var body struct {
				TotalTokens int `json:"total_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode finalize request: %v", err)
			}
			patchedIDs = append(patchedIDs, r.URL.Path)
			patchedTokens = append(patchedTokens, body.TotalTokens)
			_, _ = w.Write([]byte("{}"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-sessions/cloud-a/events":
			fallthrough
		case r.Method == http.MethodPost && r.URL.Path == "/api/agent-sessions/cloud-b/events":
			var body struct {
				Events []json.RawMessage `json:"events"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode events request: %v", err)
			}
			uploadedEventCounts = append(uploadedEventCounts, len(body.Events))
			_, _ = w.Write([]byte("{}"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	initialMessages := []api.Message{
		{Role: "user", Content: "test project one"},
		{Role: "assistant", Content: "project one response"},
	}
	var tracker CloudSessionTracker
	tracker.StartWithHistory(client, 1, "claude-sonnet-4-6", 0, 0)
	if !tracker.SwitchProject(client, 2, "claude-sonnet-4-6", 100, initialMessages) {
		t.Fatal("expected a project switch to move the cloud session")
	}

	messages := append(initialMessages,
		api.Message{Role: "user", Content: "test project two"},
		api.Message{Role: "assistant", Content: "project two response"},
	)
	tracker.CompleteCurrent(client, 150, messages)

	if len(createdProjects) != 2 || createdProjects[0] != 1 || createdProjects[1] != 2 {
		t.Errorf("created projects: got %v, want [1 2]", createdProjects)
	}
	if len(patchedIDs) != 2 || patchedIDs[0] != "/api/agent-sessions/cloud-a" || patchedIDs[1] != "/api/agent-sessions/cloud-b" {
		t.Errorf("patched sessions: got %v", patchedIDs)
	}
	if len(patchedTokens) != 2 || patchedTokens[0] != 100 || patchedTokens[1] != 50 {
		t.Errorf("per-project token totals: got %v, want [100 50]", patchedTokens)
	}
	if len(uploadedEventCounts) != 2 || uploadedEventCounts[0] != 2 || uploadedEventCounts[1] != 2 {
		t.Errorf("uploaded event counts: got %v, want [2 2]", uploadedEventCounts)
	}
}
