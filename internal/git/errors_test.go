package git

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestGitDiagnosticsAreSurfaced(t *testing.T) {
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	outside := &Client{Dir: t.TempDir(), Output: io.Discard}
	t.Setenv("GIT_CEILING_DIRECTORIES", outside.Dir)
	// PrepareCommit checks HEAD before diffing, so that is where users see this.
	if _, err := outside.HeadRef(context.Background()); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("outside repository: %v", err)
	}
	dir := repository(t)
	c := &Client{Dir: dir, Output: io.Discard}
	if _, err := c.StagedDiff(context.Background()); err == nil || !strings.Contains(err.Error(), "no staged changes") {
		t.Fatalf("nothing staged: %v", err)
	}
	if _, err := c.RemoteURL(context.Background()); err == nil || !strings.Contains(err.Error(), "No such remote") {
		t.Fatalf("missing origin: %v", err)
	}
}

func TestGitTimeoutIsLabelled(t *testing.T) {
	c := &Client{Dir: t.TempDir()}
	// Run a long-lived Git command with an intentionally tiny bound.
	_, err := c.exec(context.Background(), spec{args: []string{"-c", "alias.wait=!sleep 5", "wait"}, limit: 64, timeout: 50 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "git wait timed out after 50ms") {
		t.Fatalf("error=%v", err)
	}
}

func TestSanitizeRedactsCredentialsAndControls(t *testing.T) {
	got := sanitize("fatal: unable to access 'https://x-access-token:ghs_secret@github.com/o/r.git/': \x1b[31mdenied\u202e")
	if strings.Contains(got, "ghs_secret") || strings.ContainsAny(got, "\x1b\u202e") || !strings.Contains(got, "https://***@github.com/o/r.git/") {
		t.Fatalf("sanitized=%q", got)
	}
}

func TestStderrCaptureStreamsAndSummarizes(t *testing.T) {
	var live strings.Builder
	w := &stderrCapture{live: &live}
	for _, chunk := range []string{"Writing objects:  50%\rWriting", " objects: 100%\r\nremote: token https://u:p@h/x\n", "error: failed to push some refs\n", "hint: pull first"} {
		_, _ = w.Write([]byte(chunk))
	}
	w.flush()
	if strings.Contains(live.String(), "u:p@") || !strings.Contains(live.String(), "Writing objects: 100%\r") || !strings.HasSuffix(live.String(), "hint: pull first\n") {
		t.Fatalf("live=%q", live.String())
	}
	if got := w.summary(); got != "remote: token https://***@h/x; error: failed to push some refs; hint: pull first" {
		t.Fatalf("summary=%q", got)
	}
	if got := subcommand([]string{"-c", "a=b", "-c", "c=d", "push", "origin"}); got != "push" {
		t.Fatalf("subcommand=%q", got)
	}
}
