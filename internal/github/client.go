// Package github contains the GitHub REST adapter and strict remote parsing.
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/myusuf3/commit/internal/app"
	"github.com/myusuf3/commit/internal/httpapi"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func ValidateRepository(repository string) error {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 {
		return errors.New("repository must be owner/name")
	}
	for _, part := range parts {
		if !namePattern.MatchString(part) || part == "." || part == ".." {
			return errors.New("invalid repository name")
		}
	}
	return nil
}

func ParseRemote(remote string) (string, error) {
	var path string
	if strings.HasPrefix(remote, "git@github.com:") {
		path = strings.TrimPrefix(remote, "git@github.com:")
	} else {
		u, err := url.Parse(remote)
		if err != nil || !strings.EqualFold(u.Host, "github.com") || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
			return "", errors.New("origin must be a github.com HTTPS or SSH remote")
		}
		if u.Scheme != "https" && u.Scheme != "ssh" {
			return "", errors.New("unsupported GitHub remote scheme")
		}
		// Credentials embedded in the remote URL (common in CI checkouts such as
		// x-access-token URLs) are supported. Only the owner/name path is returned
		// to the API adapter; raw URLs are not logged. Git handles its own
		// authentication for pushes.
		if u.Scheme == "ssh" && u.User != nil && u.User.String() != "git" {
			return "", errors.New("GitHub SSH user must be git")
		}
		path = strings.TrimPrefix(u.Path, "/")
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	if err := ValidateRepository(path); err != nil {
		return "", err
	}
	return path, nil
}

type Client struct {
	HTTP       *http.Client
	Token      string
	Repository string
	BaseURL    string
}

func (c *Client) endpoint(path string) string {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	return strings.TrimRight(base, "/") + "/repos/" + c.Repository + path
}

func (c *Client) DefaultBranch(ctx context.Context) (string, error) {
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := httpapi.JSON(ctx, c.HTTP, "GET", c.endpoint(""), c.Token, nil, &repo); err != nil {
		return "", err
	}
	if repo.DefaultBranch == "" {
		return "", errors.New("GitHub returned no default branch")
	}
	return repo.DefaultBranch, nil
}

func (c *Client) FindPullRequest(ctx context.Context, head, base string) (*app.PullRequest, error) {
	owner := strings.Split(c.Repository, "/")[0]
	query := url.Values{"state": {"open"}, "head": {owner + ":" + head}, "base": {base}, "per_page": {"2"}}
	var prs []app.PullRequest
	if err := httpapi.JSON(ctx, c.HTTP, "GET", c.endpoint("/pulls?")+query.Encode(), c.Token, nil, &prs); err != nil {
		return nil, err
	}
	if len(prs) == 0 {
		return nil, nil
	}
	if len(prs) > 1 {
		return nil, errors.New("multiple matching pull requests; update the intended PR manually")
	}
	if prs[0].Number <= 0 || !validPRURL(prs[0].URL, c.Repository) {
		return nil, errors.New("GitHub returned invalid pull request metadata")
	}
	return &prs[0], nil
}

func validPRURL(raw, repository string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(u.Path, "/"+repository+"/pull/")
}

func (c *Client) save(ctx context.Context, method, path string, payload any) (string, error) {
	var pr app.PullRequest
	if err := httpapi.JSON(ctx, c.HTTP, method, c.endpoint(path), c.Token, payload, &pr); err != nil {
		return "", err
	}
	if !validPRURL(pr.URL, c.Repository) {
		return "", errors.New("GitHub may have saved the PR but returned no valid URL; check the repository before retrying")
	}
	return pr.URL, nil
}

func (c *Client) CreatePullRequest(ctx context.Context, head, base string, pr app.PullRequest, draft bool) (string, error) {
	return c.save(ctx, "POST", "/pulls", struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Draft bool   `json:"draft"`
	}{pr.Title, pr.Body, head, base, draft})
}

func (c *Client) UpdatePullRequest(ctx context.Context, number int, pr app.PullRequest) (string, error) {
	return c.save(ctx, "PATCH", fmt.Sprintf("/pulls/%d", number), struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{pr.Title, pr.Body})
}
