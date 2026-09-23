package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myusuf3/commit/internal/app"
	"github.com/myusuf3/commit/internal/config"
)

type fixtures struct {
	pushes, commits, creates, updates, calls int
	remoteHead                               string
	existing                                 *app.PullRequest
	generationErr                            error
}

func (f *fixtures) StagedDiff(context.Context) (string, error) { f.calls++; return "diff", nil }
func (f *fixtures) Commit(context.Context, string) error       { f.commits++; return nil }
func (f *fixtures) Head(context.Context) (string, error)       { return "head", nil }
func (f *fixtures) HeadRef(context.Context) (string, error)    { return "refs/heads/feature", nil }
func (f *fixtures) Branch(context.Context) (string, error)     { return "feature", nil }
func (f *fixtures) RemoteURL(context.Context) (string, error) {
	return "https://github.com/person/repo", nil
}
func (f *fixtures) BranchDiff(context.Context, string) (string, error) { return "diff", nil }
func (f *fixtures) RemoteHead(context.Context, string) (string, error) { return f.remoteHead, nil }
func (f *fixtures) Push(context.Context, string) error                 { f.pushes++; f.remoteHead = "head"; return nil }
func (f *fixtures) CommitMessage(context.Context, string) (string, error) {
	return "feat: change", f.generationErr
}
func (f *fixtures) PullRequest(context.Context, string) (app.PullRequest, error) {
	return app.PullRequest{Title: "Change", Body: "Summary"}, f.generationErr
}
func (f *fixtures) DefaultBranch(context.Context) (string, error) { return "main", nil }
func (f *fixtures) FindPullRequest(context.Context, string, string) (*app.PullRequest, error) {
	return f.existing, nil
}
func (f *fixtures) CreatePullRequest(context.Context, string, string, app.PullRequest, bool) (string, error) {
	f.creates++
	return "https://github.com/person/repo/pull/1", nil
}
func (f *fixtures) UpdatePullRequest(context.Context, int, app.PullRequest) (string, error) {
	f.updates++
	return "https://github.com/person/repo/pull/1", nil
}

func options(f *fixtures, in string, interactive bool, out, stderr io.Writer) Options {
	return Options{In: strings.NewReader(in), Out: out, Err: stderr, Interactive: interactive,
		LoadConfig: func(string) (config.Config, error) {
			c := config.Default()
			c.APIKey = "test"
			c.GitHubToken = "test"
			return c, nil
		},
		NewGit: func(config.Config) app.Git { return f }, NewGenerator: func(config.Config) app.Generator { return f },
		NewHosting:  func(config.Config, string) (app.Hosting, error) { return f, nil },
		OpenBrowser: func(context.Context, string) error { return nil },
	}
}

func TestCLICompatibility(t *testing.T) {
	root := NewRoot(Options{Out: io.Discard, Err: io.Discard})
	for name, flags := range map[string][]string{"commit": {"auto-accept"}, "pr": {"issue", "auto-accept", "draft"}, "init": {}, "version": {}, "update": {"check", "force"}} {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Fatalf("missing command %s", name)
		}
		for _, flag := range flags {
			if cmd.Flags().Lookup(flag) == nil {
				t.Fatalf("missing %s --%s", name, flag)
			}
		}
	}
}

func TestDryRunAndAutoAccept(t *testing.T) {
	for _, command := range []string{"commit", "pr"} {
		for _, flag := range []string{"--dry-run", "--auto-accept"} {
			t.Run(command+flag, func(t *testing.T) {
				f := &fixtures{}
				var out, stderr bytes.Buffer
				root := NewRoot(options(f, "", false, &out, &stderr))
				root.SetArgs([]string{command, flag})
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if out.Len() == 0 || strings.Contains(out.String(), "Generating") {
					t.Fatal("stdout is missing output or mixed with progress")
				}
				if flag == "--dry-run" && f.pushes+f.commits+f.creates+f.updates != 0 {
					t.Fatal("dry run mutated state")
				}
				if flag == "--auto-accept" && command == "pr" && (f.pushes != 1 || f.creates != 1) {
					t.Fatal("auto-accept did not create PR")
				}
				if flag == "--auto-accept" && command == "commit" && f.commits != 1 {
					t.Fatal("auto-accept did not commit")
				}
			})
		}
	}
}

func TestDeclineAndEOFNeverMutate(t *testing.T) {
	for _, tc := range []struct {
		command, input string
		wantErr        bool
	}{
		{"commit", "\n", false}, {"commit", "n\n", false}, {"commit", "", true}, {"commit", "yes", true},
		{"pr", "\nn\n", false}, {"pr", "\n", true}, {"pr", "", true},
	} {
		t.Run(tc.command+tc.input, func(t *testing.T) {
			f := &fixtures{}
			root := NewRoot(options(f, tc.input, true, io.Discard, io.Discard))
			root.SetArgs([]string{tc.command})
			if err := root.Execute(); (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if f.pushes+f.commits+f.creates+f.updates != 0 {
				t.Fatal("decline/EOF mutated state")
			}
		})
	}
}

func TestNonInteractiveAndInvalidIssuesFailBeforeGeneration(t *testing.T) {
	// Explicitly empty --issue is rejected as input validation. Omitted issues
	// and blank interactive answers still preserve recognized existing links.
	for _, args := range [][]string{
		{"commit"}, {"pr"}, {"pr", "-y", "--issue", "invalid"}, {"commit", "extra", "-y"},
		{"pr", "-y", "--issue", ""}, {"pr", "-y", "--issue", ","}, {"pr", "-y", "--issue", "   "},
	} {
		f := &fixtures{}
		root := NewRoot(options(f, "", false, io.Discard, io.Discard))
		root.SetArgs(args)
		if root.Execute() == nil {
			t.Fatalf("accepted %v", args)
		}
		if f.calls+f.pushes+f.commits+f.creates != 0 {
			t.Fatal("side effects before validation")
		}
	}
}

func TestBufferedPromptsAndExistingPR(t *testing.T) {
	f := &fixtures{existing: &app.PullRequest{Number: 7, Body: "Closes OPS-42"}, remoteHead: "head"}
	var out bytes.Buffer
	root := NewRoot(options(f, "\ny\n", true, &out, io.Discard))
	root.SetArgs([]string{"pr"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if f.updates != 1 || f.creates != 0 || !strings.Contains(out.String(), "Closes OPS-42") {
		t.Fatal("prompt buffering or existing links broken")
	}
}

func TestInitEOFAndVersionNeedNoConfig(t *testing.T) {
	isolateConfigEnv(t)
	path := filepath.Join(t.TempDir(), "config")
	root := NewRoot(Options{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard, Interactive: true})
	root.SetArgs([]string{"init", "--config", path})
	if root.Execute() == nil {
		t.Fatal("init accepted EOF")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("init wrote on EOF")
	}
	for _, args := range [][]string{{"version"}, {"--version"}, {"--help"}, {"completion", "bash"}} {
		root := NewRoot(Options{Out: io.Discard, Err: io.Discard, LoadConfig: func(string) (config.Config, error) {
			t.Error("unnecessary config load")
			return config.Config{}, errors.New("missing")
		}})
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerationFailureNeverPushes(t *testing.T) {
	f := &fixtures{generationErr: errors.New("network failure")}
	root := NewRoot(options(f, "", false, io.Discard, io.Discard))
	root.SetArgs([]string{"pr", "-y"})
	if root.Execute() == nil {
		t.Fatal("ignored error")
	}
	if f.pushes+f.creates != 0 {
		t.Fatal("pushed before generating successfully")
	}
}
