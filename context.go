package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// These are fresh counts of retained messages, not the previous generation's
// KV cache (which also contains discarded reasoning). Each mode can render a
// different chat template. Unknown counts must never disable a mode.
type contextUsage struct {
	Capacity   int  `json:"capacity"`
	Quick      *int `json:"quick,omitempty"`
	Thoughtful *int `json:"thoughtful,omitempty"`
}

func conversationMessages(c *Conversation) []map[string]string {
	messages := []map[string]string{{"role": "system", "content": systemPrompt}}
	for _, m := range c.Messages {
		if m.Role == "user" || (m.Role == "assistant" && m.Status == "complete") {
			messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
		}
	}
	return messages
}

func (a *App) inferencePayload(c *Conversation, messages []map[string]string, mode string) map[string]any {
	payload := map[string]any{"model": c.Model, "messages": messages, "stream": true, "reasoning_format": "deepseek", "max_tokens": a.cfg.MaxOutput, "return_progress": true, "timings_per_token": true}
	settings := a.cfg.Quick
	if mode == "thoughtful" {
		settings = a.cfg.Thoughtful
	}
	for k, v := range settings {
		payload[k] = v
	}
	return payload
}

// Keep auxiliary requests on the configured model endpoint, including a reverse
// proxy's base path. Do not assume an arbitrary OpenAI-compatible endpoint also
// exposes llama.cpp metadata.
func (a *App) contextURL(resource, model string) (string, error) {
	u, err := url.Parse(a.cfg.Endpoint)
	if err != nil || u.Host == "" || !strings.HasSuffix(u.Path, "/chat/completions") {
		return "", errors.New("context metadata endpoint unavailable")
	}
	if resource == "props" {
		u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/chat/completions"), "/v1") + "/props"
	} else {
		u.Path += "/input_tokens"
	}
	u.RawPath = ""
	query := u.Query()
	query.Set("model", model)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (a *App) contextJSON(ctx context.Context, resource, model string, body any, result any) error {
	endpoint, err := a.contextURL(resource, model)
	if err != nil {
		return err
	}
	method := http.MethodGet
	var data []byte
	if body != nil {
		method = http.MethodPost
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return &upstreamStatusError{status: response.StatusCode}
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(result)
}

func (a *App) contextCapacity(ctx context.Context, model string) int {
	var props struct {
		Settings struct {
			Capacity int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if a.contextJSON(ctx, "props", model, nil, &props) != nil || props.Settings.Capacity <= 0 {
		return 0
	}
	return props.Settings.Capacity
}

func (a *App) countInput(ctx context.Context, c *Conversation, messages []map[string]string, mode string) *int {
	var count struct {
		Tokens *int `json:"input_tokens"`
	}
	if a.contextJSON(ctx, "input_tokens", c.Model, a.inferencePayload(c, messages, mode), &count) != nil || count.Tokens == nil || *count.Tokens <= 0 {
		return nil
	}
	return count.Tokens
}

func (a *App) conversationContext(ctx context.Context, c *Conversation) *contextUsage {
	if c.Model != a.cfg.Model || len(c.Messages) == 0 {
		return nil
	}
	last := c.Messages[len(c.Messages)-1]
	if last.Role != "assistant" || last.Status != "complete" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	capacity := a.contextCapacity(ctx, c.Model)
	if capacity == 0 {
		return nil
	}
	// Include the next turn's framing, but no draft text. An empty user turn
	// also prevents the template from treating the last answer as a prefill.
	messages := append(conversationMessages(c), map[string]string{"role": "user", "content": ""})
	usage := &contextUsage{Capacity: capacity}
	usage.Quick = a.countInput(ctx, c, messages, "quick")
	usage.Thoughtful = a.countInput(ctx, c, messages, "thoughtful")
	return usage
}

func (a *App) promptFits(ctx context.Context, c *Conversation, question string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	capacity := a.contextCapacity(ctx, c.Model)
	if capacity == 0 {
		return true
	}
	messages := append(conversationMessages(c), map[string]string{"role": "user", "content": question})
	count := a.countInput(ctx, c, messages, c.Thinking)
	// Require space for at least one output token. The configured maximum is
	// an upper bound, not a compulsory reservation for every answer.
	return count == nil || *count < capacity
}
