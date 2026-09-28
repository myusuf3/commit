package github

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/myusuf3/commit/internal/httpapi"
)

// maxMessageRunes bounds GitHub's error text shown to the user.
const maxMessageRunes = 300

// Explain turns an HTTP failure from api.github.com into guidance that names
// GitHub and the likely fix. GitHub's own error text is shown (sanitized and
// bounded) because it comes from a fixed trusted host and is often the most
// useful part, for example "A pull request already exists for owner:branch".
// Other errors, including cancellation, are returned unchanged.
func Explain(err error, repository string, timeout time.Duration) error {
	var status *httpapi.StatusError
	var transport *httpapi.TransportError
	switch {
	case errors.As(err, &status):
		message := displayMessage(status.Message)
		detail := ""
		if message != "" {
			detail = ": " + message
		}
		switch {
		case status.Status == 401:
			return fmt.Errorf("GitHub rejected the token (HTTP 401%s); check GITHUB_TOKEN, COMMIT_GITHUB_TOKEN, or github_token", detail)
		case status.Status == 429 || (status.Status == 403 && strings.Contains(strings.ToLower(message), "rate limit")):
			return fmt.Errorf("GitHub rate limit reached (HTTP %d); wait a few minutes and try again", status.Status)
		case status.Status == 403:
			return fmt.Errorf("GitHub denied access to %s (HTTP 403%s); the token needs access to this repository's pull requests (classic token: repo scope; fine-grained: Pull requests read/write)", repository, detail)
		case status.Status == 404:
			return fmt.Errorf("GitHub could not find %s (HTTP 404); check origin, or the token cannot access this private repository", repository)
		case status.Status == 422:
			return fmt.Errorf("GitHub rejected the request (HTTP 422%s)", detail)
		case status.Status >= 500:
			return fmt.Errorf("GitHub is unavailable (HTTP %d); try again later", status.Status)
		}
		return fmt.Errorf("GitHub API returned HTTP %d%s", status.Status, detail)
	case errors.As(err, &transport) && transport.Redirect:
		return fmt.Errorf("GitHub redirected the request for %s; the repository was probably renamed or transferred. Update origin with 'git remote set-url origin <new-url>'", repository)
	case errors.As(err, &transport) && transport.Timeout:
		return fmt.Errorf("GitHub API did not respond within %s; check connectivity or raise timeout in the config", timeout)
	case errors.As(err, &transport):
		return errors.New("could not reach the GitHub API (api.github.com); check connectivity")
	}
	return err
}

func (c *Client) explain(err error) error {
	var timeout time.Duration
	if c.HTTP != nil {
		timeout = c.HTTP.Timeout
	}
	return Explain(err, c.Repository, timeout)
}

// displayMessage drops terminal controls and invisible formatting, collapses
// whitespace, and bounds the length.
func displayMessage(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(text, ""))
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > maxMessageRunes {
		text = string(runes[:maxMessageRunes]) + "…"
	}
	return text
}
