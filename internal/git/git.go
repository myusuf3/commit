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
	"regexp"
	"strings"
	"time"
	"unicode"
)

type Client struct {
	Dir          string
	MaxDiffBytes int
	Timeout      time.Duration
	// Output receives commit/hook output and streamed push progress.
	Output io.Writer
	// Interactive lets commands that may prompt (network access, commit hooks,
	// signing) use the terminal. Otherwise they are detached from it and fail
	// fast instead of waiting for input that can never arrive.
	Interactive bool
}

// longCommandTimeout bounds pushes and commits. Transfers can legitimately take
// minutes on a large branch, and commits run user hooks (linters, tests) and
// signing that may wait for a passphrase or hardware-key touch. The configured
// per-command timeout (60s by default) would kill healthy work mid-way.
// Ctrl-C still cancels immediately via the parent context.
const longCommandTimeout = 15 * time.Minute

var errNoChanges = errors.New("no changes found")

// errOutputLimit marks output that exceeded its byte bound.
var errOutputLimit = errors.New("output limit exceeded")

type diffTooLargeError struct{ limit int }

func (e diffTooLargeError) Error() string {
	return fmt.Sprintf("diff exceeds max_diff_bytes (%d bytes); reduce the change or raise max_diff_bytes", e.limit)
}

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

const stderrTailBytes = 4096

// credentialPattern matches URL userinfo such as https://x-access-token:TOKEN@host.
var credentialPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s@]*@`)

// sanitize makes Git/helper/remote text safe to display: URL credentials are
// redacted and terminal controls or text-reordering characters are dropped.
func sanitize(text string) string {
	text = credentialPattern.ReplaceAllString(strings.ToValidUTF8(text, "?"), "${1}***@")
	return strings.Map(func(r rune) rune {
		if (unicode.IsControl(r) && r != '\t') || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, text)
}

// stderrCapture keeps a bounded tail of Git's diagnostics for error messages
// and optionally streams sanitized lines (including \r progress updates) live.
type stderrCapture struct {
	tail    []byte
	live    io.Writer
	pending []byte
}

func (w *stderrCapture) Write(p []byte) (int, error) {
	w.tail = append(w.tail, p...)
	if len(w.tail) > stderrTailBytes {
		w.tail = append([]byte(nil), w.tail[len(w.tail)-stderrTailBytes:]...)
	}
	if w.live != nil {
		w.pending = append(w.pending, p...)
		for {
			i := bytes.IndexAny(w.pending, "\r\n")
			if i < 0 {
				break
			}
			_, _ = fmt.Fprintf(w.live, "%s%c", sanitize(string(w.pending[:i])), w.pending[i])
			w.pending = w.pending[i+1:]
		}
		if len(w.pending) > stderrTailBytes {
			w.flush()
		}
	}
	return len(p), nil
}

func (w *stderrCapture) flush() {
	if w.live != nil && len(w.pending) > 0 {
		_, _ = fmt.Fprintln(w.live, sanitize(string(w.pending)))
	}
	w.pending = nil
}

// summary returns the last few meaningful diagnostic lines, which is where Git
// reports the reason for a failure (for example "fatal: not a git repository").
func (w *stderrCapture) summary() string {
	lines := strings.FieldsFunc(string(w.tail), func(r rune) bool { return r == '\n' || r == '\r' })
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < 3; i-- {
		if line := strings.TrimSpace(sanitize(lines[i])); line != "" {
			kept = append([]string{line}, kept...)
		}
	}
	return strings.Join(kept, "; ")
}

func (c *Client) commandTimeout() time.Duration {
	if c.Timeout <= 0 {
		return time.Minute
	}
	return c.Timeout
}

// subcommand names the Git command for messages, skipping leading -c options.
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		return args[i]
	}
	return "command"
}

type spec struct {
	args     []string
	limit    int
	timeout  time.Duration
	terminal bool      // may prompt on the terminal (auth, hooks, signing)
	stdin    io.Reader // optional
	stream   bool      // show sanitized stderr to the user as it arrives
	raw      bool      // attach stdout/stderr directly to Output (commit hooks)
}

func (c *Client) exec(parent context.Context, s spec) (string, error) {
	name := subcommand(s.args)
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", s.args...)
	cmd.Dir = c.Dir
	cmd.WaitDelay = time.Second
	cmd.Stdin = s.stdin
	out := &limitedBuffer{max: s.limit}
	diagnostics := &stderrCapture{}
	raw := s.raw && c.Output != nil
	if raw {
		cmd.Stdout, cmd.Stderr = c.Output, c.Output
	} else {
		cmd.Stdout, cmd.Stderr = out, diagnostics
		if s.stream {
			diagnostics.live = c.Output
		}
	}
	err := runCommand(ctx, cmd, s.terminal && c.Interactive)
	diagnostics.flush()
	if err != nil {
		if parent.Err() != nil {
			return "", parent.Err()
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s timed out after %s: %w", name, s.timeout, ctx.Err())
		}
		if raw || (s.stream && c.Output != nil) {
			return "", fmt.Errorf("git %s failed (see Git output above): %w", name, err)
		}
		if detail := diagnostics.summary(); detail != "" {
			return "", fmt.Errorf("git %s failed: %s (%w)", name, detail, err)
		}
		return "", fmt.Errorf("git %s failed: %w", name, err)
	}
	if out.exceeded {
		return "", fmt.Errorf("git %s output exceeds %d bytes: %w", name, s.limit, errOutputLimit)
	}
	return out.buffer.String(), nil
}

func (c *Client) run(ctx context.Context, limit int, args ...string) (string, error) {
	return c.exec(ctx, spec{args: args, limit: limit, timeout: c.commandTimeout()})
}

func (c *Client) diff(ctx context.Context, args ...string) (string, error) {
	limit := c.MaxDiffBytes
	if limit <= 0 {
		limit = 100000
	}
	out, err := c.run(ctx, limit, append([]string{"diff", "--no-ext-diff", "--no-textconv", "--no-color"}, args...)...)
	if err != nil {
		if errors.Is(err, errOutputLimit) {
			return "", diffTooLargeError{limit}
		}
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", errNoChanges
	}
	return out, nil
}

func (c *Client) StagedDiff(ctx context.Context) (string, error) {
	diff, err := c.diff(ctx, "--cached", "--")
	if errors.Is(err, errNoChanges) {
		return "", errors.New("no staged changes; stage files with 'git add' first")
	}
	return diff, err
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
	switch {
	case errors.Is(err, errNoChanges):
		return "", fmt.Errorf("no committed changes on this branch compared with origin/%s; commit your work first", base)
	case err != nil && !errors.As(err, new(diffTooLargeError)):
		return "", fmt.Errorf("compare with origin/%s (try 'git fetch origin'): %w", base, err)
	}
	return diff, err
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
	// ls-remote may need SSH or credential prompts, so it can use the terminal.
	out, err := c.exec(ctx, spec{args: []string{"ls-remote", "--heads", "origin", "refs/heads/" + branch}, limit: 8192, timeout: c.commandTimeout(), terminal: true})
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

// Upstream returns the branch's configured upstream (for example "origin/main",
// or "main" for a local upstream), or empty when none is configured. A
// configured upstream whose remote branch no longer exists is still reported.
func (c *Client) Upstream(ctx context.Context, branch string) (string, error) {
	if err := c.validateBranch(ctx, branch); err != nil {
		return "", err
	}
	ref := "refs/heads/" + branch
	// for-each-ref patterns are prefix matches (refs/heads/a also matches
	// refs/heads/a/b), so select the exact ref rather than trusting one line.
	out, err := c.run(ctx, 1<<20, "for-each-ref", "--format=%(refname)%00%(upstream:short)", ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if name, upstream, ok := strings.Cut(line, "\x00"); ok && name == ref {
			return upstream, nil
		}
	}
	return "", nil
}

// Push publishes HEAD to origin's same-named branch. setUpstream records that
// branch as the upstream; callers pass it only when none is configured and the
// user has seen that in the plan, so an existing upstream is never replaced.
func (c *Client) Push(ctx context.Context, branch string, setUpstream bool) error {
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
	args := []string{"-c", "remote.origin.mirror=false", "-c", "push.followTags=false", "push", "--no-force", "--recurse-submodules=no"}
	if c.Interactive {
		// Stderr is filtered through a sanitizing pipe, so Git would otherwise
		// assume no terminal and hide progress for a potentially long transfer.
		args = append(args, "--progress")
	}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, "origin", "HEAD:refs/heads/"+branch)
	_, err = c.exec(ctx, spec{args: args, limit: 8192, timeout: longCommandTimeout, terminal: true, stream: true})
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
	// Passing the message through stdin avoids option parsing and OS argv limits.
	// Hook output goes straight to the user so hooks can detect a terminal.
	_, err := c.exec(ctx, spec{args: []string{"commit", "--file=-"}, limit: 1 << 16, timeout: longCommandTimeout, terminal: true, stdin: strings.NewReader(message), raw: true})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("commit failed (check Git identity and hooks): %w", err)
	}
	return err
}
