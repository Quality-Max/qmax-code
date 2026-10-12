package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/tui"
)

var knownBackends = []string{"cc", "codex", "agy", "opencode"}

type options struct {
	backends    []string
	arms        []string
	pads        []int
	repeats     int
	models      map[string]string
	effort      string
	permission  string
	out         string
	turnTimeout time.Duration
	yes         bool
}

func parseRunFlags(args []string) (options, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	backends := fs.String("backends", "cc,codex", "comma-separated backends B: "+strings.Join(knownBackends, ", "))
	arms := fs.String("arms", armCheckpoint+","+armControl, "comma-separated arms")
	pads := fs.String("pad", "0,400", "comma-separated counts of unrelated exchanges after phase A; 0 inlines the history, 400 leaves phase A only in the archive file")
	repeats := fs.Int("repeats", 3, "fresh sessions per backend/arm/pad cell")
	models := fs.String("model", "", "per-backend models, e.g. cc=claude-opus-5-5,opencode=<provider/model>; empty uses each CLI's default")
	effort := fs.String("effort", "high", "effort passed to every backend")
	permission := fs.String("permission", "unattended", "cc, agy, opencode permission mode: standard or unattended (standard cannot edit in cc's print mode); Codex uses its own config")
	out := fs.String("out", ".context/handoff-eval", "results directory; existing run results are kept and skipped")
	turnTimeout := fs.Duration("turn-timeout", 20*time.Minute, "cancel a backend turn after this long")
	yes := fs.Bool("yes", false, "execute the plan with live backends (uses their quota)")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	o := options{
		backends: splitList(*backends), arms: splitList(*arms), repeats: *repeats,
		models: map[string]string{}, effort: *effort, permission: *permission,
		out: *out, turnTimeout: *turnTimeout, yes: *yes,
	}
	for _, b := range o.backends {
		if !slices.Contains(knownBackends, b) {
			return o, fmt.Errorf("unknown backend %q (want %s)", b, strings.Join(knownBackends, ", "))
		}
	}
	for _, arm := range o.arms {
		if arm != armCheckpoint && arm != armControl {
			return o, fmt.Errorf("unknown arm %q", arm)
		}
	}
	for _, p := range splitList(*pads) {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return o, fmt.Errorf("invalid pad %q", p)
		}
		o.pads = append(o.pads, n)
	}
	for _, pair := range splitList(*models) {
		backend, model, ok := strings.Cut(pair, "=")
		if !ok || !slices.Contains(o.backends, backend) {
			return o, fmt.Errorf("invalid --model entry %q (want backend=model for a selected backend)", pair)
		}
		o.models[backend] = model
	}
	if o.permission != "standard" && o.permission != "unattended" {
		return o, fmt.Errorf("invalid --permission %q", o.permission)
	}
	if len(o.backends) == 0 || len(o.arms) == 0 || len(o.pads) == 0 || o.repeats < 1 {
		return o, errors.New("backends, arms, pad, and repeats must be non-empty")
	}
	return o, nil
}

type runSpec struct {
	Backend string
	Arm     string
	Pad     int
	Repeat  int
}

func (s runSpec) id() string {
	return fmt.Sprintf("%s-%s-pad%d-r%d", s.Backend, s.Arm, s.Pad, s.Repeat)
}

// plan interleaves arms within each cell so drift in a backend's behavior over
// the session (rate limits, model rollouts) affects both arms alike.
func plan(o options) []runSpec {
	var specs []runSpec
	for repeat := 1; repeat <= o.repeats; repeat++ {
		for _, pad := range o.pads {
			for _, backend := range o.backends {
				for _, arm := range o.arms {
					specs = append(specs, runSpec{Backend: backend, Arm: arm, Pad: pad, Repeat: repeat})
				}
			}
		}
	}
	return specs
}

type result struct {
	RunID       string    `json:"run_id"`
	Backend     string    `json:"backend"`
	Model       string    `json:"model"`
	Effort      string    `json:"effort"`
	Arm         string    `json:"arm"`
	Pad         int       `json:"pad"`
	Repeat      int       `json:"repeat"`
	CLIVersion  string    `json:"cli_version"`
	StartedAt   time.Time `json:"started_at"`
	DurationSec float64   `json:"duration_sec"`

	// What backend B was given, from an offline probe of the same handoff.
	HandoffMode      string `json:"handoff_mode"` // inline | abbreviated | omitted
	PhaseAInline     bool   `json:"phase_a_inline"`
	CheckpointInline bool   `json:"checkpoint_inline"`

	// Repeated is the primary measure: the timeout constant left its baseline
	// at any sampled point, including edits that were later reverted.
	Repeated              bool     `json:"repeated"`
	TimeoutValuesSeen     []string `json:"timeout_values_seen"`
	FirstTimeoutChangeSec float64  `json:"first_timeout_change_sec"`
	// TimeoutLikeDiff flags other wait-lengthening edits for manual review.
	TimeoutLikeDiff  bool     `json:"timeout_like_diff"`
	Fixed            bool     `json:"fixed"`
	TestFileModified bool     `json:"test_file_modified"`
	TestOutputTail   string   `json:"test_output_tail"`
	ToolActivity     []string `json:"tool_activity"`
	FinalResponse    string   `json:"final_response"`
	Error            string   `json:"error,omitempty"`

	// Filled in by a reviewer from transcript.jsonl; null until reviewed.
	CitedEvidence    *bool `json:"cited_evidence"`
	JustifiedRevisit *bool `json:"justified_revisit"`
}

type backendFactory func(spec runSpec, sctx *api.SessionContext) (cli agent.CLIAgent, bin string, err error)

func runCommand(args []string) error {
	o, err := parseRunFlags(args)
	if err != nil {
		return err
	}
	for _, backend := range o.backends {
		if backendBinary(backend) == "" {
			return fmt.Errorf("backend %q is not installed or not on PATH", backend)
		}
	}
	specs := plan(o)
	pending := 0
	for _, spec := range specs {
		if !hasResult(o.out, spec) {
			pending++
		}
	}
	fmt.Printf("Plan: %d runs (%d already recorded) = backends %v × arms %v × pad %v × %d repeats\n",
		len(specs), len(specs)-pending, o.backends, o.arms, o.pads, o.repeats)
	fmt.Printf("Results: %s\n", o.out)
	if !o.yes {
		fmt.Println("Live runs use your logged-in CLIs and their quota. Agents run with the selected")
		fmt.Println("permission mode in throwaway repositories under <out>/runs, but are not sandboxed")
		fmt.Println("beyond what each CLI enforces. Re-run with --yes to execute.")
		return nil
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	s, err := loadOrCaptureSeed(o.out)
	if err != nil {
		return fmt.Errorf("capture seed: %w", err)
	}
	cfg := api.LoadQMaxCodeConfig()
	factory := func(spec runSpec, sctx *api.SessionContext) (agent.CLIAgent, string, error) {
		return newCLI(spec.Backend, o, sctx, cfg)
	}
	for i, spec := range specs {
		if hasResult(o.out, spec) {
			continue
		}
		fmt.Printf("\n=== [%d/%d] %s\n", i+1, len(specs), spec.id())
		res := runOne(spec, o, s, factory)
		if err := writeResult(o.out, res); err != nil {
			return err
		}
		fmt.Printf("\n=== %s: repeated=%v fixed=%v %s\n", spec.id(), res.Repeated, res.Fixed, res.Error)
	}
	return summarize(o.out, os.Stdout)
}

func backendBinary(backend string) string {
	switch backend {
	case "cc":
		return agent.FindClaudeCode()
	case "codex":
		return agent.FindCodex()
	case "agy":
		return agent.FindAgy()
	case "opencode":
		return agent.FindOpenCode()
	}
	return ""
}

// newCLI builds backend B like /orch does, except that Codex and Antigravity
// keep the qmax MCP entry already in their global config: rewriting it here
// would leave the user's normal setup pointing at this temporary binary.
func newCLI(backend string, o options, sctx *api.SessionContext, cfg *api.Config) (agent.CLIAgent, string, error) {
	bin, model := backendBinary(backend), o.models[backend]
	if bin == "" {
		return nil, "", fmt.Errorf("backend %q is not installed", backend)
	}
	switch backend {
	case "cc":
		return agent.NewCCAgent(bin, model, o.effort, o.permission, false, "off", sctx), bin, nil
	case "codex":
		return agent.NewCodexAgent(bin, model, o.effort, false, "off", sctx), bin, nil
	case "agy":
		return agent.NewAgyAgent(bin, model, o.effort, o.permission, false, "off", sctx), bin, nil
	case "opencode":
		return agent.NewOpenCodeAgent(bin, model, o.effort, o.permission, false, cfg, sctx), bin, nil
	}
	return nil, "", fmt.Errorf("unknown backend %q", backend)
}

func runDir(out string, spec runSpec) string { return filepath.Join(out, "runs", spec.id()) }

func hasResult(out string, spec runSpec) bool {
	_, err := os.Stat(filepath.Join(runDir(out, spec), "result.json"))
	return err == nil
}

func runOne(spec runSpec, o options, s seed, factory backendFactory) (res result) {
	res = result{RunID: spec.id(), Backend: spec.Backend, Model: o.models[spec.Backend], Effort: o.effort,
		Arm: spec.Arm, Pad: spec.Pad, Repeat: spec.Repeat, StartedAt: time.Now().UTC(), FirstTimeoutChangeSec: -1}
	if res.Model == "" {
		res.Model = "default"
	}
	fail := func(err error) result {
		res.Error = err.Error()
		return res
	}
	dir := runDir(o.out, spec)
	// A run without result.json was interrupted; start it over cleanly.
	if err := os.RemoveAll(dir); err != nil {
		return fail(err)
	}
	repo, err := filepath.Abs(filepath.Join(dir, "repo"))
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return fail(err)
	}
	if err := newRepo(repo); err != nil {
		return fail(err)
	}

	sctx := &api.SessionContext{LocalOnly: true}
	probePrompt, err := probeHandoff(s, spec)
	if err != nil {
		return fail(fmt.Errorf("probe handoff: %w", err))
	}
	res.HandoffMode, res.PhaseAInline, res.CheckpointInline = describeHandoff(probePrompt)
	if err := os.WriteFile(filepath.Join(dir, "handoff-prompt.txt"), []byte(probePrompt), 0o600); err != nil {
		return fail(err)
	}

	a := &agent.Agent{Cfg: agent.AgentConfig{Context: sctx}}
	defer a.CleanupConversation()
	if err := seedConversation(a, s, spec.Arm, spec.Pad); err != nil {
		return fail(err)
	}
	seedLen := len(a.Conversation.Transcript)
	cli, bin, err := factory(spec, sctx)
	if err != nil {
		return fail(err)
	}
	defer cli.Cleanup()
	if bin != "" {
		version, _ := command("", 30*time.Second, bin, "--version")
		res.CLIVersion = firstLine(version)
	}

	previous, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	if err := os.Chdir(repo); err != nil {
		return fail(err)
	}
	restoreStdout, err := teeStdout(filepath.Join(dir, "output.log"))
	if err != nil {
		_ = os.Chdir(previous)
		return fail(err)
	}
	watcher := watchTimeout(repo)
	timer := time.AfterFunc(o.turnTimeout, cli.Cancel)
	start := time.Now()
	response, runErr := a.RunCLI(cli, continuePrompt, &tui.Terminal{})
	res.DurationSec = time.Since(start).Seconds()
	timer.Stop()
	res.TimeoutValuesSeen, res.FirstTimeoutChangeSec = watcher.Stop()
	restoreStdout()
	if err := os.Chdir(previous); err != nil {
		return fail(err)
	}
	for _, value := range res.TimeoutValuesSeen {
		if value != baselineTimeout {
			res.Repeated = true
		}
	}
	res.FinalResponse = truncate(response, 4000)
	res.ToolActivity = toolActivity(a.Conversation.Transcript[seedLen:], 10)
	if err := writeTranscript(filepath.Join(dir, "transcript.jsonl"), a.Conversation.Transcript); err != nil {
		return fail(err)
	}
	sc, err := scoreRepo(repo)
	if err != nil {
		return fail(err)
	}
	res.TimeoutLikeDiff, res.Fixed, res.TestFileModified = sc.TimeoutLikeDiff, sc.Fixed, sc.TestFileModified
	res.TestOutputTail = tail(sc.TestOutput, 3000)
	if err := os.WriteFile(filepath.Join(dir, "diff.patch"), []byte(sc.Diff), 0o644); err != nil {
		return fail(err)
	}
	if runErr != nil {
		// Keep the scores: a cancelled turn may still have tried the approach.
		res.Error = "backend turn failed: " + runErr.Error()
	}
	return res
}

// probeHandoff renders the exact prompt backend B receives, using a capture
// double so it costs nothing and does not touch the real run's state.
func probeHandoff(s seed, spec runSpec) (string, error) {
	a := &agent.Agent{Cfg: agent.AgentConfig{Context: &api.SessionContext{LocalOnly: true}}}
	defer a.CleanupConversation()
	if err := seedConversation(a, s, spec.Arm, spec.Pad); err != nil {
		return "", err
	}
	capture := &promptCapture{}
	if _, err := a.RunCLI(capture, continuePrompt, nil); err != nil {
		return "", err
	}
	return capture.prompt, nil
}

type promptCapture struct{ prompt string }

func (p *promptCapture) Run(prompt string, _ *tui.Terminal) (string, error) {
	p.prompt = prompt
	return "", nil
}
func (*promptCapture) Cancel()               {}
func (*promptCapture) SetOutputVerbose(bool) {}
func (*promptCapture) Cleanup()              {}

// phaseAConclusion is phase A's final message; whether it reaches B intact
// tells how deeply the padding buried the failed attempt.
const phaseAConclusion = "Raising ReconnectTimeout to 2s did not help"

func describeHandoff(prompt string) (mode string, phaseAInline, checkpointInline bool) {
	switch {
	case strings.Contains(prompt, "earlier entries are omitted"):
		mode = "omitted"
	case strings.Contains(prompt, "private JSONL file"):
		mode = "abbreviated"
	default:
		mode = "inline"
	}
	history := prompt
	if _, after, found := strings.Cut(prompt, "<conversation_history>"); found {
		history = after
	}
	return mode, strings.Contains(history, phaseAConclusion), strings.Contains(prompt, "Handoff checkpoint")
}

// toolActivity returns the first n retained tool entries from B's turn: the
// JSON records each CLI exposes, as opposed to prose or the request itself.
func toolActivity(messages []api.Message, n int) []string {
	var out []string
	for _, msg := range messages {
		text, ok := msg.Content.(string)
		if !ok || msg.Role != "assistant" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(text), &value) != nil {
			continue
		}
		if inner, ok := value.(string); ok {
			text = inner
		}
		out = append(out, truncate(text, 1500))
		if len(out) == n {
			break
		}
	}
	return out
}

func writeTranscript(file string, messages []api.Message) error {
	var b strings.Builder
	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return os.WriteFile(file, []byte(b.String()), 0o600)
}

func writeResult(out string, res result) error {
	dir := filepath.Join(out, "runs", res.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "result.json"), append(data, '\n'), 0o644)
}

// teeStdout mirrors the live turn rendering into a per-run log; the terminal
// renderer writes to os.Stdout at call time, so swapping it captures the turn.
func teeStdout(file string) (func(), error) {
	log, err := os.Create(file)
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		log.Close()
		return nil, err
	}
	original := os.Stdout
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.MultiWriter(original, log), r)
		close(done)
	}()
	return func() {
		w.Close()
		<-done
		os.Stdout = original
		r.Close()
		log.Close()
	}, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
