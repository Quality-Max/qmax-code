// Package fastapply implements the safety guards around "Fast Apply" — applying
// a targeted edit to a file by having a small/cheap model regenerate the ENTIRE
// file in one shot (à la Cursor's apply model). Regenerating the whole file is
// fast and forgiving of fuzzy edit instructions, but it has one dangerous
// failure mode: the model can silently return a truncated or partially-dropped
// file, and writing that result corrupts the file while reporting success.
//
// This package contains the defensive harness that makes that failure mode
// impossible to commit silently. Every guard converts a suspect result into a
// typed error instead of a corrupted write. It is intentionally backend-
// agnostic: the actual model call is injected as a Generator, so Apply/CheckSize
// are pure and fully unit-testable without a live model.
//
// Ported from the e2b coding agent's lib/fastApply.ts (QUA-765).
package fastapply

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Tunables. These mirror the e2b implementation and assume the underlying model
// call is made with an output budget of ~MaxOutputTokens.
const (
	// MaxOutputTokens is the output-token budget Fast Apply assumes for the
	// model call. The whole file is regenerated within this budget.
	MaxOutputTokens = 12_000

	// charsPerToken is a rough chars-per-token estimate for pre-flight sizing.
	charsPerToken = 4

	// MaxFileChars caps the original file size. Fast Apply regenerates the ENTIRE
	// file in a single response, so the file must fit the OUTPUT budget — not just
	// the input context window. Anything larger is guaranteed to truncate, so it is
	// rejected before a model call is spent. ~20% headroom is left for lines the
	// edit adds: 12_000 * 4 * 0.8 = 38_400.
	MaxFileChars = MaxOutputTokens * charsPerToken * 8 / 10

	// MaxInputChars is a secondary guard on total prompt size (file + edit) to
	// avoid input-context errors.
	MaxInputChars = 320_000

	// minRetainedLineRatio / shrinkGuardMinLines drive the drastic-shrink guard.
	// A single intended edit on a large file virtually never removes more than
	// half of it; if the regenerated file shrinks past this ratio it is treated
	// as a botched rewrite. The line floor is deliberately high: on small files a
	// >50% drop is a normal edit, so the guard only fires where it is genuinely
	// anomalous.
	minRetainedLineRatio = 0.5
	shrinkGuardMinLines  = 40
)

// Sentinel errors, one per refusal case. Callers can branch with errors.Is and
// the wrapped messages carry the specifics. A non-nil error from this package
// ALWAYS means "did not write" — never a silently corrupted file.
var (
	ErrFileTooLarge    = errors.New("file too large for fast apply")
	ErrEditTooLarge    = errors.New("edit too large for fast apply")
	ErrTruncated       = errors.New("fast apply output was truncated at the token limit")
	ErrAbnormalFinish  = errors.New("fast apply did not complete cleanly")
	ErrEmptyOutput     = errors.New("fast apply model returned an empty response")
	ErrDrasticShrink   = errors.New("fast apply produced a suspiciously short result")
	remedyUseWriteFile = "use an exact string-replace edit or write the complete updated file instead"
)

// Finish describes how the model's generation terminated. Backends map their own
// stop reasons onto this (see FinishFromAnthropic).
type Finish int

const (
	// FinishOK — the model decided it was done (a clean stop). Only this value
	// allows the regenerated file to be written.
	FinishOK Finish = iota
	// FinishTruncated — generation hit the output-token cap; the file is cut off.
	FinishTruncated
	// FinishOther — any other abnormal termination (content filter, error, …);
	// the output may be partial.
	FinishOther
)

// FinishFromAnthropic maps an Anthropic stop_reason onto a Finish.
//   - end_turn / stop_sequence → FinishOK (model stopped on its own)
//   - max_tokens               → FinishTruncated
//   - anything else            → FinishOther
//
// tool_use never occurs here because Fast Apply passes no tools.
func FinishFromAnthropic(stopReason string) Finish {
	switch stopReason {
	case "end_turn", "stop_sequence":
		return FinishOK
	case "max_tokens":
		return FinishTruncated
	default:
		return FinishOther
	}
}

// SystemPrompt is the instruction given to the regeneration model. Exposed so a
// caller's Generator can reuse the exact wording the harness was tuned against.
const SystemPrompt = `You are a precise code editor. You will receive an original file and an edit to apply.

Rules:
- Return ONLY the complete updated file content — nothing else
- No markdown fences, no explanations, no preamble, no trailing commentary
- Preserve all formatting, indentation, and coding style of the original
- Apply the minimum change needed to implement the edit
- The edit may be a code snippet, a partial diff, or a natural-language instruction`

// BuildPrompt assembles the user prompt from the original file and the edit.
func BuildPrompt(original, edit string) string {
	return fmt.Sprintf("<original_file>\n%s\n</original_file>\n\n<edit>\n%s\n</edit>", original, edit)
}

// Generator runs the underlying single-shot regeneration. It is expected to call
// the model with SystemPrompt + BuildPrompt(original, edit) and an output budget
// of ~MaxOutputTokens, returning the raw text and how generation finished.
// Injecting it keeps the guards backend-agnostic and unit-testable.
type Generator func(ctx context.Context, system, prompt string) (text string, finish Finish, err error)

// CheckSize rejects inputs that cannot be fast-applied BEFORE a (billed) model
// call is spent. Returns ErrFileTooLarge / ErrEditTooLarge on rejection.
func CheckSize(original, edit string) error {
	if len(original) > MaxFileChars {
		return fmt.Errorf("%w (%dk chars; limit ~%dk) — %s",
			ErrFileTooLarge, len(original)/1000, MaxFileChars/1000, remedyUseWriteFile)
	}
	// The prompt size is dominated by file + edit; the wrapper text is negligible.
	if promptLen := len(BuildPrompt(original, edit)); promptLen > MaxInputChars {
		return fmt.Errorf("%w (%dk chars) — %s",
			ErrEditTooLarge, promptLen/1000, remedyUseWriteFile)
	}
	return nil
}

// Apply validates and finalizes a fast-apply result. Given the original file, the
// model's raw regenerated output, and how generation finished, it returns the
// content to write or a typed error. It NEVER returns corrupted content with a
// nil error — every suspect result becomes an error.
//
// Use this directly when you already have the model output (e.g. in tests, or
// when the model call lives elsewhere); use FastApply to run the whole flow.
func Apply(original, modelOutput string, finish Finish) (string, error) {
	// Fast Apply regenerates the ENTIRE file. A truncated or otherwise abnormal
	// finish means `modelOutput` is a partial file — refuse it rather than write a
	// cut-off result that would report bogus success.
	switch finish {
	case FinishTruncated:
		return "", fmt.Errorf("%w (%d tokens) — the file is too large to regenerate safely; %s",
			ErrTruncated, MaxOutputTokens, remedyUseWriteFile)
	case FinishOther:
		return "", fmt.Errorf("%w — refusing to write a possibly partial file; %s",
			ErrAbnormalFinish, remedyUseWriteFile)
	}

	stripped := stripOuterFence(modelOutput)
	if strings.TrimSpace(stripped) == "" {
		return "", fmt.Errorf("%w — the edit was not applied", ErrEmptyOutput)
	}

	updated := preserveTrailingNewline(original, stripped)

	// Defense-in-depth: even within the token budget a small model can silently
	// drop large regions. Reject a drastic shrink rather than write a damaged file.
	origLines := lineCount(original)
	updLines := lineCount(updated)
	if origLines >= shrinkGuardMinLines && float64(updLines) < float64(origLines)*minRetainedLineRatio {
		return "", fmt.Errorf("%w (%d → %d lines) — %s",
			ErrDrasticShrink, origLines, updLines, remedyUseWriteFile)
	}

	return updated, nil
}

// FastApply runs the full flow: pre-flight size check → model regeneration via
// gen → result validation. Returns the content to write or a typed error.
func FastApply(ctx context.Context, original, edit string, gen Generator) (string, error) {
	if err := CheckSize(original, edit); err != nil {
		return "", err
	}
	text, finish, err := gen(ctx, SystemPrompt, BuildPrompt(original, edit))
	if err != nil {
		return "", err
	}
	return Apply(original, text, finish)
}

// fenceRe matches a response wholly wrapped in a single markdown code fence.
// (?s) makes . span newlines; the capture is the fenced body.
var fenceRe = regexp.MustCompile("(?s)^```[^\n]*\n(.+)\n```$")

// stripOuterFence removes a single outermost markdown fence iff the ENTIRE
// (trimmed) response is wrapped in one. On the normal no-fence path it returns
// the text verbatim — trimming there would strip the file's own leading/trailing
// whitespace, including the conventional final newline. Files that merely contain
// fenced blocks in their body are never touched (the fence must bookend the
// whole response).
func stripOuterFence(text string) string {
	if m := fenceRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		return m[1]
	}
	return text
}

// preserveTrailingNewline re-attaches the original file's final newline when the
// model dropped it. This avoids a spurious "no newline at end of file" diff on
// every edit and keeps a no-op edit (updated == original) detectable.
func preserveTrailingNewline(original, stripped string) string {
	if strings.HasSuffix(original, "\n") && !strings.HasSuffix(stripped, "\n") {
		return stripped + "\n"
	}
	return stripped
}

// lineCount matches the JS `s.split("\n").length`: a non-empty string with no
// newline is 1 line; n newlines yield n+1 lines.
func lineCount(s string) int {
	return strings.Count(s, "\n") + 1
}
