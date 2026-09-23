// Package config loads explicit user configuration. Repository-local files are
// deliberately not discovered: an untrusted checkout must not redirect API keys.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Provider          string `toml:"provider"`
	APIKey            string `toml:"api_key"`
	Model             string `toml:"model"`
	BaseURL           string `toml:"base_url"`
	APIFormat         string `toml:"api_format"`
	GitHubToken       string `toml:"github_token"`
	ReleaseRepository string `toml:"release_repository"`
	Timeout           string `toml:"timeout"`
	MaxDiffBytes      int    `toml:"max_diff_bytes"`
	Conventional      struct {
		TypeScopePrefix bool `toml:"type_scope_prefix"`
	} `toml:"conventional"`
}

func Default() Config {
	c := Config{Provider: "openai", Model: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1", Timeout: "60s", MaxDiffBytes: 100000}
	c.Conventional.TypeScopePrefix = true
	return c
}

func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", errors.New("XDG_CONFIG_HOME must be absolute")
		}
		return filepath.Join(dir, "commit", ".commitrc"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "commit", ".commitrc"), nil
}

// Path resolves an explicit flag, then COMMIT_CONFIG, then the user config path.
func Path(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if path := os.Getenv("COMMIT_CONFIG"); path != "" {
		return path, nil
	}
	return DefaultPath()
}

func Load(explicit string) (Config, error) {
	c := Default()
	var md toml.MetaData
	path, err := Path(explicit)
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || explicit != "" || os.Getenv("COMMIT_CONFIG") != "" {
			return c, fmt.Errorf("read config: %w", err)
		}
	} else {
		md, err = toml.Decode(string(data), &c)
		// TOML parser errors may contain the original line, including a secret.
		if err != nil {
			return c, fmt.Errorf("invalid TOML in %s", path)
		}
		if len(md.Undecoded()) > 0 {
			return c, fmt.Errorf("unknown configuration key: %s", md.Undecoded()[0])
		}
	}
	fileProvider := c.Provider
	if provider, ok := os.LookupEnv("COMMIT_PROVIDER"); ok {
		c.Provider = provider
	}
	switched := c.Provider != fileProvider
	if switched {
		// Never carry a different provider's stored key/endpoint across an env switch.
		c.APIKey, c.APIFormat = "", ""
	}
	defaults := DefaultForProvider(c.Provider)
	if switched || !md.IsDefined("model") {
		c.Model = defaults.Model
	}
	if switched || !md.IsDefined("base_url") {
		c.BaseURL = defaults.BaseURL
	}
	if key, ok := APIKeyFromEnv(c.Provider); ok {
		c.APIKey = key
	}
	for _, env := range []struct {
		key string
		dst *string
	}{
		{"GITHUB_TOKEN", &c.GitHubToken}, {"COMMIT_GITHUB_TOKEN", &c.GitHubToken},
		{"COMMIT_MODEL", &c.Model}, {"COMMIT_BASE_URL", &c.BaseURL},
		{"COMMIT_API_FORMAT", &c.APIFormat},
		{"COMMIT_RELEASE_REPOSITORY", &c.ReleaseRepository},
	} {
		if v, ok := os.LookupEnv(env.key); ok {
			*env.dst = v
		}
	}
	return c, nil
}

func (c Config) Duration() (time.Duration, error) {
	d, err := time.ParseDuration(c.Timeout)
	if err != nil || d <= 0 {
		return 0, errors.New("timeout must be a positive duration (for example 60s)")
	}
	return d, nil
}

func (c Config) ValidateLLM() error {
	if c.Provider != "openai" && c.Provider != "opencode-go" {
		return errors.New("provider must be openai or opencode-go")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		env := "OPENAI_API_KEY"
		if c.Provider == "opencode-go" {
			env = "OPENCODE_API_KEY"
		}
		return fmt.Errorf("API key required: set %s or run 'commit init'", env)
	}
	switch c.WireFormat() {
	case "chat-completions", "messages", "responses":
	default:
		return errors.New("api_format must be chat-completions, messages, or responses")
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("model is required")
	}
	if strings.HasPrefix(c.Model, "opencode-go/") {
		return errors.New("use the bare API model ID (for example kimi-k2.6), not opencode-go/<model>")
	}
	if c.MaxDiffBytes < 1 || c.MaxDiffBytes > 10<<20 {
		return errors.New("max_diff_bytes must be between 1 and 10485760")
	}
	if _, err := c.Duration(); err != nil {
		return err
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("base_url must be an HTTP(S) URL without credentials, query, or fragment")
	}
	if c.Provider == "opencode-go" && strings.EqualFold(u.Hostname(), "api.openai.com") {
		return errors.New("opencode-go cannot use the OpenAI endpoint; remove base_url to use the Go default")
	}
	if strings.EqualFold(u.Hostname(), "opencode.ai") && strings.TrimRight(u.Path, "/") == "/zen/go/v1" && c.Provider != "opencode-go" {
		return errors.New("the OpenCode Go endpoint requires provider = opencode-go")
	}
	ip := net.ParseIP(u.Hostname())
	local := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("base_url must use HTTPS (HTTP is allowed only for loopback hosts)")
	}
	return nil
}

// Write creates a private file exclusively. It never overwrites files or symlinks.
func Write(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create config: %w", err)
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write config: %w", err)
	}
	return f.Close()
}
