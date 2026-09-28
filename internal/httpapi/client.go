// Package httpapi implements bounded, cancellable JSON requests shared by adapters.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const MaxResponseBytes = 2 << 20

// maxErrorBodyBytes bounds how much of an error response is inspected.
const maxErrorBodyBytes = 64 << 10

// ErrRedirect is returned when an API responds with a redirect. Redirects are
// refused so credentials are never forwarded to another location.
var ErrRedirect = errors.New("API redirects are not allowed")

func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return ErrRedirect
	}}
}

// StatusError is a non-2xx response. Adapters turn it into service-specific
// guidance. Code is a machine-readable identifier from the body (for example
// "insufficient_quota" or "model_not_found"), accepted only in a strict safe
// format. Message is the body's human-readable text; it may echo request data,
// so only adapters for trusted fixed hosts (GitHub) should display it.
type StatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("HTTP %d (%s)", e.Status, e.Code)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// TransportError means no HTTP response was received. The underlying error is
// deliberately not exposed because it can contain the request URL.
type TransportError struct {
	Timeout  bool
	Redirect bool
}

func (e *TransportError) Error() string {
	switch {
	case e.Timeout:
		return "request timed out"
	case e.Redirect:
		return "API redirects are not allowed"
	}
	return "request failed; check connectivity"
}

func (e *TransportError) Unwrap() error {
	if e.Redirect {
		return ErrRedirect
	}
	return nil
}

var safeCode = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,63}$`)

// parseErrorBody understands the OpenAI ({"error":{"code","type","message"}}),
// Anthropic ({"type":"error","error":{"type","message"}}), and GitHub
// ({"message","errors":[{"message"}]}) error shapes.
func parseErrorBody(data []byte) (code, message string) {
	var body struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
		Errors  []struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &body) != nil {
		return "", ""
	}
	var nested struct {
		Code    json.RawMessage `json:"code"`
		Type    string          `json:"type"`
		Message string          `json:"message"`
	}
	if len(body.Error) > 0 && json.Unmarshal(body.Error, &nested) == nil {
		var codeText string
		_ = json.Unmarshal(nested.Code, &codeText) // codes may be numbers or null
		for _, candidate := range []string{codeText, nested.Type} {
			if safeCode.MatchString(candidate) {
				code = candidate
				break
			}
		}
		message = nested.Message
	}
	if body.Message != "" {
		message = body.Message
	}
	var details []string
	for _, item := range body.Errors {
		if item.Message != "" {
			details = append(details, item.Message)
		}
	}
	if len(details) > 0 {
		message = strings.TrimSpace(message + ": " + strings.Join(details, "; "))
	}
	return code, message
}

func JSON(ctx context.Context, client *http.Client, method, endpoint, token string, input, output any, headers ...http.Header) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("invalid API endpoint")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "commit-cli/1.0")
	for _, header := range headers {
		for key, values := range header {
			req.Header[key] = append([]string(nil), values...)
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Avoid reflecting URLs or provider-controlled content containing secrets.
		var netErr net.Error
		return &TransportError{Timeout: errors.As(err, &netErr) && netErr.Timeout(), Redirect: errors.Is(err, ErrRedirect)}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		code, message := parseErrorBody(data)
		return &StatusError{Status: resp.StatusCode, Code: code, Message: message}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read API response: %w", err)
	}
	if len(data) > MaxResponseBytes {
		return errors.New("API response exceeds size limit")
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(data, output); err != nil {
		return errors.New("API returned invalid JSON")
	}
	return nil
}
