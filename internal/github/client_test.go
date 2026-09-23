package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myusuf3/commit/internal/app"
)

func TestParseRemote(t *testing.T) {
	// Credential-bearing URLs are common in CI checkouts and must parse to the
	// repository path without the credential being surfaced anywhere.
	for _, remote := range []string{"git@github.com:person/repo.git", "https://github.com/person/repo.git", "ssh://git@github.com/person/repo.git", "https://github.com/person/repo/", "https://x-access-token:secret-token@github.com/person/repo", "https://token@github.com/person/repo.git"} {
		got, err := ParseRemote(remote)
		if err != nil || got != "person/repo" {
			t.Fatalf("%q => %q, %v", remote, got, err)
		}
		if strings.Contains(got, "token") || strings.Contains(got, "secret") {
			t.Fatalf("credential leaked into %q", got)
		}
	}
	for _, remote := range []string{"https://evil.example/github.com/person/repo", "https://github.com.evil.example/person/repo", "https://github.com/person/repo/extra", "http://github.com/person/repo", "https://github.com/person/..", "https://github.com/person/repo?key=secret", "git@github.com:/repo", "https://github.com/person/%2e%2e", "../local-repo"} {
		if _, err := ParseRemote(remote); err == nil {
			t.Fatalf("accepted unsafe remote %q", remote)
		}
	}
}

func TestPRRequests(t *testing.T) {
	t.Parallel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing token")
		}
		switch {
		case r.URL.Path == "/repos/person/repo" && r.Method == "GET":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case r.URL.Path == "/repos/person/repo/pulls" && r.Method == "GET":
			if r.URL.Query().Get("head") != "person:feature/slash" || r.URL.Query().Get("base") != "main" || r.URL.Query().Get("state") != "open" {
				t.Errorf("bad filter: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"number":7,"title":"Old","body":"Fixes #12","html_url":"https://github.com/person/repo/pull/7"}]`))
		case r.Method == "POST" || r.Method == "PATCH":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["title"] != "New" || payload["body"] != "Body" {
				t.Errorf("bad payload: %v", payload)
			}
			if r.Method == "POST" && (payload["draft"] != true || payload["head"] != "feature/slash" || payload["base"] != "main") {
				t.Errorf("bad create: %v", payload)
			}
			if r.Method == "PATCH" && (r.URL.Path != "/repos/person/repo/pulls/7" || len(payload) != 2) {
				t.Errorf("bad update: %s %v", r.URL.Path, payload)
			}
			_, _ = w.Write([]byte(`{"html_url":"https://github.com/person/repo/pull/7"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer s.Close()
	c := &Client{HTTP: s.Client(), BaseURL: s.URL, Repository: "person/repo", Token: "token"}
	ctx := context.Background()
	if branch, err := c.DefaultBranch(ctx); err != nil || branch != "main" {
		t.Fatalf("branch=%s err=%v", branch, err)
	}
	if pr, err := c.FindPullRequest(ctx, "feature/slash", "main"); err != nil || pr.Number != 7 {
		t.Fatalf("PR=%v err=%v", pr, err)
	}
	pr := app.PullRequest{Title: "New", Body: "Body"}
	if _, err := c.CreatePullRequest(ctx, "feature/slash", "main", pr, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdatePullRequest(ctx, 7, pr); err != nil {
		t.Fatal(err)
	}
}

func TestAmbiguousOrMalformedPRs(t *testing.T) {
	for _, body := range []string{`[{"number":1},{"number":2}]`, `[{"number":0}]`, `[{"number":1,"html_url":"https://evil.example"}]`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		c := &Client{HTTP: s.Client(), BaseURL: s.URL, Repository: "person/repo"}
		if _, err := c.FindPullRequest(context.Background(), "feature", "main"); err == nil {
			t.Fatalf("accepted %s", body)
		}
		s.Close()
	}
}
