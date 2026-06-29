package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qualitymax/qmax-code/internal/api"
)

func TestHandleLineReturnsParseErrorForMalformedJSON(t *testing.T) {
	resp, ok := handleLine([]byte("Reading additional input from stdin..."), &api.SessionContext{}, "test")
	if !ok {
		t.Fatal("malformed request should produce a JSON-RPC error response")
	}
	if resp.JSONRPC != "2.0" || resp.Error == nil {
		t.Fatalf("response = %+v, want JSON-RPC error", resp)
	}
	if resp.Error.Code != -32700 {
		t.Fatalf("error code = %d, want -32700", resp.Error.Code)
	}
}

func TestHandleLineIgnoresNotifications(t *testing.T) {
	_, ok := handleLine([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), &api.SessionContext{}, "test")
	if ok {
		t.Fatal("notification should not produce a response")
	}
}

func TestHandleLineRejectsInvalidJSONRPCVersion(t *testing.T) {
	resp, ok := handleLine([]byte(`{"jsonrpc":"1.0","id":1,"method":"tools/list"}`), &api.SessionContext{}, "test")
	if !ok {
		t.Fatal("invalid request should produce a response")
	}
	if resp.Error == nil || resp.Error.Code != -32600 {
		t.Fatalf("response = %+v, want invalid request error", resp)
	}
}

// TestDispatchRecoversFromPanic verifies that a panicking tool handler yields
// a JSON-RPC error response (-32603) instead of crashing the server process.
// A crash would EOF stdout and kill the client's rmcp transport worker — the
// same failure mode as stray stdout writes.
func TestDispatchRecoversFromPanic(t *testing.T) {
	// A nil sctx causes agent.ExecuteTool to nil-deref, exercising the
	// deferred recover. Whether LoadQMaxCodeConfig returns nil or not, the
	// panic fires (either at sctx.LiveFeed or sctx.API) and is recovered.
	req := request{
		JSONRPC: "2.0",
		ID:      42,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"list_projects","arguments":{}}`),
	}
	resp := dispatch(req, nil, "test")
	if resp.JSONRPC != "2.0" {
		t.Fatalf("JSONRPC = %q, want 2.0", resp.JSONRPC)
	}
	if resp.ID != 42 {
		t.Fatalf("ID = %v, want 42", resp.ID)
	}
	if resp.Error == nil {
		t.Fatal("expected an internal-error response, got nil error")
	}
	if resp.Error.Code != -32603 {
		t.Fatalf("error code = %d, want -32603 (internal error)", resp.Error.Code)
	}
}

// TestServeMCPOutputIsCleanJSON drives the serve loop through a standard
// initialize → tools/list handshake and asserts every non-empty line on the
// output writer is valid JSON-RPC. This is the output contract the rmcp
// transport depends on: a single corrupt or empty line deserializes to
// nothing and kills the worker.
func TestServeMCPOutputIsCleanJSON(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		"",
	}, "\n")

	var out bytes.Buffer
	serveMCP(strings.NewReader(input), &out, &api.SessionContext{}, "test")

	if out.Len() == 0 {
		t.Fatal("serveMCP produced no output")
	}

	for i, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			t.Fatalf("line %d: empty line on JSON-RPC output — this is exactly "+
				"what triggers rmcp's line:0,column:0 fatal", i)
		}
		var msg response
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("line %d: not valid JSON-RPC (%v): %s", i, err, line)
		}
		if msg.JSONRPC != "2.0" {
			t.Fatalf("line %d: jsonrpc = %q, want 2.0", i, msg.JSONRPC)
		}
	}

	// The notification must not produce a response — only two replies
	// (initialize + tools/list) should be on the wire.
	responses := strings.Count(out.String(), "\"jsonrpc\"")
	if responses != 2 {
		t.Fatalf("expected 2 JSON-RPC responses, got %d (notifications must not be echoed)", responses)
	}
}
