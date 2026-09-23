package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeGit struct {
	diff, head, branch, remoteHead, remote string
	pushes, commits                        int
	err                                    error
}

func newGit() *fakeGit {
	return &fakeGit{diff: "diff", head: "head", branch: "feature", remote: "remote"}
}
func (g *fakeGit) StagedDiff(context.Context) (string, error) { return g.diff, g.err }
func (g *fakeGit) Commit(context.Context, string) error       { g.commits++; return g.err }
func (g *fakeGit) Head(context.Context) (string, error)       { return g.head, g.err }
func (g *fakeGit) HeadRef(context.Context) (string, error) {
	if g.branch == "" {
		return "", g.err
	}
	return "refs/heads/" + g.branch, g.err
}
func (g *fakeGit) Branch(context.Context) (string, error)             { return g.branch, g.err }
func (g *fakeGit) RemoteURL(context.Context) (string, error)          { return g.remote, g.err }
func (g *fakeGit) BranchDiff(context.Context, string) (string, error) { return g.diff, g.err }
func (g *fakeGit) RemoteHead(context.Context, string) (string, error) { return g.remoteHead, g.err }
func (g *fakeGit) Push(context.Context, string) error {
	g.pushes++
	g.remoteHead = g.head
	return g.err
}

type fakeGenerator struct{ err error }

func (g fakeGenerator) CommitMessage(context.Context, string) (string, error) {
	return "fix: change", g.err
}
func (g fakeGenerator) PullRequest(context.Context, string) (PullRequest, error) {
	return PullRequest{Title: "Change", Body: "A change"}, g.err
}

type fakeHosting struct {
	existing         *PullRequest
	creates, updates int
	draft            bool
	err              error
}

func (h *fakeHosting) DefaultBranch(context.Context) (string, error) { return "main", h.err }
func (h *fakeHosting) FindPullRequest(context.Context, string, string) (*PullRequest, error) {
	return h.existing, h.err
}
func (h *fakeHosting) CreatePullRequest(_ context.Context, _, _ string, _ PullRequest, draft bool) (string, error) {
	h.creates++
	h.draft = draft
	return "url", h.err
}
func (h *fakeHosting) UpdatePullRequest(context.Context, int, PullRequest) (string, error) {
	h.updates++
	return "url", h.err
}

func TestCommitRechecksStagedChangesHeadAndBranch(t *testing.T) {
	for _, change := range []string{"none", "diff", "head", "branch", "detach"} {
		t.Run(change, func(t *testing.T) {
			g := newGit()
			s := Service{Git: g, Generator: fakeGenerator{}}
			p, err := s.PrepareCommit(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if change == "diff" {
				g.diff = "other"
			}
			if change == "head" {
				g.head = "other"
			}
			if change == "branch" {
				g.branch = "other"
			}
			if change == "detach" {
				g.branch = ""
			}
			err = s.Commit(context.Background(), p)
			if change == "none" && (err != nil || g.commits != 1) {
				t.Fatalf("commit failed: %v", err)
			}
			if change != "none" && (err == nil || g.commits != 0) {
				t.Fatal("committed stale plan")
			}
		})
	}
}

func TestPRPrepareIsReadOnlyAndApplyCreatesDraft(t *testing.T) {
	g := newGit()
	h := &fakeHosting{}
	s := Service{Git: g, Generator: fakeGenerator{}}
	p, err := s.PreparePR(context.Background(), h, []string{"123", "TEAM-7"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if g.pushes != 0 || h.creates != 0 || !p.NeedsPush {
		t.Fatal("prepare mutated state or missed push")
	}
	if !strings.Contains(p.PR.Body, "Closes TEAM-7") || !strings.Contains(p.PR.Title, "[123,TEAM-7]") {
		t.Fatalf("missing issues: %+v", p.PR)
	}
	if _, err := s.ApplyPR(context.Background(), h, p); err != nil {
		t.Fatal(err)
	}
	if g.pushes != 1 || h.creates != 1 || !h.draft {
		t.Fatal("apply missed push or draft creation")
	}
}

func TestPRUpdatePreservesLinksAndDoesNotRepush(t *testing.T) {
	g := newGit()
	g.remoteHead = g.head
	h := &fakeHosting{existing: &PullRequest{Number: 7, Body: "Fixes #12\nCloses OPS-42"}}
	s := Service{Git: g, Generator: fakeGenerator{}}
	p, err := s.PreparePR(context.Background(), h, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.PR.Number != 7 || !strings.Contains(p.PR.Body, "Closes OPS-42") {
		t.Fatalf("lost existing metadata: %+v", p)
	}
	if _, err := s.ApplyPR(context.Background(), h, p); err != nil {
		t.Fatal(err)
	}
	if g.pushes != 0 || h.updates != 1 || h.creates != 0 {
		t.Fatal("unexpected effects")
	}
}

func TestBlankParsedIssuesPreserveExistingLinks(t *testing.T) {
	for _, input := range []string{"", ",", "   "} {
		issues, err := ParseIssues([]string{input})
		if err != nil || issues != nil {
			t.Fatalf("blank input: issues=%v error=%v", issues, err)
		}
		s := Service{Git: newGit(), Generator: fakeGenerator{}}
		h := &fakeHosting{existing: &PullRequest{Number: 7, Body: "Closes OPS-42"}}
		p, err := s.PreparePR(context.Background(), h, issues, false)
		if err != nil || !strings.Contains(p.PR.Body, "Closes OPS-42") {
			t.Fatalf("links not preserved: %+v error=%v", p.PR, err)
		}
	}
}

func TestPRRejectsStalePlanAndDefaultBranch(t *testing.T) {
	for _, field := range []string{"head", "branch", "remote"} {
		t.Run(field, func(t *testing.T) {
			g := newGit()
			h := &fakeHosting{}
			s := Service{Git: g, Generator: fakeGenerator{}}
			p, err := s.PreparePR(context.Background(), h, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "head":
				g.head = "new"
			case "branch":
				g.branch = "new"
			case "remote":
				g.remote = "new"
			}
			if _, err := s.ApplyPR(context.Background(), h, p); err == nil {
				t.Fatal("accepted stale plan")
			}
			if g.pushes+h.creates+h.updates != 0 {
				t.Fatal("mutated stale plan")
			}
		})
	}
	g := newGit()
	g.branch = "main"
	if _, err := (Service{Git: g, Generator: fakeGenerator{}}).PreparePR(context.Background(), &fakeHosting{}, nil, false); err == nil {
		t.Fatal("PR from default branch")
	}
}

func TestGenerationFailureHasNoSideEffects(t *testing.T) {
	g := newGit()
	h := &fakeHosting{}
	s := Service{Git: g, Generator: fakeGenerator{err: errors.New("failed")}}
	if _, err := s.PreparePR(context.Background(), h, nil, false); err == nil {
		t.Fatal("ignored generation error")
	}
	if g.pushes+h.creates+h.updates != 0 {
		t.Fatal("side effects on failure")
	}
}

func TestIssues(t *testing.T) {
	got, err := ParseIssues([]string{" #12, team-34 ", "12", "OPS2-5"})
	if err != nil || !reflect.DeepEqual(got, []string{"12", "TEAM-34", "OPS2-5"}) {
		t.Fatalf("issues=%v err=%v", got, err)
	}
	for _, value := range []string{"0", "-1", "TEAM-0", "ABC", "12;bad", "TEAM-2\ncommand"} {
		if _, err := ParseIssues([]string{value}); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	body := "Fixes #12\nresolves team-34\nlinear: OPS2-5"
	if got := ExtractIssues(body); !reflect.DeepEqual(got, []string{"12", "TEAM-34", "OPS2-5"}) {
		t.Fatalf("extracted %v", got)
	}
	if linked := LinkIssues(body, []string{"12", "TEAM-34", "OPS2-5"}); linked != body {
		t.Fatal("duplicated closing lines")
	}
}
