package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/myusuf3/commit/internal/config"
)

func TestCommitWorkflowWithRealGitAndMockProvider(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Test User")
	git("config", "user.email", "test@example.com")
	git("config", "commit.gpgsign", "false")
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	if err := os.WriteFile(path, []byte("unstaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if !bytes.Contains(body, []byte("+staged")) || bytes.Contains(body, []byte("+unstaged")) {
			t.Error("sent wrong diff")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"feat: add staged file"},"finish_reason":"stop"}]}`))
	}))
	defer s.Close()
	cfg := config.Default()
	cfg.APIKey = "test"
	cfg.BaseURL = s.URL + "/v1"
	for _, flag := range []string{"--dry-run", "--auto-accept"} {
		var out bytes.Buffer
		root := NewRoot(Options{In: strings.NewReader(""), Out: &out, Err: io.Discard, Dir: dir, LoadConfig: func(string) (config.Config, error) { return cfg, nil }})
		root.SetArgs([]string{"commit", flag})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "feat: add staged file\n" {
			t.Fatalf("stdout: %q", out.String())
		}
		if flag == "--dry-run" && !strings.Contains(git("diff", "--cached"), "+staged") {
			t.Fatal("dry run committed")
		}
	}
	if git("log", "-1", "--format=%s") != "feat: add staged file" || git("show", "HEAD:file.txt") != "staged" {
		t.Fatal("incorrect commit")
	}
	if !strings.Contains(git("diff"), "+unstaged") {
		t.Fatal("lost unstaged work")
	}
}

// TestOpenCodeGoEndToEnd selects the provider through the environment only (no
// config file) and drives the messages protocol through the real command tree.
func TestOpenCodeGoEndToEnd(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("COMMIT_PROVIDER", "opencode-go")
	t.Setenv("OPENCODE_API_KEY", "go-secret-key")
	t.Setenv("COMMIT_MODEL", "minimax-m3")
	// The isolated default config directory is empty; environment-only setup
	// must not discover the caller's config file or inherit their credentials.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Test User")
	git("config", "user.email", "test@example.com")
	git("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "file.txt")
	var requests int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/zen/go/v1/messages" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "go-secret-key" || r.Header.Get("Authorization") != "" {
			t.Error("wrong auth for messages endpoint")
		}
		if r.Header.Get("x-opencode-session") == "" {
			t.Error("missing session header")
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "commit-cli/") {
			t.Errorf("generic user agent: %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(`{"type":"message","role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"feat: add staged file"}]}`))
	}))
	defer s.Close()
	t.Setenv("COMMIT_BASE_URL", s.URL+"/zen/go/v1")
	var out bytes.Buffer
	root := NewRoot(Options{In: strings.NewReader(""), Out: &out, Err: io.Discard, Dir: dir})
	root.SetArgs([]string{"commit", "--auto-accept"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || out.String() != "feat: add staged file\n" {
		t.Fatalf("requests=%d stdout=%q", requests, out.String())
	}
	if git("log", "-1", "--format=%s") != "feat: add staged file" {
		t.Fatal("incorrect commit")
	}
}

func TestInitSuccessDoesNotPersistEnvironmentSecrets(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("OPENAI_API_KEY", "env-secret")
	t.Setenv("GITHUB_TOKEN", "env-github-secret")
	path := filepath.Join(t.TempDir(), "config")
	root := NewRoot(Options{In: strings.NewReader("\ncustom-model\nn\n"), Out: io.Discard, Err: io.Discard, Interactive: true})
	root.SetArgs([]string{"init", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("env-secret")) || bytes.Contains(data, []byte("env-github-secret")) {
		t.Fatal("copied environment secrets to disk")
	}
	if !bytes.Contains(data, []byte("custom-model")) || !bytes.Contains(data, []byte("type_scope_prefix = false")) {
		t.Fatalf("wrong config: %s", data)
	}
}

func TestInitOpenCodeGoProvider(t *testing.T) {
	isolateConfigEnv(t)
	path := filepath.Join(t.TempDir(), "config")
	input := strings.Join([]string{"opencode-go", "go-key", "minimax-m3", "", ""}, "\n") + "\n"
	root := NewRoot(Options{In: strings.NewReader(input), Out: io.Discard, Err: io.Discard, Interactive: true})
	root.SetArgs([]string{"init", "--config", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`provider = "opencode-go"`, `model = "minimax-m3"`, `base_url = "https://opencode.ai/zen/go/v1"`} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("missing %q in:\n%s", want, data)
		}
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateLLM(); err != nil {
		t.Fatal(err)
	}
	if c.WireFormat() != "messages" {
		t.Fatalf("format=%s", c.WireFormat())
	}
}

func TestPromptCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s := &commandState{opts: Options{Err: io.Discard}, ctx: ctx}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.readInput(func() (string, error) { close(started); b := make([]byte, 1); _, err := reader.Read(b); return "", err })
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled prompt did not return")
	}
}
