package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recorder captures what the client sent so request shaping can be asserted.
type recorder struct {
	requests []recorded
}
type recorded struct {
	path    string
	header  http.Header
	body    map[string]any
	failure func(w http.ResponseWriter)
	reply   string
}

func newRecorder(t *testing.T, replies []func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i < len(replies) {
			replies[i](w, r)
		} else {
			t.Errorf("unexpected extra request to %s", r.URL.Path)
			w.WriteHeader(500)
		}
		i++
	}))
}

func capture(rec *recorder, reply any) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.requests = append(rec.requests, recorded{path: r.URL.Path, header: r.Header.Clone(), body: body})
		_ = json.NewEncoder(w).Encode(reply)
	}
}

func TestMessagesProtocol(t *testing.T) {
	rec := &recorder{}
	server := newRecorder(t, []func(http.ResponseWriter, *http.Request){capture(rec, map[string]any{
		"type": "message", "role": "assistant", "stop_reason": "end_turn",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "internal"},
			map[string]any{"type": "text", "text": "feat: add a thing"},
		},
	})})
	defer server.Close()
	c := &Client{HTTP: server.Client(), BaseURL: server.URL + "/v1", APIKey: "go-key", Model: "minimax-m3", Provider: "opencode-go", APIFormat: "messages"}
	got, err := c.CommitMessage(context.Background(), "a diff")
	if err != nil || got != "feat: add a thing" {
		t.Fatalf("text=%q err=%v", got, err)
	}
	if len(rec.requests) != 1 {
		t.Fatal("wrong request count")
	}
	req := rec.requests[0]
	if req.path != "/v1/messages" {
		t.Fatalf("path=%s", req.path)
	}
	if req.header.Get("x-api-key") != "go-key" || req.header.Get("anthropic-version") == "" {
		t.Fatal("missing Anthropic auth headers")
	}
	if req.header.Get("Authorization") != "" {
		t.Fatal("sent a bearer token to the messages endpoint")
	}
	if req.body["model"] != "minimax-m3" || req.body["system"] == nil || req.body["max_tokens"] == nil {
		t.Fatalf("body=%v", req.body)
	}
	messages, ok := req.body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages=%v", req.body["messages"])
	}
}

func TestMessagesRejectsBadResponses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply map[string]any
	}{
		{"truncated", map[string]any{"type": "message", "role": "assistant", "stop_reason": "max_tokens", "content": []any{map[string]any{"type": "text", "text": "partial"}}}},
		{"refusal", map[string]any{"type": "message", "role": "assistant", "stop_reason": "end_turn", "content": []any{map[string]any{"type": "tool_use", "id": "t"}}}},
		{"error", map[string]any{"type": "error", "error": map[string]any{"message": "secret"}}},
		{"empty", map[string]any{"type": "message", "role": "assistant", "stop_reason": "end_turn", "content": []any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			server := newRecorder(t, []func(http.ResponseWriter, *http.Request){capture(rec, tc.reply)})
			defer server.Close()
			c := &Client{HTTP: server.Client(), BaseURL: server.URL, APIKey: "k", Model: "minimax-m3", Provider: "opencode-go", APIFormat: "messages"}
			if _, err := c.CommitMessage(context.Background(), "diff"); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestResponsesProtocol(t *testing.T) {
	rec := &recorder{}
	server := newRecorder(t, []func(http.ResponseWriter, *http.Request){capture(rec, map[string]any{
		"status": "completed",
		"output": []any{
			map[string]any{"type": "reasoning", "id": "r1"},
			map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": `{"title":"Add a thing","body":"Summary"}`}}},
		},
	})})
	defer server.Close()
	c := &Client{HTTP: server.Client(), BaseURL: server.URL + "/v1", APIKey: "go-key", Model: "grok-4.7", Provider: "opencode-go", APIFormat: "responses"}
	pr, err := c.PullRequest(context.Background(), "a diff")
	if err != nil || pr.Title != "Add a thing" {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
	req := rec.requests[0]
	if req.path != "/v1/responses" {
		t.Fatalf("path=%s", req.path)
	}
	if req.header.Get("Authorization") != "Bearer go-key" {
		t.Fatal("missing bearer auth")
	}
	if req.body["store"] != false {
		t.Fatalf("store=%v", req.body["store"])
	}
	if req.body["instructions"] == nil || req.body["max_output_tokens"] == nil {
		t.Fatalf("body=%v", req.body)
	}
}

func TestResponsesRejectsBadResponses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply map[string]any
	}{
		{"incomplete", map[string]any{"status": "incomplete", "output": []any{}}},
		{"refusal", map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "refusal", "refusal": "no"}}}}}},
		{"unfinished item", map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "in_progress", "content": []any{map[string]any{"type": "output_text", "text": "x"}}}}}},
		{"error", map[string]any{"error": map[string]any{"message": "boom"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			server := newRecorder(t, []func(http.ResponseWriter, *http.Request){capture(rec, tc.reply)})
			defer server.Close()
			c := &Client{HTTP: server.Client(), BaseURL: server.URL, APIKey: "k", Model: "grok-4.7", Provider: "opencode-go", APIFormat: "responses"}
			if _, err := c.CommitMessage(context.Background(), "diff"); err == nil {
				t.Fatal("accepted bad response")
			}
		})
	}
}

func TestOpenCodeSessionHeaderIsStableAndProviderScoped(t *testing.T) {
	rec := &recorder{}
	server := newRecorder(t, []func(http.ResponseWriter, *http.Request){
		capture(rec, map[string]any{"choices": []any{map[string]any{"message": message{Content: "feat: one"}, "finish_reason": "stop"}}}),
		capture(rec, map[string]any{"choices": []any{map[string]any{"message": message{Content: "feat: two"}, "finish_reason": "stop"}}}),
	})
	defer server.Close()
	c := &Client{HTTP: server.Client(), BaseURL: server.URL, APIKey: "k", Model: "kimi-k2.6", Provider: "opencode-go"}
	for _, want := range []string{"feat: one", "feat: two"} {
		if got, err := c.CommitMessage(context.Background(), "diff"); err != nil || got != want {
			t.Fatalf("got=%q err=%v", got, err)
		}
	}
	if len(rec.requests) != 2 {
		t.Fatal("wrong request count")
	}
	first, second := rec.requests[0].header.Get("x-opencode-session"), rec.requests[1].header.Get("x-opencode-session")
	if first == "" || first != second {
		t.Fatalf("session header not stable: %q %q", first, second)
	}

	plain := &Client{HTTP: server.Client(), BaseURL: server.URL, APIKey: "k", Model: "gpt-4o-mini", Provider: "openai"}
	server2 := newRecorder(t, []func(http.ResponseWriter, *http.Request){capture(&recorder{}, map[string]any{"choices": []any{map[string]any{"message": message{Content: "feat: x"}, "finish_reason": "stop"}}})})
	defer server2.Close()
	plain.HTTP = server2.Client()
	plain.BaseURL = server2.URL
	if _, err := plain.CommitMessage(context.Background(), "diff"); err != nil {
		t.Fatal(err)
	}
	var seen http.Header
	plain.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Clone()
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"feat: ok"},"finish_reason":"stop"}]}`))}, nil
	})}
	if _, err := plain.CommitMessage(context.Background(), "diff"); err != nil {
		t.Fatal(err)
	}
	if seen.Get("x-opencode-session") != "" || seen.Get("Authorization") != "Bearer k" {
		t.Fatalf("OpenAI request carried OpenCode headers: %v", seen)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
