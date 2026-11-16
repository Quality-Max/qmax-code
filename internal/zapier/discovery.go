// Package zapier qualifies the read-only Codex MCP integration. Authentication
// and HTTP transport belong to Codex; qmax never reads OAuth credentials.
package zapier

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"time"
)

// Server contains only structural discovery evidence. No URLs, descriptions,
// arguments, resource contents, provider errors or credentials are returned.
type Server struct {
	Name       string   `json:"name"`
	AuthStatus string   `json:"auth_status"`
	Tools      []string `json:"tools"`
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)

// Discover starts no model turn and never approves server requests. The caller
// must set a deadline. Codex inherits CODEX_HOME exactly as an agent turn does.
func Discover(ctx context.Context, binary string) ([]Server, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-c", `mcp_oauth_credentials_store="keyring"`, "app-server", "--stdio", "--strict-config")
	// A descendant can inherit stderr. Bound Wait even if it outlives Codex.
	cmd.WaitDelay = 2 * time.Second
	// A nil stderr goes directly to the null device, avoiding an inherited
	// copy pipe when Codex is installed through a launcher script.
	cmd.Stderr = nil // diagnostics can contain OAuth URLs or provider data
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New("cannot open Codex discovery input")
	}
	defer func() { _ = in.Close() }()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.New("cannot open Codex discovery output")
	}
	if cmd.Start() != nil {
		return nil, errors.New("cannot start Codex MCP discovery")
	}
	defer func() { _ = in.Close(); cancel(); _ = cmd.Wait() }()
	return discover(ctx, in, out)
}

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func discover(ctx context.Context, in io.Writer, out io.Reader) ([]Server, error) {
	responses := make(chan message)
	readCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		defer close(responses)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var msg message
			if json.Unmarshal(scanner.Bytes(), &msg) != nil {
				return
			}
			select {
			case responses <- msg:
			case <-readCtx.Done():
				return
			}
		}
	}()
	encoder := json.NewEncoder(in)
	nextID := 0
	request := func(method string, params any) (json.RawMessage, error) {
		nextID++
		if encoder.Encode(map[string]any{"id": nextID, "method": method, "params": params}) != nil {
			return nil, errors.New("codex discovery connection closed")
		}
		for {
			select {
			case <-ctx.Done():
				return nil, errors.New("codex MCP discovery canceled or timed out")
			case msg, ok := <-responses:
				if !ok {
					return nil, errors.New("codex MCP discovery failed; check client compatibility and connection")
				}
				if msg.Method != "" {
					if len(msg.ID) != 0 {
						// Elicitation, approval and OAuth navigation need a human.
						if encoder.Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32601, "message": "Interactive requests are not authorized during discovery"}}) != nil {
							return nil, errors.New("codex discovery connection closed")
						}
					}
					continue
				}
				var id int
				if json.Unmarshal(msg.ID, &id) != nil || id != nextID {
					return nil, errors.New("unexpected Codex discovery response")
				}
				if len(msg.Error) != 0 || len(msg.Result) == 0 || string(msg.Result) == "null" {
					return nil, errors.New("codex rejected MCP discovery; details suppressed")
				}
				return msg.Result, nil
			}
		}
	}
	if _, err := request("initialize", map[string]any{"clientInfo": map[string]string{"name": "qmax_zapier_status", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}); err != nil {
		return nil, err
	}
	if encoder.Encode(map[string]string{"method": "initialized"}) != nil {
		return nil, errors.New("codex discovery connection closed")
	}
	servers := []Server{}
	var cursor *string
	for range 100 {
		raw, err := request("mcpServerStatus/list", map[string]any{"detail": "toolsAndAuthOnly", "cursor": cursor})
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []struct {
				Name  string                     `json:"name"`
				Auth  string                     `json:"authStatus"`
				Tools map[string]json.RawMessage `json:"tools"`
			} `json:"data"`
			Cursor *string `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &page) != nil || page.Data == nil {
			return nil, errors.New("invalid Codex MCP inventory")
		}
		for _, row := range page.Data {
			// Other user servers may have private names. Only this integration's
			// documented coexistence partners appear in the receipt.
			if row.Name != "qmax" && row.Name != "zapier" && row.Name != "9lives" {
				continue
			}
			auth := "unknown"
			switch row.Auth {
			case "unsupported", "notLoggedIn", "bearerToken", "oAuth":
				auth = row.Auth
			}
			server := Server{Name: row.Name, AuthStatus: auth, Tools: []string{}}
			for name := range row.Tools {
				if !safeName.MatchString(name) {
					return nil, errors.New("invalid MCP tool name; inventory suppressed")
				}
				server.Tools = append(server.Tools, name)
			}
			sort.Strings(server.Tools)
			servers = append(servers, server)
		}
		cursor = page.Cursor
		if cursor == nil || *cursor == "" {
			sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
			return servers, nil
		}
	}
	return nil, errors.New("codex MCP inventory pagination limit exceeded")
}

// CheckReadTools never treats configured or cached OAuth state as proof that a
// source read succeeded. It only checks runtime availability of the two tools.
func CheckReadTools(servers []Server) error {
	for _, server := range servers {
		if server.Name != "zapier" {
			continue
		}
		if server.AuthStatus == "notLoggedIn" {
			return errors.New("zapier authentication required or expired; run qmax-code zapier connect")
		}
		inspect, read := false, false
		for _, name := range server.Tools {
			switch name {
			case "inspect_zapier_actions":
				inspect = true
			case "execute_zapier_read_action":
				read = true
			default:
				return errors.New("zapier exposes tools outside the read-only allowlist; do not start an agent turn with this configuration")
			}
		}
		if inspect && read {
			return nil
		}
		return errors.New("zapier read tools unavailable; check connection, enabled actions and Codex compatibility")
	}
	return errors.New("zapier is disabled, unavailable or not configured; run qmax-code zapier connect")
}
