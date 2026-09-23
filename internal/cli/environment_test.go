package cli

import (
	"os"
	"testing"
)

var configEnvNames = []string{
	"COMMIT_CONFIG", "COMMIT_PROVIDER", "COMMIT_API_KEY", "COMMIT_GITHUB_TOKEN",
	"COMMIT_MODEL", "COMMIT_BASE_URL", "COMMIT_API_FORMAT", "COMMIT_RELEASE_REPOSITORY",
	"OPENAI_API_KEY", "OPENCODE_API_KEY", "OPENCODE_GO_API_KEY", "GITHUB_TOKEN",
}

// Unset, don't blank: explicit empty credentials clear file values. Setenv
// registers restoration of the original presence/value and prevents concurrent
// use in parallel tests. The following Unsetenv is therefore cleanup-safe.
func isolateConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range configEnvNames {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestConfigEnvironmentIsolationRestoresParent(t *testing.T) {
	for _, name := range configEnvNames {
		t.Setenv(name, "parent-dummy-value")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	parentDir := os.Getenv("XDG_CONFIG_HOME")
	t.Run("isolated", func(t *testing.T) {
		isolateConfigEnv(t)
		for _, name := range configEnvNames {
			if _, ok := os.LookupEnv(name); ok {
				t.Errorf("%s was not unset", name)
			}
		}
		if os.Getenv("XDG_CONFIG_HOME") == parentDir {
			t.Error("config directory was not isolated")
		}
	})
	for _, name := range configEnvNames {
		if os.Getenv(name) != "parent-dummy-value" {
			t.Errorf("%s was not restored", name)
		}
	}
	if os.Getenv("XDG_CONFIG_HOME") != parentDir {
		t.Error("config directory was not restored")
	}
}
