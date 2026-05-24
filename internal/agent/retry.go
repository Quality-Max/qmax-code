package agent

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/qualitymax/qmax-code/internal/tui"
)

const maxAPIRetries = 3

// doWithRetry executes newReq() and retries up to maxAPIRetries times on HTTP 429
// with exponential backoff starting at 1 s (doubling each attempt). Parses the
// Retry-After response header when present to override the computed delay.
// A status line is printed via term (or to stdout when term is nil) while waiting.
// The context is respected during sleep so a cancellation (Ctrl+C) exits immediately.
func doWithRetry(ctx context.Context, client *http.Client, newReq func() (*http.Request, error), term *tui.Terminal) (*http.Response, error) {
	wait := time.Second

	for attempt := 0; ; attempt++ {
		req, err := newReq()
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests || attempt >= maxAPIRetries {
			return resp, nil
		}

		// 429 — use Retry-After header when provided, otherwise exponential backoff.
		delay := wait
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, parseErr := strconv.Atoi(ra); parseErr == nil && secs > 0 {
				delay = time.Duration(secs) * time.Second
			}
		}
		_ = resp.Body.Close()

		msg := fmt.Sprintf("rate limited — retrying in %.0fs (%d/%d)…", delay.Seconds(), attempt+1, maxAPIRetries)
		if term != nil {
			term.PrintSystem(msg)
		} else {
			fmt.Printf("  ⚠ %s\n", msg)
		}

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		wait *= 2
	}
}
