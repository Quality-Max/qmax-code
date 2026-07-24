package agent

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// isPlanLimitMessage reports whether an error string looks like a subscription
// plan usage-limit / rate-limit refusal rather than some other failure. Kept
// broad on purpose — providers phrase this differently (Anthropic "usage
// limit", OpenAI "rate limit", Z.AI/GLM "quota") and qmax-code only needs a
// good-enough signal to flag the coding-plan window as exhausted.
func isPlanLimitMessage(s string) bool {
	s = strings.ToLower(s)
	for _, needle := range []string{
		"rate limit",
		"rate-limit",
		"ratelimit",
		"usage limit",
		"usage-limit",
		"quota",
		"too many requests",
		"limit reached",
		"limit exceeded",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// parseResetTime extracts a window reset time from an HTTP response's headers,
// so the plan window can show the provider's real reset instead of qmax-code's
// 5-hour estimate. It understands Retry-After (delta-seconds or HTTP date) and
// the common *-ratelimit-reset headers (epoch seconds, or seconds-from-now for
// small values). Returns the zero time when nothing usable is present.
func parseResetTime(headers map[string]string) time.Time {
	if len(headers) == 0 {
		return time.Time{}
	}
	get := func(key string) string {
		for k, v := range headers {
			if strings.EqualFold(k, key) {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}

	if ra := get("retry-after"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
		if t, err := http.ParseTime(ra); err == nil {
			return t
		}
	}

	for _, key := range []string{
		"anthropic-ratelimit-unified-reset",
		"x-ratelimit-reset",
		"x-ratelimit-reset-requests",
		"x-ratelimit-reset-tokens",
		"ratelimit-reset",
	} {
		v := get(key)
		if v == "" {
			continue
		}
		if epoch, err := strconv.ParseInt(v, 10, 64); err == nil && epoch > 0 {
			// Large values are unix epoch seconds; small ones are a delta.
			if epoch > 1_000_000_000 {
				return time.Unix(epoch, 0)
			}
			return time.Now().Add(time.Duration(epoch) * time.Second)
		}
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Time{}
}
