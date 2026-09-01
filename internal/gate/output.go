package gate

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// WriteText renders the stable human-readable gate report.
func WriteText(w io.Writer, result Result) {
	_, _ = fmt.Fprintln(w, "qmax-code gate")
	_, _ = fmt.Fprintf(w, "Base: %s\n", result.Base)
	if result.MergeBase != "" {
		_, _ = fmt.Fprintf(w, "Merge base: %s\n", shortSHA(result.MergeBase))
	}
	if result.Head != "" {
		_, _ = fmt.Fprintf(w, "Head: %s\n", shortSHA(result.Head))
	}
	if result.Incomplete != "" {
		_, _ = fmt.Fprintf(w, "Scope: incomplete — %s\n", result.Incomplete)
	} else if result.FilesTruncated {
		_, _ = fmt.Fprintf(w, "Scope: at least %d changed file(s); list truncated\n", len(result.Files))
		for _, file := range result.Files {
			_, _ = fmt.Fprintf(w, "  %s\n", DisplayPath(file))
		}
	} else {
		_, _ = fmt.Fprintf(w, "Scope: %d changed file(s)\n", len(result.Files))
		for _, file := range result.Files {
			_, _ = fmt.Fprintf(w, "  %s\n", DisplayPath(file))
		}
	}
	if len(result.Checks) > 0 {
		_, _ = fmt.Fprintln(w, "Checks:")
		for _, check := range result.Checks {
			_, _ = fmt.Fprintf(w, "  %-10s %s (%s)\n", check.Status, check.Command, formatDuration(check.Duration))
			if check.Evidence != "" {
				for _, line := range strings.Split(check.Evidence, "\n") {
					_, _ = fmt.Fprintf(w, "    %s\n", line)
				}
			}
		}
	}
	_, _ = fmt.Fprintf(w, "Verdict: %s\n", result.Verdict)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func formatDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1ms"
	}
	return duration.Round(time.Millisecond).String()
}
