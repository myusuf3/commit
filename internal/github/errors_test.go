package github

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/myusuf3/commit/internal/httpapi"
)

func TestExplain(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&httpapi.StatusError{Status: 401, Message: "Bad credentials"}, "GitHub rejected the token (HTTP 401: Bad credentials); check GITHUB_TOKEN"},
		{&httpapi.StatusError{Status: 403, Message: "Resource not accessible by personal access token"}, "GitHub denied access to o/r (HTTP 403: Resource not accessible"},
		{&httpapi.StatusError{Status: 403, Message: "API rate limit exceeded for user"}, "GitHub rate limit reached (HTTP 403)"},
		{&httpapi.StatusError{Status: 404, Message: "Not Found"}, "GitHub could not find o/r (HTTP 404); check origin, or the token cannot access this private repository"},
		{&httpapi.StatusError{Status: 422, Message: "Validation Failed: A pull request already exists for o:b."}, "GitHub rejected the request (HTTP 422: Validation Failed: A pull request already exists for o:b.)"},
		{&httpapi.StatusError{Status: 502}, "GitHub is unavailable (HTTP 502); try again later"},
		{&httpapi.StatusError{Status: 422, Message: "bad\x1b[2J\u202e\nthing"}, "(HTTP 422: bad [2J thing)"},
		{&httpapi.TransportError{Redirect: true}, "the repository was probably renamed or transferred"},
		{&httpapi.TransportError{Timeout: true}, "GitHub API did not respond within 1m0s"},
		{&httpapi.TransportError{}, "could not reach the GitHub API"},
	} {
		if got := Explain(tc.err, "o/r", time.Minute).Error(); !strings.Contains(got, tc.want) {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
	if err := Explain(context.Canceled, "o/r", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation changed: %v", err)
	}
	if Explain(nil, "o/r", time.Minute) != nil {
		t.Fatal("nil error changed")
	}
	long := strings.Repeat("x", 1000)
	if got := Explain(&httpapi.StatusError{Status: 422, Message: long}, "o/r", 0).Error(); len(got) > 400 {
		t.Fatalf("unbounded message: %d bytes", len(got))
	}
}
