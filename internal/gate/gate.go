// Package gate implements the deterministic, local PR quality gate shared by
// qmax-code's CLI and interactive console.
package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/qualitymax/qmax-code/internal/security"
)

const (
	DefaultBase    = "origin/main"
	DefaultTimeout = 10 * time.Minute
)

// Verdict is the gate's stable merge decision.
type Verdict string

const (
	Pass       Verdict = "PASS"
	Fail       Verdict = "FAIL"
	Incomplete Verdict = "INCOMPLETE"
)

// CheckStatus describes the result of one deterministic check.
type CheckStatus string

const (
	CheckPass       CheckStatus = "PASS"
	CheckFail       CheckStatus = "FAIL"
	CheckIncomplete CheckStatus = "INCOMPLETE"
)

// Check records one command without retaining unbounded command output.
type Check struct {
	Name     string
	Command  string
	Status   CheckStatus
	Duration time.Duration
	Evidence string
}

// Result is shared by all gate presentation surfaces.
type Result struct {
	Base      string
	MergeBase string
	Head      string
	Files     []string
	// FilesTruncated indicates that Files contains only the complete paths that
	// fit inside the bounded command-output budget.
	FilesTruncated bool
	Checks         []Check
	Verdict        Verdict
	Incomplete     string
}

// ExitCode follows the documented command contract.
func (r Result) ExitCode() int {
	switch r.Verdict {
	case Pass:
		return 0
	case Fail:
		return 1
	default:
		return 2
	}
}

// Options controls local gate execution.
type Options struct {
	Base       string
	Dir        string
	Timeout    time.Duration
	Runner     Runner
	OnProgress func(string)
}

// Run scopes the complete local candidate diff and runs the repository checks.
// It does not modify source files, invoke an inference backend, or contact
// QualityMax.
func Run(ctx context.Context, opts Options) Result {
	base := strings.TrimSpace(opts.Base)
	if base == "" {
		base = DefaultBase
	}
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}

	result := Result{Base: base, Verdict: Incomplete}
	if err := validateBase(base); err != nil {
		result.Incomplete = err.Error()
		return result
	}

	progress(opts, "scoping local changes against "+base)
	baseSHA, err := runRequired(ctx, runner, dir, timeout, "git", "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		result.Incomplete = fmt.Sprintf("cannot resolve base %q: %s", base, safeError(err))
		return result
	}
	headSHA, err := runRequired(ctx, runner, dir, timeout, "git", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		result.Incomplete = "cannot resolve HEAD: " + safeError(err)
		return result
	}
	mergeBase, err := runRequired(ctx, runner, dir, timeout, "git", "merge-base", strings.TrimSpace(baseSHA), strings.TrimSpace(headSHA))
	if err != nil {
		result.Incomplete = fmt.Sprintf("cannot find a merge base with %q: %s", base, safeError(err))
		return result
	}
	result.MergeBase = strings.TrimSpace(mergeBase)
	result.Head = strings.TrimSpace(headSHA)

	fileOutput, err := runRequired(ctx, runner, dir, timeout, "git", "diff", "--name-only", "-z", result.MergeBase, "--")
	if err != nil {
		result.Incomplete = "cannot read changed-file scope: " + safeError(err)
		return result
	}
	untrackedOutput, err := runRequired(ctx, runner, dir, timeout, "git", "ls-files", "--others", "--exclude-standard", "-z", "--")
	if err != nil {
		result.Incomplete = "cannot read untracked-file scope: " + safeError(err)
		return result
	}
	trackedFiles, trackedTruncated := parseNULTerminated(fileOutput)
	untrackedFiles, untrackedTruncated := parseNULTerminated(untrackedOutput)
	result.Files = mergeFiles(trackedFiles, untrackedFiles)
	result.FilesTruncated = trackedTruncated || untrackedTruncated

	checks := []commandCheck{{
		name:    "diff integrity",
		command: "git diff --check " + result.MergeBase,
		binary:  "git",
		args:    []string{"diff", "--check", result.MergeBase, "--"},
	}}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		checks = append(checks,
			commandCheck{name: "Go tests", command: "go test ./...", binary: "go", args: []string{"test", "./..."}},
			commandCheck{name: "Go vet", command: "go vet ./...", binary: "go", args: []string{"vet", "./..."}},
		)
	} else if errors.Is(err, os.ErrNotExist) {
		result.Incomplete = "no supported repository checks found (the gate MVP currently supports Go repositories)"
		return result
	} else {
		result.Incomplete = "cannot inspect repository manifest: " + safeError(err)
		return result
	}

	for _, spec := range checks {
		progress(opts, "running "+spec.command)
		result.Checks = append(result.Checks, runCheck(ctx, runner, dir, timeout, spec))
	}
	result.Verdict = aggregateVerdict(result.Checks)
	return result
}

type commandCheck struct {
	name    string
	command string
	binary  string
	args    []string
}

func runCheck(ctx context.Context, runner Runner, dir string, timeout time.Duration, spec commandCheck) Check {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	output, err := runner.Run(checkCtx, dir, spec.binary, spec.args...)
	check := Check{Name: spec.name, Command: spec.command, Duration: time.Since(started)}
	if err == nil {
		check.Status = CheckPass
		return check
	}
	check.Evidence = cleanEvidence(output, err)
	if errors.Is(err, ErrToolUnavailable) || errors.Is(err, ErrTimedOut) || errors.Is(err, context.Canceled) {
		check.Status = CheckIncomplete
	} else {
		check.Status = CheckFail
	}
	return check
}

func runRequired(ctx context.Context, runner Runner, dir string, timeout time.Duration, name string, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := runner.Run(commandCtx, dir, name, args...)
	if err != nil {
		if evidence := cleanEvidence(output, nil); evidence != "" {
			return output, fmt.Errorf("%w: %s", err, evidence)
		}
	}
	return output, err
}

func aggregateVerdict(checks []Check) Verdict {
	verdict := Pass
	for _, check := range checks {
		switch check.Status {
		case CheckIncomplete:
			return Incomplete
		case CheckFail:
			verdict = Fail
		}
	}
	return verdict
}

func validateBase(base string) error {
	if base == "" {
		return errors.New("base ref cannot be empty")
	}
	if len(base) > 256 {
		return errors.New("base ref is too long")
	}
	if strings.HasPrefix(base, "-") {
		return fmt.Errorf("invalid base ref %q", base)
	}
	for _, r := range base {
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return fmt.Errorf("invalid base ref %q", base)
		}
	}
	return nil
}

func parseNULTerminated(output string) ([]string, bool) {
	truncated := strings.HasSuffix(output, truncationMarker)
	if truncated {
		output = strings.TrimSuffix(output, truncationMarker)
		if lastTerminator := strings.LastIndexByte(output, 0); lastTerminator >= 0 {
			output = output[:lastTerminator+1]
		} else {
			output = ""
		}
	}
	parts := strings.Split(output, "\x00")
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			files = append(files, part)
		}
	}
	return files, truncated
}

func mergeFiles(groups ...[]string) []string {
	unique := make(map[string]struct{})
	for _, group := range groups {
		for _, file := range group {
			unique[file] = struct{}{}
		}
	}
	files := make([]string, 0, len(unique))
	for file := range unique {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

func cleanEvidence(output string, err error) string {
	evidence := strings.TrimSpace(output)
	if evidence == "" && err != nil {
		evidence = err.Error()
	}
	return sanitizeTerminalText(security.RedactSensitive(evidence))
}

func safeError(err error) string {
	if err == nil {
		return "unknown error"
	}
	return sanitizeTerminalText(security.RedactSensitive(err.Error()))
}

func sanitizeTerminalText(text string) string {
	var safe strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			safe.WriteRune(r)
		case unicode.IsControl(r) || unicode.In(r, unicode.Cf):
			if r <= 0xffff {
				fmt.Fprintf(&safe, "\\u%04x", r)
			} else {
				fmt.Fprintf(&safe, "\\U%08x", r)
			}
		default:
			safe.WriteRune(r)
		}
	}
	return safe.String()
}

func progress(opts Options, message string) {
	if opts.OnProgress != nil {
		opts.OnProgress(message)
	}
}

// DisplayPath quotes paths so unusual filenames cannot inject terminal lines.
func DisplayPath(path string) string {
	return strconv.QuoteToASCII(path)
}
