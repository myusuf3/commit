// Package httpapi implements bounded, cancellable JSON requests shared by adapters.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const MaxResponseBytes = 2 << 20

func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("API redirects are not allowed")
	}}
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
		return errors.New("API request failed; check connectivity and timeout")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		hint := ""
		switch resp.StatusCode {
		case 401, 403:
			hint = "; check credentials and permissions"
		case 429:
			hint = "; rate limited, try again later"
		case 422:
			hint = "; check repository, branch, and request values"
		}
		return fmt.Errorf("API returned HTTP %d%s", resp.StatusCode, hint)
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
