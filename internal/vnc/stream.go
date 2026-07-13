package vnc

// EGRESS CARVE-OUT (QUA-1316): this package is the sole non-httpx egress path
// in qmax-code. It opens a WebSocket (coder/websocket) to the cloud-sandbox
// noVNC endpoint to stream the live browser framebuffer. This traffic carries
// rendered pixels (screenshots), never source code, prompts, or API keys, so it
// is outside the Exposure Receipt's content-accountability scope. The static
// egress guard (internal/httpx/guard_test.go) allowlists this package
// explicitly — any *new* WebSocket or raw-socket egress must be reviewed and
// added to the carve-out list, not silently merged.
//
// Minimal RFB 3.8 client over WebSocket, tailored for QM Cloud Sandbox
// noVNC endpoints. Decodes Raw + CopyRect into a 32-bit BGRX framebuffer
// so the renderer can emit ANSI half blocks. Keeps the dependency surface
// to one websocket library — RFB framing is small enough to roll by hand
// and lets us pin the wire format we want from the server.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// VNCFrame is a snapshot of the framebuffer after one or more updates.
// Pixels are packed as little-endian BGRX (4 bytes per pixel, X = ignored).
type VNCFrame struct {
	Width  int
	Height int
	Pixels []byte // len == Width*Height*4
}

// Clone returns a deep copy so the consumer can hold onto a frame while the
// stream keeps mutating its internal buffer.
func (f *VNCFrame) Clone() *VNCFrame {
	out := &VNCFrame{Width: f.Width, Height: f.Height, Pixels: make([]byte, len(f.Pixels))}
	copy(out.Pixels, f.Pixels)
	return out
}

// VNCStream owns the connection. Frames are pushed onto Frames; errors that
// terminate the stream land on Err. Both channels close when the stream ends.
type VNCStream struct {
	Frames chan *VNCFrame
	Err    chan error

	ctx    context.Context
	cancel context.CancelFunc

	conn   *websocket.Conn
	netCxn net.Conn

	width  int
	height int
	fb     []byte // BGRX framebuffer

	writeMu sync.Mutex
}

// DialVNC connects to a noVNC websockify endpoint. The URL may be either:
//
//	http(s)://host:port           → /websockify is appended automatically
//	ws(s)://host:port/websockify  → used as-is
//
// The returned stream is already running its read loop; call Close when done.
// fps caps how often FramebufferUpdateRequest is re-issued (the cloud
// sandbox's xtigervnc pushes deltas, so this acts as a backstop, not a
// polling rate).
// DialVNC connects to a noVNC websockify endpoint. ctx may be nil, in which
// case context.Background() is used. The returned stream is already running
// its read loop; call Close when done.
func DialVNC(ctx context.Context, rawURL string, fps int) (*VNCStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	wsURL, err := normalizeNoVNCURL(rawURL)
	if err != nil {
		return nil, err
	}

	// dialOnce attempts a single WebSocket upgrade (subprotocol then bare).
	// Returns the live conn or (nil, err) on hard failure.
	dialOnce := func(dctx context.Context) (*websocket.Conn, error) {
		c, _, e := websocket.Dial(dctx, wsURL, &websocket.DialOptions{
			Subprotocols: []string{"binary"},
		})
		if e == nil {
			return c, nil
		}
		c, _, e2 := websocket.Dial(dctx, wsURL, nil)
		if e2 == nil {
			return c, nil
		}
		return nil, fmt.Errorf("websocket dial %s: %w", wsURL, e)
	}

	// Retry up to 4 times with a 3-second gap for 502/503 responses. The
	// sandbox's websockify process can take a moment to become ready after
	// the keepalive hold kicks in; a brief wait is enough to clear it.
	const maxAttempts = 4
	var conn *websocket.Conn
	for attempt := range maxAttempts {
		dialCtx, cancelDial := context.WithTimeout(ctx, 15*time.Second)
		conn, err = dialOnce(dialCtx)
		cancelDial()
		if err == nil {
			break
		}
		// Only retry on gateway errors — other failures (TLS, DNS, auth) won't
		// resolve on their own.
		errStr := err.Error()
		is5xx := strings.Contains(errStr, "502") || strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "504")
		if !is5xx || attempt == maxAttempts-1 {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	// conn is guaranteed non-nil here: loop exits via break (err==nil) or
	// returns early on error, so this point is only reached on success.
	conn.SetReadLimit(-1) // RFB framebuffer updates can be megabytes

	streamCtx, cancel := context.WithCancel(ctx)
	netCxn := websocket.NetConn(streamCtx, conn, websocket.MessageBinary)

	s := &VNCStream{
		Frames: make(chan *VNCFrame, 4),
		Err:    make(chan error, 1),
		ctx:    streamCtx,
		cancel: cancel,
		conn:   conn,
		netCxn: netCxn,
	}

	if err := s.handshake(); err != nil {
		s.Close()
		return nil, err
	}

	go s.readLoop()
	go s.requestLoop(fps)

	return s, nil
}

// Close terminates the stream. Safe to call multiple times.
func (s *VNCStream) Close() {
	s.cancel()
	if s.netCxn != nil {
		_ = s.netCxn.Close()
	}
	if s.conn != nil {
		_ = s.conn.Close(websocket.StatusNormalClosure, "")
	}
}

// normalizeNoVNCURL accepts an http/https/ws/wss URL and returns a wss/ws
// URL pointing at the websockify endpoint. The QM Cloud Sandbox returns
// the HTML5 client URL (e.g. https://<host>/vnc.html?autoconnect=true&...)
// — we rewrite that (plus the empty/root path case) to /websockify so we
// can speak RFB directly. Any non-standard path is preserved as-is so
// self-hosted setups with a moved websockify endpoint keep working.
func normalizeNoVNCURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
		// already a websocket URL, leave alone
	case "":
		// bare host:port — assume wss (cloud sandbox exposed ports are HTTPS by default)
		u, err = url.Parse("wss://" + raw)
		if err != nil {
			return "", fmt.Errorf("parse url: %w", err)
		}
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if isStandardNoVNCPath(u.Path) {
		u.Path = "/websockify"
		u.RawQuery = "" // drop the HTML5 client's autoconnect/resize/etc params
	}
	return u.String(), nil
}

// isStandardNoVNCPath returns true for the well-known noVNC HTTP entry
// points: empty, "/", "/vnc.html", "/vnc_lite.html". On these we rewrite
// to /websockify; any other path is treated as an explicit override.
func isStandardNoVNCPath(p string) bool {
	switch p {
	case "", "/", "/vnc.html", "/vnc_lite.html":
		return true
	}
	return false
}

// ─── RFB 3.8 handshake ────────────────────────────────────────────────────────

func (s *VNCStream) handshake() error {
	// 1. ProtocolVersion: server sends "RFB 003.008\n" (12 bytes), we echo.
	ver := make([]byte, 12)
	if _, err := io.ReadFull(s.netCxn, ver); err != nil {
		return fmt.Errorf("read protocol version: %w", err)
	}
	if !strings.HasPrefix(string(ver), "RFB 003.") {
		return fmt.Errorf("unexpected protocol banner: %q", string(ver))
	}
	if _, err := s.netCxn.Write([]byte("RFB 003.008\n")); err != nil {
		return fmt.Errorf("write protocol version: %w", err)
	}

	// 2. Security types: 1 byte count, N bytes of types. We require type 1
	//    (None). The QM Cloud Sandbox desktop template doesn't use VNC auth.
	hdr := make([]byte, 1)
	if _, err := io.ReadFull(s.netCxn, hdr); err != nil {
		return fmt.Errorf("read security count: %w", err)
	}
	if hdr[0] == 0 {
		// Failure: 4-byte reason length + reason string
		var rl [4]byte
		_, _ = io.ReadFull(s.netCxn, rl[:])
		reason := make([]byte, binary.BigEndian.Uint32(rl[:]))
		_, _ = io.ReadFull(s.netCxn, reason)
		return fmt.Errorf("server refused handshake: %s", reason)
	}
	types := make([]byte, hdr[0])
	if _, err := io.ReadFull(s.netCxn, types); err != nil {
		return fmt.Errorf("read security types: %w", err)
	}
	if !bytesContain(types, 1) {
		return fmt.Errorf("server requires VNC auth (types=%v); only None is supported", types)
	}
	if _, err := s.netCxn.Write([]byte{1}); err != nil {
		return fmt.Errorf("select security: %w", err)
	}

	// 3. SecurityResult: 4-byte status. 0 = OK.
	var status [4]byte
	if _, err := io.ReadFull(s.netCxn, status[:]); err != nil {
		return fmt.Errorf("read security result: %w", err)
	}
	if binary.BigEndian.Uint32(status[:]) != 0 {
		return errors.New("security handshake failed")
	}

	// 4. ClientInit: 1 byte shared-flag (1 = allow other clients).
	if _, err := s.netCxn.Write([]byte{1}); err != nil {
		return fmt.Errorf("client init: %w", err)
	}

	// 5. ServerInit: 24-byte fixed header + name length + name.
	si := make([]byte, 24)
	if _, err := io.ReadFull(s.netCxn, si); err != nil {
		return fmt.Errorf("read server init: %w", err)
	}
	s.width = int(binary.BigEndian.Uint16(si[0:2]))
	s.height = int(binary.BigEndian.Uint16(si[2:4]))
	nameLen := binary.BigEndian.Uint32(si[20:24])
	if nameLen > 0 {
		name := make([]byte, nameLen)
		if _, err := io.ReadFull(s.netCxn, name); err != nil {
			return fmt.Errorf("read server name: %w", err)
		}
	}

	s.fb = make([]byte, s.width*s.height*4)

	// 6. SetPixelFormat — pin to 32bpp, depth 24, little-endian, true-colour
	//    BGRX so we never have to do palette/colourmap work in the renderer.
	if err := s.setPixelFormat(); err != nil {
		return err
	}
	// 7. SetEncodings — Raw (0), CopyRect (1), DesktopSize (-223).
	if err := s.setEncodings(0, 1, -223); err != nil {
		return err
	}
	// 8. Initial full-frame request.
	return s.requestUpdate(false)
}

func (s *VNCStream) setPixelFormat() error {
	// 1 type + 3 padding + 16-byte PixelFormat
	buf := make([]byte, 20)
	buf[0] = 0 // SetPixelFormat
	// PixelFormat:
	buf[4] = 32                                 // bits-per-pixel
	buf[5] = 24                                 // depth
	buf[6] = 0                                  // big-endian-flag (0 = little-endian)
	buf[7] = 1                                  // true-colour-flag
	binary.BigEndian.PutUint16(buf[8:10], 255)  // red-max
	binary.BigEndian.PutUint16(buf[10:12], 255) // green-max
	binary.BigEndian.PutUint16(buf[12:14], 255) // blue-max
	buf[14] = 16                                // red-shift
	buf[15] = 8                                 // green-shift
	buf[16] = 0                                 // blue-shift
	// padding [17..19]
	return s.write(buf)
}

func (s *VNCStream) setEncodings(encs ...int32) error {
	buf := make([]byte, 4+4*len(encs))
	buf[0] = 2 // SetEncodings
	binary.BigEndian.PutUint16(buf[2:4], uint16(len(encs)))
	for i, e := range encs {
		binary.BigEndian.PutUint32(buf[4+i*4:8+i*4], uint32(e))
	}
	return s.write(buf)
}

func (s *VNCStream) requestUpdate(incremental bool) error {
	buf := make([]byte, 10)
	buf[0] = 3 // FramebufferUpdateRequest
	if incremental {
		buf[1] = 1
	}
	binary.BigEndian.PutUint16(buf[2:4], 0)
	binary.BigEndian.PutUint16(buf[4:6], 0)
	binary.BigEndian.PutUint16(buf[6:8], uint16(s.width))
	binary.BigEndian.PutUint16(buf[8:10], uint16(s.height))
	return s.write(buf)
}

func (s *VNCStream) write(b []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.netCxn.Write(b)
	return err
}

// ─── Outbound input events ───────────────────────────────────────────────────

// SendKey emits an RFB KeyEvent for the given X11 keysym. down=true is a
// press; down=false is a release. RFB has no concept of "key repeat" — the
// caller is responsible for sending matched press/release pairs.
func (s *VNCStream) SendKey(keysym uint32, down bool) error {
	buf := make([]byte, 8)
	buf[0] = 4 // KeyEvent
	if down {
		buf[1] = 1
	}
	// buf[2:4] padding
	binary.BigEndian.PutUint32(buf[4:8], keysym)
	return s.write(buf)
}

// SendPointer emits an RFB PointerEvent. buttonMask is a bitmap with bit
// 0=left, 1=middle, 2=right, 3=wheel-up, 4=wheel-down. x,y are in the
// remote framebuffer's pixel coordinate space.
func (s *VNCStream) SendPointer(x, y int, buttonMask byte) error {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if x >= s.width {
		x = s.width - 1
	}
	if y >= s.height {
		y = s.height - 1
	}
	buf := make([]byte, 6)
	buf[0] = 5 // PointerEvent
	buf[1] = buttonMask
	binary.BigEndian.PutUint16(buf[2:4], uint16(x))
	binary.BigEndian.PutUint16(buf[4:6], uint16(y))
	return s.write(buf)
}

// ─── Read + request loops ────────────────────────────────────────────────────

func (s *VNCStream) readLoop() {
	defer close(s.Frames)
	defer close(s.Err)

	for {
		if err := s.ctx.Err(); err != nil {
			return
		}
		var msgType [1]byte
		if _, err := io.ReadFull(s.netCxn, msgType[:]); err != nil {
			s.emitErr(fmt.Errorf("read msg type: %w", err))
			return
		}
		switch msgType[0] {
		case 0:
			if err := s.readFramebufferUpdate(); err != nil {
				s.emitErr(err)
				return
			}
		case 1:
			// SetColourMapEntries — ignore (we asked for true-colour)
			if err := s.skipColourMap(); err != nil {
				s.emitErr(err)
				return
			}
		case 2:
			// Bell — single byte, nothing else to read
		case 3:
			// ServerCutText — drain
			if err := s.skipServerCutText(); err != nil {
				s.emitErr(err)
				return
			}
		default:
			s.emitErr(fmt.Errorf("unknown server msg type %d", msgType[0]))
			return
		}
	}
}

// requestLoop ticks an incremental FramebufferUpdateRequest. xtigervnc pushes
// updates without prompting, but if the desktop is idle we still want a
// periodic nudge so a late re-attach gets a frame within ~1s.
func (s *VNCStream) requestLoop(fps int) {
	if fps <= 0 {
		fps = 10
	}
	t := time.NewTicker(time.Second / time.Duration(fps))
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			if err := s.requestUpdate(true); err != nil {
				return
			}
		}
	}
}

// ─── FramebufferUpdate parsing ───────────────────────────────────────────────

func (s *VNCStream) readFramebufferUpdate() error {
	hdr := make([]byte, 3) // 1 padding + 2 nrects
	if _, err := io.ReadFull(s.netCxn, hdr); err != nil {
		return fmt.Errorf("read fb update hdr: %w", err)
	}
	nrects := int(binary.BigEndian.Uint16(hdr[1:3]))

	for i := 0; i < nrects; i++ {
		rh := make([]byte, 12)
		if _, err := io.ReadFull(s.netCxn, rh); err != nil {
			return fmt.Errorf("read rect hdr: %w", err)
		}
		x := int(binary.BigEndian.Uint16(rh[0:2]))
		y := int(binary.BigEndian.Uint16(rh[2:4]))
		w := int(binary.BigEndian.Uint16(rh[4:6]))
		h := int(binary.BigEndian.Uint16(rh[6:8]))
		enc := int32(binary.BigEndian.Uint32(rh[8:12]))

		switch enc {
		case 0: // Raw
			if err := s.readRaw(x, y, w, h); err != nil {
				return err
			}
		case 1: // CopyRect
			if err := s.readCopyRect(x, y, w, h); err != nil {
				return err
			}
		case -223: // DesktopSize
			s.resize(w, h)
		default:
			return fmt.Errorf("unsupported encoding %d (server ignored SetEncodings)", enc)
		}
	}

	// Emit a clone so consumers can hold it past the next update.
	frame := &VNCFrame{Width: s.width, Height: s.height, Pixels: s.fb}
	cloned := frame.Clone()
	select {
	case s.Frames <- cloned:
	default:
		// Drop the previous queued frame and replace — we always want the
		// freshest snapshot, and a slow consumer should never stall reads.
		select {
		case <-s.Frames:
		default:
		}
		select {
		case s.Frames <- cloned:
		default:
		}
	}
	return nil
}

func (s *VNCStream) readRaw(x, y, w, h int) error {
	if w == 0 || h == 0 {
		return nil
	}
	row := make([]byte, w*4)
	for j := 0; j < h; j++ {
		if _, err := io.ReadFull(s.netCxn, row); err != nil {
			return fmt.Errorf("read raw row: %w", err)
		}
		dy := y + j
		if dy < 0 || dy >= s.height {
			continue
		}
		dst := s.fb[(dy*s.width+x)*4:]
		// Clip horizontally if the rect overflows (shouldn't, but cheap).
		n := w * 4
		if x+w > s.width {
			n = (s.width - x) * 4
			if n <= 0 {
				continue
			}
		}
		copy(dst[:n], row[:n])
	}
	return nil
}

func (s *VNCStream) readCopyRect(dx, dy, w, h int) error {
	src := make([]byte, 4)
	if _, err := io.ReadFull(s.netCxn, src); err != nil {
		return fmt.Errorf("read copyrect src: %w", err)
	}
	sx := int(binary.BigEndian.Uint16(src[0:2]))
	sy := int(binary.BigEndian.Uint16(src[2:4]))

	// Copy row-by-row. If src and dst rows overlap vertically, walk in the
	// safe direction so we don't trample bytes we still need to read.
	if sy < dy {
		for j := h - 1; j >= 0; j-- {
			s.copyRow(sx, sy+j, dx, dy+j, w)
		}
	} else {
		for j := 0; j < h; j++ {
			s.copyRow(sx, sy+j, dx, dy+j, w)
		}
	}
	return nil
}

func (s *VNCStream) copyRow(sx, sy, dx, dy, w int) {
	if sy < 0 || sy >= s.height || dy < 0 || dy >= s.height {
		return
	}
	if sx < 0 || dx < 0 {
		return
	}
	if sx+w > s.width {
		w = s.width - sx
	}
	if dx+w > s.width {
		w = s.width - dx
	}
	if w <= 0 {
		return
	}
	src := s.fb[(sy*s.width+sx)*4 : (sy*s.width+sx+w)*4]
	dst := s.fb[(dy*s.width+dx)*4 : (dy*s.width+dx+w)*4]
	copy(dst, src)
}

func (s *VNCStream) resize(w, h int) {
	s.width, s.height = w, h
	s.fb = make([]byte, w*h*4)
}

func (s *VNCStream) skipColourMap() error {
	hdr := make([]byte, 5) // 1 padding + 2 first-colour + 2 nentries
	if _, err := io.ReadFull(s.netCxn, hdr); err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint16(hdr[3:5]))
	_, err := io.CopyN(io.Discard, s.netCxn, int64(n*6))
	return err
}

func (s *VNCStream) skipServerCutText() error {
	hdr := make([]byte, 7) // 3 padding + 4 length
	if _, err := io.ReadFull(s.netCxn, hdr); err != nil {
		return err
	}
	n := int64(binary.BigEndian.Uint32(hdr[3:7]))
	_, err := io.CopyN(io.Discard, s.netCxn, n)
	return err
}

func (s *VNCStream) emitErr(err error) {
	select {
	case s.Err <- err:
	default:
	}
}

func bytesContain(b []byte, v byte) bool {
	for _, x := range b {
		if x == v {
			return true
		}
	}
	return false
}
