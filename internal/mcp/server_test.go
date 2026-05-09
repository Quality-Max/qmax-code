package mcp

import (
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
