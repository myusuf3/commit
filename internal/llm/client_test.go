package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myusuf3/commit/internal/httpapi"
)

func TestGenerateRequestsAndResults(t *testing.T) {
	t.Parallel()
	var requests []struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("invalid request")
		}
		var req struct {
			Model    string    `json:"model"`
			Messages []message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		requests = append(requests, req)
		text := "feat: add a thing"
		if len(requests) == 2 {
			text = `{"title":"Add a thing","body":"## Changes\n- A thing\n\n## Testing\nNot run."}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message{Content: text}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), BaseURL: server.URL + "/v1", APIKey: "test-key", Model: "test-model", Conventional: true}
	msg, err := c.CommitMessage(context.Background(), "a diff")
	if err != nil || msg != "feat: add a thing" {
		t.Fatalf("message=%q error=%v", msg, err)
	}
	pr, err := c.PullRequest(context.Background(), "another diff")
	if err != nil || pr.Title != "Add a thing" || !strings.Contains(pr.Body, "Not run") {
		t.Fatalf("PR=%+v error=%v", pr, err)
	}
	for _, req := range requests {
		if req.Model != "test-model" || len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Fatalf("request: %+v", req)
		}
		if !strings.Contains(req.Messages[0].Content, "untrusted data") {
			t.Fatal("missing trust boundary")
		}
	}
}

func TestInvalidProviderResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, content, finish string
		pr                    bool
	}{
		{"empty", "", "stop", false}, {"truncated", "fix: partial", "length", false},
		{"control", "fix: \x1b[2J", "stop", false}, {"fenced", "```fix: hi```", "stop", false},
		{"invalid JSON", "Title: a\nbody", "stop", true},
		{"empty title", `{"title":"","body":"b"}`, "stop", true},
		{"extra field", `{"title":"a","body":"b","number":123}`, "stop", true},
		{"decoded escape", `{"title":"a","body":"\u001b[2J"}`, "stop", true},
		{"multiple objects", `{"title":"a","body":"b"}{}`, "stop", true},
		{"multiline title", `{"title":"a\nb","body":"b"}`, "stop", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message{Content: tc.content}, "finish_reason": tc.finish}}})
			}))
			defer s.Close()
			c := &Client{HTTP: s.Client(), BaseURL: s.URL}
			var err error
			if tc.pr {
				_, err = c.PullRequest(context.Background(), "diff")
			} else {
				_, err = c.CommitMessage(context.Background(), "diff")
			}
			if err == nil {
				t.Fatal("accepted invalid provider output")
			}
		})
	}
}

func TestValidTextAllowsLegitimateUnicodeAndRejectsSpoofing(t *testing.T) {
	allowed := []string{
		"feat: add café support",             // accents
		"رسید: رفع اشکال‌ها",                 // Persian with ZWNJ (U+200C)
		"feat: هه‌",                          // ZWNJ alone
		"feat: family 👨\u200d👩\u200d👧 emoji", // ZWJ sequence
		"feat: 日本語のコミット",                     // CJK
		"feat: multi\n\nbody with\ttab",      // newline and tab
	}
	for _, text := range allowed {
		if !validText(text) {
			t.Fatalf("rejected legitimate text: %q", text)
		}
	}
	rejected := map[string]string{
		"ANSI escape":            "feat: \x1b[2Jcleared",
		"bell":                   "feat: \a",
		"NUL":                    "feat: a\x00b",
		"right-to-left override": "feat: \u202egpj.exe",
		"left-to-right mark":     "feat: a\u200eb",
		"right-to-left mark":     "feat: a\u200fb",
		"bidi isolate":           "feat: \u2066a\u2069",
		"zero width space":       "feat: a\u200bb",
		"BOM":                    "\ufefffeat: a",
		"soft hyphen":            "feat: a\u00adb",
		"invalid utf8":           "feat: \xff\xfe",
	}
	for name, text := range rejected {
		t.Run(name, func(t *testing.T) {
			if validText(text) {
				t.Fatalf("accepted %q", text)
			}
		})
	}
}

func TestProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{401, `{"error":{"message":"secret-key"}}`}, {429, "slow down"}, {502, "<html>bad gateway</html>"},
		{200, `{"choices":[]}`}, {200, `{"error":{"message":"secret-key"}}`}, {200, "{"},
		{200, strings.Repeat(" ", httpapi.MaxResponseBytes+1)},
	} {
		t.Run(http.StatusText(tc.status)+tc.body[:1], func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c := &Client{HTTP: s.Client(), BaseURL: s.URL}
			_, err := c.CommitMessage(context.Background(), "diff")
			if err == nil || strings.Contains(err.Error(), "secret-key") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
