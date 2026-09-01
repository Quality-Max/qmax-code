package gate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeResponse struct {
	output string
	err    error
}

type fakeRunner struct {
	responses map[string]fakeResponse
	calls     []string
}

func (f *fakeRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, key)
	response, ok := f.responses[key]
	if !ok {
		return "", errors.New("unexpected command: " + key)
	}
	return response.output, response.err
}

func TestRunPassesSharedGoGate(t *testing.T) {
	dir := goRepository(t)
	runner := successfulRunner()
	var progress []string

	result := Run(context.Background(), Options{
		Base:   "origin/main",
		Dir:    dir,
		Runner: runner,
		OnProgress: func(message string) {
			progress = append(progress, message)
		},
	})

	if result.Verdict != Pass {
		t.Fatalf("verdict = %s, want PASS; result=%+v", result.Verdict, result)
	}
	if result.ExitCode() != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode())
	}
	if got, want := result.Files, []string{"docs/COMMANDS.md", "internal/gate/gate.go", "main.go"}; !equalStrings(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if len(result.Checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(result.Checks))
	}
	if len(progress) != 4 {
		t.Fatalf("progress events = %v, want scope plus 3 checks", progress)
	}
}

func TestRunReturnsIncompleteWhenBaseCannotResolve(t *testing.T) {
	dir := goRepository(t)
	runner := &fakeRunner{responses: map[string]fakeResponse{
		"git rev-parse --verify --end-of-options missing^{commit}": {output: "fatal: bad revision", err: errors.New("exit status 128")},
	}}

	result := Run(context.Background(), Options{Base: "missing", Dir: dir, Runner: runner})

	if result.Verdict != Incomplete || result.ExitCode() != 2 {
		t.Fatalf("result = %+v, want INCOMPLETE/2", result)
	}
	if !strings.Contains(result.Incomplete, `cannot resolve base "missing"`) {
		t.Fatalf("incomplete reason = %q", result.Incomplete)
	}
	if !strings.Contains(result.Incomplete, "fatal: bad revision") {
		t.Fatalf("incomplete reason lacks bounded command evidence: %q", result.Incomplete)
	}
	if len(result.Checks) != 0 {
		t.Fatalf("checks ran after scope failure: %+v", result.Checks)
	}
}

func TestRunFailsAndRedactsCheckEvidence(t *testing.T) {
	dir := goRepository(t)
	runner := successfulRunner()
	runner.responses["go test ./..."] = fakeResponse{
		output: "TestBroken failed\napi_key=do-not-print",
		err:    errors.New("exit status 1"),
	}

	result := Run(context.Background(), Options{Base: DefaultBase, Dir: dir, Runner: runner})

	if result.Verdict != Fail || result.ExitCode() != 1 {
		t.Fatalf("result = %+v, want FAIL/1", result)
	}
	evidence := result.Checks[1].Evidence
	if strings.Contains(evidence, "do-not-print") || !strings.Contains(evidence, "[REDACTED]") {
		t.Fatalf("evidence was not redacted: %q", evidence)
	}
}

func TestRunMarksMissingToolIncomplete(t *testing.T) {
	dir := goRepository(t)
	runner := successfulRunner()
	runner.responses["go vet ./..."] = fakeResponse{err: ErrToolUnavailable}

	result := Run(context.Background(), Options{Base: DefaultBase, Dir: dir, Runner: runner})

	if result.Verdict != Incomplete || result.Checks[2].Status != CheckIncomplete {
		t.Fatalf("result = %+v, want incomplete tooling result", result)
	}
}

func TestRunWithoutSupportedManifestIsIncomplete(t *testing.T) {
	runner := successfulRunner()
	result := Run(context.Background(), Options{Base: DefaultBase, Dir: t.TempDir(), Runner: runner})
	if result.Verdict != Incomplete || !strings.Contains(result.Incomplete, "supports Go") {
		t.Fatalf("result = %+v, want unsupported repository message", result)
	}
}

func TestAggregateVerdict(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   Verdict
	}{
		{name: "all pass", checks: []Check{{Status: CheckPass}}, want: Pass},
		{name: "failure", checks: []Check{{Status: CheckPass}, {Status: CheckFail}}, want: Fail},
		{name: "incomplete dominates", checks: []Check{{Status: CheckFail}, {Status: CheckIncomplete}}, want: Incomplete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := aggregateVerdict(tc.checks); got != tc.want {
				t.Fatalf("aggregateVerdict() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDisplayPathQuotesTerminalControlCharacters(t *testing.T) {
	if got := DisplayPath("safe\nforged"); got != `"safe\nforged"` {
		t.Fatalf("DisplayPath() = %q", got)
	}
}

func TestParseNULTerminatedDropsPartialPathWhenOutputIsTruncated(t *testing.T) {
	files, truncated := parseNULTerminated("one.go\x00partial" + truncationMarker)
	if !truncated {
		t.Fatal("parseNULTerminated() did not report truncation")
	}
	if want := []string{"one.go"}; !equalStrings(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestCleanEvidenceNeutralizesTerminalControlCharacters(t *testing.T) {
	got := cleanEvidence("failure\x1b[2J\rforged", errors.New("exit status 1"))
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\r') {
		t.Fatalf("cleanEvidence() retained terminal controls: %q", got)
	}
	if !strings.Contains(got, `\u001b`) || !strings.Contains(got, `\u000d`) {
		t.Fatalf("cleanEvidence() did not preserve visible evidence: %q", got)
	}
}

func goRepository(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gate\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func successfulRunner() *fakeRunner {
	baseSHA := strings.Repeat("a", 40)
	headSHA := strings.Repeat("b", 40)
	mergeSHA := strings.Repeat("c", 40)
	return &fakeRunner{responses: map[string]fakeResponse{
		"git rev-parse --verify --end-of-options origin/main^{commit}": {output: baseSHA + "\n"},
		"git rev-parse --verify HEAD^{commit}":                         {output: headSHA + "\n"},
		"git merge-base " + baseSHA + " " + headSHA:                    {output: mergeSHA + "\n"},
		"git diff --name-only -z " + mergeSHA + " --":                  {output: "main.go\x00internal/gate/gate.go\x00"},
		"git ls-files --others --exclude-standard -z --":               {output: "docs/COMMANDS.md\x00internal/gate/gate.go\x00"},
		"git diff --check " + mergeSHA + " --":                         {},
		"go test ./...":                                                {},
		"go vet ./...":                                                 {},
	}}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
