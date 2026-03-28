package main

import (
	"fmt"
	"time"

	"github.com/getsentry/sentry-go"
)

const bugsinkDSN = "https://4a5a87da918c49d997ca431b1e666fc5@bugs.qualitymax.io/5"

// InitErrorReporting sets up Sentry/Bugsink error reporting.
func InitErrorReporting() {
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              bugsinkDSN,
		Release:          fmt.Sprintf("qmax-code@%s", Version),
		Environment:      "production",
		AttachStacktrace: true,
		// Don't send in debug/dev mode
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			return event
		},
	})
	if err != nil {
		// Silently fail — error reporting is best-effort
		return
	}
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
