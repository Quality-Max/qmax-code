package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Quality-Max/qmax-code/internal/agent"
	"github.com/Quality-Max/qmax-code/internal/api"
)

const (
	armCheckpoint = "checkpoint"
	armControl    = "control"

	task             = "TestReconnect fails in ./reconnect. Find and fix the cause without changing the public API."
	rejectedApproach = "Increase ReconnectTimeout"
	rejectedEvidence = "Raised ReconnectTimeout from 200ms to 2s; TestReconnect still failed the same way after waiting the full 2s. The change was reverted."
	// The request deliberately does not mention the rejected approach.
	continuePrompt = "continue the fix"
)

// seed holds real test output from the fixture, captured once per results
// directory so every run and arm starts from byte-identical history.
type seed struct {
	BaselineOutput      string `json:"baseline_output"`
	LongerTimeoutOutput string `json:"longer_timeout_output"`
}

func loadOrCaptureSeed(outDir string) (seed, error) {
	file := filepath.Join(outDir, "seed.json")
	var s seed
	if data, err := os.ReadFile(file); err == nil {
		return s, json.Unmarshal(data, &s)
	} else if !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	dir, err := os.MkdirTemp("", "handoff-eval-seed-*")
	if err != nil {
		return s, err
	}
	defer os.RemoveAll(dir)
	if err := newRepo(dir); err != nil {
		return s, err
	}
	s.BaselineOutput, _ = goTest(dir)
	if err := setTimeout(dir, rejectedTimeout); err != nil {
		return s, err
	}
	s.LongerTimeoutOutput, _ = goTest(dir)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return s, err
	}
	return s, os.WriteFile(file, data, 0o644)
}

// seedConversation reproduces backend A's attempt: it tried the tempting
// approach, the test still failed, and it reverted. Phase A is scripted so
// both arms and every repeat start from identical history and repository state.
func seedConversation(a *agent.Agent, s seed, arm string, pad int) error {
	activity := func(fields map[string]string) api.Message {
		data, _ := json.Marshal(fields)
		return api.Message{Role: "assistant", Content: string(data)}
	}
	a.AppendHistory(
		api.Message{Role: "user", Content: task},
		activity(map[string]string{"tool": "run_command", "command": "go test ./...", "output": s.BaselineOutput}),
		api.Message{Role: "assistant", Content: "Send gives up after ReconnectTimeout (200ms). That window looks too short for the server to accept a new connection, so I'll try a longer timeout."},
		activity(map[string]string{"tool": "edit_file", "path": timeoutFile, "old_string": baselineTimeout, "new_string": rejectedTimeout}),
		activity(map[string]string{"tool": "run_command", "command": "go test ./...", "output": s.LongerTimeoutOutput}),
		activity(map[string]string{"tool": "edit_file", "path": timeoutFile, "old_string": rejectedTimeout, "new_string": baselineTimeout}),
		api.Message{Role: "assistant", Content: "Raising ReconnectTimeout to 2s did not help: TestReconnect fails the same way after waiting the full 2s. I reverted the change; the cause is still open."},
	)
	for i := 0; i < pad; i++ {
		a.AppendHistory(padding(i)...)
	}
	switch arm {
	case armCheckpoint:
		return a.RejectApproach(rejectedApproach, rejectedEvidence)
	case armControl:
		return nil
	}
	return fmt.Errorf("unknown arm %q", arm)
}

// padding is unrelated follow-up work that buries phase A, so a long session
// exercises the abbreviated handoff instead of inlining the whole history.
// It must not hint at the cause or at the rejected approach.
func padding(i int) []api.Message {
	return []api.Message{
		{Role: "user", Content: fmt.Sprintf("Unrelated question %d: summarize the methods a net.Conn implementation provides.", i+1)},
		{Role: "assistant", Content: strings.Repeat(connSummary, 2)},
	}
}

const connSummary = `A net.Conn is a generic stream-oriented network connection. Read reads data
from the connection into a byte slice and returns the number of bytes read. Write writes a
byte slice to the connection. Close closes the connection; blocked Read or Write calls are
unblocked and return errors. LocalAddr and RemoteAddr report the local and remote network
addresses when known. SetDeadline sets both the read and write deadlines, SetReadDeadline
sets the deadline for future Read calls and any currently blocked Read call, and
SetWriteDeadline does the same for Write. A zero time value means no deadline. Multiple
goroutines may invoke methods on a Conn simultaneously. Implementations include TCPConn,
UDPConn, UnixConn, and IPConn, plus wrappers such as tls.Conn that layer protocols over
another Conn. `
