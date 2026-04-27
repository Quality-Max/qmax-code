package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
)

const (
	telemetryEnabledEnv = "QMAX_CODE_TELEMETRY"
	telemetryDSNEnv     = "QMAX_CODE_TELEMETRY_DSN"
)

// InitErrorReporting sets up Sentry-compatible error reporting when explicitly
// enabled. Public builds should not report anything unless the user opts in.
func InitErrorReporting() {
	if !envEnabled(telemetryEnabledEnv) {
		return
	}

	dsn := os.Getenv(telemetryDSNEnv)
	if dsn == "" {
		return
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Release:          fmt.Sprintf("qmax-code@%s", Version),
		Environment:      "production",
		AttachStacktrace: true,
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			return event
		},
	})
	if err != nil {
		// Silently fail — error reporting is best-effort
		return
	}
}

func envEnabled(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

// FlushErrorReporting flushes pending events before exit.
func FlushErrorReporting() {
	sentry.Flush(2 * time.Second)
}

// CaptureError reports a non-fatal error to Bugsink.
func CaptureError(err error, context map[string]interface{}) {
	if err != nil {
		err = fmt.Errorf("%s", redactSensitive(err.Error()))
	}
	if context != nil {
		sentry.WithScope(func(scope *sentry.Scope) {
			for k, v := range context {
				scope.SetTag(k, redactSensitive(fmt.Sprintf("%v", v)))
			}
			sentry.CaptureException(err)
		})
	} else {
		sentry.CaptureException(err)
	}
}

// CaptureMessage reports an informational message to Bugsink.
func CaptureMessage(msg string) {
	sentry.CaptureMessage(redactSensitive(msg))
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
