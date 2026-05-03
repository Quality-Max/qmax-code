package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// ---- applyCloudSyncChoice ----

func TestApplyCloudSyncChoice_YesVariants(t *testing.T) {
	for _, line := range []string{"y\n", "Y\n", "yes\n", "YES\n", "\n", "  \n"} {
		withTempHome(t)
		cfg := defaultConfig()
		got := applyCloudSyncChoice(cfg, line)
		if !got {
			t.Errorf("applyCloudSyncChoice(%q): got false, want true", line)
		}
		if cfg.CloudSync == nil || !*cfg.CloudSync {
			t.Errorf("applyCloudSyncChoice(%q): cfg.CloudSync not set to true", line)
		}
	}
}

func TestApplyCloudSyncChoice_NoVariants(t *testing.T) {
	for _, line := range []string{"n\n", "N\n", "no\n", "NO\n"} {
		withTempHome(t)
		cfg := defaultConfig()
		got := applyCloudSyncChoice(cfg, line)
		if got {
			t.Errorf("applyCloudSyncChoice(%q): got true, want false", line)
		}
		if cfg.CloudSync == nil || *cfg.CloudSync {
			t.Errorf("applyCloudSyncChoice(%q): cfg.CloudSync not set to false", line)
		}
	}
}

func TestApplyCloudSyncChoice_PersistsToDisk(t *testing.T) {
	withTempHome(t)
	cfg := defaultConfig()
	applyCloudSyncChoice(cfg, "y\n")

	loaded := LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || !*loaded.CloudSync {
		t.Error("CloudSync=true not persisted to disk")
	}
}

// ---- Config.CloudSync JSON round-trip ----

func TestConfigCloudSync_NilOmittedFromJSON(t *testing.T) {
	cfg := &Config{}
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
	cfg := defaultConfig()
	cfg.CloudSync = &v
	_ = cfg.Save()

	loaded := LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || !*loaded.CloudSync {
		t.Error("CloudSync true did not survive Save/Load round-trip")
	}
}

func TestConfigCloudSync_FalsePersistedAndLoaded(t *testing.T) {
	withTempHome(t)
	v := false
	cfg := defaultConfig()
	cfg.CloudSync = &v
	_ = cfg.Save()

	loaded := LoadQMaxCodeConfig()
	if loaded.CloudSync == nil || *loaded.CloudSync {
		t.Error("CloudSync false did not survive Save/Load round-trip")
	}
}

// ---- cloudSessionTracker.Start ----

func TestCloudTracker_Start_SkipsWhenAPIIsNil(t *testing.T) {
	calls := 0
	var tracker cloudSessionTracker
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

	var tracker cloudSessionTracker
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

	var tracker cloudSessionTracker
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

	var tracker cloudSessionTracker
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

	var tracker cloudSessionTracker
	tracker.Start(client, 184, "claude-sonnet-4-6")

	if tracker.cloudID != "" {
		t.Errorf("cloudID should be empty after server error, got %q", tracker.cloudID)
	}
}

// ---- cloudSessionTracker.Complete ----

func TestCloudTracker_Complete_SkipsWhenCloudIDEmpty(t *testing.T) {
	calls := 0
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{}`))
	})

	var tracker cloudSessionTracker // cloudID == ""
	tracker.Complete(client, 500, "some summary")

	if calls != 0 {
		t.Errorf("expected no HTTP call when cloudID is empty, got %d", calls)
	}
}

func TestCloudTracker_Complete_SkipsWhenAPIIsNil(t *testing.T) {
	// Manually set cloudID to simulate a started session, then pass nil api.
	tracker := cloudSessionTracker{cloudID: "cloud-abc"}
	// Should not panic.
	tracker.Complete(nil, 100, "summary")
}

func TestCloudTracker_Complete_PatchesWithCloudID(t *testing.T) {
	var gotPath, gotMethod string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{}`))
	})

	tracker := cloudSessionTracker{cloudID: "cloud-abc"}
	tracker.Complete(client, 200, "did stuff  [2 turns]")

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

	tracker := cloudSessionTracker{cloudID: "cloud-abc"}
	// Must not panic or block.
	tracker.Complete(client, 100, "summary")
}

// ---- full Start → Complete lifecycle ----

func TestCloudTracker_StartThenComplete_UsesCorrectCloudID(t *testing.T) {
	var patchedID string
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			_, _ = w.Write([]byte(`{"session_id":"lifecycle-id"}`))
		} else {
			patchedID = r.URL.Path
			_, _ = w.Write([]byte(`{}`))
		}
	})

	var tracker cloudSessionTracker
	tracker.Start(client, 184, "claude-sonnet-4-6")
	tracker.Complete(client, 750, "lifecycle test  [5 turns]")

	if patchedID != "/api/agent-sessions/lifecycle-id" {
		t.Errorf("Complete used wrong id: path %q", patchedID)
	}
}
