package tui

import (
	"fmt"
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
	prompt   string
	status   *StatusInfo
	output   *strings.Builder
	live     *strings.Builder
	revision uint64
	cache    *turnViewportCache
	text     []rune
	cursor   int
	width    int
	height   int
	thinking bool
	activity string
	frame    int
	done     bool
	result   TurnInputResult
	cancelFn func()
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
	m.output.WriteString(text)
	m.live.WriteString(text)
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
		kept := safeOutputSuffix(m.live.String(), keptLiveOutput)
		next := &strings.Builder{}
		next.Grow(len(kept))
		next.WriteString(kept)
		m.live = next
	}
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
	if m.activity != "" {
		b.WriteString(fmt.Sprintf("  %s%s%s\n", ColorDim, m.activity, ColorReset))
	} else if m.thinking {
		frame := spinnerFrames[m.frame%len(spinnerFrames)]
		b.WriteString(fmt.Sprintf("  %s%s agent working...%s\n", ColorDim, frame, ColorReset))
	}
	b.WriteString(input.renderInputBox(string(display), w))
	b.WriteString("\n")
	b.WriteString(menuHintStyle.Render("  Enter queue + interrupt • Ctrl+C cancel turn • type while the agent works"))
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
	// Reserve the bordered input, hint, metrics, bottom bar, and spinner.
	maxLines := m.height - 8
	if maxLines < 3 {
		maxLines = 3
	}
	if len(lines) <= maxLines {
		if m.cache != nil && m.cache.lines != nil {
			return m.cache.wrapped
		}
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
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
