package main

// Side channel for handing captured live_browser_url values from a CC /
// Codex MCP subprocess back to the parent qmax-code REPL.
//
// In CC/Codex backend mode, MCP tools (run_test, start_crawl) are
// dispatched inside a child `qmax-code serve --mcp` process. That child
// has its own SessionContext, so a URL captured there never reaches the
// parent's auto-launcher. We use a small file with a unique per-session
// path: parent picks it at startup, exports QMAX_LIVE_URL_FILE for the
// subprocess, subprocess writes captured URLs there, parent reads and
// clears it between turns.
//
// One file per parent process keeps multiple concurrent qmax-code
// sessions from stomping on each other. The path lives under the user's
// .qmax-code dir so it gets cleaned up with the rest of the local state.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	liveURLFileOnce sync.Once
	liveURLFileVal  string
)

// liveFeedHoldSeconds is the post-run sandbox-keepalive window we ask the
// server to honour when LiveFeed is on. Long enough to absorb network +
// agent-stream latency between server-side run completion and the moment
// the client opens /browserfeed; short enough that abandoned sandboxes
// don't cost much. Server caps this via QMAX_LIVE_FEED_HOLD_MAX_SECONDS
// (default 600) so a misconfigured client can't pin sandboxes forever.
const liveFeedHoldSeconds = 60

// liveURLFilePath returns the path to the per-process side-channel file,
// computing it once per process. Empty string means we couldn't resolve
// a home directory — caller should treat that as "feature unavailable"
// rather than failing.
func liveURLFilePath() string {
	liveURLFileOnce.Do(func() {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir := filepath.Join(home, ".qmax-code")
		// Best-effort directory creation. If this fails the writes will
		// surface the error individually; we don't want to block startup.
		_ = os.MkdirAll(dir, 0o700)
		liveURLFileVal = filepath.Join(dir, fmt.Sprintf(".live-url-%d", os.Getpid()))
	})
	return liveURLFileVal
}

// liveURLFileForChild returns the path that *this* qmax-code instance
// will read from. In MCP subprocess mode that's whatever the parent
// passed via QMAX_LIVE_URL_FILE; in standalone mode it's the same path
// liveURLFilePath() chose for itself, but no one writes to it so reads
// are a no-op.
func liveURLFileForChild() string {
	if p := strings.TrimSpace(os.Getenv("QMAX_LIVE_URL_FILE")); p != "" {
		return p
	}
	return ""
}

// persistLiveURLForParent writes `url` into the side-channel file the
// parent watches. No-op when the env var isn't set (i.e. we're running
// standalone, where captureLiveURL already wrote to sctx in-process).
// Best-effort: errors are swallowed because failing here would corrupt
// otherwise-valid tool output.
func persistLiveURLForParent(url string) {
	path := liveURLFileForChild()
	if path == "" || url == "" {
		return
	}
	tmp := path + ".tmp"
	// Atomic write via tmp+rename so the parent never reads a partial URL.
	if err := os.WriteFile(tmp, []byte(url), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// drainLiveURLFromChild reads any URL the subprocess wrote to the side
// channel since the last drain, then deletes the file so a stale URL
// from a previous turn can't auto-launch on the next one. Returns ""
// when the file is missing, empty, or unreadable.
func drainLiveURLFromChild() string {
	path := liveURLFilePath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_ = os.Remove(path)
	return strings.TrimSpace(string(data))
}
