// Package git is the subprocess adapter. Commands never use a shell, external
// diff drivers, or text conversion filters, and all commands inherit cancellation.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

type Client struct {
	Dir          string
	MaxDiffBytes int
	Timeout      time.Duration
	Output       io.Writer
}

// pushTimeout bounds a single network push. Two things differ from every other
// command: transfers can legitimately take minutes on a large branch, and the
// work is not local, so the configured per-command timeout (60s by default) is
// too aggressive and would kill a healthy push mid-transfer. Ctrl-C still
// cancels immediately via the parent context.
const pushTimeout = 15 * time.Minute

// limitedBuffer bounds memory even when a repository or helper prints huge output.
type limitedBuffer struct {
	buffer   bytes.Buffer
	max      int
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.max - b.buffer.Len()
	if n > left {
		b.exceeded = true
		p = p[:left]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func (c *Client) commandTimeout() time.Duration {
	if c.Timeout <= 0 {
		return time.Minute
	}
	return c.Timeout
}

func (c *Client) run(ctx context.Context, limit int, args ...string) (string, error) {
	return c.runFor(ctx, limit, c.commandTimeout(), args...)
}

func (c *Client) runFor(ctx context.Context, limit int, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.Dir
	cmd.WaitDelay = time.Second
	out := &limitedBuffer{max: limit}
	cmd.Stdout = out
	// Do not echo credential-bearing remote URLs or arbitrary helper output.
	if err := runCommand(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %s failed: %w", args[0], err)
	}
	if out.exceeded {
		return "", errors.New("Git output exceeds configured limit; stage fewer changes or increase max_diff_bytes")
	}
	return out.buffer.String(), nil
}

func (c *Client) diff(ctx context.Context, args ...string) (string, error) {
	limit := c.MaxDiffBytes
	if limit <= 0 {
		limit = 100000
	}
	out, err := c.run(ctx, limit, append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-color"}, args...)...)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", errors.New("no changes found")
	}
	return out, nil
}

func (c *Client) StagedDiff(ctx context.Context) (string, error) {
	return c.diff(ctx, "--cached", "--")
}

func (c *Client) Head(ctx context.Context) (string, error) {
	out, err := c.run(ctx, 1024, "rev-parse", "--verify", "HEAD")
	if err == nil {
		return strings.TrimSpace(out), nil
	}
	// Exit 1 from show-ref --verify --quiet means a missing ref (an unborn branch).
	// Other failures must not be mistaken for an initial commit.
	ref, refErr := c.run(ctx, 4096, "symbolic-ref", "--quiet", "HEAD")
	if refErr != nil {
		return "", err
	}
	_, refErr = c.run(ctx, 1024, "show-ref", "--verify", "--quiet", strings.TrimSpace(ref))
	var exit *exec.ExitError
	if errors.As(refErr, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return "", err
}

// HeadRef records branch identity independently of the commit hash. An unborn
// branch still has a symbolic target; detached HEAD deliberately returns empty.
func (c *Client) HeadRef(ctx context.Context) (string, error) {
	ref, err := c.run(ctx, 4096, "symbolic-ref", "--quiet", "HEAD")
	if err == nil {
		return strings.TrimSpace(ref), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return "", err
}

func (c *Client) Branch(ctx context.Context) (string, error) {
	out, err := c.run(ctx, 4096, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", errors.New("not on a branch (or not in a Git repository)")
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) validateBranch(ctx context.Context, branch string) error {
	if strings.HasPrefix(branch, "-") || branch == "" {
		return errors.New("invalid branch name")
	}
	_, err := c.run(ctx, 4096, "check-ref-format", "refs/heads/"+branch)
	return err
}

func (c *Client) BranchDiff(ctx context.Context, base string) (string, error) {
	if err := c.validateBranch(ctx, base); err != nil {
		return "", err
	}
	diff, err := c.diff(ctx, "refs/remotes/origin/"+base+"...HEAD", "--")
	if err != nil {
		return "", fmt.Errorf("compare with origin/%s (try 'git fetch origin'): %w", base, err)
	}
	return diff, nil
}

func (c *Client) RemoteURL(ctx context.Context) (string, error) {
	out, err := c.run(ctx, 8192, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Client) RemoteHead(ctx context.Context, branch string) (string, error) {
	if err := c.validateBranch(ctx, branch); err != nil {
		return "", err
	}
	out, err := c.run(ctx, 8192, "ls-remote", "--heads", "origin", "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("query origin (check network and Git authentication): %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return "", nil
	}
	fields := strings.Fields(out)
	if len(fields) != 2 || fields[1] != "refs/heads/"+branch {
		return "", errors.New("unexpected remote branch response")
	}
	return fields[0], nil
}

func (c *Client) Push(ctx context.Context, branch string) error {
	if err := c.validateBranch(ctx, branch); err != nil {
		return err
	}
	fetchURL, err := c.RemoteURL(ctx)
	if err != nil {
		return err
	}
	pushURL, err := c.run(ctx, 8192, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	if !sameDestination(fetchURL, pushURL) {
		return errors.New("origin has a different or multiple push destinations; push manually before running 'commit pr'")
	}
	_, err = c.runFor(ctx, 8192, pushTimeout, "-c", "remote.origin.mirror=false", "-c", "push.followTags=false", "push", "--no-force", "--recurse-submodules=no", "-u", "origin", "HEAD:refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("push failed; inspect 'git push origin' manually: %w", err)
	}
	return nil
}

// sameDestination ignores only HTTPS userinfo, allowing CI token rotation.
// Git emits one push URL per line; a local path can legitimately contain spaces.
func sameDestination(fetchURL, pushURLs string) bool {
	urls := strings.Split(strings.TrimSpace(pushURLs), "\n")
	if len(urls) != 1 {
		return false
	}
	fetch, fetchOK := remoteDestination(fetchURL)
	push, pushOK := remoteDestination(urls[0])
	return fetchOK && pushOK && fetch == push
}

// SSH usernames affect the remote identity and must be preserved. Queries and
// fragments are rejected instead of erased: they can change routing semantics.
// SCP-style remotes and local paths are compared verbatim.
func remoteDestination(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		return raw, true
	}
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") {
		return "", false
	}
	if u.Scheme == "https" {
		if u.Host == "" {
			return "", false
		}
		u.User = nil
	}
	return u.String(), true
}

func (c *Client) Commit(ctx context.Context, message string) error {
	ctx, cancel := context.WithTimeout(ctx, c.commandTimeout())
	defer cancel()
	// Passing the message through stdin avoids option parsing and OS argv limits.
	cmd := exec.CommandContext(ctx, "git", "commit", "--file=-")
	cmd.Dir = c.Dir
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(message)
	cmd.Stdout, cmd.Stderr = c.Output, c.Output
	if err := runCommand(ctx, cmd); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git commit failed (check Git identity and hooks): %w", err)
	}
	return nil
}
