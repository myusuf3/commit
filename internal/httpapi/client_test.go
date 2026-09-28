package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCancellationAndTimeout(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := JSON(ctx, NewClient(time.Second), "GET", s.URL, "", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := JSON(context.Background(), NewClient(20*time.Millisecond), "GET", s.URL, "", nil, nil); err == nil {
		t.Fatal("timeout not enforced")
	}
}

func TestRejectsRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer s.Close()
	if err := JSON(context.Background(), NewClient(time.Second), "GET", s.URL, "secret", nil, nil); err == nil {
		t.Fatal("redirect allowed")
	}
	if hits.Load() != 0 {
		t.Fatal("followed redirect")
	}
}

func TestTypedErrors(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/quota":
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"You exceeded your quota, key sk-abc","type":"insufficient_quota","code":"insufficient_quota"}}`))
		case "/anthropic":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: x"}}`))
		case "/github":
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for o:b."}]}`))
		case "/unsafe-code":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"sk-secret value!","type":"invalid_request_error"}}`))
		case "/numeric-code":
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":{"code":500,"type":null}}`))
		case "/redirect":
			http.Redirect(w, r, "/github", 301)
		default:
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`<html>not json</html>`))
		}
	}))
	defer s.Close()
	for path, want := range map[string]StatusError{
		"/quota":        {Status: 429, Code: "insufficient_quota", Message: "You exceeded your quota, key sk-abc"},
		"/anthropic":    {Status: 404, Code: "not_found_error", Message: "model: x"},
		"/github":       {Status: 422, Message: "Validation Failed: A pull request already exists for o:b."},
		"/unsafe-code":  {Status: 400, Code: "invalid_request_error"},
		"/numeric-code": {Status: 500},
		"/html":         {Status: 503},
	} {
		err := JSON(context.Background(), NewClient(time.Second), "GET", s.URL+path, "", nil, nil)
		var status *StatusError
		if !errors.As(err, &status) || *status != want {
			t.Fatalf("%s: %#v", path, err)
		}
	}
	err := JSON(context.Background(), NewClient(time.Second), "GET", s.URL+"/redirect", "", nil, nil)
	var transport *TransportError
	if !errors.As(err, &transport) || !transport.Redirect || !errors.Is(err, ErrRedirect) {
		t.Fatalf("redirect: %#v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	err = JSON(context.Background(), NewClient(20*time.Millisecond), "GET", slow.URL, "", nil, nil)
	if !errors.As(err, &transport) || !transport.Timeout {
		t.Fatalf("timeout: %#v", err)
	}
}
