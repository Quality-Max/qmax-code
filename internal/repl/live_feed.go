package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
	"github.com/qualitymax/qmax-code/internal/vnc"
)

// maybeLaunchLiveFeed opens /browserfeed using a pre-established VNCStream
// (from the mid-turn pre-connect goroutine) or by dialling fresh from the
// captured URL. When neither a URL nor a pre-stream is available but a
// pendingExecID is set, it polls CheckTestStatus directly (bypassing the LLM)
// until the live_browser_url appears — eliminating the 60–90s REPL freeze
// caused by E2B cold-start blocking the MCP subprocess.
//
// Idempotent: no-op when LiveFeed is off. Clears LastLiveURL on success so
// a stale URL from a previous run doesn't auto-launch on the next turn.
func maybeLaunchLiveFeed(sctx *api.SessionContext, term *tui.Terminal, preStream *vnc.VNCStream, pendingExecID string) {
	if sctx == nil || !sctx.LiveFeed {
		if preStream != nil {
			preStream.Close()
		}
		return
	}

	url := sctx.LastLiveURL

	// Fast-return path: run_test wrote an execution_id and returned immediately.
	// Poll the API directly here (post-LLM-turn) so the user sees the feed as
	// soon as the sandbox is ready rather than waiting inside the MCP tool call.
	if url == "" && pendingExecID != "" && sctx.API != nil {
		term.PrintSystem(fmt.Sprintf("Test started — waiting for live browser feed (exec: %s)...", pendingExecID))
		url = waitForLiveFeedURL(sctx.API, pendingExecID, 5*time.Minute)
	}

	if url == "" {
		if preStream != nil {
			preStream.Close()
		}
		// Only diagnose if the agent actually ran a tool this turn that
		// reported sandbox mode (i.e. a run/crawl actually happened).
		// Otherwise the user just chatted and we shouldn't nag.
		if !sctx.SandboxModeLogged && pendingExecID == "" {
			return
		}
		if pendingExecID != "" {
			term.PrintSystem("Live feed: sandbox did not expose a live_browser_url within 5 minutes.")
		} else {
			term.PrintSystem("Live feed was on, but no live_browser_url came back this turn.")
			if sctx.SandboxFallbackSeen {
				term.PrintSystem("  Server reported is_e2b=false. Most common reason:")
				term.PrintSystem("   • Script has agent_id set → server silently rejects the use_e2b combo")
			} else {
				term.PrintSystem("  Possible causes:")
				term.PrintSystem("   • Server's E2B_API_KEY env var isn't configured (check /api/playwright-execution/health)")
				term.PrintSystem("   • VNC stack failed to start in the sandbox (server logs: 'VNC setup FAILED')")
				term.PrintSystem("   • Script has agent_id set → server silently rejects the use_e2b combo")
			}
		}
		return
	}

	sctx.LastLiveURL = ""
	term.PrintSystem("Opening live browser feed... (Ctrl+] to return)")

	// Determine which stream to use. When we have a pendingExecID, dial a
	// fresh stream so we can monitor test status and auto-close it the moment
	// the test finishes — avoids the "black screen until sandbox teardown" hang.
	stream := preStream
	if stream == nil {
		var dialErr error
		stream, dialErr = vnc.DialVNC(context.Background(), url, 10)
		if dialErr != nil {
			term.PrintError(fmt.Sprintf("browserfeed: connect: %v", dialErr))
			sctx.LastLiveURL = url
			return
		}
	}

	// When tracking a specific execution, close the stream as soon as the test
	// reaches a terminal status — this triggers streamClosedMsg → tea.Quit so
	// the feed exits automatically instead of showing a black screen.
	if pendingExecID != "" && sctx.API != nil {
		api := sctx.API
		execID := pendingExecID
		go func() {
			for {
				time.Sleep(2 * time.Second)
				raw := api.CheckTestStatus(context.Background(), execID)
				var sm map[string]interface{}
				if json.Unmarshal([]byte(raw), &sm) != nil {
					continue
				}
				if st, _ := sm["status"].(string); st == "passed" || st == "failed" || st == "completed" {
					stream.Close()
					return
				}
			}
		}()
	}

	feedErr := showBrowserFeedFromStream(stream, blockModeQuarter,
		fmt.Sprintf("connected to %s — Ctrl+] to quit", url))
	if feedErr != nil {
		term.PrintError(fmt.Sprintf("browserfeed: %v", feedErr))
		sctx.LastLiveURL = url
	}
}

// waitForLiveFeedURL polls CheckTestStatus until live_browser_url appears or
// the test ends without one. Returns the URL on success, "" on timeout/failure.
func waitForLiveFeedURL(api *api.APIClient, execID string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		statusRaw := api.CheckTestStatus(context.Background(), execID)
		var m map[string]interface{}
		if json.Unmarshal([]byte(statusRaw), &m) != nil {
			continue
		}
		if urlVal, _ := m["live_browser_url"].(string); urlVal != "" {
			return urlVal
		}
		// Test already finished (no sandbox / fast failure) — stop waiting.
		if st, _ := m["status"].(string); st == "passed" || st == "failed" || st == "completed" {
			return ""
		}
	}
	return ""
}
