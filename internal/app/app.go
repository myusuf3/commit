// Package app owns workflows and the small interfaces they consume. It has no
// knowledge of Cobra, environment variables, subprocesses, or HTTP.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"html_url"`
}

type Git interface {
	StagedDiff(context.Context) (string, error)
	Commit(context.Context, string) error
	Branch(context.Context) (string, error)
	Head(context.Context) (string, error)
	// HeadRef returns the full symbolic HEAD target, or empty for detached HEAD.
	HeadRef(context.Context) (string, error)
	RemoteURL(context.Context) (string, error)
	BranchDiff(context.Context, string) (string, error)
	RemoteHead(context.Context, string) (string, error)
	Push(context.Context, string) error
}

type Generator interface {
	CommitMessage(context.Context, string) (string, error)
	PullRequest(context.Context, string) (PullRequest, error)
}

type Hosting interface {
	DefaultBranch(context.Context) (string, error)
	FindPullRequest(context.Context, string, string) (*PullRequest, error)
	CreatePullRequest(context.Context, string, string, PullRequest, bool) (string, error)
	UpdatePullRequest(context.Context, int, PullRequest) (string, error)
}

type Service struct {
	Git       Git
	Generator Generator
}

type CommitPlan struct {
	Message string
	diff    string
	head    string
	headRef string
}

// PrepareCommit inspects Git state and generates a message in one step.
func (s Service) PrepareCommit(ctx context.Context) (CommitPlan, error) {
	p, err := s.InspectCommit(ctx)
	if err != nil {
		return p, err
	}
	return s.GenerateCommit(ctx, p)
}

// InspectCommit records branch, HEAD, and staged diff without contacting the
// provider, so local problems (no repository, nothing staged, oversized diff)
// surface before anything is sent.
func (s Service) InspectCommit(ctx context.Context) (CommitPlan, error) {
	var p CommitPlan
	var err error
	p.headRef, err = s.Git.HeadRef(ctx)
	if err != nil {
		return p, err
	}
	p.head, err = s.Git.Head(ctx)
	if err != nil {
		return p, err
	}
	p.diff, err = s.Git.StagedDiff(ctx)
	return p, err
}

// GenerateCommit sends the inspected staged diff to the provider.
func (s Service) GenerateCommit(ctx context.Context, p CommitPlan) (CommitPlan, error) {
	var err error
	p.Message, err = s.Generator.CommitMessage(ctx, p.diff)
	return p, err
}

func (s Service) Commit(ctx context.Context, p CommitPlan) error {
	head, err := s.Git.Head(ctx)
	if err != nil {
		return err
	}
	diff, err := s.Git.StagedDiff(ctx)
	if err != nil {
		return err
	}
	headRef, err := s.Git.HeadRef(ctx)
	if err != nil {
		return err
	}
	if headRef != p.headRef || head != p.head || diff != p.diff {
		return errors.New("branch, HEAD, or staged changes changed during generation; run the command again")
	}
	return s.Git.Commit(ctx, p.Message)
}

type PRPlan struct {
	PR        PullRequest
	Branch    string
	Base      string
	NeedsPush bool
	Draft     bool
	// Existing is the open pull request that will be updated, if any.
	Existing *PullRequest
	head     string
	remote   string
	diff     string
}

// ExistingIssues returns the recognized closing links of the PR being updated,
// which are kept when no issues are supplied.
func (p PRPlan) ExistingIssues() []string {
	if p.Existing == nil {
		return nil
	}
	return ExtractIssues(p.Existing.Body)
}

// PreparePR inspects Git and GitHub state and generates the PR in one step.
func (s Service) PreparePR(ctx context.Context, host Hosting, issues []string, draft bool) (PRPlan, error) {
	p, err := s.InspectPR(ctx, host)
	if err != nil {
		return p, err
	}
	return s.GeneratePR(ctx, p, issues, draft)
}

// InspectPR performs every read-only Git and GitHub check without contacting
// the provider, so problems surface before any prompt or generation.
func (s Service) InspectPR(ctx context.Context, host Hosting) (PRPlan, error) {
	var p PRPlan
	var err error
	p.Branch, err = s.Git.Branch(ctx)
	if err != nil {
		return p, err
	}
	p.head, err = s.Git.Head(ctx)
	if err != nil {
		return p, err
	}
	p.remote, err = s.Git.RemoteURL(ctx)
	if err != nil {
		return p, err
	}
	p.Base, err = host.DefaultBranch(ctx)
	if err != nil {
		return p, err
	}
	if p.Branch == p.Base {
		return p, errors.New("switch to a feature branch before creating a pull request")
	}
	p.diff, err = s.Git.BranchDiff(ctx, p.Base)
	if err != nil {
		return p, err
	}
	p.Existing, err = host.FindPullRequest(ctx, p.Branch, p.Base)
	if err != nil {
		return p, err
	}
	remoteHead, err := s.Git.RemoteHead(ctx, p.Branch)
	if err != nil {
		return p, err
	}
	p.NeedsPush = remoteHead != p.head
	return p, nil
}

// GeneratePR sends the inspected branch diff to the provider and links issues.
// Nil issues keep an existing PR's recognized links; a non-nil set replaces them.
func (s Service) GeneratePR(ctx context.Context, p PRPlan, issues []string, draft bool) (PRPlan, error) {
	p.Draft = draft
	if issues == nil {
		issues = p.ExistingIssues()
	}
	var err error
	p.PR, err = s.Generator.PullRequest(ctx, p.diff)
	if err != nil {
		return p, err
	}
	if len(issues) > 0 {
		p.PR.Title += " [" + strings.Join(issues, ",") + "]"
		p.PR.Body = LinkIssues(p.PR.Body, issues)
	}
	if utf8.RuneCountInString(p.PR.Title) > 256 || len(p.PR.Body) > 65536 {
		return p, errors.New("generated pull request including issue links exceeds GitHub limits; use fewer issues or a shorter description")
	}
	if p.Existing != nil {
		p.PR.Number = p.Existing.Number
		p.PR.URL = p.Existing.URL
	}
	return p, nil
}

// ApplyPR runs only after the caller has displayed and approved the full plan.
func (s Service) ApplyPR(ctx context.Context, host Hosting, p PRPlan) (string, error) {
	branch, err := s.Git.Branch(ctx)
	if err != nil {
		return "", err
	}
	head, err := s.Git.Head(ctx)
	if err != nil {
		return "", err
	}
	remote, err := s.Git.RemoteURL(ctx)
	if err != nil {
		return "", err
	}
	if branch != p.Branch || head != p.head || remote != p.remote {
		return "", errors.New("branch, HEAD, or origin changed; run the command again")
	}
	if p.NeedsPush {
		if err := s.Git.Push(ctx, p.Branch); err != nil {
			return "", err
		}
	}
	remoteHead, err := s.Git.RemoteHead(ctx, p.Branch)
	if err != nil {
		return "", err
	}
	if remoteHead != p.head {
		return "", errors.New("remote branch does not match the reviewed HEAD; run the command again")
	}
	var result string
	if p.PR.Number > 0 {
		result, err = host.UpdatePullRequest(ctx, p.PR.Number, p.PR)
	} else {
		result, err = host.CreatePullRequest(ctx, p.Branch, p.Base, p.PR, p.Draft)
	}
	if err != nil && p.NeedsPush {
		return "", fmt.Errorf("branch was pushed, but the pull request operation failed: %w", err)
	}
	return result, err
}
