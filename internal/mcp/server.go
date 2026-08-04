package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/qualitymax/qmax-code/internal/agent"
	"github.com/qualitymax/qmax-code/internal/api"
	"github.com/qualitymax/qmax-code/internal/security"
	"github.com/qualitymax/qmax-code/internal/sysutil"
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
	appConfig := api.LoadQMaxCodeConfig()
	localOnly := appConfig.LocalOnly || sysutil.EnvEnabled(api.LocalOnlyEnv)

	var auth *api.AuthConfig
	var apiClient *api.APIClient
	var qmaxCfg api.QMaxConfig
	var qmaxBin string
	if !localOnly {
		auth = api.LoadAuth()
		if auth != nil && auth.IsAuthenticated() {
			apiClient = api.NewAPIClient(auth)
		}
		qmaxCfg = api.LoadQMaxConfig()
		qmaxBin = api.DiscoverQMaxBinary()
	}

	sctx := &api.SessionContext{
		LocalOnly: localOnly,
		QMaxCfg:   qmaxCfg,
		QMaxBin:   qmaxBin,
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
	serveMCPWithState(in, out, newServerState(sctx, version))
}

type serverState struct {
	sctx           *api.SessionContext
	version        string
	cloud          *cloudMCPClient
	cloudToolNames map[string]bool
}

func newServerState(sctx *api.SessionContext, version string) *serverState {
	var cloud *cloudMCPClient
	if sctx != nil && !sctx.LocalOnly {
		cloud = newCloudMCPClient(sctx.Auth, version)
	}
	return &serverState{sctx: sctx, version: version, cloud: cloud}
}

func serveMCPWithState(in io.Reader, out io.Writer, state *serverState) {
	encoder := json.NewEncoder(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB — tool results can be verbose

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if resp, ok := handleLineWithState(line, state); ok {
			_ = encoder.Encode(resp)
		}
	}
}

func handleLine(line []byte, sctx *api.SessionContext, version string) (response, bool) {
	return handleLineWithState(line, newServerState(sctx, version))
}

func handleLineWithState(line []byte, state *serverState) (response, bool) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return errResp(nil, -32700, "parse error"), true
	}

	// JSON-RPC notifications have no id and require no response.
	if req.ID == nil {
		if state.cloud != nil && strings.HasPrefix(req.Method, "notifications/") {
			if _, _, err := state.cloud.call(context.Background(), req); err != nil {
				fmt.Fprintf(os.Stderr, "qmax-code MCP notification forwarding failed: %s\n",
					security.RedactSensitive(err.Error()))
			}
		}
		return response{}, false
	}

	if req.JSONRPC != "2.0" {
		return errResp(req.ID, -32600, "invalid request: jsonrpc must be 2.0"), true
	}
	if req.Method == "" {
		return errResp(req.ID, -32600, "invalid request: method is required"), true
	}

	return dispatchWithState(req, state), true
}

// --- JSON-RPC / MCP types ---

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
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
	Name         string                     `json:"name"`
	Title        string                     `json:"title,omitempty"`
	Description  string                     `json:"description"`
	InputSchema  map[string]interface{}     `json:"inputSchema"`
	OutputSchema map[string]interface{}     `json:"outputSchema,omitempty"`
	Annotations  map[string]interface{}     `json:"annotations,omitempty"`
	Meta         map[string]interface{}     `json:"_meta,omitempty"`
	Icons        []interface{}              `json:"icons,omitempty"`
	Extra        map[string]json.RawMessage `json:"-"`
}

func (t *toolDef) UnmarshalJSON(data []byte) error {
	type knownToolDef toolDef
	var known knownToolDef
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{
		"name", "title", "description", "inputSchema", "outputSchema",
		"annotations", "_meta", "icons",
	} {
		delete(fields, name)
	}
	*t = toolDef(known)
	if len(fields) > 0 {
		t.Extra = fields
	}
	return nil
}

func (t toolDef) MarshalJSON() ([]byte, error) {
	type knownToolDef toolDef
	data, err := json.Marshal(knownToolDef(t))
	if err != nil {
		return nil, err
	}
	if len(t.Extra) == 0 {
		return data, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for name, value := range t.Extra {
		if _, known := fields[name]; !known {
			fields[name] = value
		}
	}
	return json.Marshal(fields)
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
	return dispatchWithState(req, newServerState(sctx, version))
}

func dispatchWithState(req request, state *serverState) (resp response) {
	defer func() {
		if r := recover(); r != nil {
			resp = errResp(req.ID, -32603, fmt.Sprintf("internal error: %v", r))
		}
	}()

	switch req.Method {
	case "initialize":
		if state.cloud != nil {
			rawParams, err := state.cloud.setClientInfo(req.Params)
			if err != nil {
				return errResp(req.ID, -32602, "invalid params: "+err.Error())
			}
			upstream := req
			upstream.Params = rawParams
			cloudResp, ok, err := state.cloud.call(context.Background(), upstream)
			if err != nil {
				return errResp(req.ID, -32603, err.Error())
			}
			if !ok {
				return errResp(req.ID, -32603, "cloud MCP returned no initialize response")
			}
			if cloudResp.Error != nil {
				return cloudErrorResponse(req.ID, cloudResp.Error)
			}
			result, ok := cloudResp.Result.(map[string]interface{})
			if !ok {
				return errResp(req.ID, -32603, "cloud MCP returned an invalid initialize result")
			}
			result["serverInfo"] = map[string]interface{}{
				"name":    "qmax-code",
				"version": state.version,
			}
			return okResp(req.ID, result)
		}
		return okResp(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "qmax-code", "version": state.version},
		})

	case "tools/list":
		if state.cloud != nil {
			result, cloudErr, err := state.listCloudTools(context.Background(), req)
			if err != nil {
				return errResp(req.ID, -32603, err.Error())
			}
			if cloudErr != nil {
				return cloudErrorResponse(req.ID, cloudErr)
			}
			return okResp(req.ID, result)
		}
		localOnly := state.sctx != nil && state.sctx.LocalOnly
		return okResp(req.ID, map[string]interface{}{"tools": buildToolList(localOnly)})

	case "tools/call":
		var params callParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, -32602, "invalid params: "+err.Error())
		}
		if params.Arguments == nil {
			params.Arguments = map[string]interface{}{}
		}
		if state.cloud != nil && !agent.IsLocalTool(params.Name) {
			projectID := 0
			if state.sctx != nil {
				projectID = state.sctx.ProjectID
			}
			cloudName, cloudArgs, err := translateCloudCall(
				params.Name,
				params.Arguments,
				projectID,
				state.cloudToolNames,
			)
			if err != nil {
				return errResp(req.ID, -32602, "invalid params: "+err.Error())
			}
			rawParams, err := json.Marshal(callParams{Name: cloudName, Arguments: cloudArgs})
			if err != nil {
				return errResp(req.ID, -32603, "failed to encode cloud tool call")
			}
			upstream := req
			upstream.Params = rawParams
			cloudResp, ok, err := state.cloud.call(context.Background(), upstream)
			if err != nil {
				return errResp(req.ID, -32603, err.Error())
			}
			if !ok {
				return errResp(req.ID, -32603, "cloud MCP returned no tools/call response")
			}
			if cloudResp.Error != nil {
				return cloudErrorResponse(req.ID, cloudResp.Error)
			}
			return okResp(req.ID, cloudResp.Result)
		}

		// Refresh LiveFeed from on-disk config every local or standalone call so the
		// parent REPL's `/live on|off` toggle takes effect without
		// restarting the subprocess. ProjectID is read once at startup
		// because it's plumbed via env; LiveFeed flips often enough
		// during a session that a per-call disk read pays for itself.
		if cfg := api.LoadQMaxCodeConfig(); cfg != nil {
			if state.sctx != nil {
				state.sctx.LiveFeed = cfg.LiveFeed
				if v := os.Getenv("QMAX_LIVE_FEED"); v == "1" || v == "true" {
					state.sctx.LiveFeed = true
				}
			}
		}
		result := agent.ExecuteTool(params.Name, params.Arguments, state.sctx, context.Background())
		return okResp(req.ID, map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": result}},
			"isError": false,
		})

	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (state *serverState) listCloudTools(ctx context.Context, downstream request) (map[string]interface{}, *rpcErr, error) {
	const maxToolPages = 100

	pageReq := downstream
	pageReq.Params = nil
	var combined map[string]interface{}
	var allTools []toolDef
	seenCursors := make(map[string]bool)

	for page := 0; page < maxToolPages; page++ {
		cloudResp, ok, err := state.cloud.call(ctx, pageReq)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("cloud MCP returned no tools/list response")
		}
		if cloudResp.Error != nil {
			return nil, cloudResp.Error, nil
		}
		result, ok := cloudResp.Result.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("cloud MCP returned an invalid tools/list result")
		}
		rawTools, err := json.Marshal(result["tools"])
		if err != nil {
			return nil, nil, fmt.Errorf("cloud MCP returned an invalid tools/list result")
		}
		var pageTools []toolDef
		if err := json.Unmarshal(rawTools, &pageTools); err != nil || pageTools == nil {
			return nil, nil, fmt.Errorf("cloud MCP returned an invalid tools/list result")
		}
		if combined == nil {
			combined = result
		}
		allTools = append(allTools, pageTools...)

		nextCursor, _ := result["nextCursor"].(string)
		if nextCursor == "" {
			state.cloudToolNames = make(map[string]bool, len(allTools))
			for _, tool := range allTools {
				state.cloudToolNames[tool.Name] = true
			}
			delete(combined, "nextCursor")
			combined["tools"] = buildAuthenticatedToolList(allTools)
			return combined, nil, nil
		}
		if seenCursors[nextCursor] {
			return nil, nil, fmt.Errorf("cloud MCP tools/list repeated a pagination cursor")
		}
		seenCursors[nextCursor] = true
		pageReq.Params, err = json.Marshal(map[string]string{"cursor": nextCursor})
		if err != nil {
			return nil, nil, fmt.Errorf("encode cloud MCP tools/list cursor: %w", err)
		}
	}
	return nil, nil, fmt.Errorf("cloud MCP tools/list exceeded %d pages", maxToolPages)
}

func okResp(id interface{}, result interface{}) response {
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id interface{}, code int, msg string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: msg}}
}

func cloudErrorResponse(id interface{}, cloudErr *rpcErr) response {
	return errResp(id, cloudErr.Code, security.RedactSensitive(cloudErr.Message))
}

// buildToolList converts qmax ToolDefs to MCP format.
// The only structural difference is camelCase inputSchema vs Anthropic's input_schema.
func buildToolList(localOnly bool) []toolDef {
	defs := agent.BuildMCPToolDefsForMode(localOnly)
	out := make([]toolDef, len(defs))
	for i, d := range defs {
		out[i] = toolDef{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		}
	}
	return out
}
