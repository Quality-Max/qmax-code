package api

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

func getenvTrimmed(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

// Provider describes a user-selectable, opt-in inference provider reachable
// through the opencode backend. These are OpenAI-compatible endpoints (Z.AI's
// coding plan, Groq, OpenRouter, …) that a user turns on individually via the
// /providers command. Nothing here is enabled for everyone by default: a
// provider only appears in a user's model picker once THAT user opts in and
// supplies a key.
//
// Two kinds of provider:
//   - Custom (Custom=true): not known to opencode/models.dev, so we write a full
//     provider block into the managed opencode config (npm openai-compatible
//     adapter + baseURL + a seeded model list). Z.AI's coding plan is the case.
//   - Known (Custom=false): recognised by opencode via models.dev (Groq,
//     OpenRouter). We don't define a block — supplying the standard provider
//     env var is enough for opencode to auto-discover the live model catalogue.
type Provider struct {
	ID          string // opencode provider id, e.g. "zai-coding-plan", "groq"
	DisplayName string // human label shown in /providers and the picker
	SignupURL   string // where a user gets an API key

	Custom  bool            // true → write an openai-compatible provider block
	BaseURL string          // custom only: OpenAI-compatible endpoint
	Models  []ProviderModel // custom only: seed models exposed to opencode

	// KeyEnvVars are the environment variable names to set to the API key when
	// launching the opencode subprocess. For custom providers this is our own
	// substitution var referenced as "{env:...}" in the provider block. For
	// known providers these are opencode's standard vars (e.g. GROQ_API_KEY),
	// which opencode reads directly.
	KeyEnvVars []string

	KeyPrefix string // optional soft-validation hint (e.g. "gsk_" for Groq)
}

// ProviderModel is one model exposed by a custom provider block.
type ProviderModel struct {
	ID   string // model id opencode uses after the "provider/" prefix
	Name string // display name in the opencode config models map
}

// builtinProviders is the static catalogue of opt-in providers. Cerebras is
// deliberately absent: it keeps its first-class native backend (see the
// "cerebras" path in config.go / agent) and is surfaced in /providers as a
// note, not routed through opencode.
var builtinProviders = []Provider{
	{
		ID:          "zai-coding-plan",
		DisplayName: "Z.AI Coding Plan",
		SignupURL:   "https://z.ai",
		Custom:      true,
		BaseURL:     "https://open.bigmodel.cn/api/coding/paas/v4",
		Models: []ProviderModel{
			{ID: "glm-4.6", Name: "GLM 4.6"},
			{ID: "glm-4.5", Name: "GLM 4.5"},
			{ID: "glm-4.5-air", Name: "GLM 4.5 Air"},
		},
		KeyEnvVars: []string{"QMAX_PC_ZAI_CODING_PLAN"},
	},
	{
		ID:          "groq",
		DisplayName: "Groq",
		SignupURL:   "https://console.groq.com/keys",
		KeyEnvVars:  []string{"GROQ_API_KEY"},
		KeyPrefix:   "gsk_",
	},
	{
		ID:          "openrouter",
		DisplayName: "OpenRouter",
		SignupURL:   "https://openrouter.ai/keys",
		KeyEnvVars:  []string{"OPENROUTER_API_KEY"},
		KeyPrefix:   "sk-or-",
	},
}

// BuiltinProviders returns the catalogue of opt-in opencode providers.
func BuiltinProviders() []Provider {
	out := make([]Provider, len(builtinProviders))
	copy(out, builtinProviders)
	return out
}

// ProviderByID looks up a builtin provider definition.
func ProviderByID(id string) (Provider, bool) {
	for _, p := range builtinProviders {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// ProviderAllowed is the entitlement seam. Today it returns true for every
// builtin provider — enablement is purely a local, per-user opt-in. Later this
// is where a cloud check (QualityMax auth / server-driven entitlement) plugs
// in so an org can gate which providers a given user may turn on, WITHOUT any
// change to the /providers UI or the picker: both consult this function, and
// the visible set is always (entitled ∧ enabled). Keep it the single chokepoint.
func ProviderAllowed(id string) bool {
	_, ok := ProviderByID(id)
	return ok
}

// providerKeychainAccount is the OS-keychain account name for a provider's key.
func providerKeychainAccount(id string) string {
	return "provider_" + id + "_key"
}

// SaveProviderKey stores a provider's API key in the OS keychain.
func SaveProviderKey(id, key string) error {
	if _, ok := ProviderByID(id); !ok {
		return fmt.Errorf("unknown provider %q", id)
	}
	return SaveToKeychain(providerKeychainAccount(id), key)
}

// LoadProviderKey retrieves a provider's API key from the OS keychain, falling
// back to any of the provider's standard environment variables.
func LoadProviderKey(id string) string {
	if key, err := LoadFromKeychain(providerKeychainAccount(id)); err == nil && key != "" {
		return key
	}
	if p, ok := ProviderByID(id); ok {
		for _, ev := range p.KeyEnvVars {
			if v := getenvTrimmed(ev); v != "" {
				return v
			}
		}
	}
	return ""
}

// DeleteProviderKey removes a provider's API key from the OS keychain.
func DeleteProviderKey(id string) error {
	return DeleteFromKeychain(providerKeychainAccount(id))
}

// ProviderKeySet reports whether a usable key exists for the provider.
func ProviderKeySet(id string) bool {
	return LoadProviderKey(id) != ""
}

// ValidateProviderKey applies the same lenient sanity check used for the
// Cerebras key: reject empty, whitespace, or non-printable pastes (the common
// no-echo mistakes) while accepting any single opaque token. The bool reports
// whether the key matches the provider's expected prefix — a soft signal, not
// a hard failure, so future key formats keep working.
func ValidateProviderKey(id, key string) (looksLikeKey bool, err error) {
	if key == "" {
		return false, fmt.Errorf("key is empty")
	}
	for _, r := range key {
		if unicode.IsSpace(r) {
			return false, fmt.Errorf("key contains whitespace — looks like a pasted command or multiple values, not a single API key")
		}
		if !unicode.IsPrint(r) {
			return false, fmt.Errorf("key contains a non-printable character")
		}
	}
	if p, ok := ProviderByID(id); ok && p.KeyPrefix != "" {
		return strings.HasPrefix(key, p.KeyPrefix), nil
	}
	return true, nil
}

// IsProviderEnabled reports whether the user opted this provider in.
func (c *Config) IsProviderEnabled(id string) bool {
	for _, e := range c.EnabledProviders {
		if e == id {
			return true
		}
	}
	return false
}

// EnableProviderID adds a provider to the user's opt-in set (idempotent, sorted).
func (c *Config) EnableProviderID(id string) {
	if c.IsProviderEnabled(id) {
		return
	}
	c.EnabledProviders = append(c.EnabledProviders, id)
	sort.Strings(c.EnabledProviders)
}

// DisableProviderID removes a provider from the user's opt-in set.
func (c *Config) DisableProviderID(id string) {
	out := c.EnabledProviders[:0]
	for _, e := range c.EnabledProviders {
		if e != id {
			out = append(out, e)
		}
	}
	c.EnabledProviders = out
}

// ActiveProviders returns the providers that are BOTH enabled by the user AND
// allowed by the entitlement seam — i.e. exactly what should be visible in the
// picker and driven through opencode.
func (c *Config) ActiveProviders() []Provider {
	var out []Provider
	for _, id := range c.EnabledProviders {
		if !ProviderAllowed(id) {
			continue
		}
		if p, ok := ProviderByID(id); ok {
			out = append(out, p)
		}
	}
	return out
}
