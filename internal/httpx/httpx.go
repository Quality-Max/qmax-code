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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"net/http"
	"time"

	receipt "github.com/Quality-Max/qmax-receipt"
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

// WithModel annotates ctx with the LLM model id for requests built from it.
// LLM call sites (Anthropic, Cerebras, Ollama) wrap their context with this so
// the receipt records model attribution; all other traffic omits it.
func WithModel(ctx context.Context, model string) context.Context {
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, modelKey{}, model)
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

type receiptTransport struct{ base http.RoundTripper }

// hashingBody hashes and counts bytes as the transport reads the request body
// to send it — no full-body buffering.
type hashingBody struct {
	rc io.ReadCloser
	h  hash.Hash
	n  int64
}

func (b *hashingBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		_, _ = b.h.Write(p[:n]) // hash.Hash.Write never errors
		b.n += int64(n)
	}
	return n, err
}

func (b *hashingBody) Close() error { return b.rc.Close() }

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

	// Wrap the body so it is hashed+counted as it streams to the wire.
	var hb *hashingBody
	if req.Body != nil {
		hb = &hashingBody{rc: req.Body, h: sha256.New()}
		req.Body = hb
	} else {
		hb = &hashingBody{rc: io.NopCloser(bytes.NewReader(nil)), h: sha256.New()}
	}

	resp, err := t.base.RoundTrip(req)

	entry.ReqBytes = hb.n
	entry.ReqSHA256 = hex.EncodeToString(hb.h.Sum(nil))
	if err != nil {
		entry.Note = "transport-error: " + err.Error()
	} else if resp != nil {
		entry.RespStatus = resp.StatusCode
		entry.RespBytes = resp.ContentLength
	}
	rec.Record(entry)
	return resp, err
}
