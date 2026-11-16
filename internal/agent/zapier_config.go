package agent

import (
	"errors"
	"reflect"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const ZapierMCPURL = "https://mcp.zapier.com/api/v1/connect"

// ConfigureCodexZapier is an explicit opt-in. Only read dispatch and schema
// inspection are exposed. Action enablement, connections and write dispatch
// remain outside the agent's catalog, even when enabled in Zapier itself.
// A pre-existing, differently configured server is never overwritten.
func ConfigureCodexZapier(path string, enabled bool) error {
	return updateCodexConfig(path, func(config string) (string, error) {
		var doc struct {
			Servers map[string]map[string]any `toml:"mcp_servers"`
			Store   any                       `toml:"mcp_oauth_credentials_store"`
		}
		if toml.Unmarshal([]byte(config), &doc) != nil {
			return "", errors.New("invalid Codex TOML; existing settings were not changed")
		}
		if entry, exists := doc.Servers["zapier"]; exists && !managedZapierEntry(entry) {
			return "", errors.New("existing Zapier configuration is user-managed; use a separate CODEX_HOME or configure its read permissions manually")
		}
		if _, exists := doc.Servers["zapier"]; !enabled && !exists {
			return config, nil
		}
		if enabled {
			if doc.Store != nil && doc.Store != "keyring" {
				return "", errors.New("zapier requires mcp_oauth_credentials_store = \"keyring\"; existing credential-storage policy was not changed")
			}
			if doc.Store == nil {
				if strings.TrimSpace(config) != "" {
					return "", errors.New("existing Codex config has no explicit keyring policy; choose a fresh CODEX_HOME or set mcp_oauth_credentials_store = \"keyring\" after checking other MCP connections")
				}
				config = "mcp_oauth_credentials_store = \"keyring\"\n" + config
			}
		}
		state := "false"
		if enabled {
			state = "true"
		}
		return replaceCodexMCPTable(config, "zapier", "[mcp_servers.zapier]\nurl = "+tomlQuote(ZapierMCPURL)+"\nenabled = "+state+"\nenabled_tools = [\"inspect_zapier_actions\", \"execute_zapier_read_action\"]\n")
	})
}

func managedZapierEntry(entry map[string]any) bool {
	if len(entry) != 3 || entry["url"] != ZapierMCPURL {
		return false
	}
	if _, ok := entry["enabled"].(bool); !ok {
		return false
	}
	return reflect.DeepEqual(entry["enabled_tools"], []any{"inspect_zapier_actions", "execute_zapier_read_action"})
}
