package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/myusuf3/commit/internal/httpapi"
)

func apiError(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }

func (c *Client) request(ctx context.Context, path, token string, input, output any, headers http.Header) error {
	return httpapi.JSON(ctx, c.HTTP, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, token, input, output, headers)
}

func (c *Client) chat(ctx context.Context, system, diff string, headers http.Header) (string, error) {
	request := struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}{c.Model, []message{{"system", system}, {"user", diff}}}
	var response struct {
		Choices []struct {
			Message      message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Error json.RawMessage `json:"error"`
	}
	if err := c.request(ctx, "/chat/completions", c.APIKey, request, &response, headers); err != nil {
		return "", err
	}
	if apiError(response.Error) {
		return "", errors.New("provider returned an API error")
	}
	if len(response.Choices) == 0 {
		return "", errors.New("provider returned no choices")
	}
	choice := response.Choices[0]
	if choice.FinishReason != "stop" {
		return "", errors.New("provider returned incomplete or filtered output")
	}
	return choice.Message.Content, nil
}

func (c *Client) messages(ctx context.Context, system, diff string, headers http.Header) (string, error) {
	request := struct {
		Model     string    `json:"model"`
		System    string    `json:"system"`
		Messages  []message `json:"messages"`
		MaxTokens int       `json:"max_tokens"`
	}{c.Model, system, []message{{"user", diff}}, 8192}
	headers.Set("x-api-key", c.APIKey)
	headers.Set("anthropic-version", "2023-06-01")
	var response struct {
		Type       string `json:"type"`
		Role       string `json:"role"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error json.RawMessage `json:"error"`
	}
	if err := c.request(ctx, "/messages", "", request, &response, headers); err != nil {
		return "", err
	}
	if apiError(response.Error) {
		return "", errors.New("provider returned an API error")
	}
	if response.Type != "message" || response.Role != "assistant" || response.StopReason != "end_turn" {
		return "", errors.New("provider returned incomplete, filtered, or non-text output")
	}
	var text strings.Builder
	for _, block := range response.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking", "redacted_thinking": // Reasoning is not user-facing metadata.
		default:
			return "", errors.New("provider returned unexpected non-text content")
		}
	}
	return text.String(), nil
}

func (c *Client) responses(ctx context.Context, system, diff string, headers http.Header) (string, error) {
	request := struct {
		Model           string    `json:"model"`
		Instructions    string    `json:"instructions"`
		Input           []message `json:"input"`
		Store           bool      `json:"store"`
		MaxOutputTokens int       `json:"max_output_tokens"`
	}{c.Model, system, []message{{"user", diff}}, false, 8192}
	var response struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Status  string `json:"status"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Error json.RawMessage `json:"error"`
	}
	if err := c.request(ctx, "/responses", c.APIKey, request, &response, headers); err != nil {
		return "", err
	}
	if apiError(response.Error) {
		return "", errors.New("provider returned an API error")
	}
	if response.Status != "completed" {
		return "", errors.New("provider returned an incomplete or failed response")
	}
	var text strings.Builder
	for _, item := range response.Output {
		if item.Type == "reasoning" {
			continue
		}
		if item.Type != "message" || item.Role != "assistant" || item.Status != "completed" {
			return "", errors.New("provider returned incomplete or non-text output")
		}
		for _, block := range item.Content {
			if block.Type != "output_text" {
				return "", errors.New("provider refused or returned non-text content")
			}
			text.WriteString(block.Text)
		}
	}
	return text.String(), nil
}
