package main

import "testing"

// captureLiveURL pulls live_browser_url out of a status JSON payload
// and stores it on the session context. These tests pin the contract
// the server publishes (see qa-rag-app/services/test_execution_service.py
// and ai_crawl/pipeline_orchestrator.py — both write the field as a
// vnc.html URL with autoconnect/resize query params).

func TestCaptureLiveURLFromExecutionStatus(t *testing.T) {
	sctx := &SessionContext{}
	raw := `{"status":"running","progress":42,"live_browser_url":"https://6080-abc.qm-cloud-sndbx.app/vnc.html?autoconnect=true&resize=scale"}`
	captureLiveURL(sctx, raw)
	if sctx.LastLiveURL == "" {
		t.Fatal("expected LastLiveURL to be set from status payload")
	}
	if sctx.LastLiveURL[:5] != "https" {
		t.Errorf("captured URL should be the https one; got %q", sctx.LastLiveURL)
	}
}

// Field absent → no-op; sctx untouched. Crawl status responses don't
// include the URL until the sandbox boots, and we shouldn't clear a
// previously-captured URL just because one poll missed it.
func TestCaptureLiveURLNoopWhenMissing(t *testing.T) {
	sctx := &SessionContext{LastLiveURL: "previously-set"}
	captureLiveURL(sctx, `{"status":"queued","progress":5}`)
	if sctx.LastLiveURL != "previously-set" {
		t.Errorf("LastLiveURL should not be cleared by URL-less payload; got %q", sctx.LastLiveURL)
	}
}

// Empty string field → also no-op. The server sometimes returns the
// field with an empty value mid-flight (URL not yet resolved).
func TestCaptureLiveURLNoopWhenEmpty(t *testing.T) {
	sctx := &SessionContext{LastLiveURL: "previously-set"}
	captureLiveURL(sctx, `{"status":"running","live_browser_url":""}`)
	if sctx.LastLiveURL != "previously-set" {
		t.Errorf("empty URL should not overwrite previous; got %q", sctx.LastLiveURL)
	}
}

// Non-JSON or malformed payloads must not panic or scribble over state.
func TestCaptureLiveURLNoopOnBadJSON(t *testing.T) {
	sctx := &SessionContext{LastLiveURL: "kept"}
	captureLiveURL(sctx, "not json at all")
	if sctx.LastLiveURL != "kept" {
		t.Errorf("malformed JSON should be a no-op; got %q", sctx.LastLiveURL)
	}
}

// nil sctx → must not panic. The dispatch path occasionally calls with
// a nil sctx in early initialisation.
func TestCaptureLiveURLNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("captureLiveURL panicked on nil sctx: %v", r)
		}
	}()
	captureLiveURL(nil, `{"live_browser_url":"https://x"}`)
}
