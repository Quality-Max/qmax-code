package repl

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"github.com/Quality-Max/qmax-code/internal/api"
	"github.com/Quality-Max/qmax-code/internal/tui"
	"github.com/Quality-Max/qmax-code/internal/vnc"
)

// rfbServer is a minimal RFB 3.8 server over WebSocket. It completes the
// handshake for a 4x4 desktop, optionally sends one raw framebuffer update,
// then records every client message until the client disconnects.
type rfbServer struct {
	srv       *httptest.Server
	url       string
	sendFrame bool

	mu       sync.Mutex
	received []byte
	done     chan struct{}
	once     sync.Once
}

// disconnect drops every client connection from the server side.
func (rs *rfbServer) disconnect() { rs.once.Do(func() { close(rs.done) }) }

func newRFBServer(t *testing.T, sendFrame bool) *rfbServer {
	t.Helper()
	rs := &rfbServer{sendFrame: sendFrame, done: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/websockify", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}, InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		nc := websocket.NetConn(r.Context(), conn, websocket.MessageBinary)
		_, _ = nc.Write([]byte("RFB 003.008\n"))
		_, _ = io.ReadFull(nc, make([]byte, 12))
		_, _ = nc.Write([]byte{1, 1})
		_, _ = io.ReadFull(nc, make([]byte, 1))
		_, _ = nc.Write([]byte{0, 0, 0, 0})
		_, _ = io.ReadFull(nc, make([]byte, 1))
		si := make([]byte, 24)
		binary.BigEndian.PutUint16(si[0:], 4)
		binary.BigEndian.PutUint16(si[2:], 4)
		_, _ = nc.Write(si)
		if rs.sendFrame {
			msg := []byte{0, 0, 0, 1}
			rect := make([]byte, 12)
			binary.BigEndian.PutUint16(rect[4:], 4)
			binary.BigEndian.PutUint16(rect[6:], 4)
			msg = append(msg, rect...)
			for i := 0; i < 16; i++ {
				msg = append(msg, byte(i*16), byte(255-i*16), byte(i*8), 0)
			}
			_, _ = nc.Write(msg)
		}
		go func() {
			select {
			case <-rs.done:
				_ = conn.Close(websocket.StatusGoingAway, "test disconnect")
			case <-r.Context().Done():
			}
		}()
		buf := make([]byte, 1024)
		for {
			n, err := nc.Read(buf)
			if err != nil {
				return
			}
			rs.mu.Lock()
			rs.received = append(rs.received, buf[:n]...)
			rs.mu.Unlock()
		}
	})
	rs.srv = httptest.NewServer(mux)
	rs.url = "ws://" + rs.srv.Listener.Addr().String() + "/websockify"
	t.Cleanup(func() {
		rs.disconnect()
		rs.srv.Close()
	})
	return rs
}

func (rs *rfbServer) bytes() []byte {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return append([]byte(nil), rs.received...)
}

func dialTestStream(t *testing.T, rs *rfbServer) *vnc.VNCStream {
	t.Helper()
	stream, err := vnc.DialVNC(context.Background(), rs.url, 1)
	if err != nil {
		t.Fatalf("DialVNC: %v", err)
	}
	t.Cleanup(stream.Close)
	return stream
}

func gradientFrame(w, h int) *vnc.VNCFrame {
	f := &vnc.VNCFrame{Width: w, Height: h, Pixels: make([]byte, w*h*4)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			f.Pixels[i] = byte(x * 255 / w)
			f.Pixels[i+1] = byte(y * 255 / h)
			if (x+y)%2 == 0 {
				f.Pixels[i+2] = 255
			}
		}
	}
	return f
}

func TestRenderBlocksBothModes(t *testing.T) {
	frame := gradientFrame(64, 32)
	for _, mode := range []blockMode{blockModeQuarter, blockModeHalf} {
		body, layout := renderBlocks(frame, 40, 12, mode)
		if !strings.Contains(body, "\x1b[") {
			t.Fatalf("mode %v: expected ANSI colour output", mode)
		}
		if layout.srcW != 64 || layout.srcH != 32 || layout.cellSrcWidth <= 0 || layout.cellSrcHeight <= 0 {
			t.Fatalf("mode %v: layout = %+v", mode, layout)
		}
		if x, y, ok := mouseToSrcPixel(layout.leftPadCells, layout.topPadCells, layout); !ok || x < 0 || y < 0 {
			t.Fatalf("mode %v: top-left cell should map into the frame, got %d,%d ok=%v", mode, x, y, ok)
		}
		if _, _, ok := mouseToSrcPixel(-1, -1, layout); ok {
			t.Fatalf("mode %v: negative cell must not map", mode)
		}
		if _, _, ok := mouseToSrcPixel(1000, 1000, layout); ok {
			t.Fatalf("mode %v: far cell must not map", mode)
		}

		tiny, _ := renderBlocks(frame, 1, 1, mode)
		if !strings.Contains(tiny, "too small") {
			t.Fatalf("mode %v: tiny terminal = %q", mode, tiny)
		}
		empty, _ := renderBlocks(&vnc.VNCFrame{}, 40, 12, mode)
		if empty != "" {
			t.Fatalf("mode %v: empty frame rendered %q", mode, empty)
		}
	}
	if _, _, ok := mouseToSrcPixel(0, 0, blockLayout{}); ok {
		t.Fatal("zero layout must not map")
	}
}

func TestPixelAtBoundsAndIndexMap(t *testing.T) {
	f := gradientFrame(2, 2)
	if got := pixelAt(f, -1, 0); got != [3]byte{} {
		t.Fatalf("out-of-bounds pixel = %v", got)
	}
	if got := pixelAt(f, 1, 1); got != [3]byte{f.Pixels[14], f.Pixels[13], f.Pixels[12]} {
		t.Fatalf("pixelAt(1,1) = %v", got)
	}
	if m := buildIndexMap(10, 0); len(m) != 0 {
		t.Fatalf("empty map = %v", m)
	}
	if m := buildIndexMap(3, 6); m[5] != 2 {
		t.Fatalf("upscale map = %v", m)
	}
	if bfTruncate("abcdef", 0) != "" || bfTruncate("abc", 5) != "abc" || len([]rune(bfTruncate("abcdefgh", 4))) > 4 {
		t.Fatal("bfTruncate bounds")
	}
}

func TestKeyMsgToKeysym(t *testing.T) {
	cases := map[tea.KeyType]uint32{
		tea.KeyEnter: 0xff0d, tea.KeyBackspace: 0xff08, tea.KeyTab: 0xff09, tea.KeyShiftTab: 0xfe20,
		tea.KeyEsc: 0xff1b, tea.KeySpace: 0x20, tea.KeyDelete: 0xffff, tea.KeyHome: 0xff50,
		tea.KeyEnd: 0xff57, tea.KeyPgUp: 0xff55, tea.KeyPgDown: 0xff56, tea.KeyLeft: 0xff51,
		tea.KeyUp: 0xff52, tea.KeyRight: 0xff53, tea.KeyDown: 0xff54,
		tea.KeyF1: 0xffbe, tea.KeyF2: 0xffbf, tea.KeyF3: 0xffc0, tea.KeyF4: 0xffc1,
		tea.KeyF5: 0xffc2, tea.KeyF6: 0xffc3, tea.KeyF7: 0xffc4, tea.KeyF8: 0xffc5,
		tea.KeyF9: 0xffc6, tea.KeyF10: 0xffc7, tea.KeyF11: 0xffc8, tea.KeyF12: 0xffc9,
	}
	for k, want := range cases {
		if got, ok := keyMsgToKeysym(tea.KeyMsg{Type: k}); !ok || got != want {
			t.Errorf("key %v = %#x ok=%v, want %#x", k, got, ok, want)
		}
	}
	if got, ok := keyMsgToKeysym(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é")}); !ok || got != 'é' {
		t.Fatalf("rune key = %#x ok=%v", got, ok)
	}
	if _, ok := keyMsgToKeysym(tea.KeyMsg{Type: tea.KeyRunes}); ok {
		t.Fatal("empty runes must not map")
	}
	if _, ok := keyMsgToKeysym(tea.KeyMsg{Type: tea.KeyCtrlA}); ok {
		t.Fatal("unmapped control key must not map")
	}
}

func TestMouseButtonMask(t *testing.T) {
	press := func(b tea.MouseButton) tea.MouseMsg {
		return tea.MouseMsg{Button: b, Action: tea.MouseActionPress}
	}
	var mask byte
	for bit, b := range []tea.MouseButton{tea.MouseButtonLeft, tea.MouseButtonMiddle, tea.MouseButtonRight, tea.MouseButtonWheelUp, tea.MouseButtonWheelDown} {
		mask = updateButtonMask(mask, press(b))
		if mask&(1<<bit) == 0 {
			t.Fatalf("button %v did not set bit %d", b, bit)
		}
	}
	mask = updateButtonMask(mask, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease})
	if mask&1 != 0 {
		t.Fatal("release did not clear the left bit")
	}
	if got := updateButtonMask(mask, tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion}); got != mask {
		t.Fatal("motion changed the mask")
	}
	if got := updateButtonMask(mask, tea.MouseMsg{Button: tea.MouseButtonNone}); got != mask {
		t.Fatal("unknown button changed the mask")
	}
}

func TestBrowserFeedModelForwardsInput(t *testing.T) {
	rs := newRFBServer(t, false)
	stream := dialTestStream(t, rs)
	m := browserFeedModel{stream: stream, mode: blockModeQuarter, status: "connecting"}

	if got := m.View(); !strings.Contains(got, "connecting") {
		t.Fatalf("pre-frame view = %q", got)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(browserFeedModel)
	if m.cols != 60 || m.rows != 24 {
		t.Fatalf("small mode dims = %dx%d", m.cols, m.rows)
	}
	next, _ = m.Update(frameMsg{frame: gradientFrame(64, 32)})
	m = next.(browserFeedModel)
	if !strings.Contains(m.status, "64x32") || !strings.Contains(m.status, "quarter") || !strings.Contains(m.View(), "64x32") {
		t.Fatalf("status after frame = %q", m.status)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = next.(browserFeedModel)
	if !m.fullscreen || m.cols != 100 {
		t.Fatalf("Ctrl+F did not go fullscreen: %+v", m.cols)
	}
	next, _ = m.Update(frameMsg{frame: gradientFrame(64, 32)})
	m = next.(browserFeedModel)
	if !strings.Contains(m.status, "full · Ctrl+F small") {
		t.Fatalf("fullscreen status = %q", m.status)
	}

	before := len(rs.bytes())
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	cx, cy := m.layout.leftPadCells+1, m.layout.topPadCells+1
	m.Update(tea.MouseMsg{X: cx, Y: cy, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: cx, Y: cy, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m.Update(tea.MouseMsg{X: -5, Y: -5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})

	// key press+release (2×8 bytes) + click (6) + wheel press+clear (2×6).
	want := before + 16 + 6 + 12
	deadline := time.Now().Add(2 * time.Second)
	for len(rs.bytes()) < want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := rs.bytes(); !containsMsg(got[before:], 4) || !containsMsg(got[before:], 5) {
		t.Fatalf("server did not receive key and pointer events: % x", got[before:])
	}

	next, _ = m.Update(streamErrMsg{err: io.ErrUnexpectedEOF})
	m = next.(browserFeedModel)
	if !strings.Contains(m.status, "stream error") {
		t.Fatalf("status after error = %q", m.status)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}); cmd == nil {
		t.Fatal("Ctrl+] did not quit")
	}
	if _, cmd := m.Update(streamClosedMsg{}); cmd == nil {
		t.Fatal("stream close did not quit")
	}
	if _, cmd := m.Update("ignored"); cmd != nil {
		t.Fatal("unknown message produced a command")
	}

	half := browserFeedModel{stream: stream, mode: blockModeHalf, termCols: 80, termRows: 30}
	half.updateEffectiveDims()
	next, _ = half.Update(frameMsg{frame: gradientFrame(32, 32)})
	if !strings.Contains(next.(browserFeedModel).status, "half") {
		t.Fatal("half-block mode not reported in status")
	}
}

func containsMsg(b []byte, msgType byte) bool {
	for _, x := range b {
		if x == msgType {
			return true
		}
	}
	return false
}

func TestShowBrowserFeedRunsUntilStreamCloses(t *testing.T) {
	orig := browserFeedProgramOptions
	t.Cleanup(func() { browserFeedProgramOptions = orig })
	browserFeedProgramOptions = func() []tea.ProgramOption {
		return []tea.ProgramOption{tea.WithInput(nil), tea.WithOutput(io.Discard)}
	}

	rs := newRFBServer(t, true)
	stream := dialTestStream(t, rs)
	go func() {
		time.Sleep(300 * time.Millisecond)
		stream.Close()
	}()
	if err := showBrowserFeedFromStream(stream, blockModeQuarter, "test"); err != nil {
		t.Fatalf("showBrowserFeedFromStream: %v", err)
	}

	rs2 := newRFBServer(t, true)
	done := make(chan error, 1)
	go func() { done <- showBrowserFeed(rs2.url, blockModeHalf) }()
	time.Sleep(300 * time.Millisecond)
	rs2.disconnect()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("showBrowserFeed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("showBrowserFeed did not return after the server closed")
	}

	if err := showBrowserFeed("ftp://nope", blockModeHalf); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("bad URL error = %v", err)
	}
}

func TestMaybeLaunchLiveFeed(t *testing.T) {
	term := &tui.Terminal{}

	out := captureOutput(t, func() { maybeLaunchLiveFeed(nil, term, nil, "") })
	if out != "" {
		t.Fatalf("nil context printed %q", out)
	}

	rs := newRFBServer(t, false)
	pre := dialTestStream(t, rs)
	maybeLaunchLiveFeed(&api.SessionContext{}, term, pre, "") // feed off: closes the pre-stream

	sctx := &api.SessionContext{LiveFeed: true}
	out = captureOutput(t, func() { maybeLaunchLiveFeed(sctx, term, nil, "") })
	if out != "" {
		t.Fatalf("plain chat turn should stay quiet, got %q", out)
	}

	sctx.SandboxModeLogged = true
	out = captureOutput(t, func() { maybeLaunchLiveFeed(sctx, term, nil, "") })
	assertContains(t, out, "no live_browser_url came back", "E2B_API_KEY")

	sctx.SandboxFallbackSeen = true
	out = captureOutput(t, func() { maybeLaunchLiveFeed(sctx, term, nil, "") })
	assertContains(t, out, "is_e2b=false")

	out = captureOutput(t, func() { maybeLaunchLiveFeed(&api.SessionContext{LiveFeed: true}, term, nil, "exec-1") })
	assertContains(t, out, "within 5 minutes")

	sctx = &api.SessionContext{LiveFeed: true, LastLiveURL: "ftp://bad"}
	out = captureOutput(t, func() { maybeLaunchLiveFeed(sctx, term, nil, "") })
	assertContains(t, out, "Opening live browser feed", "browserfeed: connect:")
	if sctx.LastLiveURL != "ftp://bad" {
		t.Fatal("failed launch should keep the URL for /feed")
	}
}
