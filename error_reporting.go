package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

const bugsinkDSN = "https://4a5a87da918c49d997ca431b1e666fc5@bugs.qualitymax.io/5"

// Privacy promise (mirrors privacy policy): qmax-code never sends prompt content,
// file contents, LLM responses, or shell output to Bugsink. Only structural
// metadata (backend, status_code, model, input_len, image_count, …) is captured.
//
// telemetryDeniedTagPrefixes is the defense-in-depth filter: if a future code
// path accidentally adds a tag whose name matches one of these prefixes, the
// BeforeSend hook strips it before the event leaves the process.
var telemetryDeniedTagPrefixes = []string{
	"input", "prompt", "message", "content", "file", "output",
	"response", "body", "text", "data",
}

// InitErrorReporting sets up Sentry/Bugsink error reporting.
func InitErrorReporting() {
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              bugsinkDSN,
		Release:          fmt.Sprintf("qmax-code@%s", Version),
		Environment:      "production",
		AttachStacktrace: true,
		BeforeSend:       sanitizeEvent,
	})
	if err != nil {
		// Silently fail — error reporting is best-effort
		return
	}
}

// sanitizeEvent is the last-line filter before any event leaves the process.
// Strips tags that look like user content even if upstream code accidentally
// added them. Truncates exception messages and breadcrumbs to bounded length
// to avoid leaking large blobs (e.g. echoed-back prompt content in a 4KB
// Anthropic API error body).
func sanitizeEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil {
		return nil
	}

	// Filter tags by deny-then-allow.
	clean := make(map[string]string, len(event.Tags))
	for k, v := range event.Tags {
		lk := strings.ToLower(k)
		denied := false
		for _, d := range telemetryDeniedTagPrefixes {
			if lk == d || strings.HasPrefix(lk, d+"_") || strings.HasPrefix(lk, d+".") {
				denied = true
				break
			}
		}
		if denied {
			continue
		}
		// Cap tag value length defensively (Sentry limit is 200; we cap at 256).
		if len(v) > 256 {
			v = v[:256] + "…"
		}
		clean[k] = v
	}
	event.Tags = clean

	// Cap exception messages to 1 KiB so a large Anthropic error body can't
	// smuggle prompt content through the message field.
	for i, ex := range event.Exception {
		if len(ex.Value) > 1024 {
			event.Exception[i].Value = ex.Value[:1024] + " …[truncated]"
		}
	}

	// Cap breadcrumb messages similarly and drop any data payloads.
	for i, bc := range event.Breadcrumbs {
		if len(bc.Message) > 256 {
			event.Breadcrumbs[i].Message = bc.Message[:256] + "…"
		}
		event.Breadcrumbs[i].Data = nil
	}

	return event
}

// FlushErrorReporting flushes pending events before exit.
func FlushErrorReporting() {
	sentry.Flush(2 * time.Second)
}

// CaptureError reports a non-fatal error to Bugsink.
func CaptureError(err error, context map[string]interface{}) {
	if context != nil {
		sentry.WithScope(func(scope *sentry.Scope) {
			for k, v := range context {
				scope.SetTag(k, fmt.Sprintf("%v", v))
			}
			sentry.CaptureException(err)
		})
	} else {
		sentry.CaptureException(err)
	}
}

// CaptureMessage reports an informational message to Bugsink.
func CaptureMessage(msg string) {
	sentry.CaptureMessage(msg)
}

// RecoverPanic catches panics and reports them to Bugsink before re-panicking.
func RecoverPanic() {
	if r := recover(); r != nil {
		err, ok := r.(error)
		if !ok {
			err = fmt.Errorf("panic: %v", r)
		}
		sentry.CurrentHub().Recover(err)
		sentry.Flush(3 * time.Second)
		panic(r) // re-panic after reporting
	}
}
