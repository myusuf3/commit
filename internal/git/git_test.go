package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func command(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func repository(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	command(t, dir, "init", "-b", "main")
	command(t, dir, "config", "user.name", "Test User")
	command(t, dir, "config", "user.email", "test@example.com")
	command(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func stage(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	command(t, dir, "add", "file.txt")
}

func TestInitialCommitAndStagedOnly(t *testing.T) {
	dir := repository(t)
	c := &Client{Dir: dir}
	ctx := context.Background()
	if head, err := c.Head(ctx); err != nil || head != "" {
		t.Fatalf("unborn HEAD: %q %v", head, err)
	}
	if ref, err := c.HeadRef(ctx); err != nil || ref != "refs/heads/main" {
		t.Fatalf("unborn symbolic HEAD: %q %v", ref, err)
	}
	if _, err := c.StagedDiff(ctx); err == nil {
		t.Fatal("expected no staged changes error")
	}
	stage(t, dir, "staged\n")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("unstaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := c.StagedDiff(ctx)
	if err != nil || !strings.Contains(diff, "+staged") || strings.Contains(diff, "+unstaged") {
		t.Fatalf("diff=%q error=%v", diff, err)
	}
	if err := c.Commit(ctx, "fix: quotes \" and $(not-a-shell)\n\nBody"); err != nil {
		t.Fatal(err)
	}
	if got := command(t, dir, "show", "HEAD:file.txt"); got != "staged" {
		t.Fatalf("committed wrong data: %s", got)
	}
	if head, err := c.Head(ctx); err != nil || len(head) != 40 {
		t.Fatalf("HEAD=%s error=%v", head, err)
	}
	command(t, dir, "checkout", "--detach")
	if ref, err := c.HeadRef(ctx); err != nil || ref != "" {
		t.Fatalf("detached symbolic HEAD: %q %v", ref, err)
	}
	if _, err := c.Branch(ctx); err == nil {
		t.Fatal("detached branch accepted")
	}
}

func TestDiffLimitsAndNoExternalHelpers(t *testing.T) {
	dir := repository(t)
	stage(t, dir, strings.Repeat("large\n", 100))
	c := &Client{Dir: dir, MaxDiffBytes: 30}
	if _, err := c.StagedDiff(context.Background()); err == nil {
		t.Fatal("silently truncated diff")
	}
	c.MaxDiffBytes = 100000
	command(t, dir, "config", "diff.external", "nonexistent-unsafe-helper")
	t.Setenv("GIT_EXTERNAL_DIFF", "nonexistent-unsafe-helper")
	if _, err := c.StagedDiff(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.StagedDiff(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
}

// TestSameDestination is a pure decision test: it must never touch the network.
func TestSameDestination(t *testing.T) {
	for _, tc := range []struct {
		name, fetch, push string
		same              bool
	}{
		{"rotated credential", "https://token1@github.com/o/r.git", "https://token2@github.com/o/r.git", true},
		{"credential added", "https://github.com/o/r.git", "https://x:y@github.com/o/r.git", true},
		{"exact match", "git@github.com:o/r.git", "git@github.com:o/r.git", true},
		{"different repo", "https://github.com/o/r.git", "https://github.com/o/other.git", false},
		{"different host", "https://github.com/o/r.git", "https://evil.example/o/r.git", false},
		{"different ssh repo", "git@github.com:o/r.git", "git@github.com:o/other.git", false},
		{"multiple push urls", "https://github.com/o/r.git", "https://github.com/o/r.git\nhttps://github.com/o/x.git", false},
		{"empty push url", "https://github.com/o/r.git", "", false},
		{"different query", "https://github.com/o/r.git", "https://github.com/o/r.git?route=other", false},
		{"identical queries rejected", "https://github.com/o/r.git?q=1", "https://github.com/o/r.git?q=1", false},
		{"empty query rejected", "https://github.com/o/r.git", "https://github.com/o/r.git?", false},
		{"fragment rejected", "https://github.com/o/r.git", "https://github.com/o/r.git#other", false},
		{"empty fragment rejected", "https://github.com/o/r.git", "https://github.com/o/r.git#", false},
		{"SSH username preserved", "ssh://git@github.com/o/r.git", "ssh://other@github.com/o/r.git", false},
		{"SSH username cannot disappear", "ssh://git@github.com/o/r.git", "ssh://github.com/o/r.git", false},
		{"same SSH URI", "ssh://git@github.com/o/r.git", "ssh://git@github.com/o/r.git", true},
		{"HTTP userinfo preserved", "http://one@localhost/repo", "http://two@localhost/repo", false},
		{"port preserved", "https://github.com/o/r.git", "https://github.com:8443/o/r.git", false},
		{"malformed URLs rejected", "https://%zz/repo", "https://%zz/repo", false},
		{"missing HTTPS host rejected", "https:///repo", "https:///repo", false},
		{"local path with spaces", "/tmp/review remote.git", "/tmp/review remote.git\n", true},
		{"Windows line ending", "https://github.com/o/r.git", "https://github.com/o/r.git\r\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameDestination(tc.fetch, tc.push); got != tc.same {
				t.Fatalf("sameDestination=%v, want %v", got, tc.same)
			}
		})
	}
}

func TestBranchDiffAndPush(t *testing.T) {
	dir := repository(t)
	bare := filepath.Join(t.TempDir(), "remote with spaces.git")
	if err := os.Mkdir(bare, 0700); err != nil {
		t.Fatal(err)
	}
	command(t, bare, "init", "--bare")
	stage(t, dir, "base\n")
	command(t, dir, "commit", "-m", "base")
	command(t, dir, "remote", "add", "origin", bare)
	command(t, dir, "push", "-u", "origin", "main")
	command(t, dir, "checkout", "-b", "feature/unicode-é")
	stage(t, dir, "changed\n")
	command(t, dir, "commit", "-m", "change")
	c := &Client{Dir: dir}
	ctx := context.Background()
	diff, err := c.BranchDiff(ctx, "main")
	if err != nil || !strings.Contains(diff, "+changed") {
		t.Fatalf("diff=%q err=%v", diff, err)
	}
	if remote, err := c.RemoteHead(ctx, "feature/unicode-é"); err != nil || remote != "" {
		t.Fatalf("remote=%q err=%v", remote, err)
	}
	if upstream, err := c.Upstream(ctx, "feature/unicode-é"); err != nil || upstream != "" {
		t.Fatalf("upstream=%q err=%v", upstream, err)
	}
	if err := c.Push(ctx, "feature/unicode-é", true); err != nil {
		t.Fatal(err)
	}
	if upstream, err := c.Upstream(ctx, "feature/unicode-é"); err != nil || upstream != "origin/feature/unicode-é" {
		t.Fatalf("upstream after push=%q err=%v", upstream, err)
	}
	remote, err := c.RemoteHead(ctx, "feature/unicode-é")
	head, _ := c.Head(ctx)
	if err != nil || remote != head {
		t.Fatalf("remote=%q head=%q err=%v", remote, head, err)
	}
	for _, ref := range []string{"--output=/tmp/unsafe", "main..HEAD", "main:bad", ""} {
		if _, err := c.BranchDiff(ctx, ref); err == nil {
			t.Fatalf("accepted invalid ref: %s", ref)
		}
	}
	command(t, dir, "remote", "set-url", "--push", "origin", t.TempDir())
	if err := c.Push(ctx, "feature/unicode-é", false); err == nil {
		t.Fatal("pushed to different destination")
	}
	command(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
	if _, err := c.RemoteHead(ctx, "main"); err == nil {
		t.Fatal("network/repo error treated as missing branch")
	}
}

// A branch created with --track (or from origin/main with the default
// branch.autoSetupMerge) has a deliberate upstream that a push must not replace.
func TestPushPreservesExistingUpstream(t *testing.T) {
	dir := repository(t)
	bare := t.TempDir()
	command(t, bare, "init", "--bare")
	stage(t, dir, "base\n")
	command(t, dir, "commit", "-m", "base")
	command(t, dir, "remote", "add", "origin", bare)
	command(t, dir, "push", "origin", "main")
	command(t, dir, "fetch", "origin")
	command(t, dir, "switch", "-c", "work", "--track", "origin/main")
	c := &Client{Dir: dir}
	ctx := context.Background()
	if upstream, err := c.Upstream(ctx, "work"); err != nil || upstream != "origin/main" {
		t.Fatalf("upstream=%q err=%v", upstream, err)
	}
	if upstream, err := c.Upstream(ctx, "missing"); err != nil || upstream != "" {
		t.Fatalf("missing branch upstream=%q err=%v", upstream, err)
	}
	stage(t, dir, "work\n")
	command(t, dir, "commit", "-m", "work")
	if err := c.Push(ctx, "work", false); err != nil {
		t.Fatal(err)
	}
	if upstream, err := c.Upstream(ctx, "work"); err != nil || upstream != "origin/main" {
		t.Fatalf("push replaced upstream: %q %v", upstream, err)
	}
	if remote, err := c.RemoteHead(ctx, "work"); err != nil || remote == "" {
		t.Fatalf("branch not pushed: %q %v", remote, err)
	}
}
