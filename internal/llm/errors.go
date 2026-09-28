package llm

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/myusuf3/commit/internal/httpapi"
)

// explain turns an HTTP failure into guidance that names the provider and the
// likely fix. Only the provider's machine-readable error code is shown, never
// its message text, which can echo request content or part of the API key.
// Other errors, including cancellation, are returned unchanged.
func (c *Client) explain(err error) error {
	service := c.serviceName()
	var status *httpapi.StatusError
	var transport *httpapi.TransportError
	switch {
	case errors.As(err, &status):
		code := ""
		if status.Code != "" {
			code = ", " + status.Code
		}
		switch {
		case status.Status == 401:
			return fmt.Errorf("%s rejected the API key (HTTP 401%s); check %s", service, code, c.keyVariable())
		case status.Status == 403:
			return fmt.Errorf("%s denied access (HTTP 403%s); check the API key's permissions and that model %q is available to your account", service, code, c.Model)
		case status.Status == 404:
			return fmt.Errorf("%s could not find model %q or its endpoint (HTTP 404%s); check model, base_url, and api_format", service, c.Model, code)
		case status.Code == "context_length_exceeded" || status.Status == 413:
			return fmt.Errorf("the diff is too large for model %q at %s (HTTP %d%s); reduce the change or lower max_diff_bytes", c.Model, service, status.Status, code)
		case status.Code == "insufficient_quota":
			return fmt.Errorf("%s account has no remaining quota or credits (HTTP %d%s); check billing, since retrying will not help", service, status.Status, code)
		case status.Status == 429:
			return fmt.Errorf("%s rate limit reached (HTTP 429%s); wait a moment and try again", service, code)
		case status.Status == 400:
			return fmt.Errorf("%s rejected the request (HTTP 400%s); check model %q and api_format", service, code, c.Model)
		case status.Status >= 500:
			return fmt.Errorf("%s is unavailable (HTTP %d%s); try again later", service, status.Status, code)
		}
		return fmt.Errorf("%s returned HTTP %d%s", service, status.Status, code)
	case errors.As(err, &transport) && transport.Redirect:
		return fmt.Errorf("%s redirected the request; check base_url (redirects are refused so the API key is never forwarded)", service)
	case errors.As(err, &transport) && transport.Timeout:
		timeout := "the configured timeout"
		if c.HTTP != nil && c.HTTP.Timeout > 0 {
			timeout = c.HTTP.Timeout.String()
		}
		return fmt.Errorf("%s did not respond within %s; large diffs and reasoning models can be slow, so raise timeout in the config", service, timeout)
	case errors.As(err, &transport):
		return fmt.Errorf("could not reach %s; check base_url and connectivity", service)
	}
	return err
}

// serviceName identifies the provider and host, e.g. "OpenAI (api.openai.com)".
func (c *Client) serviceName() string {
	name := "AI provider"
	switch c.Provider {
	case "openai":
		name = "OpenAI-compatible provider"
	case "opencode-go":
		name = "OpenCode Go"
	}
	if u, err := url.Parse(c.BaseURL); err == nil && u.Host != "" {
		if u.Hostname() == "api.openai.com" {
			name = "OpenAI"
		}
		return fmt.Sprintf("%s (%s)", name, u.Host)
	}
	return name
}

func (c *Client) keyVariable() string {
	if c.Provider == "opencode-go" {
		return "OPENCODE_API_KEY (or COMMIT_API_KEY)"
	}
	return "OPENAI_API_KEY (or COMMIT_API_KEY)"
}
