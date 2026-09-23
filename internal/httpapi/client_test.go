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
