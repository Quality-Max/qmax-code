package repl

// /browserfeed: experimental ASCII live feed of a QM Cloud Sandbox desktop.
//
// Connects to a noVNC websockify endpoint exposed by a sandbox running
// the cloud desktop template (websockify on port 6080), pulls RFB
// framebuffer updates, and renders them into the terminal as 24-bit
// ANSI quarter-blocks. Each terminal cell encodes a 2×2 source-pixel
// region with a 2-colour partition chosen per cell to minimise within-
// group variance — gives ~4× the resolution of half-blocks at the cost
// of some colour bleed on high-frequency content. Half-block mode is
// available as a fallback (`/browserfeed --half <url>`) for terminals
// that struggle with the wider Unicode set.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/qualitymax/qmax-code/internal/vnc"
)

type blockMode int

const (
	blockModeQuarter blockMode = iota
	blockModeHalf
)

// showBrowserFeed opens the live feed for `url` and blocks until the user
// quits with Ctrl+]. Returns nil on clean exit. mode controls the renderer:
// blockModeQuarter (default, sharper) or blockModeHalf (simpler, more
// portable).
func showBrowserFeed(url string, mode blockMode) error {
	stream, err := vnc.DialVNC(context.Background(), url, 10)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return showBrowserFeedStream(stream, mode, fmt.Sprintf("connected to %s — Ctrl+] to quit", url))
}

// showBrowserFeedFromStream opens the live feed UI using an already-dialled
// VNCStream. Ownership of the stream is transferred; the stream is closed
// when the user quits. statusHint is shown in the status line until the
// first frame arrives.
func showBrowserFeedFromStream(stream *vnc.VNCStream, mode blockMode, statusHint string) error {
	return showBrowserFeedStream(stream, mode, statusHint)
}

func showBrowserFeedStream(stream *vnc.VNCStream, mode blockMode, status string) error {
	cols, rows := bfTermSize()
	m := browserFeedModel{
		stream:   stream,
		termCols: cols,
		termRows: rows,
		mode:     mode,
		status:   status,
	}
	m.updateEffectiveDims()
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)

	// Bridge stream channels → Bubble Tea. Both goroutines exit when their
	// respective channels close (stream.Close() closes both). A WaitGroup
	// ensures they've exited before we return so callers never see a
	// dangling goroutine after showBrowserFeed returns.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for f := range stream.Frames {
			p.Send(frameMsg{frame: f})
		}
		p.Send(streamClosedMsg{})
	}()
	go func() {
		defer wg.Done()
		for e := range stream.Err {
			p.Send(streamErrMsg{err: e})
		}
	}()

	_, runErr := p.Run()
	stream.Close() // cancels context + closes channels → goroutines above exit
	wg.Wait()
	return runErr
}

// ─── Bubble Tea model ────────────────────────────────────────────────────────

type frameMsg struct{ frame *vnc.VNCFrame }
type streamErrMsg struct{ err error }
type streamClosedMsg struct{}

// blockLayout records the geometry of the most recent render so mouse
// events can be mapped back to source-image coordinates.
type blockLayout struct {
	srcW, srcH     int
	dstW, dstH     int     // size of the rendered region in source-pixel units
	leftPadCells   int     // cells of left padding before image starts
	topPadCells    int     // cells of top padding before image starts
	cellSrcWidth   float64 // source pixels covered by one cell horizontally
	cellSrcHeight  float64 // source pixels covered by one cell vertically
	pixelsPerCellX int     // 1 for half-block, 2 for quarter-block
	pixelsPerCellY int     // 2 for both modes
	buttonMask     byte    // current pressed buttons (RFB encoding)
}

// bfSmallFraction is the fraction of the terminal used in small (default) mode.
const bfSmallFraction = 0.6

type browserFeedModel struct {
	stream *vnc.VNCStream

	frame      *vnc.VNCFrame
	termCols   int  // actual terminal width
	termRows   int  // actual terminal height
	cols       int  // effective render width (capped in small mode)
	rows       int  // effective render height
	fullscreen bool // false = small (bfSmallFraction of terminal), true = fill
	mode       blockMode
	frames     int // counter for status line
	layout     blockLayout
	cachedView string // pre-rendered body, refreshed on frame or resize

	status string // last status / error line
}

// updateEffectiveDims recomputes cols/rows from termCols/termRows + fullscreen.
func (m *browserFeedModel) updateEffectiveDims() {
	if m.fullscreen {
		m.cols = m.termCols
		m.rows = m.termRows
	} else {
		m.cols = bfMaxI(int(float64(m.termCols)*bfSmallFraction), 20)
		m.rows = bfMaxI(int(float64(m.termRows)*bfSmallFraction), 8)
	}
}

// rerender updates cachedView + layout from the current frame and dimensions.
// Called from Update (never from View) so the model stays the source of truth.
func (m *browserFeedModel) rerender() {
	if m.frame == nil {
		m.cachedView = ""
		return
	}
	body, layout := renderBlocks(m.frame, m.cols, m.rows-1, m.mode)
	// Bottom-left anchor: prepend extra blank lines so the image sits at the
	// bottom of the actual terminal rather than the top of the small viewport.
	extraPad := m.termRows - m.rows
	if extraPad > 0 {
		body = strings.Repeat("\n", extraPad) + body
		layout.topPadCells += extraPad
	}
	m.cachedView = body
	m.layout = layout
}

func (m browserFeedModel) Init() tea.Cmd { return nil }

func (m browserFeedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termCols, m.termRows = msg.Width, msg.Height
		m.updateEffectiveDims()
		m.rerender()
		return m, nil

	case tea.KeyMsg:
		// Ctrl+] exits. Borrowed from telnet so q, Esc, Ctrl+C stay forwardable.
		if msg.Type == tea.KeyCtrlCloseBracket {
			return m, tea.Quit
		}
		// Ctrl+F toggles small ↔ full-screen rendering.
		if msg.Type == tea.KeyCtrlF {
			m.fullscreen = !m.fullscreen
			m.updateEffectiveDims()
			m.rerender()
			return m, nil
		}
		// Forward everything else to the remote desktop. We don't model
		// modifier-only press/release, so each key is sent as a press +
		// release pair — enough for typing and most shortcuts.
		if sym, ok := keyMsgToKeysym(msg); ok {
			_ = m.stream.SendKey(sym, true)
			_ = m.stream.SendKey(sym, false)
		}
		return m, nil

	case tea.MouseMsg:
		x, y, ok := mouseToSrcPixel(msg.X, msg.Y, m.layout)
		if !ok {
			return m, nil
		}
		m.layout.buttonMask = updateButtonMask(m.layout.buttonMask, msg)
		_ = m.stream.SendPointer(x, y, m.layout.buttonMask)
		// Wheel events are reported once with no release in Bubble Tea, so
		// mimic the X11 convention and immediately drop the wheel bits.
		if isWheel(msg) {
			cleared := m.layout.buttonMask &^ (1<<3 | 1<<4)
			m.layout.buttonMask = cleared
			_ = m.stream.SendPointer(x, y, cleared)
		}
		return m, nil

	case frameMsg:
		m.frame = msg.frame
		m.frames++
		m.rerender()
		modeName := "quarter"
		if m.mode == blockModeHalf {
			modeName = "half"
		}
		// Show cell layout so it's obvious whether the fit math is using
		// the available terminal real estate.
		cellsW := 0
		cellsH := 0
		if m.layout.cellSrcWidth > 0 {
			cellsW = int(float64(m.layout.srcW) / m.layout.cellSrcWidth)
			cellsH = int(float64(m.layout.srcH) / m.layout.cellSrcHeight)
		}
		sizeHint := "small · Ctrl+F full"
		if m.fullscreen {
			sizeHint = "full · Ctrl+F small"
		}
		m.status = fmt.Sprintf("%dx%d → %dx%d cells  ·  %s  ·  %s  ·  %d frames  ·  Ctrl+] quit",
			msg.frame.Width, msg.frame.Height, cellsW, cellsH, modeName, sizeHint, m.frames)
		return m, nil

	case streamErrMsg:
		m.status = "stream error: " + msg.err.Error()
		return m, nil

	case streamClosedMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m browserFeedModel) View() string {
	if m.frame == nil {
		return fmt.Sprintf("\n  %s\n", m.status)
	}
	return m.cachedView + "\n" + bfTruncate(m.status, m.cols)
}

// ─── Block renderers ─────────────────────────────────────────────────────────

func renderBlocks(frame *vnc.VNCFrame, cols, rows int, mode blockMode) (string, blockLayout) {
	switch mode {
	case blockModeHalf:
		return renderHalfBlocks(frame, cols, rows)
	default:
		return renderQuarterBlocks(frame, cols, rows)
	}
}

// cellAspectRatio is the assumed terminal cell height / width ratio.
// 2.0 is correct for monospace fonts in nearly every modern terminal.
// If the actual ratio differs the rendered image will be subtly distorted;
// values from ~1.8 to ~2.2 all look fine.
const cellAspectRatio = 2.0

// computeFit returns the largest cell layout (cellsW × cellsH) that fits in
// a cols × rows grid while preserving the source's aspect ratio on screen.
// Source pixels are assumed square; terminal cells are assumed
// `cellAspectRatio` times taller than wide.
//
// pxCellX/pxCellY only determine sub-pixel granularity per cell — they
// do not change the cell layout. The renderer multiplies by them to get
// the final source-pixel canvas dimensions (dstW = cellsW * pxCellX, etc).
func computeFit(srcW, srcH, cols, rows int) (cellsW, cellsH int) {
	if srcW <= 0 || srcH <= 0 || cols <= 0 || rows <= 0 {
		return 0, 0
	}
	// Preserve aspect: cellsW / (cellsH * R) == srcW / srcH.
	// Maximise cellsW subject to cellsW ≤ cols and cellsH ≤ rows.
	byCols := float64(cols)
	byRows := float64(rows) * cellAspectRatio * float64(srcW) / float64(srcH)
	cw := bfMinF(byCols, byRows)
	ch := cw * float64(srcH) / (float64(srcW) * cellAspectRatio)
	return int(cw), int(ch)
}

// ─── Quarter-block renderer ──────────────────────────────────────────────────

// pixel index inside a 2×2 cell:
//   0 1
//   2 3

// quarterChars maps a 4-bit "fg mask" (bit i set = pixel i is foreground) to
// the Unicode block character that lights those quadrants.
var quarterChars = [16]string{
	" ", // 0000 — empty (bg only)
	"▘", // 0001 — TL
	"▝", // 0010 — TR
	"▀", // 0011 — top
	"▖", // 0100 — BL
	"▌", // 0101 — left
	"▞", // 0110 — anti-diagonal (TR + BL)
	"▛", // 0111 — TL + TR + BL
	"▗", // 1000 — BR
	"▚", // 1001 — diagonal (TL + BR)
	"▐", // 1010 — right
	"▜", // 1011 — TL + TR + BR
	"▄", // 1100 — bottom
	"▙", // 1101 — TL + BL + BR
	"▟", // 1110 — TR + BL + BR
	"█", // 1111 — solid (fg only)
}

// quarterPartitions enumerates all 7 unique partitions of a 4-pixel cell
// into 2 non-empty groups. Each entry is the 4-bit mask of "group A".
// (Each partition's complement is implicit — group B is the inverse mask.)
var quarterPartitions = [7]uint8{
	0b0001, // {0} vs {1,2,3} — top-left alone
	0b0010, // {1} vs {0,2,3} — top-right alone
	0b0100, // {2} vs {0,1,3} — bottom-left alone
	0b1000, // {3} vs {0,1,2} — bottom-right alone
	0b0011, // {0,1} vs {2,3} — top vs bottom
	0b0101, // {0,2} vs {1,3} — left vs right
	0b0110, // {1,2} vs {0,3} — diagonals
}

func renderQuarterBlocks(frame *vnc.VNCFrame, cols, rows int) (string, blockLayout) {
	layout := blockLayout{
		srcW: frame.Width, srcH: frame.Height,
		pixelsPerCellX: 2, pixelsPerCellY: 2,
	}
	if cols <= 0 || rows <= 0 || frame.Width == 0 || frame.Height == 0 {
		return "", layout
	}

	cellsW, cellsH := computeFit(frame.Width, frame.Height, cols, rows)
	if cellsW < 2 || cellsH < 2 {
		return "  (terminal too small)", layout
	}
	dstW := cellsW * 2
	dstH := cellsH * 2
	layout.dstW, layout.dstH = dstW, dstH

	// Anchor bottom-left: no left indent, all vertical slack goes above.
	leftPad := 0
	topPad := rows - cellsH
	layout.leftPadCells = leftPad
	layout.topPadCells = topPad
	layout.cellSrcWidth = float64(frame.Width) / float64(cellsW)
	layout.cellSrcHeight = float64(frame.Height) / float64(cellsH)

	xMap := buildIndexMap(frame.Width, dstW)
	yMap := buildIndexMap(frame.Height, dstH)

	var b strings.Builder
	for i := 0; i < topPad; i++ {
		b.WriteByte('\n')
	}
	leftPadStr := strings.Repeat(" ", bfMaxI(leftPad, 0))

	for cy := 0; cy < cellsH; cy++ {
		yt := yMap[2*cy]
		yb := yMap[2*cy+1]
		b.WriteString(leftPadStr)
		var lastFg, lastBg [3]byte
		var haveLast bool
		for cx := 0; cx < cellsW; cx++ {
			xl := xMap[2*cx]
			xr := xMap[2*cx+1]
			pixels := [4][3]byte{
				pixelAt(frame, xl, yt), // 0: TL
				pixelAt(frame, xr, yt), // 1: TR
				pixelAt(frame, xl, yb), // 2: BL
				pixelAt(frame, xr, yb), // 3: BR
			}
			mask, fg, bg := bestQuarterCell(pixels)
			if !haveLast || fg != lastFg || bg != lastBg {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm",
					fg[0], fg[1], fg[2], bg[0], bg[1], bg[2])
				lastFg, lastBg = fg, bg
				haveLast = true
			}
			b.WriteString(quarterChars[mask])
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), layout
}

// bestQuarterCell picks the 2-colour partition of a 4-pixel cell that
// minimises within-group sum-of-squares. Returns the 4-bit foreground mask
// (bit i = 1 means pixel i takes the fg colour) plus the chosen fg/bg.
//
// Fast-path: if all four pixels are equal, return a solid fg cell with that
// colour and bg = 0. The terminal renders the full block in fg colour; bg
// is unused for mask=1111.
func bestQuarterCell(p [4][3]byte) (uint8, [3]byte, [3]byte) {
	if p[0] == p[1] && p[1] == p[2] && p[2] == p[3] {
		return 0b1111, p[0], [3]byte{}
	}

	bestScore := -1
	var bestMask uint8
	var bestFg, bestBg [3]byte

	for _, partA := range quarterPartitions {
		fg, bg, score := scorePartition(p, partA)
		if bestScore < 0 || score < bestScore {
			bestScore = score
			bestMask = partA
			bestFg = fg
			bestBg = bg
		}
	}
	return bestMask, bestFg, bestBg
}

// scorePartition returns the mean colour of group A (mask), the mean colour
// of group B (~mask), and the total within-group sum-of-squared distances.
func scorePartition(p [4][3]byte, mask uint8) ([3]byte, [3]byte, int) {
	var sumA, sumB [3]int
	var nA, nB int
	for i := 0; i < 4; i++ {
		if mask&(1<<i) != 0 {
			sumA[0] += int(p[i][0])
			sumA[1] += int(p[i][1])
			sumA[2] += int(p[i][2])
			nA++
		} else {
			sumB[0] += int(p[i][0])
			sumB[1] += int(p[i][1])
			sumB[2] += int(p[i][2])
			nB++
		}
	}
	var meanA, meanB [3]byte
	if nA > 0 {
		meanA = [3]byte{byte(sumA[0] / nA), byte(sumA[1] / nA), byte(sumA[2] / nA)}
	}
	if nB > 0 {
		meanB = [3]byte{byte(sumB[0] / nB), byte(sumB[1] / nB), byte(sumB[2] / nB)}
	}
	score := 0
	for i := 0; i < 4; i++ {
		var ref [3]byte
		if mask&(1<<i) != 0 {
			ref = meanA
		} else {
			ref = meanB
		}
		for c := 0; c < 3; c++ {
			d := int(p[i][c]) - int(ref[c])
			score += d * d
		}
	}
	return meanA, meanB, score
}

// ─── Half-block renderer (kept as fallback) ──────────────────────────────────

func renderHalfBlocks(frame *vnc.VNCFrame, cols, rows int) (string, blockLayout) {
	layout := blockLayout{
		srcW: frame.Width, srcH: frame.Height,
		pixelsPerCellX: 1, pixelsPerCellY: 2,
	}
	if cols <= 0 || rows <= 0 || frame.Width == 0 || frame.Height == 0 {
		return "", layout
	}
	cellsW, cellsH := computeFit(frame.Width, frame.Height, cols, rows)
	if cellsW < 2 || cellsH < 2 {
		return "  (terminal too small)", layout
	}
	// One sub-pixel column per cell column, two sub-pixel rows per cell row.
	dstW := cellsW
	dstH := cellsH * 2
	layout.dstW, layout.dstH = dstW, dstH

	// Anchor bottom-left: no left indent, all vertical slack goes above.
	leftPad := 0
	topPad := rows - cellsH
	layout.leftPadCells = leftPad
	layout.topPadCells = topPad
	layout.cellSrcWidth = float64(frame.Width) / float64(cellsW)
	layout.cellSrcHeight = float64(frame.Height) / float64(cellsH)

	xMap := buildIndexMap(frame.Width, dstW)
	yMap := buildIndexMap(frame.Height, dstH)
	leftPadStr := strings.Repeat(" ", bfMaxI(leftPad, 0))

	var b strings.Builder
	for i := 0; i < topPad; i++ {
		b.WriteByte('\n')
	}
	for cy := 0; cy < cellsH; cy++ {
		topRow := 2 * cy
		botRow := topRow + 1
		if topRow >= dstH {
			break
		}
		ySrcTop := yMap[topRow]
		ySrcBot := ySrcTop
		if botRow < dstH {
			ySrcBot = yMap[botRow]
		}
		b.WriteString(leftPadStr)
		var lastFg, lastBg [3]byte
		var haveLast bool
		for cx := 0; cx < dstW; cx++ {
			xs := xMap[cx]
			fg := pixelAt(frame, xs, ySrcTop)
			bg := pixelAt(frame, xs, ySrcBot)
			if !haveLast || fg != lastFg || bg != lastBg {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm",
					fg[0], fg[1], fg[2], bg[0], bg[1], bg[2])
				lastFg, lastBg = fg, bg
				haveLast = true
			}
			b.WriteString("▀")
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), layout
}

// ─── Mouse + keyboard mapping ────────────────────────────────────────────────

// mouseToSrcPixel converts a Bubble Tea cell coordinate to a source-image
// pixel using the most recent render's layout. Returns ok=false when the
// click lands in the letterbox margins.
func mouseToSrcPixel(cellX, cellY int, layout blockLayout) (int, int, bool) {
	if layout.cellSrcWidth == 0 || layout.cellSrcHeight == 0 {
		return 0, 0, false
	}
	relX := cellX - layout.leftPadCells
	relY := cellY - layout.topPadCells
	if relX < 0 || relY < 0 {
		return 0, 0, false
	}
	srcX := int((float64(relX) + 0.5) * layout.cellSrcWidth)
	srcY := int((float64(relY) + 0.5) * layout.cellSrcHeight)
	if srcX < 0 || srcY < 0 || srcX >= layout.srcW || srcY >= layout.srcH {
		return 0, 0, false
	}
	return srcX, srcY, true
}

// updateButtonMask applies a mouse event to the running RFB button mask.
// RFB encoding: bit 0 = left, 1 = middle, 2 = right, 3 = wheel-up,
// 4 = wheel-down.
func updateButtonMask(mask byte, msg tea.MouseMsg) byte {
	bit, ok := mouseButtonBit(msg)
	if !ok {
		return mask
	}
	switch msg.Action {
	case tea.MouseActionPress:
		return mask | (1 << bit)
	case tea.MouseActionRelease:
		return mask &^ (1 << bit)
	}
	return mask
}

func mouseButtonBit(msg tea.MouseMsg) (uint, bool) {
	switch msg.Button {
	case tea.MouseButtonLeft:
		return 0, true
	case tea.MouseButtonMiddle:
		return 1, true
	case tea.MouseButtonRight:
		return 2, true
	case tea.MouseButtonWheelUp:
		return 3, true
	case tea.MouseButtonWheelDown:
		return 4, true
	}
	return 0, false
}

func isWheel(msg tea.MouseMsg) bool {
	return msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown
}

// keyMsgToKeysym translates a Bubble Tea key event to an X11 keysym suitable
// for an RFB KeyEvent. Returns ok=false when the key is one we don't forward
// (modifier-only events, function keys we haven't mapped).
func keyMsgToKeysym(msg tea.KeyMsg) (uint32, bool) {
	switch msg.Type {
	case tea.KeyEnter:
		return 0xff0d, true
	case tea.KeyBackspace:
		return 0xff08, true
	case tea.KeyTab:
		return 0xff09, true
	case tea.KeyShiftTab:
		return 0xfe20, true
	case tea.KeyEsc:
		return 0xff1b, true
	case tea.KeySpace:
		return 0x0020, true
	case tea.KeyDelete:
		return 0xffff, true
	case tea.KeyHome:
		return 0xff50, true
	case tea.KeyEnd:
		return 0xff57, true
	case tea.KeyPgUp:
		return 0xff55, true
	case tea.KeyPgDown:
		return 0xff56, true
	case tea.KeyLeft:
		return 0xff51, true
	case tea.KeyUp:
		return 0xff52, true
	case tea.KeyRight:
		return 0xff53, true
	case tea.KeyDown:
		return 0xff54, true
	case tea.KeyF1:
		return 0xffbe, true
	case tea.KeyF2:
		return 0xffbf, true
	case tea.KeyF3:
		return 0xffc0, true
	case tea.KeyF4:
		return 0xffc1, true
	case tea.KeyF5:
		return 0xffc2, true
	case tea.KeyF6:
		return 0xffc3, true
	case tea.KeyF7:
		return 0xffc4, true
	case tea.KeyF8:
		return 0xffc5, true
	case tea.KeyF9:
		return 0xffc6, true
	case tea.KeyF10:
		return 0xffc7, true
	case tea.KeyF11:
		return 0xffc8, true
	case tea.KeyF12:
		return 0xffc9, true
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			return uint32(msg.Runes[0]), true
		}
	}
	return 0, false
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// buildIndexMap returns a length-`dst` slice mapping each destination index
// to a source index via nearest-neighbour. Cheap, allocation-light, and
// good enough for an experimental viewer — proper bilinear costs more in
// CPU than the terminal can keep up with anyway at 10 fps.
func buildIndexMap(srcLen, dstLen int) []int {
	out := make([]int, dstLen)
	if dstLen == 0 {
		return out
	}
	scale := float64(srcLen) / float64(dstLen)
	for i := 0; i < dstLen; i++ {
		s := int((float64(i) + 0.5) * scale)
		if s >= srcLen {
			s = srcLen - 1
		}
		out[i] = s
	}
	return out
}

// pixelAt returns the BGRX pixel at (x, y) as RGB. The framebuffer stores
// little-endian 32-bit pixels with shifts R<<16, G<<8, B<<0 (see SetPixelFormat
// in vnc_stream.go), which on disk is [B, G, R, X].
func pixelAt(f *vnc.VNCFrame, x, y int) [3]byte {
	if x < 0 || y < 0 || x >= f.Width || y >= f.Height {
		return [3]byte{0, 0, 0}
	}
	i := (y*f.Width + x) * 4
	return [3]byte{f.Pixels[i+2], f.Pixels[i+1], f.Pixels[i]}
}

func bfTermSize() (int, int) {
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		return w, h
	}
	return 80, 24
}

func bfTruncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func bfMinF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func bfMaxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}
