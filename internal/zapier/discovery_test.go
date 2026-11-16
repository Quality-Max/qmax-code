package zapier

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryProjectsInventoryAndRejectsApproval(t *testing.T) {
	var requests bytes.Buffer
	wire := `{"id":1,"result":{}}
{"id":100,"method":"mcpServer/elicitation/request","params":{"secret":"private-value"}}
{"id":2,"result":{"data":[{"name":"private-server","authStatus":"oAuth","tools":{"private-tool":{}}},{"name":"qmax","authStatus":"unsupported","tools":{"read_file":{"description":"private-value"}}}],"nextCursor":"next"}}
{"id":3,"result":{"data":[{"name":"zapier","authStatus":"oAuth","tools":{"execute_zapier_read_action":{},"inspect_zapier_actions":{}}},{"name":"9lives","authStatus":"unsupported","tools":{"run_test":{}}}]}}
`
	servers, err := discover(context.Background(), &requests, strings.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 3 || servers[0].Name != "9lives" || servers[1].Name != "qmax" || servers[2].Name != "zapier" {
		t.Fatal("coexistence inventory incomplete")
	}
	if err := CheckReadTools(servers); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(servers)
	for _, content := range []string{"private-value", "private-server", "private-tool", "description"} {
		if strings.Contains(string(encoded), content) {
			t.Fatal("provider data entered structural evidence")
		}
	}
	var methods []string
	decoder := json.NewDecoder(&requests)
	denied := false
	for {
		var req map[string]any
		if err := decoder.Decode(&req); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if method, ok := req["method"].(string); ok {
			methods = append(methods, method)
		}
		if req["id"] == float64(100) && req["error"] != nil && req["result"] == nil {
			denied = true
		}
	}
	if strings.Join(methods, ",") != "initialize,initialized,mcpServerStatus/list,mcpServerStatus/list" || !denied {
		t.Fatal("discovery authorized extra behavior")
	}
}

func TestDiscoveryFailsWithoutLeakingDiagnostics(t *testing.T) {
	for _, wire := range []string{
		`{"id":1,"error":{"message":"private-value"}}`,
		`{"id":1,"result":{}}` + "\n" + `{"id":2,"result":{"data":null}}`,
		`{"id":7,"result":{"private":"private-value"}}`,
		`private-value`,
	} {
		_, err := discover(context.Background(), io.Discard, strings.NewReader(wire))
		if err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("expected sanitized failure")
		}
	}
}

func TestDiscoveryCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	if _, err := discover(ctx, io.Discard, r); err == nil {
		t.Fatal("expected timeout")
	}
}

func TestReadToolFailures(t *testing.T) {
	for _, servers := range [][]Server{
		nil,
		{{Name: "zapier", AuthStatus: "notLoggedIn"}},
		{{Name: "zapier", AuthStatus: "oAuth"}},
		{{Name: "zapier", Tools: []string{"inspect_zapier_actions"}}},
		{{Name: "zapier", Tools: []string{"inspect_zapier_actions", "execute_zapier_read_action", "execute_zapier_write_action"}}},
	} {
		if CheckReadTools(servers) == nil {
			t.Fatal("unsafe or incomplete discovery accepted")
		}
	}
}
