package git

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/myusuf3/commit/internal/app"
)

type planGenerator struct{}

func (planGenerator) CommitMessage(context.Context, string) (string, error) {
	return "test: staged change", nil
}
func (planGenerator) PullRequest(context.Context, string) (app.PullRequest, error) {
	return app.PullRequest{}, nil
}

func TestCommitPlanChecksSymbolicHEAD(t *testing.T) {
	for _, tc := range []struct {
		name             string
		unborn, detached bool
		change           string
	}{
		{name: "unchanged branch"},
		{name: "unchanged unborn branch", unborn: true},
		{name: "unchanged detached HEAD", detached: true},
		{name: "switch same hash", change: "switch"},
		{name: "rename branch", change: "rename"},
		{name: "detach same hash", change: "detach"},
		{name: "attach same hash", detached: true, change: "attach"},
		{name: "switch unborn branch", unborn: true, change: "unborn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := repository(t)
			if !tc.unborn {
				command(t, dir, "commit", "--allow-empty", "-m", "base")
			}
			if tc.detached {
				command(t, dir, "checkout", "--detach")
			}
			stage(t, dir, "staged change\n")
			client := &Client{Dir: dir, Output: io.Discard}
			service := app.Service{Git: client, Generator: planGenerator{}}
			ctx := context.Background()
			before, err := client.Head(ctx)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := service.PrepareCommit(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.change {
			case "switch":
				command(t, dir, "checkout", "-b", "other")
			case "rename":
				command(t, dir, "branch", "-m", "renamed")
			case "detach":
				command(t, dir, "checkout", "--detach")
			case "attach":
				command(t, dir, "checkout", "main")
			case "unborn":
				command(t, dir, "symbolic-ref", "HEAD", "refs/heads/other")
			}
			err = service.Commit(ctx, plan)
			if tc.change == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got := command(t, dir, "log", "-1", "--format=%s"); got != plan.Message {
					t.Fatalf("committed %q", got)
				}
				return
			}
			if err == nil {
				t.Fatal("committed on a different branch or attachment state")
			}
			after, headErr := client.Head(ctx)
			if headErr != nil || before != after {
				t.Fatalf("HEAD changed: before=%q after=%q error=%v", before, after, headErr)
			}
			if _, err := client.StagedDiff(ctx); err != nil {
				t.Fatal("rejected plan consumed the staged changes")
			}
		})
	}
}

func TestHeadRefDoesNotSwallowErrors(t *testing.T) {
	client := &Client{Dir: t.TempDir()}
	if _, err := client.HeadRef(context.Background()); err == nil {
		t.Fatal("non-repository treated as detached HEAD")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.HeadRef(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
