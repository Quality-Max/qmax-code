// Package httpx is the SINGLE egress chokepoint for qmax-code. Every outbound
// HTTP request in the codebase must be built with NewRequest and sent through a
// client from NewClient, so that the Exposure Receipt records it — destination,
// category, byte size, and content SHA-256, never content itself.
//
// The static guard in guard_test.go enforces that no other package constructs
// HTTP clients/requests or imports a third-party HTTP library, making an
// un-receipted egress path impossible to merge — the load-bearing half of the
// "Receipts, not promises" guarantee.
//
// Quality commitments enforced here:
//   - Stream, don't buffer: request bodies are hashed by a streaming reader as
//     the transport sends them, so a multi-MB prompt (inlined files, images) is
//     never doubled in memory (io.ReadAll on the request body is forbidden).
//   - Never silent: every attempt is recorded, including transport errors. The
//     receipt module routes entries to the process-global session receipt when
//     no receipt-bearing context is present, so even a plain context.Background
//     request is accounted for.
package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"sync"
	"time"

	receipt "github.com/Quality-Max/qmax-receipt"
	"github.com/coder/websocket"
	"github.com/qualitymax/qmax-code/internal/exposure"
)

// NewClient returns an *http.Client whose Transport records every request into
// the active Exposure Receipt. Timeout semantics are unchanged from a plain
// &http.Client{Timeout: timeout}.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &receiptTransport{base: http.DefaultTransport},
	}
}

// NewRequest builds an *http.Request bound to ctx. It is the only sanctioned way
// to construct an outbound request outside this package: callers that need to set
// custom headers or stream the response (LLM SSE) use NewRequest + NewClient.Do
// rather than a raw http.NewRequest, which the egress guard forbids.
//
// Pass context.Background() when no request-scoped context is available; the
// receipt still routes to the process-global session run.
func NewRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return http.NewRequestWithContext(ctx, method, url, body)
}

// modelKey carries the LLM model id for the current request so the receipt can
// attribute which model saw a prompt. It is optional; non-LLM requests leave it
// unset and the recorded Entry.Model stays nil.
type modelKey struct{}

// categoryKey carries an explicit category for egress protocols whose URL path
// alone cannot identify their purpose (for example a WebSocket handshake).
type categoryKey struct{}

const webSocketHandshakeTimeout = 15 * time.Second

// WithModel annotates ctx with the LLM model id for requests built from it.
// LLM call sites (Anthropic, Cerebras, Ollama) wrap their context with this so
// the receipt records model attribution; all other traffic omits it.
func WithModel(ctx context.Context, model string) context.Context {
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, modelKey{}, model)
}

// WithCategory annotates ctx with an explicit receipt category. Most HTTP
// callers rely on exposure.Classify; use this only for protocol handshakes.
func WithCategory(ctx context.Context, category string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if category == "" {
		return ctx
	}
	return context.WithValue(ctx, categoryKey{}, category)
}

func modelFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if m, ok := ctx.Value(modelKey{}).(string); ok {
		return m
	}
	return ""
}

func categoryFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if category, ok := ctx.Value(categoryKey{}).(string); ok {
		return category
	}
	return ""
}

// DialWebSocket performs a WebSocket handshake through the recording HTTP
// client. The WebSocket protocol itself uses the upgraded connection, while
// the destination and handshake egress are captured in the session receipt.
func DialWebSocket(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = WithCategory(ctx, exposure.CatVNCControl)

	var dialOpts websocket.DialOptions
	if opts != nil {
		dialOpts = *opts
		dialOpts.Subprotocols = append([]string(nil), opts.Subprotocols...)
	}
	// coder/websocket converts this client timeout into a handshake context
	// deadline, then clears the client timeout before returning the upgraded
	// connection. This bounds a stalled upgrade without imposing a lifetime on
	// the VNC stream itself.
	dialOpts.HTTPClient = NewClient(webSocketHandshakeTimeout)
	return websocket.Dial(ctx, url, &dialOpts)
}

type receiptTransport struct{ base http.RoundTripper }

// hashingBody hashes and counts bytes as the transport reads the request body
// to send it — no full-body buffering.
type hashingBody struct {
	rc io.ReadCloser
	h  hash.Hash
	n  int64
	mu sync.Mutex
	// complete is true only when the transport consumed the body through EOF.
	// A server can respond before consuming a request body, in which case a
	// prefix hash would be misleading and must not be signed as complete.
	complete bool
}

func (b *hashingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > 0 {
		_, _ = b.h.Write(p[:n]) // hash.Hash.Write never errors
		b.n += int64(n)
	}
	if err == io.EOF {
		b.complete = true
	}
	return n, err
}

func (b *hashingBody) Close() error { return b.rc.Close() }

func (b *hashingBody) snapshot() (bytes int64, sha256 string, complete bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n, hex.EncodeToString(b.h.Sum(nil)), b.complete
}

func (t *receiptTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := receipt.FromContext(req.Context())

	entry := receipt.Entry{
		Timestamp: time.Now().UTC(),
		Method:    req.Method,
		Host:      req.URL.Host,
		Path:      receipt.Templatize(req.URL.Path),
		Category:  exposure.Classify(req.URL.Host, req.URL.Path),
		Allowed:   true, // qmax-code records egress; runtime allow-listing is tracked separately on the epic.
	}
	if m := modelFromContext(req.Context()); m != "" {
		entry.Model = &m
	}
	if category := categoryFromContext(req.Context()); category != "" {
		entry.Category = category
	}

	// Wrap the body so it is hashed+counted as it streams to the wire.
	var hb *hashingBody
	if req.Body != nil {
		hb = &hashingBody{rc: req.Body, h: sha256.New()}
		req.Body = hb
	}

	resp, err := t.base.RoundTrip(req)

	// A body hash is only trustworthy after EOF. If a peer responds before the
	// body is drained, report the metadata as unavailable rather than signing a
	// hash of a prefix as if it represented the complete request.
	if hb == nil {
		empty := sha256.Sum256(nil)
		entry.ReqSHA256 = hex.EncodeToString(empty[:])
	} else {
		bytes, digest, complete := hb.snapshot()
		if complete {
			entry.ReqBytes = bytes
			entry.ReqSHA256 = digest
		} else {
			entry.Note = "request-body-incomplete: byte count and hash unavailable"
		}
	}
	if err != nil {
		entry.Note = appendNote(entry.Note, transportErrorNote(err))
	} else if resp != nil {
		entry.RespStatus = resp.StatusCode
		entry.RespBytes = resp.ContentLength
	}
	rec.Record(entry)
	return resp, err
}

// transportErrorNote records a stable diagnostic category without copying an
// arbitrary transport error into a customer-held receipt. Error strings may
// contain URLs, proxy details, or credentials supplied by a custom transport.
func transportErrorNote(err error) string {
	return fmt.Sprintf("transport-error: %T", err)
}

func appendNote(existing, next string) string {
	if next == "" {
		return existing
	}
	if existing == "" {
		return next
	}
	return existing + "; " + next
}
