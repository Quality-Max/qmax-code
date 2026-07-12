package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
)

// RunServer starts an MCP (Model Context Protocol) server over stdin/stdout.
// CC spawns this as a subprocess when qmax-code is configured as an MCP server:
//
//	qmax-code serve --mcp
//
// The server exposes all qmax tools to Claude Code so CC can call them via
// its native tool-use mechanism — no Anthropic API tokens consumed.
//
// version is the qmax-code build version, surfaced in the initialize handshake.
func RunServer(version string) {
	auth := api.LoadAuth()
	var apiClient *api.APIClient
	if auth != nil && auth.IsAuthenticated() {
		apiClient = api.NewAPIClient(auth)
	}

	appConfig := api.LoadQMaxCodeConfig()
	sctx := &api.SessionContext{
		QMaxCfg:   api.LoadQMaxConfig(),
		QMaxBin:   api.DiscoverQMaxBinary(),
		API:       apiClient,
		Auth:      auth,
		ProjectID: appConfig.DefaultProject,
		LiveFeed:  appConfig.LiveFeed,
	}

	// Project ID override: agent.CCAgent writes the active project into the MCP env.
	if pid, err := strconv.Atoi(os.Getenv("QMAX_PROJECT_ID")); err == nil && pid > 0 {
		sctx.ProjectID = pid
	}
	// Parent sets QMAX_LIVE_FEED=1 when /live is on. Honour that even if
	// the on-disk config disagrees — the env var reflects the current
	// state of the running parent REPL more reliably than disk.
	if v := os.Getenv("QMAX_LIVE_FEED"); v == "1" || v == "true" {
		sctx.LiveFeed = true
	}

	// Reserve the original stdout stream for JSON-RPC only, then route every
	// other stdout write to stderr. Reassigning os.Stdout protects normal Go
	// fmt.Print calls, but not lower-level fd 1 writes from subprocesses or
	// libraries. redirectStdoutForMCP duplicates the original stdout for the
	// encoder and, on Unix, also redirects the actual fd 1 to stderr.
	jsonOut, restoreStdout, err := redirectStdoutForMCP()
	if err != nil {
		fmt.Fprintf(os.Stderr, "qmax-code MCP stdout isolation failed: %v\n", err)
		return
	}
	defer restoreStdout()

	serveMCP(os.Stdin, jsonOut, sctx, version)
}

// serveMCP runs the newline-delimited JSON-RPC read/respond loop against the
// given reader (client stdin) and writer (client stdout). Extracted from
// RunServer so the output contract — only valid JSON-RPC lines on out — can be
// tested without swapping global os.Stdin/os.Stdout.
func serveMCP(in io.Reader, out io.Writer, sctx *api.SessionContext, version string) {
	encoder := json.NewEncoder(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB — tool results can be verbose

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if resp, ok := handleLine(line, sctx, version); ok {
			_ = encoder.Encode(resp)
		}
	}
}

func handleLine(line []byte, sctx *api.SessionContext, version string) (response, bool) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return errResp(nil, -32700, "parse error"), true
	}

	// JSON-RPC notifications have no id and require no response.
	if req.ID == nil {
		return response{}, false
	}

	if req.JSONRPC != "2.0" {
		return errResp(req.ID, -32600, "invalid request: jsonrpc must be 2.0"), true
	}
	if req.Method == "" {
		return errResp(req.ID, -32600, "invalid request: method is required"), true
	}

	return dispatch(req, sctx, version), true
}

// --- JSON-RPC / MCP types ---

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcErr     `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type callParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// --- Request dispatcher ---

// dispatch routes a parsed JSON-RPC request to the appropriate handler. A
// deferred recover ensures a panicking tool handler (nil deref, index out of
// range, etc.) yields a JSON-RPC error response rather than crashing the
// server process — a crash would EOF stdout and kill the client's transport
// worker the same way stray stdout writes do.
func dispatch(req request, sctx *api.SessionContext, version string) (resp response) {
	defer func() {
		if r := recover(); r != nil {
			resp = errResp(req.ID, -32603, fmt.Sprintf("internal error: %v", r))
		}
	}()

	switch req.Method {
	case "initialize":
		return okResp(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "qmax-code", "version": version},
		})

	case "tools/list":
		return okResp(req.ID, map[string]interface{}{"tools": buildToolList()})

	case "tools/call":
		var params callParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, -32602, "invalid params: "+err.Error())
		}
		// Refresh LiveFeed from on-disk config every call so the
		// parent REPL's `/live on|off` toggle takes effect without
		// restarting the subprocess. ProjectID is read once at startup
		// because it's plumbed via env; LiveFeed flips often enough
		// during a session that a per-call disk read pays for itself.
		if cfg := api.LoadQMaxCodeConfig(); cfg != nil {
			sctx.LiveFeed = cfg.LiveFeed
			if v := os.Getenv("QMAX_LIVE_FEED"); v == "1" || v == "true" {
				sctx.LiveFeed = true
			}
		}
		result := agent.ExecuteTool(params.Name, params.Arguments, sctx, context.Background())
		return okResp(req.ID, map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": result}},
			"isError": false,
		})

	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func okResp(id interface{}, result interface{}) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id interface{}, code int, msg string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
}

// buildToolList converts qmax ToolDefs to MCP format.
// The only structural difference is camelCase inputSchema vs Anthropic's input_schema.
func buildToolList() []toolDef {
	defs := agent.BuildMCPToolDefs()
	out := make([]toolDef, len(defs))
	for i, d := range defs {
		out[i] = toolDef(d)
	}
	return out
}
