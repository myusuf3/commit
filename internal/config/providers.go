package config

import (
	"os"
	"strings"
)

// DefaultForProvider sets provider-specific defaults without changing the CLI.
func DefaultForProvider(provider string) Config {
	c := Default()
	c.Provider = provider
	if provider == "opencode-go" {
		c.Model = "kimi-k2.6"
		c.BaseURL = "https://opencode.ai/zen/go/v1"
	}
	return c
}

// APIKeyFromEnv only considers the selected provider's key, then the explicit
// cross-provider COMMIT_API_KEY override. Empty values deliberately clear keys.
func APIKeyFromEnv(provider string) (string, bool) {
	var names []string
	switch provider {
	case "openai":
		names = []string{"OPENAI_API_KEY"}
	case "opencode-go":
		names = []string{"OPENCODE_API_KEY", "OPENCODE_GO_API_KEY"}
	}
	names = append(names, "COMMIT_API_KEY")
	var key string
	var found bool
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			key, found = value, true
		}
	}
	return key, found
}

// WireFormat follows OpenCode Go's documented model families. An explicit
// api_format handles custom endpoints or future models with a different API.
// See https://opencode.ai/docs/go/#endpoints.
func (c Config) WireFormat() string {
	if c.APIFormat != "" {
		return c.APIFormat
	}
	if c.Provider == "opencode-go" {
		model := strings.ToLower(c.Model)
		switch {
		case strings.HasPrefix(model, "minimax-"), strings.HasPrefix(model, "qwen"):
			return "messages"
		case strings.HasPrefix(model, "grok-"), strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "muse-spark-"):
			return "responses"
		}
	}
	return "chat-completions"
}
