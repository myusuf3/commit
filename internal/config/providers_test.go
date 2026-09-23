package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodeGoDefaults(t *testing.T) {
	cleanEnv(t)
	t.Setenv("OPENCODE_API_KEY", "go-key")
	t.Setenv("OPENAI_API_KEY", "other-provider-key")
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("provider = \"opencode-go\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "opencode-go" || c.APIKey != "go-key" || c.Model != "kimi-k2.6" || c.BaseURL != "https://opencode.ai/zen/go/v1" || c.WireFormat() != "chat-completions" {
		t.Fatalf("wrong provider defaults: %+v", c)
	}
	if err := c.ValidateLLM(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeGoEnvironmentOnly(t *testing.T) {
	cleanEnv(t)
	t.Setenv("COMMIT_PROVIDER", "opencode-go")
	t.Setenv("OPENCODE_GO_API_KEY", "go-key")
	t.Setenv("COMMIT_MODEL", "minimax-m2.7")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "minimax-m2.7" || c.WireFormat() != "messages" || c.APIKey != "go-key" || c.BaseURL != DefaultForProvider("opencode-go").BaseURL {
		t.Fatalf("wrong config: %+v", c)
	}
}

func TestProviderKeysAreIsolated(t *testing.T) {
	cleanEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("OPENCODE_API_KEY", "go-key")
	for _, tc := range []struct{ provider, want string }{{"openai", "openai-key"}, {"opencode-go", "go-key"}} {
		if got, ok := APIKeyFromEnv(tc.provider); !ok || got != tc.want {
			t.Fatalf("%s key=%q", tc.provider, got)
		}
	}
	t.Setenv("OPENCODE_GO_API_KEY", "go-specific-key")
	if key, _ := APIKeyFromEnv("opencode-go"); key != "go-specific-key" {
		t.Fatal("Go-specific alias did not win")
	}
	t.Setenv("COMMIT_API_KEY", "explicit-key")
	for _, provider := range []string{"openai", "opencode-go"} {
		if key, _ := APIKeyFromEnv(provider); key != "explicit-key" {
			t.Fatal("explicit key did not win")
		}
	}
	t.Setenv("COMMIT_API_KEY", "")
	if key, ok := APIKeyFromEnv("opencode-go"); !ok || key != "" {
		t.Fatal("empty explicit key did not clear")
	}
}

func TestProviderSwitchDiscardsOtherProviderSettings(t *testing.T) {
	for _, from := range []string{"openai", "opencode-go"} {
		t.Run(from, func(t *testing.T) {
			cleanEnv(t)
			to := "opencode-go"
			if from == to {
				to = "openai"
			}
			c := DefaultForProvider(from)
			c.APIKey, c.APIFormat, c.GitHubToken = "stored-key", "messages", "github-key"
			path := filepath.Join(t.TempDir(), "config")
			if err := Write(path, c); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COMMIT_PROVIDER", to)
			loaded, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			defaults := DefaultForProvider(to)
			if loaded.APIKey != "" || loaded.APIFormat != "" || loaded.Model != defaults.Model || loaded.BaseURL != defaults.BaseURL || loaded.GitHubToken != "github-key" {
				t.Fatalf("leaked prior provider settings: %+v", loaded)
			}
			if loaded.ValidateLLM() == nil {
				t.Fatal("provider switch reused a stored credential")
			}
		})
	}
}

func TestOpenAIKeyCannotAuthenticateOpenCode(t *testing.T) {
	cleanEnv(t)
	t.Setenv("OPENAI_API_KEY", "wrong-key")
	t.Setenv("COMMIT_PROVIDER", "opencode-go")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "" || c.ValidateLLM() == nil {
		t.Fatal("used OpenAI key for OpenCode")
	}
}

func TestWireFormats(t *testing.T) {
	for _, tc := range []struct{ model, format string }{
		{"kimi-k2.6", "chat-completions"}, {"glm-5.3-flash", "chat-completions"}, {"deepseek-v4-pro", "chat-completions"},
		{"minimax-m2.5", "messages"}, {"minimax-m3", "messages"}, {"qwen3.8-max", "messages"},
		{"grok-4.7", "responses"}, {"gpt-5.6-luna", "responses"}, {"muse-spark-1.3-contributor", "responses"},
	} {
		c := DefaultForProvider("opencode-go")
		c.Model = tc.model
		if c.WireFormat() != tc.format {
			t.Fatalf("%s uses %s", tc.model, c.WireFormat())
		}
		c.APIFormat = "responses"
		if c.WireFormat() != "responses" {
			t.Fatal("explicit API format not honored")
		}
	}
	c := Default()
	c.Model = "gpt-anything"
	if c.WireFormat() != "chat-completions" {
		t.Fatal("changed default OpenAI API")
	}
}

func TestOpenCodeOverridesAndValidation(t *testing.T) {
	cleanEnv(t)
	path := filepath.Join(t.TempDir(), "config")
	text := "provider = \"opencode-go\"\nmodel = \"custom-model\"\nbase_url = \"http://localhost:1234/v1\"\napi_format = \"responses\"\napi_key = \"test\"\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model != "custom-model" || c.BaseURL != "http://localhost:1234/v1" || c.WireFormat() != "responses" {
		t.Fatal("explicit settings lost")
	}
	if err := c.ValidateLLM(); err != nil {
		t.Fatal(err)
	}
	c.APIFormat = "bogus"
	if c.ValidateLLM() == nil {
		t.Fatal("invalid API accepted")
	}
	c.APIFormat = ""
	c.BaseURL = Default().BaseURL
	if c.ValidateLLM() == nil {
		t.Fatal("OpenAI destination accepted for OpenCode credentials")
	}
	c.BaseURL = DefaultForProvider("opencode-go").BaseURL
	c.Model = "opencode-go/kimi-k2.6"
	if c.ValidateLLM() == nil {
		t.Fatal("qualified model ID accepted")
	}
}
