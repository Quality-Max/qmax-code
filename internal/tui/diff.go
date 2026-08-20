package tui

import "strings"

// DiffLine is a single line of a file diff. T is "+", "-", or " " (context).
type DiffLine struct {
	T string
	S string
}

// maxDiffLines caps how many diff lines RenderFileDiff prints so a full-file
// rewrite cannot flood the transcript.
const maxDiffLines = 40

// lcsCellCap bounds the LCS table; larger middles degrade to a whole-block
// replace (all old lines removed, all new lines added), keeping memory flat.
const lcsCellCap = 4 << 20

// ComputeDiff produces a line-level diff between old and new content.
// Common prefix/suffix are trimmed first (most edits are local); the middle is
// matched with an LCS when small enough. Runs of unchanged lines longer than
// two collapse into an ellipsis marker line ("⋯ N unchanged") emitted as a
// context DiffLine with T "…".
func ComputeDiff(oldContent, newContent string) []DiffLine {
	oldL := splitDiffLines(oldContent)
	newL := splitDiffLines(newContent)

	// Trim common prefix.
	p := 0
	for p < len(oldL) && p < len(newL) && oldL[p] == newL[p] {
		p++
	}
	// Trim common suffix.
	s := 0
	for s < len(oldL)-p && s < len(newL)-p && oldL[len(oldL)-1-s] == newL[len(newL)-1-s] {
		s++
	}
	oldMid := oldL[p : len(oldL)-s]
	newMid := newL[p : len(newL)-s]

	var ops []DiffLine
	ops = append(ops, prefixRun(oldL[:p])...)
	if len(oldMid)*len(newMid) > lcsCellCap {
		ops = append(ops, markRun(len(oldMid))...)
		for _, l := range oldMid {
			ops = append(ops, DiffLine{T: "-", S: l})
		}
		for _, l := range newMid {
			ops = append(ops, DiffLine{T: "+", S: l})
		}
	} else {
		ops = append(ops, lcsDiff(oldMid, newMid)...)
	}
	ops = append(ops, suffixRun(oldL[len(oldL)-s:])...)
	if added, removed := DiffStat(ops); added == 0 && removed == 0 && oldContent != newContent {
		// Only a trailing-newline difference: represent it explicitly so the
		// edit is visible instead of rendering as no change.
		last := ""
		if n := len(oldL); n > 0 {
			last = oldL[n-1]
		}
		if last == "" && len(newL) > 0 {
			last = newL[len(newL)-1]
		}
		marker := "trailing newline added"
		if strings.HasSuffix(oldContent, "\n") && !strings.HasSuffix(newContent, "\n") {
			marker = "trailing newline removed"
		}
		ops = append(ops,
			DiffLine{T: "-", S: last},
			DiffLine{T: "+", S: last},
			DiffLine{T: "\\", S: marker},
		)
	}
	return orderChanges(ops)
}

// orderChanges rewrites each contiguous +/- run so all removed lines precede
// added lines, matching conventional unified-diff ordering.
func orderChanges(ops []DiffLine) []DiffLine {
	out := make([]DiffLine, 0, len(ops))
	i := 0
	for i < len(ops) {
		if ops[i].T != "-" && ops[i].T != "+" {
			out = append(out, ops[i])
			i++
			continue
		}
		j := i
		for j < len(ops) && (ops[j].T == "-" || ops[j].T == "+") {
			j++
		}
		for k := i; k < j; k++ {
			if ops[k].T == "-" {
				out = append(out, ops[k])
			}
		}
		for k := i; k < j; k++ {
			if ops[k].T == "+" {
				out = append(out, ops[k])
			}
		}
		i = j
	}
	return out
}
// lcsDiff diffs two slices via a standard LCS table with op reconstruction.
func lcsDiff(a, b []string) []DiffLine {
	n, m := len(a), len(b)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	var ops []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, DiffLine{T: " ", S: a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, DiffLine{T: "-", S: a[i]})
			i++
		default:
			ops = append(ops, DiffLine{T: "+", S: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, DiffLine{T: "-", S: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, DiffLine{T: "+", S: b[j]})
	}
	return collapseContext(ops)
}

// collapseContext keeps at most two context lines around changes and replaces
// longer unchanged runs with an ellipsis marker.
func collapseContext(ops []DiffLine) []DiffLine {
	const keepCtx = 2
	keep := make([]bool, len(ops))
	for idx, op := range ops {
		if op.T == " " {
			continue
		}
		for k := idx - keepCtx; k <= idx+keepCtx; k++ {
			if k >= 0 && k < len(ops) {
				keep[k] = true
			}
		}
	}
	var out []DiffLine
	runStart := -1
	for idx, op := range ops {
		if op.T == " " && !keep[idx] {
			if runStart < 0 {
				runStart = idx
			}
			continue
		}
		if runStart >= 0 {
			out = append(out, markRun(idx-runStart)...)
			runStart = -1
		}
		out = append(out, op)
	}
	if runStart >= 0 {
		out = append(out, markRun(len(ops)-runStart)...)
	}
	return out
}

// prefixRun renders the trimmed common prefix, keeping the lines closest to
// the change (the last ones) with the ellipsis before them.
func prefixRun(lines []string) []DiffLine {
	return edgeRun(lines, false)
}

// suffixRun renders the trimmed common suffix, keeping the lines closest to
// the change (the first ones) with the ellipsis after them.
func suffixRun(lines []string) []DiffLine {
	return edgeRun(lines, true)
}

func edgeRun(lines []string, keepFirst bool) []DiffLine {
	if len(lines) == 0 {
		return nil
	}
	const keep = 2
	var out []DiffLine
	if keepFirst {
		for i := 0; i < keep && i < len(lines); i++ {
			out = append(out, DiffLine{T: " ", S: lines[i]})
		}
		out = append(out, markRun(len(lines)-keep)...)
		return out
	}
	out = append(out, markRun(len(lines)-keep)...)
	for i := len(lines) - keep; i < len(lines); i++ {
		if i >= 0 {
			out = append(out, DiffLine{T: " ", S: lines[i]})
		}
	}
	return out
}

func markRun(n int) []DiffLine {
	if n <= 0 {
		return nil
	}
	return []DiffLine{{T: "…", S: itoa(n) + " unchanged"}}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// splitDiffLines splits content into lines, dropping the trailing empty element
// produced by a final newline so it does not pollute the diff.
func splitDiffLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// DiffStat returns the added and removed line counts for a diff.
func DiffStat(ops []DiffLine) (added, removed int) {
	for _, op := range ops {
		switch op.T {
		case "+":
			added++
		case "-":
			removed++
		}
	}
	return added, removed
}

// RenderFileDiff renders a compact colored diff for the transcript:
// a header line ("path +A −R") followed by capped +/-/context lines.
// Returns "" when the contents are identical.
func RenderFileDiff(path, oldContent, newContent string) string {
	ops := ComputeDiff(oldContent, newContent)
	added, removed := DiffStat(ops)
	if added == 0 && removed == 0 {
		return ""
	}

	var b strings.Builder
	if removed == 0 {
		b.WriteString(styleSuccess.Render("+ " + path))
	} else {
		b.WriteString(styleTool.Render("~ " + path))
	}
	b.WriteString(" ")
	b.WriteString(styleSuccess.Render("+" + itoa(added)))
	b.WriteString(" ")
	b.WriteString(styleError.Render("-" + itoa(removed)))
	b.WriteString("\n")

	shown := ops
	truncated := 0
	if len(shown) > maxDiffLines {
		truncated = len(shown) - maxDiffLines
		shown = shown[:maxDiffLines]
	}
	for _, op := range shown {
		line := op.S
		if len(line) > 160 {
			line = line[:160] + "…"
		}
		switch op.T {
		case "+":
			b.WriteString(styleSuccess.Render("+ " + line))
		case "-":
			b.WriteString(styleError.Render("- " + line))
		case "…":
			b.WriteString(styleDim.Render("  ⋯ " + line))
		case "\\":
			b.WriteString(styleDim.Render("  \\ " + line))
		default:
			b.WriteString(styleDim.Render("  " + line))
		}
		b.WriteString("\n")
	}
	if truncated > 0 {
		b.WriteString(styleDim.Render("  ⋯ " + itoa(truncated) + " more diff lines"))
		b.WriteString("\n")
	}
	return b.String()
}
