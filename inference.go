package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func event(w http.ResponseWriter, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	return rc.Flush()
}

// SSE data can span lines and transport chunks. Never equate reads with events.
func consumeSSE(r io.Reader, consume func(string) error) error {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 4096), 1<<20)
	var lines []string
	for scan.Scan() {
		line := strings.TrimSuffix(scan.Text(), "\r")
		if line == "" {
			if len(lines) > 0 {
				if err := consume(strings.Join(lines, "\n")); err != nil {
					return err
				}
				lines = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if len(lines) > 0 {
		return errors.New("incomplete SSE frame")
	}
	return nil
}

var errDone = errors.New("stream complete")

type upstreamStatusError struct{ status int }

func (e *upstreamStatusError) Error() string { return fmt.Sprintf("inference status %d", e.status) }

func (a *App) infer(ctx context.Context, c *Conversation, delta func(string) error) error {
	messages := []map[string]string{{"role": "system", "content": "Respond in German."}}
	for _, m := range c.Messages {
		if m.Role == "user" || (m.Role == "assistant" && m.Status == "complete") {
			messages = append(messages, map[string]string{"role": m.Role, "content": m.Content})
		}
	}
	payload := map[string]any{"model": c.Model, "messages": messages, "stream": true, "reasoning_format": "deepseek", "max_tokens": a.cfg.MaxOutput}
	settings := a.cfg.Quick
	if c.Thinking == "thoughtful" {
		settings = a.cfg.Thoughtful
	}
	for k, v := range settings {
		payload[k] = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.cfg.Endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &upstreamStatusError{status: resp.StatusCode}
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("inference did not return SSE")
	}
	finished := false
	answer := false
	size := 0
	err = consumeSSE(resp.Body, func(data string) error {
		if data == "[DONE]" {
			if !finished || !answer {
				return errors.New("inference ended without a complete answer")
			}
			return errDone
		}
		var v struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				Finish *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &v); err != nil {
			return err
		}
		if len(v.Error) > 0 && string(v.Error) != "null" {
			return errors.New("upstream stream error")
		}
		for _, choice := range v.Choices {
			if choice.Finish != nil {
				if *choice.Finish != "stop" {
					return errors.New("inference truncated or failed")
				}
				finished = true
			}
			if text := choice.Delta.Content; text != "" {
				if !utf8.ValidString(text) {
					return errors.New("invalid UTF-8")
				}
				size += len(text)
				if size > 1<<20 {
					return errors.New("answer too large")
				}
				answer = true
				if err := delta(text); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if errors.Is(err, errDone) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("inference disconnected before DONE")
}
func (a *App) message(w http.ResponseWriter, r *http.Request, u string) {
	var input struct {
		Content string `json:"content"`
	}
	if !decode(w, r, &input) {
		return
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" || len(input.Content) > 16000 {
		problem(w, 400, "Nachricht leer oder zu lang (maximal 16000 Bytes).")
		return
	}
	if !a.reserve(u) {
		problem(w, 409, "Eine Antwort läuft bereits.")
		return
	}
	defer a.release(u)
	c, err := a.store.Get(u, r.PathValue("id"))
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	n := len(input.Content)
	for _, m := range c.Messages {
		n += len(m.Content)
	}
	if n > a.cfg.MaxHistory {
		problem(w, 413, "Chat zu lang. Bitte einen neuen Chat starten.")
		return
	}
	if c.Model != a.cfg.Model {
		problem(w, 409, "Das Modell dieses Chats ist nicht mehr konfiguriert.")
		return
	}
	if len(c.Messages) == 0 {
		title := []rune(input.Content)
		if len(title) > 60 {
			title = title[:60]
		}
		c.Title = string(title)
	}
	c.Messages = append(c.Messages, Message{opaqueID(), "user", input.Content, "complete"}, Message{opaqueID(), "assistant", "", "pending"})
	c.Updated = time.Now().UTC()
	if err = a.store.Save(u, c); err != nil {
		logFailure(r.Context(), "save_submission", err)
		problem(w, 500, "Nachricht konnte nicht gespeichert werden.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.GenerationTimeout)
	defer cancel()
	reply := &c.Messages[len(c.Messages)-1]
	if err = event(w, "start", map[string]string{"message_id": reply.ID}); err == nil {
		err = a.streamReply(ctx, c, reply, func(html string) error {
			return event(w, "snapshot", map[string]string{"html": html})
		})
	}
	reply.Status = "complete"
	if err != nil {
		reply.Status = "failed"
		if r.Context().Err() != nil {
			reply.Status = "cancelled"
		} else if reply.Content != "" {
			reply.Status = "partial"
		}
	}
	c.Updated = time.Now().UTC()
	if saveErr := a.store.Save(u, c); saveErr != nil {
		logFailure(r.Context(), "save_reply", saveErr)
		_ = event(w, "error", map[string]string{"error": "Antwort konnte nicht gespeichert werden. Bitte Chat neu laden."})
		return
	}
	if err != nil {
		if r.Context().Err() == nil {
			logFailure(r.Context(), "generate_reply", err)
		}
		_ = event(w, "error", map[string]string{"error": "Antwort unterbrochen. Bitte erneut versuchen.", "status": reply.Status})
		return
	}
	_ = event(w, "done", map[string]string{"message_id": reply.ID})
}
