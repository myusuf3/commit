package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/myusuf3/commit/internal/httpapi"
)

func TestProviderErrorsNameServiceAndFix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status     int
		body, want string
		provider   string
	}{
		{401, `{"error":{"message":"Incorrect API key provided: sk-abc***xyz","code":"invalid_api_key"}}`, "rejected the API key (HTTP 401, invalid_api_key); check OPENAI_API_KEY", "openai"},
		{401, `{}`, "check OPENCODE_API_KEY", "opencode-go"},
		{404, `{"error":{"code":"model_not_found"}}`, `could not find model "m1" or its endpoint (HTTP 404, model_not_found)`, "openai"},
		{429, `{"error":{"type":"insufficient_quota","code":"insufficient_quota"}}`, "no remaining quota or credits (HTTP 429, insufficient_quota); check billing, since retrying will not help", "openai"},
		{429, `{"error":{"code":"rate_limit_exceeded"}}`, "rate limit reached (HTTP 429, rate_limit_exceeded); wait a moment", "openai"},
		{400, `{"error":{"code":"context_length_exceeded"}}`, `the diff is too large for model "m1"`, "openai"},
		{400, `{"error":{"type":"invalid_request_error"}}`, `rejected the request (HTTP 400, invalid_request_error); check model "m1" and api_format`, "openai"},
		{529, `{"type":"error","error":{"type":"overloaded_error"}}`, "is unavailable (HTTP 529, overloaded_error)", "opencode-go"},
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := &Client{HTTP: httpapi.NewClient(time.Second), BaseURL: s.URL, APIKey: "k", Model: "m1", Provider: tc.provider}
		_, err := c.CommitMessage(context.Background(), "diff")
		s.Close()
		host := strings.TrimPrefix(s.URL, "http://")
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), host) {
			t.Fatalf("status %d: %v", tc.status, err)
		}
		// Provider message text can echo part of the key; it must not be shown.
		if strings.Contains(err.Error(), "sk-") || strings.Contains(err.Error(), "Incorrect") {
			t.Fatalf("reflected provider message: %v", err)
		}
	}
}

func TestProviderTransportErrors(t *testing.T) {
	t.Parallel()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the body so the server notices when the client gives up.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer slow.Close()
	c := &Client{HTTP: httpapi.NewClient(30 * time.Millisecond), BaseURL: slow.URL, APIKey: "k", Model: "m", Provider: "openai"}
	if _, err := c.CommitMessage(context.Background(), "diff"); err == nil || !strings.Contains(err.Error(), "did not respond within 30ms; large diffs and reasoning models can be slow") {
		t.Fatalf("timeout: %v", err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "https://elsewhere.example/", 307) }))
	defer redirect.Close()
	c = &Client{HTTP: httpapi.NewClient(time.Second), BaseURL: redirect.URL, APIKey: "k", Model: "m", Provider: "openai"}
	if _, err := c.CommitMessage(context.Background(), "diff"); err == nil || !strings.Contains(err.Error(), "redirected the request; check base_url") {
		t.Fatalf("redirect: %v", err)
	}
	c = &Client{HTTP: httpapi.NewClient(time.Second), BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m", Provider: "openai"}
	if _, err := c.CommitMessage(context.Background(), "diff"); err == nil || !strings.Contains(err.Error(), "could not reach OpenAI-compatible provider (127.0.0.1:1)") {
		t.Fatalf("unreachable: %v", err)
	}
	if name := (&Client{Provider: "openai", BaseURL: "https://api.openai.com/v1"}).serviceName(); name != "OpenAI (api.openai.com)" {
		t.Fatalf("name=%q", name)
	}
}
