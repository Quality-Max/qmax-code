package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

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

func TestStandaloneToolsListContainsOnlyWorkspaceTools(t *testing.T) {
	req := request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}
	resp := dispatch(req, &api.SessionContext{LocalOnly: true}, "test")
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map", resp.Result)
	}
	tools, ok := result["tools"].([]toolDef)
	if !ok {
		t.Fatalf("tools type = %T, want []toolDef", result["tools"])
	}
	want := map[string]bool{
		"read_file":   true,
		"run_command": true,
		"edit_file":   true,
		"write_file":  true,
	}
	if len(tools) != len(want) {
		t.Fatalf("standalone MCP tools = %d, want %d: %+v", len(tools), len(want), tools)
	}
	for _, tool := range tools {
		if !want[tool.Name] {
			t.Errorf("standalone MCP exposed non-local tool %q", tool.Name)
		}
	}
}

func TestStandaloneToolsCallBlocksUndisclosedCloudTool(t *testing.T) {
	req := request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"list_projects","arguments":{}}`),
	}
	resp := dispatch(req, &api.SessionContext{LocalOnly: true}, "test")
	data, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(data), "unavailable in standalone mode") {
		t.Fatalf("cloud tool call was not blocked: %s", data)
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

func TestRunServerRedirectsProcessStdoutAwayFromJSONRPC(t *testing.T) {
	origStdin := os.Stdin
	origStdout := os.Stdout
	origStderr := os.Stderr

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	restore := func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
		os.Stderr = origStderr
	}
	defer func() {
		restore()
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		_ = errR.Close()
		_ = errW.Close()
	}()

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW

	done := make(chan struct{})
	go func() {
		RunServer("test")
		close(done)
	}()

	if _, err := fmt.Fprintln(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`); err != nil {
		t.Fatal(err)
	}

	stdoutReader := bufio.NewReader(outR)
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := stdoutReader.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()

	var firstLine string
	select {
	case firstLine = <-lineCh:
	case err := <-errCh:
		t.Fatalf("reading initialize response: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initialize response")
	}

	var msg response
	if err := json.Unmarshal([]byte(firstLine), &msg); err != nil {
		t.Fatalf("initialize response is not JSON-RPC: %v: %q", err, firstLine)
	}
	if msg.JSONRPC != "2.0" || msg.ID != float64(1) {
		t.Fatalf("initialize response = %+v, want JSON-RPC response with id 1", msg)
	}

	const stray = "stray stdout from tool path"
	if _, err := fmt.Fprintln(os.Stdout, stray); err != nil {
		t.Fatal(err)
	}

	if err := inW.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunServer to exit")
	}
	restore()

	if err := outW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := errW.Close(); err != nil {
		t.Fatal(err)
	}

	remainingStdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderrBytes, err := io.ReadAll(errR)
	if err != nil {
		t.Fatal(err)
	}

	if stdout := firstLine + string(remainingStdout); strings.Contains(stdout, stray) {
		t.Fatalf("stray process stdout leaked onto JSON-RPC stdout: %q", stdout)
	}
	if stderr := string(stderrBytes); !strings.Contains(stderr, stray) {
		t.Fatalf("stray process stdout was not redirected to stderr; stderr = %q", stderr)
	}
}
