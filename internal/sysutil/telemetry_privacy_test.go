package sysutil

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

func TestTelemetryRequiresBothOptInAndDSN(t *testing.T) {
	old := initSentry
	t.Cleanup(func() { initSentry = old })
	for _, tc := range []struct {
		name, enabled, dsn string
		wantCalls          int
	}{
		{"disabled", "0", "https://public@example.test/1", 0},
		{"missing DSN", "yes", "", 0},
		{"enabled", "TRUE", "https://public@example.test/1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(telemetryEnabledEnv, tc.enabled)
			t.Setenv(telemetryDSNEnv, tc.dsn)
			calls := 0
			initSentry = func(opts sentry.ClientOptions) error {
				calls++
				if opts.Release != "qmax-code@test" || opts.Environment != "production" || !opts.AttachStacktrace || opts.BeforeSend == nil {
					t.Error("telemetry release, stacktrace, or privacy configuration missing")
				}
				return errors.New("fixture initialization failure")
			}
			InitErrorReporting("test") // initialization failures must remain best-effort
			if calls != tc.wantCalls {
				t.Fatalf("initialization calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestEnvEnabledRecognizesAffirmativeValues(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on", "enabled", "0", "false", "", "maybe"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("QMAX_TEST_FLAG", value)
			want := value == "1" || value == "true" || value == "TRUE" || value == "yes" || value == "on" || value == "enabled"
			if EnvEnabled("QMAX_TEST_FLAG") != want {
				t.Fatalf("incorrect flag result for %q", value)
			}
		})
	}
}

func TestTelemetrySanitizerDropsContentAndBoundsMetadata(t *testing.T) {
	if sanitizeEvent(nil, nil) != nil {
		t.Fatal("nil event must remain nil")
	}
	event := &sentry.Event{
		Tags: map[string]string{
			"Prompt": "fixture content", "input.text": "fixture content", "file_path": "fixture path",
			"response": "fixture response", "backend": "ollama", "input_len": "25", "status_code": "500",
			"model": strings.Repeat("m", 300),
		},
		Exception: []sentry.Exception{{Value: strings.Repeat("e", 1100)}, {Value: "short"}},
		Breadcrumbs: []*sentry.Breadcrumb{
			{Message: strings.Repeat("b", 300), Data: map[string]interface{}{"content": "fixture"}},
			{Message: "short", Data: map[string]interface{}{"body": "fixture"}},
		},
	}
	got := sanitizeEvent(event, nil)
	if got != event || len(got.Tags) != 3 || got.Tags["backend"] != "ollama" || got.Tags["status_code"] != "500" {
		t.Fatal("content tags were retained or structural metadata was lost")
	}
	if got.Tags["model"] != strings.Repeat("m", 256)+"…" || got.Exception[0].Value != strings.Repeat("e", 1024)+" …[truncated]" || got.Exception[1].Value != "short" {
		t.Fatal("metadata limits were not applied")
	}
	if got.Breadcrumbs[0].Message != strings.Repeat("b", 256)+"…" || got.Breadcrumbs[1].Message != "short" {
		t.Fatal("breadcrumb message limits were not applied")
	}
	for _, bc := range got.Breadcrumbs {
		if bc.Data != nil {
			t.Fatal("breadcrumb content was retained")
		}
	}
}

// This transport records events synchronously and can never send network traffic.
type recordingTelemetryTransport struct{ events []*sentry.Event }

func (t *recordingTelemetryTransport) Configure(sentry.ClientOptions) {}
func (t *recordingTelemetryTransport) SendEvent(event *sentry.Event) {
	t.events = append(t.events, event)
}
func (t *recordingTelemetryTransport) Flush(time.Duration) bool              { return true }
func (t *recordingTelemetryTransport) FlushWithContext(context.Context) bool { return true }
func (t *recordingTelemetryTransport) Close()                                {}

func TestCaptureRedactsCredentialsAndScopesTags(t *testing.T) {
	transport := &recordingTelemetryTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn: "https://public@example.test/1", Transport: transport, BeforeSend: sanitizeEvent,
		Integrations: func([]sentry.Integration) []sentry.Integration { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	hub := sentry.CurrentHub()
	oldClient := hub.Client()
	hub.PushScope()
	hub.BindClient(client)
	t.Cleanup(func() { hub.BindClient(oldClient); hub.PopScope() })
	const fixtureURL = "https://fixture-user:fixture-password@example.test"
	CaptureError(errors.New("failed at "+fixtureURL), map[string]interface{}{"backend": "ollama", "endpoint": fixtureURL, "prompt": "fixture content"})
	CaptureMessage("retry " + fixtureURL)
	CaptureError(errors.New("without context"), nil)
	CaptureError(nil, nil)
	FlushErrorReporting()
	if len(transport.events) != 4 {
		t.Fatalf("captured %d events, want 4 including Sentry's nil-error diagnostic", len(transport.events))
	}
	first := transport.events[0]
	if first.Tags["backend"] != "ollama" || first.Tags["prompt"] != "" || !strings.Contains(first.Tags["endpoint"], "REDACTED") {
		t.Fatal("capture failed to sanitize context tags")
	}
	if len(first.Exception) != 1 || strings.Contains(first.Exception[0].Value, "fixture-password") || !strings.Contains(first.Exception[0].Value, "REDACTED") {
		t.Fatal("captured error retained fixture credentials")
	}
	second := transport.events[1]
	if strings.Contains(second.Message, "fixture-password") || !strings.Contains(second.Message, "REDACTED") || len(second.Tags) != 0 {
		t.Fatal("message retained credentials or tags leaked out of their scope")
	}
	for _, value := range []interface{}{errors.New("fixture panic"), "fixture panic"} {
		func() {
			defer func() {
				if recovered := recover(); recovered != value {
					t.Errorf("RecoverPanic changed the original panic")
				}
			}()
			defer RecoverPanic()
			panic(value)
		}()
	}
	if len(transport.events) != 6 {
		t.Fatal("panic events were not captured")
	}
	func() { defer RecoverPanic() }() // normal completion must not generate an event
	if len(transport.events) != 6 {
		t.Fatal("normal completion generated a panic event")
	}
}
