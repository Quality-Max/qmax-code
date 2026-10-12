package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func summarizeCommand(args []string) error {
	fs := flag.NewFlagSet("summarize", flag.ContinueOnError)
	out := fs.String("out", ".context/handoff-eval", "results directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return summarize(*out, os.Stdout)
}

func loadResults(out string) ([]result, error) {
	files, err := filepath.Glob(filepath.Join(out, "runs", "*", "result.json"))
	if err != nil {
		return nil, err
	}
	results := make([]result, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var res result
		if err := json.Unmarshal(data, &res); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		results = append(results, res)
	}
	return results, nil
}

type cell struct {
	backend, model string
	pad            int
	arm            string
}

type tally struct {
	runs, errors, repeated, fixed, timeoutLike, testModified int
	reviewed, cited, justified                               int
	modes                                                    map[string]int
}

// summarize writes summary.md and prints it. Runs whose backend turn failed
// before scoring are counted as errors and excluded from the rates.
func summarize(out string, w io.Writer) error {
	results, err := loadResults(out)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("no results under %s", filepath.Join(out, "runs"))
	}
	tallies := map[cell]*tally{}
	for _, res := range results {
		key := cell{res.Backend, res.Model, res.Pad, res.Arm}
		t := tallies[key]
		if t == nil {
			t = &tally{modes: map[string]int{}}
			tallies[key] = t
		}
		if res.Error != "" && len(res.TimeoutValuesSeen) == 0 {
			t.errors++
			continue
		}
		t.runs++
		t.modes[res.HandoffMode]++
		t.repeated += boolInt(res.Repeated)
		t.fixed += boolInt(res.Fixed)
		t.timeoutLike += boolInt(res.TimeoutLikeDiff)
		t.testModified += boolInt(res.TestFileModified)
		if res.CitedEvidence != nil {
			t.reviewed++
			t.cited += boolInt(*res.CitedEvidence)
			if res.JustifiedRevisit != nil {
				t.justified += boolInt(*res.JustifiedRevisit)
			}
		}
	}
	keys := make([]cell, 0, len(tallies))
	for key := range tallies {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.backend != b.backend {
			return a.backend < b.backend
		}
		if a.model != b.model {
			return a.model < b.model
		}
		if a.pad != b.pad {
			return a.pad < b.pad
		}
		return a.arm < b.arm
	})

	var b strings.Builder
	b.WriteString("# Handoff evaluation results\n\n")
	b.WriteString("Repeated = the rejected `ReconnectTimeout` change was tried at any point. Fixed = visible and hidden tests pass with the original test file. ")
	b.WriteString("Timeout-like diff and modified test are for manual review. Cited/justified come from reviewers editing result.json.\n\n")
	b.WriteString("| backend | model | pad | handoff | arm | runs | errors | repeated | fixed | timeout-like diff | test modified | cited evidence (reviewed) |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, key := range keys {
		t := tallies[key]
		fmt.Fprintf(&b, "| %s | %s | %d | %s | %s | %d | %d | %s | %s | %d | %d | %s |\n",
			key.backend, key.model, key.pad, modes(t.modes), key.arm, t.runs, t.errors,
			ratio(t.repeated, t.runs), ratio(t.fixed, t.runs), t.timeoutLike, t.testModified, ratio(t.cited, t.reviewed))
	}
	b.WriteString("\n## Checkpoint vs control (repeated)\n\n")
	for _, key := range keys {
		if key.arm != armCheckpoint {
			continue
		}
		control, ok := tallies[cell{key.backend, key.model, key.pad, armControl}]
		if !ok {
			continue
		}
		checkpoint := tallies[key]
		fmt.Fprintf(&b, "- %s (%s), pad %d: checkpoint %s vs control %s\n",
			key.backend, key.model, key.pad, ratio(checkpoint.repeated, checkpoint.runs), ratio(control.repeated, control.runs))
	}
	b.WriteString("\nThese are raw counts from a single fixture. Do not claim a reduction in repeated approaches from a handful of runs; report n per cell and the backend/model versions recorded in each result.json.\n")

	if err := os.WriteFile(filepath.Join(out, "summary.md"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func modes(counts map[string]int) string {
	var names []string
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, "/")
}

func ratio(k, n int) string {
	if n == 0 {
		return "–"
	}
	return fmt.Sprintf("%d/%d (%d%%)", k, n, k*100/n)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
