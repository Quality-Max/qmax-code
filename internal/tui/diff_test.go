package tui

import (
	"strings"
	"testing"
)

func TestComputeDiffModify(t *testing.T) {
	old := "a\nb\nc\nd\ne\n"
	new := "a\nB\nc\nd\ne\n"
	ops := ComputeDiff(old, new)
	added, removed := DiffStat(ops)
	if added != 1 || removed != 1 {
		t.Fatalf("stat = +%d −%d, want +1 −1; ops=%v", added, removed, ops)
	}
	var plus, minus string
	for _, op := range ops {
		if op.T == "+" {
			plus = op.S
		}
		if op.T == "-" {
			minus = op.S
		}
	}
	if plus != "B" || minus != "b" {
		t.Fatalf("changed lines = +%q −%q, want +B −b", plus, minus)
	}
}

func TestComputeDiffNewFile(t *testing.T) {
	ops := ComputeDiff("", "x\ny\n")
	added, removed := DiffStat(ops)
	if added != 2 || removed != 0 {
		t.Fatalf("stat = +%d −%d, want +2 −0", added, removed)
	}
}

func TestComputeDiffDeleteFile(t *testing.T) {
	ops := ComputeDiff("x\ny\n", "")
	added, removed := DiffStat(ops)
	if added != 0 || removed != 2 {
		t.Fatalf("stat = +%d −%d, want +0 −2", added, removed)
	}
}

func TestComputeDiffIdentical(t *testing.T) {
	ops := ComputeDiff("same\nsame\n", "same\nsame\n")
	if out := RenderFileDiff("f", "same\nsame\n", "same\nsame\n"); out != "" {
		t.Fatalf("identical content rendered %q, want empty", out)
	}
	if len(ops) == 0 {
		t.Fatal("ops should include context/ellipsis lines, not panic")
	}
}

func TestComputeDiffCollapsesLongContext(t *testing.T) {
	var oldB, newB strings.Builder
	for i := 0; i < 50; i++ {
		oldB.WriteString("line\n")
	}
	newB.WriteString(oldB.String())
	newB.WriteString("tail\n")
	ops := ComputeDiff(oldB.String(), newB.String())
	added, _ := DiffStat(ops)
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	sawEllipsis := false
	for _, op := range ops {
		if op.T == "…" {
			sawEllipsis = true
		}
	}
	if !sawEllipsis {
		t.Fatalf("expected an ellipsis marker for the 50-line unchanged run, got %v", ops)
	}
}

func TestComputeDiffLargeMiddleDegrades(t *testing.T) {
	// 3k distinct lines on each side exceeds lcsCellCap → whole-block replace.
	var oldB, newB strings.Builder
	for i := 0; i < 3000; i++ {
		oldB.WriteString("o")
		oldB.WriteString(strings.Repeat("x", i%7))
		oldB.WriteString("\n")
		newB.WriteString("n")
		newB.WriteString(strings.Repeat("y", i%7))
		newB.WriteString("\n")
	}
	ops := ComputeDiff(oldB.String(), newB.String())
	added, removed := DiffStat(ops)
	if added != 3000 || removed != 3000 {
		t.Fatalf("degraded stat = +%d −%d, want +3000 −3000", added, removed)
	}
}

func TestRenderFileDiffNewFileHeader(t *testing.T) {
	out := RenderFileDiff("new.go", "", "package main\nfunc f() {}\n")
	if !strings.Contains(out, "new.go") || !strings.Contains(out, "+2") {
		t.Fatalf("header missing path/added stat: %q", out)
	}
	if !strings.Contains(out, "+ package main") {
		t.Fatalf("missing added line: %q", out)
	}
}

func TestRenderFileDiffCapsOutput(t *testing.T) {
	var oldB, newB strings.Builder
	for i := 0; i < 500; i++ {
		oldB.WriteString("old\n")
		newB.WriteString("new\n")
	}
	out := RenderFileDiff("big.txt", oldB.String(), newB.String())
	lines := strings.Count(out, "\n")
	if lines > maxDiffLines+3 { // header + cap marker tolerance
		t.Fatalf("rendered %d lines, want ≤ %d", lines, maxDiffLines+3)
	}
	if !strings.Contains(out, "more diff lines") {
		t.Fatalf("missing truncation marker: %q", tail(out, 80))
	}
}

func TestRenderFileDiffModifyHeader(t *testing.T) {
	out := RenderFileDiff("main.go", "a\n", "b\n")
	if !strings.Contains(out, "~ main.go") {
		t.Fatalf("modify header missing: %q", head(out, 40))
	}
	if !strings.Contains(out, "+1") || !strings.Contains(out, "-1") {
		t.Fatalf("stats missing: %q", head(out, 40))
	}
}

func head(s string, n int) string { return s[:min(n, len(s))] }
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestComputeDiffTrailingNewlineOnly(t *testing.T) {
	out := RenderFileDiff("f.go", "a\nb\n", "a\nb")
	if out == "" {
		t.Fatal("trailing-newline-only change must render, got empty")
	}
	if !strings.Contains(out, "trailing newline removed") {
		t.Fatalf("missing newline marker: %q", out)
	}
	ops := ComputeDiff("a\n", "a")
	added, removed := DiffStat(ops)
	if added != 1 || removed != 1 {
		t.Fatalf("newline-only stat = +%d −%d, want +1 −1", added, removed)
	}
}

func TestComputeDiffOrdersRemovesBeforeAdds(t *testing.T) {
	// Interleavable middle; every contiguous change run must list − lines
	// before + lines.
	ops := ComputeDiff("a\nx\nb\nx\nc\n", "a\ny\nb\ny\nc\n")
	runPlusFirst := false
	sawPlus := false
	for _, op := range ops {
		switch op.T {
		case "-":
			if sawPlus {
				runPlusFirst = true
			}
		case "+":
			sawPlus = true
		case " ", "…":
			sawPlus = false
		}
	}
	if runPlusFirst {
		t.Fatalf("change run has + before −: %v", ops)
	}
}

func TestComputeDiffContextAdjacentToChange(t *testing.T) {
	// Change at the end: shown context must be the lines immediately above.
	ops := ComputeDiff("l1\nl2\nl3\nl4\nend\n", "l1\nl2\nl3\nl4\nEND\n")
	var ctx []string
	for _, op := range ops {
		if op.T == " " {
			ctx = append(ctx, op.S)
		}
	}
	if len(ctx) == 0 || ctx[len(ctx)-1] != "l4" {
		t.Fatalf("context nearest the change should be l4, got %v", ctx)
	}
	// Change at the start: context nearest the change should be l2 (the line
	// right after).
	ops = ComputeDiff("start\nl1\nl2\nl3\nl4\n", "START\nl1\nl2\nl3\nl4\n")
	ctx = nil
	for _, op := range ops {
		if op.T == " " {
			ctx = append(ctx, op.S)
		}
	}
	if len(ctx) == 0 || ctx[0] != "l1" {
		t.Fatalf("context nearest the change should be l1, got %v", ctx)
	}
}
