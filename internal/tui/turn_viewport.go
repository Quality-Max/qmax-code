package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

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

type turnViewportModel struct {
	prompt   string
	status   *StatusInfo
	output   *strings.Builder
	liveFrom int
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
		m.output.WriteString(string(msg))
		// The complete turn is retained for scrollback restoration, but live
		// redraws only need a bounded recent window. Advance at a newline so ANSI
		// sequences and UTF-8 runes are never cut in half.
		const maxLiveOutput = 64 * 1024
		if m.output.Len()-m.liveFrom > maxLiveOutput {
			out := m.output.String()
			target := len(out) - maxLiveOutput
			if newline := strings.IndexByte(out[target:], '\n'); newline >= 0 {
				m.liveFrom = target + newline + 1
			}
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
		// completed. The REPL queues it for the next turn, matching the old
		// concurrent readline behaviour without surrendering the viewport.
		m.result.Text = strings.TrimSpace(string(m.text))
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
	out := m.output.String()[m.liveFrom:]
	width := m.width
	if width <= 0 {
		width = 80
	}
	// Bubble Tea truncates over-wide physical lines. Wrap them first so a
	// streamed paragraph remains readable while the persistent panel is active.
	out = ansi.Wrap(out, width, "")
	if m.height <= 0 {
		return out
	}
	// Reserve the bordered input, hint, metrics, bottom bar, and spinner.
	maxLines := m.height - 8
	if maxLines < 3 {
		maxLines = 3
	}
	lines := strings.Split(out, "\n")
	if len(lines) <= maxLines {
		return out
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
		// Bubble Tea only owns the live viewport. Re-emit the complete turn once
		// after it exits so normal terminal scrollback retains the full response.
		term.emit(final.output.String())
	}
	if err != nil || !ok {
		return TurnInputResult{}
	}
	return final.result
}
