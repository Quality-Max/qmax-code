package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TurnInputResult is input collected while an agent turn is running.
// A submitted line is queued by the REPL and also interrupts the active turn.
type TurnInputResult struct {
	Text     string
	Pasted   bool
	Canceled bool
}

type turnOutputMsg string
type turnThinkingMsg bool
type turnActivityMsg string
type turnDoneMsg struct{}
type turnTickMsg time.Time

// turnReplaceTailMsg swaps the most recently appended rawLen bytes of the
// output/live buffers for rendered — used to upgrade raw streamed markdown
// to its glamour-rendered form once a text block finishes.
type turnReplaceTailMsg struct {
	rawLen   int
	rendered string
}

// turnMarkMsg records the current position in the live buffer as a
// diff-hunk landmark, letting Alt+↓/Alt+↑ jump straight to it later.
type turnMarkMsg struct{}

const (
	maxLiveOutput       = 64 * 1024
	keptLiveOutput      = 48 * 1024
	maxStoredTurnOutput = 4 * 1024 * 1024
	keptTurnOutput      = 3 * 1024 * 1024
)

const turnOutputTruncatedNotice = "\n  … earlier turn output omitted to keep memory bounded …\n"

type turnViewportCache struct {
	revision uint64
	width    int
	wrapped  string
	lines    []string
	wraps    int
}

type turnViewportModel struct {
	prompt       string
	status       *StatusInfo
	output       *strings.Builder
	live         *strings.Builder
	revision     uint64
	cache        *turnViewportCache
	text         []rune
	cursor       int
	width        int
	height       int
	thinking     bool
	activity     string
	frame        int
	done         bool
	result       TurnInputResult
	cancelFn     func()
	scrollOffset int   // wrapped-display-lines back from the live tail; 0 = following it
	diffMarks    []int // raw-line indices into m.live where a diff block starts
	// crPending is the unsanitized incomplete last line (no trailing '\n'),
	// which may still contain bare '\r' redraw markers. The displayed live
	// buffer holds only the collapsed form; keeping the raw pending line lets
	// a '\r' in a later chunk overwrite this one, matching a real terminal
	// across emit boundaries.
	crPending string
}

func newTurnViewportModel(prompt string, status *StatusInfo, cancelFn func()) turnViewportModel {
	return turnViewportModel{
		prompt:   prompt,
		status:   status,
		output:   &strings.Builder{},
		live:     &strings.Builder{},
		cache:    &turnViewportCache{},
		width:    80,
		height:   24,
		cancelFn: cancelFn,
	}
}

func (m turnViewportModel) Init() tea.Cmd { return turnViewportTick() }

func turnViewportTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return turnTickMsg(t) })
}

func (m turnViewportModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case turnOutputMsg:
		m.appendOutput(string(msg))
		return m, nil
	case turnReplaceTailMsg:
		m.replaceTail(msg.rawLen, msg.rendered)
		return m, nil
	case turnMarkMsg:
		if m.live != nil {
			m.diffMarks = append(m.diffMarks, strings.Count(m.live.String(), "\n"))
		}
		return m, nil
	case turnThinkingMsg:
		m.thinking = bool(msg)
		return m, nil
	case turnActivityMsg:
		m.activity = string(msg)
		return m, nil
	case turnDoneMsg:
		// Preserve text that was still being composed when the backend
		// completed. Do not overwrite a prompt already submitted by Enter if the
		// backend's cancellation races with the queued turnDoneMsg.
		if m.result.Text == "" {
			m.result.Text = strings.TrimSpace(string(m.text))
		}
		m.done = true
		return m, tea.Quit
	case turnTickMsg:
		m.frame++
		return m, turnViewportTick()
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *turnViewportModel) appendOutput(text string) {
	if m.output == nil {
		m.output = &strings.Builder{}
	}
	if m.live == nil {
		m.live = &strings.Builder{}
	}
	m.writeSanitized(text)
	m.revision++

	// Retain enough output to restore useful scrollback after the viewport
	// exits, but place a hard ceiling on unusually verbose CLI turns. Shrinking
	// to a lower watermark avoids repeatedly copying on every subsequent token.
	if m.output.Len() > maxStoredTurnOutput {
		kept := safeOutputSuffix(m.output.String(), keptTurnOutput)
		next := &strings.Builder{}
		next.Grow(len(turnOutputTruncatedNotice) + len(kept))
		next.WriteString(turnOutputTruncatedNotice)
		next.WriteString(kept)
		m.output = next
	}

	// Live redraws retain a much smaller suffix than final scrollback. Keeping
	// this as a separate buffer bounds wrapping work even for a huge line with
	// no newline, where a byte offset into the full ANSI stream would be unsafe.
	if m.live.Len() > maxLiveOutput {
		old := m.live.String()
		kept := safeOutputSuffix(old, keptLiveOutput)
		next := &strings.Builder{}
		next.Grow(len(kept))
		next.WriteString(kept)
		m.live = next
		// diffMarks are line indices into m.live; re-anchor them to the
		// trimmed buffer so a jump doesn't land on the wrong line.
		m.diffMarks = rebaseLineMarks(m.diffMarks, old, kept)
		// The pending incomplete line lives at the tail; reconstruct it
		// from the kept (already-collapsed) suffix so a later '\r' can
		// still overwrite that last displayed line.
		m.crPending = incompleteLine(kept)
	}
}

func (m *turnViewportModel) writeSanitized(text string) {
	write := func(s string) {
		if s == "" {
			return
		}
		m.output.WriteString(s)
		m.live.WriteString(s)
	}

	// Fast path: no carriage returns in flight, so each chunk can append
	// directly. Still track the incomplete last line so a later '\r' can
	// overwrite it.
	if !strings.ContainsRune(m.crPending, '\r') && !strings.ContainsRune(text, '\r') {
		write(text)
		if i := strings.LastIndexByte(text, '\n'); i >= 0 {
			m.crPending = text[i+1:]
		} else {
			m.crPending += text
		}
		return
	}

	text = strings.ReplaceAll(text, "\r\n", "\n")
	if m.crPending != "" {
		dropExactSuffix(m.output, stripCROverwrites(m.crPending))
		dropExactSuffix(m.live, stripCROverwrites(m.crPending))
	}
	combined := m.crPending + text
	if i := strings.LastIndexByte(combined, '\n'); i >= 0 {
		write(stripCROverwrites(combined[:i+1]))
		m.crPending = combined[i+1:]
	} else {
		m.crPending = combined
	}
	write(stripCROverwrites(m.crPending))
}

func dropExactSuffix(b *strings.Builder, suffix string) {
	if b == nil || suffix == "" {
		return
	}
	s := b.String()
	if !strings.HasSuffix(s, suffix) {
		return
	}
	b.Reset()
	b.WriteString(s[:len(s)-len(suffix)])
}

func incompleteLine(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return ""
	}
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// stripCROverwrites collapses text carrying a real terminal's raw '\r'
// in-place progress redraws (git clone, npm install, pip, docker, curl -#,
// etc., piped through a tool call) down to the final state of each line —
// the way a real terminal would display it. Left as literal bytes, a bare
// '\r' in this append-only buffer doesn't erase anything; it just makes the
// real terminal overwrite part of a later, unrelated line once the buffer is
// finally printed, producing garbled/overlapping text.
func stripCROverwrites(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	// Normalize CRLF line endings first so they're left untouched below —
	// only a bare '\r' (not immediately followed by '\n') is a redraw marker.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if !strings.ContainsRune(line, '\r') {
			continue
		}
		// Last non-empty CR-delimited frame. A trailing '\r' (cursor
		// returned, nothing written yet) still shows the previous frame
		// instead of going blank until the next chunk arrives.
		parts := strings.Split(line, "\r")
		chosen := parts[len(parts)-1]
		if chosen == "" {
			for j := len(parts) - 2; j >= 0; j-- {
				if parts[j] != "" {
					chosen = parts[j]
					break
				}
			}
		}
		lines[i] = chosen
	}
	return strings.Join(lines, "\n")
}

// replaceTail swaps the most recently appended rawLen bytes of both the
// scrollback and live buffers for rendered. Used to upgrade raw streamed
// markdown to its glamour-rendered form once a text block completes,
// without disturbing anything emitted before it. rawLen is clamped to each
// buffer's length so a concurrent trim (above) can't underflow the slice.
func (m *turnViewportModel) replaceTail(rawLen int, rendered string) {
	// The tail being replaced includes any unsanitized pending line.
	m.crPending = ""
	trim := func(b *strings.Builder) {
		if b == nil {
			return
		}
		s := b.String()
		n := rawLen
		if n > len(s) {
			n = len(s)
		}
		b.Reset()
		b.WriteString(s[:len(s)-n])
	}
	trim(m.output)
	trim(m.live)
	m.appendOutput(rendered)
}

// rebaseLineMarks re-anchors raw-line-index marks after a buffer's front has
// been trimmed. Marks that fell within the dropped prefix are discarded; the
// rest are shifted down by the number of complete lines removed. When kept
// isn't an exact byte suffix of old (the ANSI-stripping fallback in
// safeOutputSuffix for one huge unbroken line), the marks can't be rebased
// safely, so all of them are dropped rather than risk a dangling index.
func rebaseLineMarks(marks []int, old, kept string) []int {
	if len(marks) == 0 {
		return marks
	}
	if !strings.HasSuffix(old, kept) {
		return nil
	}
	droppedLines := strings.Count(old[:len(old)-len(kept)], "\n")
	out := make([]int, 0, len(marks))
	for _, ln := range marks {
		if ln >= droppedLines {
			out = append(out, ln-droppedLines)
		}
	}
	return out
}

func safeOutputSuffix(out string, keep int) string {
	start := len(out) - keep
	if start <= 0 {
		return out
	}
	if newline := strings.IndexByte(out[start:], '\n'); newline >= 0 {
		return out[start+newline+1:]
	}

	// A very long line has no safe textual boundary. Strip styling before
	// slicing so an ANSI escape sequence can never be cut in half.
	plain := ansi.Strip(out)
	start = len(plain) - keep
	if start < 0 {
		start = 0
	}
	for start < len(plain) && !utf8.RuneStart(plain[start]) {
		start++
	}
	return plain[start:]
}

func (m turnViewportModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		if msg.Alt {
			m.jumpToMark(-1) // previous diff/code-change block
		} else {
			m.scrollOffset++ // clamped against actual content in visibleOutput
		}
		return m, nil
	case tea.KeyDown:
		if msg.Alt {
			m.jumpToMark(1) // next diff/code-change block
		} else if m.scrollOffset > 0 {
			m.scrollOffset--
		}
		return m, nil
	case tea.KeyPgUp:
		m.scrollOffset += m.outputMaxLines()
		return m, nil
	case tea.KeyPgDown:
		m.scrollOffset -= m.outputMaxLines()
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}
		return m, nil
	case tea.KeyEnter:
		text := strings.TrimSpace(string(m.text))
		if text == "" {
			return m, nil
		}
		m.result.Text = text
		m.result.Canceled = true
		m.done = true
		return m, tea.Sequence(func() tea.Msg {
			if m.cancelFn != nil {
				m.cancelFn()
			}
			return nil
		}, tea.Quit)
	case tea.KeyCtrlC:
		m.result.Canceled = true
		m.done = true
		return m, tea.Sequence(func() tea.Msg {
			if m.cancelFn != nil {
				m.cancelFn()
			}
			return nil
		}, tea.Quit)
	case tea.KeyBackspace:
		if m.cursor > 0 {
			m.text = append(m.text[:m.cursor-1], m.text[m.cursor:]...)
			m.cursor--
		}
	case tea.KeyDelete:
		if m.cursor < len(m.text) {
			m.text = append(m.text[:m.cursor], m.text[m.cursor+1:]...)
		}
	case tea.KeyLeft:
		if msg.Alt {
			m.cursor = previousWordBoundary(m.text, m.cursor)
		} else if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyRight:
		if msg.Alt {
			m.cursor = nextWordBoundary(m.text, m.cursor)
		} else if m.cursor < len(m.text) {
			m.cursor++
		}
	case tea.KeyCtrlLeft:
		m.cursor = previousWordBoundary(m.text, m.cursor)
	case tea.KeyCtrlRight:
		m.cursor = nextWordBoundary(m.text, m.cursor)
	case tea.KeyCtrlA:
		m.cursor = 0
	case tea.KeyCtrlE:
		m.cursor = len(m.text)
	case tea.KeyCtrlW:
		end := m.cursor
		m.cursor = previousWordBoundary(m.text, m.cursor)
		m.text = append(m.text[:m.cursor], m.text[end:]...)
	case tea.KeyCtrlU:
		m.text = nil
		m.cursor = 0
	case tea.KeyCtrlK:
		m.text = m.text[:m.cursor]
	case tea.KeyHome:
		m.cursor = 0
	case tea.KeyEnd:
		m.cursor = len(m.text)
	case tea.KeySpace:
		m.text = append(m.text, 0)
		copy(m.text[m.cursor+1:], m.text[m.cursor:])
		m.text[m.cursor] = ' '
		m.cursor++
	case tea.KeyRunes:
		if msg.Alt && len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'b', 'B':
				m.cursor = previousWordBoundary(m.text, m.cursor)
				return m, nil
			case 'f', 'F':
				m.cursor = nextWordBoundary(m.text, m.cursor)
				return m, nil
			}
		}
		for _, r := range msg.Runes {
			if unicode.IsControl(r) {
				continue
			}
			m.text = append(m.text, 0)
			copy(m.text[m.cursor+1:], m.text[m.cursor:])
			m.text[m.cursor] = r
			m.cursor++
		}
		m.result.Pasted = m.result.Pasted || msg.Paste
	}
	return m, nil
}

func (m turnViewportModel) View() string {
	if m.done {
		return ""
	}

	w := m.width
	if w <= 0 {
		w = 80
	}
	input := inputModel{prompt: m.prompt, width: w, status: m.status}
	display := append([]rune(nil), m.text[:m.cursor]...)
	display = append(display, '█')
	display = append(display, m.text[m.cursor:]...)

	var b strings.Builder
	b.WriteString(m.visibleOutput())
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	if m.scrollOffset > 0 {
		b.WriteString(fmt.Sprintf("  %s↑ scrolled back • ↓ or Alt+↓ toward live%s\n", ColorDim, ColorReset))
	} else if m.activity != "" {
		b.WriteString(fmt.Sprintf("  %s%s%s\n", ColorDim, m.activity, ColorReset))
	} else if m.thinking {
		frame := spinnerFrames[m.frame%len(spinnerFrames)]
		b.WriteString(fmt.Sprintf("  %s%s agent working...%s\n", ColorDim, frame, ColorReset))
	}
	b.WriteString(input.renderInputBox(string(display), w))
	b.WriteString("\n")
	b.WriteString(menuHintStyle.Render("  ↑↓ scroll • PgUp/PgDn page • Alt+↑/↓ prev/next change • Enter queue + interrupt • Ctrl+C cancel"))
	if status := input.renderStatus(w); status != "" {
		b.WriteString("\n")
		b.WriteString(status)
	}
	return b.String()
}

func (m turnViewportModel) visibleOutput() string {
	if m.live == nil {
		return ""
	}
	out := m.live.String()
	width := m.width
	if width <= 0 {
		width = 80
	}

	var lines []string
	if m.cache != nil &&
		m.cache.revision == m.revision &&
		m.cache.width == width &&
		m.cache.lines != nil {
		lines = m.cache.lines
	} else {
		// Bubble Tea truncates over-wide physical lines. Wrap them first so a
		// streamed paragraph remains readable while the persistent panel is active.
		wrapped := ansi.Wrap(out, width, "")
		lines = strings.Split(wrapped, "\n")
		if m.cache != nil {
			m.cache.revision = m.revision
			m.cache.width = width
			m.cache.wrapped = wrapped
			m.cache.lines = lines
			m.cache.wraps++
		}
	}
	if m.height <= 0 {
		return strings.Join(lines, "\n")
	}
	maxLines := m.outputMaxLines()
	if len(lines) <= maxLines {
		if m.cache != nil && m.cache.lines != nil {
			return m.cache.wrapped
		}
		return strings.Join(lines, "\n")
	}

	// scrollOffset counts wrapped lines back from the live tail; 0 (the
	// default) reproduces the old always-show-the-tail behavior exactly.
	maxOffset := len(lines) - maxLines
	offset := m.scrollOffset
	if offset < 0 {
		offset = 0
	} else if offset > maxOffset {
		offset = maxOffset
	}
	end := len(lines) - offset
	return strings.Join(lines[end-maxLines:end], "\n")
}

// outputMaxLines is how many wrapped output lines fit above the persistent
// input panel: height minus the bordered input, hint, metrics, bottom bar,
// and spinner/activity line.
func (m turnViewportModel) outputMaxLines() int {
	h := m.height - 8
	if h < 3 {
		h = 3
	}
	return h
}

// jumpToMark scrolls to the next (dir > 0) or previous (dir < 0) recorded
// diff block relative to the current scroll position, landing with that
// diff's header line at the top of the visible window. Wraps at either end.
func (m *turnViewportModel) jumpToMark(dir int) {
	if len(m.diffMarks) == 0 || m.live == nil {
		return
	}
	width := m.width
	if width <= 0 {
		width = 80
	}
	rawLines := strings.Split(m.live.String(), "\n")

	// wrappedStart[i] is how many wrapped display rows precede raw line i.
	// Wrapping decisions reset at every '\n' (see ansi.Wrap), so wrapping
	// each raw line in isolation reproduces exactly what wrapping the whole
	// buffer would have produced for that line.
	wrappedStart := make([]int, len(rawLines)+1)
	total := 0
	for i, line := range rawLines {
		wrappedStart[i] = total
		total += strings.Count(ansi.Wrap(line, width, ""), "\n") + 1
	}
	wrappedStart[len(rawLines)] = total

	maxLines := m.outputMaxLines()
	targets := make([]int, 0, len(m.diffMarks))
	for _, ln := range m.diffMarks {
		if ln < 0 || ln >= len(wrappedStart) {
			continue
		}
		// visibleOutput shows lines[end-maxLines:end] with
		// end = total - offset. To put raw line ln at the top,
		// end-maxLines == wrappedStart[ln], so
		// offset = total - wrappedStart[ln] - maxLines.
		target := total - wrappedStart[ln] - maxLines
		if target < 0 {
			target = 0
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return
	}
	sort.Ints(targets)

	cur := m.scrollOffset
	if dir < 0 { // older content = larger offset
		for _, t := range targets {
			if t > cur {
				m.scrollOffset = t
				return
			}
		}
		m.scrollOffset = targets[len(targets)-1] // wrap to the oldest
		return
	}
	for i := len(targets) - 1; i >= 0; i-- { // newer content = smaller offset
		if targets[i] < cur {
			m.scrollOffset = targets[i]
			return
		}
	}
	m.scrollOffset = targets[0] // wrap to the newest
}

// RunTurnViewport keeps a Bubble Tea-owned input/status region active while
// run emits streaming output through Terminal. It returns only after run exits.
func RunTurnViewport(term *Terminal, prompt string, status *StatusInfo, cancelFn func(), run func()) TurnInputResult {
	m := newTurnViewportModel(prompt, status, cancelFn)
	p := tea.NewProgram(m)
	term.attachTurnProgram(p)
	done := make(chan struct{})
	go func() {
		defer close(done)
		run()
		p.Send(turnDoneMsg{})
	}()
	result, err := p.Run()
	<-done
	// Keep the terminal attached until the canceled backend has actually
	// stopped. Any late subprocess output is then discarded by the completed
	// Bubble Tea program instead of escaping below the former input panel.
	term.detachTurnProgram(p)
	final, ok := result.(turnViewportModel)
	if ok && final.output != nil {
		// Bubble Tea only owns the live viewport. Re-emit the retained turn output
		// once after it exits so normal terminal scrollback remains useful.
		term.emit(final.output.String())
	}
	if err != nil || !ok {
		return TurnInputResult{}
	}
	return final.result
}
