package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger provides structured JSON logging to a file.
type Logger struct {
	file    *os.File
	mu      sync.Mutex
	traceID string
}

// NewLogger creates a logger that writes to ~/.qmax-code/logs/session-{id}.log
func NewLogger(sessionID string) *Logger {
	home, err := os.UserHomeDir()
	if err != nil {
		return &Logger{traceID: generateTraceID()}
	}

	dir := filepath.Join(home, ".qmax-code", "logs")
	_ = os.MkdirAll(dir, 0700)

	path := filepath.Join(dir, fmt.Sprintf("session-%s.log", sessionID))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return &Logger{traceID: generateTraceID()}
	}

	return &Logger{file: f, traceID: generateTraceID()}
}

func generateTraceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Log writes a structured log entry.
func (l *Logger) Log(level, component, msg string, fields map[string]interface{}) {
	if l.file == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := map[string]interface{}{
		"ts":        time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level,
		"component": component,
		"trace_id":  l.traceID,
		"msg":       msg,
	}
	for k, v := range fields {
		entry[k] = v
	}

	// Simple JSON line
	data, _ := json.Marshal(entry)
	fmt.Fprintf(l.file, "%s\n", data)
}

// Info logs an info-level message.
func (l *Logger) Info(component, msg string, fields ...map[string]interface{}) {
	f := map[string]interface{}{}
	if len(fields) > 0 {
		f = fields[0]
	}
	l.Log("info", component, msg, f)
}

// Error logs an error-level message.
func (l *Logger) Error(component, msg string, fields ...map[string]interface{}) {
	f := map[string]interface{}{}
	if len(fields) > 0 {
		f = fields[0]
	}
	l.Log("error", component, msg, f)
}

// Close closes the log file.
func (l *Logger) Close() {
	if l.file != nil {
		l.file.Close()
	}
}

// TraceID returns the current trace ID for this session.
func (l *Logger) TraceID() string {
	return l.traceID
}
