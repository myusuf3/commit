package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"COMMIT_CONFIG", "COMMIT_API_KEY", "COMMIT_GITHUB_TOKEN", "COMMIT_PROVIDER", "COMMIT_MODEL", "COMMIT_BASE_URL", "COMMIT_API_FORMAT", "COMMIT_RELEASE_REPOSITORY", "OPENAI_API_KEY", "OPENCODE_API_KEY", "OPENCODE_GO_API_KEY", "GITHUB_TOKEN"} {
		old, ok := os.LookupEnv(key)
		_ = os.Unsetenv(key)
		t.Cleanup(func() {
			if ok {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestLoadDefaultsAndEnvironment(t *testing.T) {
	cleanEnv(t)
	t.Setenv("OPENAI_API_KEY", "fallback")
	t.Setenv("COMMIT_API_KEY", "preferred")
	t.Setenv("COMMIT_MODEL", "custom")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "preferred" || c.Model != "custom" || !c.Conventional.TypeScopePrefix {
		t.Fatalf("wrong defaults or precedence: %+v", c)
	}
	if err := c.ValidateLLM(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("explicit missing config must fail")
	}
}

func TestConfigRoundTripAndPermissions(t *testing.T) {
	cleanEnv(t)
	c := Default()
	c.APIKey = "test-\"quoted\\value\n"
	c.Conventional.TypeScopePrefix = false
	path := filepath.Join(t.TempDir(), "nested", "config")
	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, Default()); err == nil {
		t.Fatal("overwrote an existing config")
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != c.APIKey || got.Conventional.TypeScopePrefix {
		t.Fatalf("incorrect round trip: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode())
	}
}

func TestConfigRejectsUnknownKeysAndRedactsParseErrors(t *testing.T) {
	cleanEnv(t)
	for _, text := range []string{"typo = true", "api_key = \"super-secret\" broken"} {
		path := filepath.Join(t.TempDir(), "config")
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil {
			t.Fatal("expected error")
		}
		if strings.Contains(err.Error(), "super-secret") {
			t.Fatal("secret leaked in error")
		}
	}
}

func TestConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"https://api.example.com/v1", true}, {"http://localhost:1234/v1", true}, {"http://127.0.0.1/v1", true},
		{"http://[::1]:1234/v1", true}, {"http://example.com/v1", false}, {"https://key@example.com", false},
		{"https://api.example.com?key=secret", false}, {"file:///tmp/socket", false}, {"broken", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			c := Default()
			c.APIKey = "test"
			c.BaseURL = tc.url
			if err := c.ValidateLLM(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
	c := Default()
	c.APIKey = "test"
	c.Provider = "unsupported"
	if c.ValidateLLM() == nil {
		t.Fatal("unsupported provider")
	}
	c = Default()
	c.APIKey = "test"
	c.Timeout = "0s"
	if c.ValidateLLM() == nil {
		t.Fatal("unbounded timeout")
	}
	c = Default()
	c.APIKey = "test"
	c.MaxDiffBytes = -1
	if c.ValidateLLM() == nil {
		t.Fatal("negative limit")
	}
}

func TestWriteRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require additional Windows privileges")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if Write(link, Default()) == nil {
		t.Fatal("followed symlink")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "unchanged" {
		t.Fatal("modified symlink target")
	}
}
