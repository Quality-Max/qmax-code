package vnc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
)

// Memory-backed connection keeps parser tests independent of network timing.
// Embedding net.Conn leaves unused connection operations outside the fixture.
type rfbMemoryConn struct {
	net.Conn
	input  *bytes.Reader
	output bytes.Buffer
}

func (c *rfbMemoryConn) Read(p []byte) (int, error)  { return c.input.Read(p) }
func (c *rfbMemoryConn) Write(p []byte) (int, error) { return c.output.Write(p) }

func framebufferStream(data []byte, width, height int) *VNCStream {
	return &VNCStream{
		ctx:    context.Background(),
		netCxn: &rfbMemoryConn{input: bytes.NewReader(data)},
		Frames: make(chan *VNCFrame, 1),
		Err:    make(chan error, 1),
		width:  width, height: height, fb: make([]byte, width*height*4),
	}
}

func rfbRect(x, y, width, height uint16, encoding int32, payload []byte) []byte {
	var b []byte
	for _, v := range []uint16{x, y, width, height} {
		b = binary.BigEndian.AppendUint16(b, v)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(encoding))
	return append(b, payload...)
}

func TestFramebufferResizeRawAndSnapshotOwnership(t *testing.T) {
	pixels := []byte{1, 2, 3, 0, 4, 5, 6, 0}
	data := []byte{0, 0, 2} // padding, two rectangles
	data = append(data, rfbRect(0, 0, 2, 1, -223, nil)...)
	data = append(data, rfbRect(0, 0, 2, 1, 0, pixels)...)
	s := framebufferStream(data, 1, 1)
	if err := s.readFramebufferUpdate(); err != nil {
		t.Fatal(err)
	}
	frame := <-s.Frames
	if frame.Width != 2 || frame.Height != 1 || !bytes.Equal(frame.Pixels, pixels) {
		t.Fatalf("decoded frame = %+v", frame)
	}
	s.fb[0] = 99
	if frame.Pixels[0] != 1 {
		t.Fatal("published frame aliases the live framebuffer")
	}
	clone := frame.Clone()
	frame.Pixels[1] = 88
	if clone.Pixels[1] != 2 {
		t.Fatal("Clone shares the original pixel buffer")
	}
}

func TestFramebufferReplacesQueuedStaleFrame(t *testing.T) {
	s := framebufferStream([]byte{0, 0, 0}, 1, 1)
	s.Frames <- &VNCFrame{Width: 99}
	s.fb[0] = 7
	if err := s.readFramebufferUpdate(); err != nil {
		t.Fatal(err)
	}
	if frame := <-s.Frames; frame.Width != 1 || frame.Pixels[0] != 7 {
		t.Fatalf("slow consumer received stale frame: %+v", frame)
	}
	// A consumer with no queue must not block the protocol reader either.
	s = framebufferStream([]byte{0, 0, 0}, 1, 1)
	s.Frames = make(chan *VNCFrame)
	if err := s.readFramebufferUpdate(); err != nil {
		t.Fatal(err)
	}
}

func TestCopyRectPreservesOverlappingSource(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		sx, sy, dx, dy, w, h uint16
		want                 []byte
	}{
		{"down", 0, 0, 0, 1, 2, 2, []byte{1, 2, 1, 2, 3, 4}},
		{"up", 0, 1, 0, 0, 2, 2, []byte{3, 4, 5, 6, 5, 6}},
		{"right", 0, 0, 1, 0, 1, 3, []byte{1, 1, 3, 3, 5, 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := binary.BigEndian.AppendUint16(nil, tc.sx)
			payload = binary.BigEndian.AppendUint16(payload, tc.sy)
			data := append([]byte{0, 0, 1}, rfbRect(tc.dx, tc.dy, tc.w, tc.h, 1, payload)...)
			s := framebufferStream(data, 2, 3)
			for i := 0; i < 6; i++ {
				s.fb[i*4] = byte(i + 1)
			}
			if err := s.readFramebufferUpdate(); err != nil {
				t.Fatal(err)
			}
			var got []byte
			for i := 0; i < 6; i++ {
				got = append(got, s.fb[i*4])
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("pixels = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRawRectangleClipsAndDrainsPayload(t *testing.T) {
	for _, tc := range []struct {
		name       string
		x, y, w, h int
		want       []byte
	}{
		{"right edge", 1, 0, 2, 1, []byte{0, 1, 0, 0}},
		{"bottom edge", 0, 1, 1, 2, []byte{0, 0, 1, 0}},
		{"beyond right edge", 5, 0, 1, 1, []byte{0, 0, 0, 0}},
		{"beyond bottom edge", 0, 5, 1, 1, []byte{0, 0, 0, 0}},
		{"zero width", 0, 0, 0, 1, []byte{0, 0, 0, 0}},
		{"zero height", 0, 0, 1, 0, []byte{0, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte{1, 2, 3, 0}, tc.w*tc.h)
			s := framebufferStream(append(payload, 99), 2, 2)
			if err := s.readRaw(tc.x, tc.y, tc.w, tc.h); err != nil {
				t.Fatal(err)
			}
			for i, want := range tc.want {
				if s.fb[i*4] != want {
					t.Fatalf("pixel %d = %d, want %d", i, s.fb[i*4], want)
				}
			}
			var next [1]byte
			if _, err := io.ReadFull(s.netCxn, next[:]); err != nil || next[0] != 99 {
				t.Fatalf("rectangle did not consume its payload: next=%v err=%v", next, err)
			}
		})
	}
}

func TestCopyRowClipsInvalidCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name              string
		sx, sy, dx, dy, w int
		want              []byte
	}{
		{"source row outside", 0, 2, 0, 0, 1, []byte{1, 2, 3, 4}},
		{"destination row outside", 0, 0, 0, 2, 1, []byte{1, 2, 3, 4}},
		{"negative source", -1, 0, 0, 1, 1, []byte{1, 2, 3, 4}},
		{"negative destination", 0, 0, -1, 1, 1, []byte{1, 2, 3, 4}},
		{"source clipped", 1, 0, 0, 1, 2, []byte{1, 2, 2, 4}},
		{"destination clipped", 0, 0, 1, 1, 2, []byte{1, 2, 3, 1}},
		{"source beyond width", 3, 0, 0, 1, 1, []byte{1, 2, 3, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := framebufferStream(nil, 2, 2)
			for i := 0; i < 4; i++ {
				s.fb[i*4] = byte(i + 1)
			}
			s.copyRow(tc.sx, tc.sy, tc.dx, tc.dy, tc.w)
			for i, want := range tc.want {
				if s.fb[i*4] != want {
					t.Fatalf("pixel %d = %d, want %d", i, s.fb[i*4], want)
				}
			}
		})
	}
}

func TestFramebufferRejectsTruncatedAndUnsupportedMessages(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"header", []byte{0}, "read fb update hdr"},
		{"rectangle", []byte{0, 0, 1, 0}, "read rect hdr"},
		{"raw pixels", append([]byte{0, 0, 1}, rfbRect(0, 0, 1, 1, 0, []byte{1})...), "read raw row"},
		{"copy source", append([]byte{0, 0, 1}, rfbRect(0, 0, 1, 1, 1, []byte{1})...), "read copyrect src"},
		{"encoding", append([]byte{0, 0, 1}, rfbRect(0, 0, 1, 1, 99, nil)...), "unsupported encoding 99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := framebufferStream(tc.data, 1, 1)
			err := s.readFramebufferUpdate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if len(s.Frames) != 0 {
				t.Fatal("published a partial frame after parse failure")
			}
		})
	}
}

func TestReadLoopDrainsSideMessagesAndClosesChannels(t *testing.T) {
	data := []byte{2}                     // Bell
	data = append(data, 1, 0, 0, 0, 0, 1) // one colour-map entry
	data = append(data, 0, 1, 0, 2, 0, 3)
	data = append(data, 3, 0, 0, 0, 0, 0, 0, 3, 'a', 'b', 'c') // clipboard
	data = append(data, 0, 0, 0, 1)                            // framebuffer update, one rectangle
	data = append(data, rfbRect(0, 0, 1, 1, 0, []byte{7, 8, 9, 0})...)
	s := framebufferStream(data, 1, 1)
	s.readLoop()
	frame, ok := <-s.Frames
	if !ok || !bytes.Equal(frame.Pixels, []byte{7, 8, 9, 0}) {
		t.Fatalf("decoded frame = %+v, open=%v", frame, ok)
	}
	if _, ok := <-s.Frames; ok {
		t.Fatal("Frames channel was not closed")
	}
	if err := <-s.Err; !errors.Is(err, io.EOF) {
		t.Fatalf("terminal error = %v, want EOF", err)
	}
	if _, ok := <-s.Err; ok {
		t.Fatal("Err channel was not closed")
	}
}

func TestReadLoopReportsProtocolErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"unknown type", []byte{99}, "unknown server msg type 99"},
		{"truncated frame", []byte{0}, "read fb update hdr"},
		{"colour header", []byte{1}, "EOF"},
		{"colour payload", []byte{1, 0, 0, 0, 0, 1}, "EOF"},
		{"clipboard header", []byte{3}, "EOF"},
		{"clipboard payload", []byte{3, 0, 0, 0, 0, 0, 0, 2}, "EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := framebufferStream(tc.data, 1, 1)
			s.readLoop()
			err := <-s.Err
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	// Cancellation must close both channels without inventing a protocol error.
	s := framebufferStream(nil, 1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	cancel()
	s.readLoop()
	if err, ok := <-s.Err; ok || err != nil {
		t.Fatalf("cancelled stream reported %v", err)
	}
	if _, ok := <-s.Frames; ok {
		t.Fatal("cancelled stream left Frames open")
	}
	// Reporting a second error cannot block the reader when Err is full.
	s = framebufferStream(nil, 1, 1)
	s.emitErr(io.EOF)
	s.emitErr(io.ErrUnexpectedEOF)
	if err := <-s.Err; err != io.EOF {
		t.Fatalf("first error replaced: %v", err)
	}
}

func TestInputEventsEncodeKeysAndClampPointers(t *testing.T) {
	s := framebufferStream(nil, 10, 20)
	if err := s.SendKey(0xff0d, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SendKey(0xff0d, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SendPointer(-1, -5, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SendPointer(100, 200, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SendPointer(3, 4, 2); err != nil {
		t.Fatal(err)
	}
	want := []byte{
		4, 1, 0, 0, 0, 0, 0xff, 0x0d,
		4, 0, 0, 0, 0, 0, 0xff, 0x0d,
		5, 1, 0, 0, 0, 0,
		5, 0, 0, 9, 0, 19,
		5, 2, 0, 3, 0, 4,
	}
	if got := s.netCxn.(*rfbMemoryConn).output.Bytes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("wire events = %v, want %v", got, want)
	}
}
