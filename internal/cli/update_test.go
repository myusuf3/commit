package cli

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/myusuf3/commit/internal/config"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateUsesDefaultRepositoryAndReportsLatestForDevBuilds(t *testing.T) {
	var requested string
	transport := releaseTransport(func(r *http.Request) (*http.Response, error) {
		requested = r.URL.String()
		body := `{"tag_name":"v0.2.0","assets":[]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	run := func(version string, args ...string) (string, error) {
		var out bytes.Buffer
		root := NewRoot(Options{Out: &out, Err: io.Discard, Build: Build{Version: version}, UpdateTransport: transport,
			LoadConfig: func(string) (config.Config, error) { return config.Default(), nil }})
		root.SetArgs(append([]string{"update"}, args...))
		err := root.Execute()
		return out.String(), err
	}
	out, err := run("dev", "--check")
	if err != nil || out != "Latest release: v0.2.0 (current: development build). Run 'commit update --force' to install it.\n" {
		t.Fatalf("dev --check: out=%q err=%v", out, err)
	}
	if requested != "https://api.github.com/repos/myusuf3/commit/releases/latest" {
		t.Fatalf("requested %s", requested)
	}
	if _, err := run("dev"); err == nil || !strings.Contains(err.Error(), "the latest release is v0.2.0. Use --force") {
		t.Fatalf("dev update: %v", err)
	}
	if out, err := run("0.2.0", "--check"); err != nil || out != "Already up to date (0.2.0).\n" {
		t.Fatalf("current --check: out=%q err=%v", out, err)
	}
}
