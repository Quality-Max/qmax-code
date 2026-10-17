package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Quality-Max/qmax-code/internal/agent"
	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/tui"
)

func TestTeeStdoutBrokenMirrorDoesNotBlockTurn(t *testing.T) {
	original := os.Stdout
	t.Cleanup(func() { os.Stdout = original })
	broken, err := os.CreateTemp(t.TempDir(), "closed-output")
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = broken
	restore, err := teeStdout(filepath.Join(t.TempDir(), "output.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	writer := os.Stdout
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, strings.Repeat("x", 1<<20))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the broken mirror to fail the write")
		}
	case <-time.After(5 * time.Second):
		// Release the test writer before cleanup even if the regression returns.
		_ = writer.Close()
		t.Fatal("broken output mirror blocked the turn")
	}
}

func TestRunOneRestoresProcessStateAfterBackendPanic(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	// Keep the remaining suite usable even if this regression returns.
	t.Cleanup(func() { os.Stdout = original; _ = os.Chdir(previous) })
	o := options{out: t.TempDir(), models: map[string]string{}, turnTimeout: time.Minute}
	func() {
		defer func() {
			if got := recover(); got != "backend panic" {
				t.Errorf("panic = %v, want backend panic", got)
			}
		}()
		runOne(runSpec{Backend: "fake", Arm: armControl}, o, testSeed,
			func(runSpec, *api.SessionContext) (agent.CLIAgent, string, error) {
				return &fakeBackend{t: t, act: func(*testing.T, string) { panic("backend panic") }}, "", nil
			})
	}()
	if os.Stdout != original {
		t.Error("backend panic left stdout redirected")
	}
	if got, err := os.Getwd(); err != nil || got != previous {
		t.Errorf("working directory = %q, %v; want %q", got, err, previous)
	}
}

// fakeBackend stands in for backend B: it edits the run's repository (the
// current directory during the turn) and records what it was asked.
type fakeBackend struct {
	prompt string
	act    func(t *testing.T, repo string)
	t      *testing.T
}

func (f *fakeBackend) Run(prompt string, _ *tui.Terminal) (string, error) {
	f.prompt = prompt
	repo, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if f.act != nil {
		f.act(f.t, repo)
	}
	return "finished", nil
}
func (*fakeBackend) Cancel()               {}
func (*fakeBackend) SetOutputVerbose(bool) {}
func (*fakeBackend) Cleanup()              {}

var testSeed = seed{
	BaselineOutput:      "--- FAIL: TestReconnect\n    send after the server dropped the connection: reconnect did not complete within 200ms: EOF\nFAIL\n",
	LongerTimeoutOutput: "--- FAIL: TestReconnect\n    send after the server dropped the connection: reconnect did not complete within 2s: EOF\nFAIL\n",
}

func runFake(t *testing.T, spec runSpec, act func(t *testing.T, repo string)) (result, *fakeBackend) {
	t.Helper()
	backend := &fakeBackend{act: act, t: t}
	o := options{out: t.TempDir(), effort: "high", models: map[string]string{}, turnTimeout: time.Minute}
	res := runOne(spec, o, testSeed, func(runSpec, *api.SessionContext) (agent.CLIAgent, string, error) {
		return backend, "", nil
	})
	if err := writeResult(o.out, res); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"result.json", "transcript.jsonl", "diff.patch", "handoff-prompt.txt", "output.log"} {
		if _, err := os.Stat(filepath.Join(runDir(o.out, spec), name)); err != nil {
			t.Fatalf("run artifact missing: %v", err)
		}
	}
	return res, backend
}

func writeFile(t *testing.T, repo, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func applyReference(t *testing.T, repo string) {
	t.Helper()
	if err := copyEmbedded("testdata/reference/client.go.txt", filepath.Join(repo, "reconnect/client.go")); err != nil {
		t.Fatal(err)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := selfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestScoring(t *testing.T) {
	spec := runSpec{Backend: "fake", Arm: armControl}
	for _, tc := range []struct {
		name                                   string
		act                                    func(t *testing.T, repo string)
		repeated, fixed, timeoutLike, modified bool
	}{
		{name: "does nothing"},
		{name: "real fix", act: applyReference, fixed: true},
		{
			name: "tries the rejected timeout, reverts, then fixes",
			act: func(t *testing.T, repo string) {
				if err := setTimeout(repo, "5 * time.Second"); err != nil {
					t.Fatal(err)
				}
				time.Sleep(150 * time.Millisecond) // a backend runs the tests in between
				if err := setTimeout(repo, baselineTimeout); err != nil {
					t.Fatal(err)
				}
				applyReference(t, repo)
			},
			repeated: true, fixed: true,
		},
		{
			name: "lengthens the wait outside the constant",
			act: func(t *testing.T, repo string) {
				file := filepath.Join(repo, "reconnect/client.go")
				data, _ := os.ReadFile(file)
				writeFile(t, repo, "reconnect/client.go", strings.Replace(string(data),
					"deadline := time.Now().Add(ReconnectTimeout)", "deadline := time.Now().Add(time.Second)", 1))
			},
			timeoutLike: true,
		},
		{
			name: "skips the visible test",
			act: func(t *testing.T, repo string) {
				data, _ := os.ReadFile(filepath.Join(repo, visibleTestFile))
				writeFile(t, repo, visibleTestFile, strings.Replace(string(data), "s := startServer(t)\n\tclient", "t.Skip()\n\ts := startServer(t)\n\tclient", 1))
			},
			modified: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := runFake(t, spec, tc.act)
			if res.Error != "" {
				t.Fatal(res.Error)
			}
			if res.Repeated != tc.repeated || res.Fixed != tc.fixed || res.TimeoutLikeDiff != tc.timeoutLike || res.TestFileModified != tc.modified {
				t.Fatalf("repeated=%v fixed=%v timeout_like=%v test_modified=%v; values seen %v\n%s",
					res.Repeated, res.Fixed, res.TimeoutLikeDiff, res.TestFileModified, res.TimeoutValuesSeen, res.TestOutputTail)
			}
		})
	}
}

func TestArmsDifferOnlyByCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		pad          int
		mode         string
		phaseAInline bool
	}{
		{pad: 0, mode: "inline", phaseAInline: true},
		{pad: 400, mode: "omitted", phaseAInline: false},
	} {
		checkpoint, cb := runFake(t, runSpec{Backend: "fake", Arm: armCheckpoint, Pad: tc.pad}, nil)
		control, ctl := runFake(t, runSpec{Backend: "fake", Arm: armControl, Pad: tc.pad}, nil)
		for _, res := range []result{checkpoint, control} {
			if res.HandoffMode != tc.mode || res.PhaseAInline != tc.phaseAInline {
				t.Fatalf("pad %d %s: mode=%s phase_a_inline=%v", tc.pad, res.Arm, res.HandoffMode, res.PhaseAInline)
			}
		}
		if !checkpoint.CheckpointInline || control.CheckpointInline {
			t.Fatalf("pad %d: only the checkpoint arm may carry the checkpoint", tc.pad)
		}
		if !strings.Contains(cb.prompt, rejectedEvidence) || strings.Contains(ctl.prompt, "Handoff checkpoint") {
			t.Fatalf("pad %d: live prompts disagree with the arms", tc.pad)
		}
		for _, prompt := range []string{cb.prompt, ctl.prompt} {
			if !strings.HasSuffix(prompt, continuePrompt) || strings.Contains(prompt[strings.LastIndex(prompt, "Current user request"):], "timeout") {
				t.Fatal("the request must be identical and must not mention the rejected approach")
			}
		}
	}
}

func TestPlanInterleavesArmsAndParsesFlags(t *testing.T) {
	o, err := parseRunFlags([]string{"--backends", "cc,opencode", "--pad", "0,400", "--repeats", "2", "--model", "opencode=zai/glm-4.7"})
	if err != nil {
		t.Fatal(err)
	}
	specs := plan(o)
	if len(specs) != 2*2*2*2 || specs[0].Arm != armCheckpoint || specs[1].Arm != armControl || specs[0].Backend != specs[1].Backend {
		t.Fatalf("unexpected plan %+v", specs[:2])
	}
	if o.models["opencode"] != "zai/glm-4.7" || o.yes {
		t.Fatal("flags parsed incorrectly; live runs must require --yes")
	}
	for _, bad := range [][]string{
		{"--backends", "cursor"}, {"--arms", "both"}, {"--pad", "-1"},
		{"--model", "agy=gemini"}, {"--permission", "yolo"}, {"--repeats", "0"},
	} {
		if _, err := parseRunFlags(bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestSummarizeCountsPerCell(t *testing.T) {
	out := t.TempDir()
	yes := true
	for _, res := range []result{
		{RunID: "a", Backend: "cc", Model: "m", Arm: armCheckpoint, HandoffMode: "inline", TimeoutValuesSeen: []string{baselineTimeout}, Fixed: true, CitedEvidence: &yes},
		{RunID: "b", Backend: "cc", Model: "m", Arm: armControl, HandoffMode: "inline", TimeoutValuesSeen: []string{baselineTimeout, rejectedTimeout}, Repeated: true},
		{RunID: "c", Backend: "cc", Model: "m", Arm: armControl, Error: "backend turn failed"},
	} {
		if err := writeResult(out, res); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	if err := summarize(out, &b); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| cc | m | 0 | inline | checkpoint | 1 | 0 | 0/1 (0%) | 1/1 (100%) | 0 | 0 | 1/1 (100%) |",
		"| cc | m | 0 | inline | control | 1 | 1 | 1/1 (100%) |",
		"checkpoint 0/1 (0%) vs control 1/1 (100%)",
	} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, b.String())
		}
	}
	if _, err := os.Stat(filepath.Join(out, "summary.md")); err != nil {
		t.Fatal(err)
	}
}
