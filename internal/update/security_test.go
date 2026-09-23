package update

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestVersionVerificationAcceptsTagPrefix(t *testing.T) {
	for _, output := range []string{"commit version 1.2.3\n  commit: abc\n", "commit version v1.2.3\n"} {
		if err := verifyVersion(output, "v1.2.3"); err != nil {
			t.Fatal(err)
		}
	}
	for _, output := range []string{"commit version 1.2.4\n", "other version 1.2.3\n", "commit version invalid\n"} {
		if verifyVersion(output, "v1.2.3") == nil {
			t.Fatalf("accepted %q", output)
		}
	}
	var out versionOutput
	if _, err := out.Write([]byte(strings.Repeat("x", 4097))); err == nil {
		t.Fatal("unbounded version output")
	}
}

func TestDownloadRedirectsNeverCarryCredentials(t *testing.T) {
	for _, target := range []string{"https://release-assets.githubusercontent.com/binary", "https://evil.example/binary", "http://release-assets.githubusercontent.com/binary"} {
		requests := 0
		c := &Client{Token: "secret", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			requests++
			if r.Header.Get("Authorization") != "" {
				t.Fatal("sent credentials to public asset download")
			}
			if requests == 1 {
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("binary"))}, nil
		})}}
		data, err := c.download(context.Background(), "https://github.com/person/repo/releases/download/v1/archive", 100)
		allowed := strings.HasPrefix(target, "https://release-assets.")
		if allowed && (err != nil || string(data) != "binary" || requests != 2) {
			t.Fatalf("allowed redirect: %q %v", data, err)
		}
		if !allowed && (err == nil || requests != 1) {
			t.Fatal("followed an untrusted redirect")
		}
	}
}

func TestDownloadSizeLimit(t *testing.T) {
	c := &Client{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("too large"))}, nil
	})}}
	if _, err := c.download(context.Background(), "https://github.com/person/repo/releases/download/v1/archive", 3); err == nil {
		t.Fatal("accepted oversized download")
	}
}
