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
const maxSSEEventBytes = 1 << 20

func consumeSSE(r io.Reader, consume func(string) error) error {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 4096), maxSSEEventBytes)
	var lines []string
	size := 0
	for scan.Scan() {
		line := strings.TrimSuffix(scan.Text(), "\r")
		if line == "" {
			if len(lines) > 0 {
				if err := consume(strings.Join(lines, "\n")); err != nil {
					return err
				}
				lines = nil
				size = 0
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			size += len(data) + 1 // Include separators, even for empty data lines.
			if size > maxSSEEventBytes {
				return errors.New("SSE event too large")
			}
			lines = append(lines, data)
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

const systemPrompt = `Du bist ein deutschsprachiger Assistent namens Walter.

SPRACHE
- Antworte ausschließlich in natürlichem, idiomatischem Deutsch.
- Formuliere jeden Satz eigenständig auf Deutsch.
- Vermeide englische Wörter, Redewendungen und Satzkonstruktionen, wenn eine gebräuchliche deutsche Entsprechung existiert.
- Übernimm englische Ausdrücke nicht allein deshalb, weil sie in der Eingabe vorkommen.
- Ausnahmen gelten für Eigennamen, etablierte Fachbegriffe, Quelltext und wörtliche Zitate.
- Verwende keine wörtlichen Übersetzungen, die im Deutschen unnatürlich klingen.

STIL
- Schreibe wie ein deutscher Muttersprachler.
- Bevorzuge klare, präzise Formulierungen.
- Vermeide künstliche Förmlichkeit und unnötige Anglizismen.

IRONIE UND SARKASMUS
- Erkenne ironische und sarkastische Aussagen anhand ihres Zusammenhangs.
- Interpretiere solche Aussagen nicht automatisch wörtlich.
- Bewahre bei Übersetzungen die kommunikative Absicht, den Humor und die Wirkung.
- Verwende dafür natürliche deutsche Redewendungen statt wörtlicher Übersetzungen.
- Erkläre Ironie nicht ungefragt.

QUALITÄTSKONTROLLE
- Prüfe die Antwort vor der Ausgabe auf vermeidbare englische Wörter.
- Ersetze unpassende Anglizismen durch idiomatische deutsche Ausdrücke.
- Gib ausschließlich die fertige Antwort aus.`

// Only aggregate progress leaves the adapter; reasoning text is never forwarded.
type inferenceProgress struct {
	Phase   string `json:"phase"`
	Percent int    `json:"percent,omitempty"`
	Tokens  int    `json:"tokens,omitempty"`
}

// Keep credentials and prompts on the configured endpoint, including on 307/308.
func inferenceClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (a *App) authorizeInference(req *http.Request) {
	if a.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)
	}
}

func (a *App) infer(ctx context.Context, c *Conversation, delta func(string) error, progress func(inferenceProgress) error) error {
	requestLogger(ctx).DebugContext(ctx, "inference started")
	payload := a.inferencePayload(c, conversationMessages(c), c.Thinking)
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
	a.authorizeInference(req)
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
	var lastProgress inferenceProgress
	var lastProgressAt time.Time
	report := func(p inferenceProgress) error {
		if progress == nil || answer || p == lastProgress {
			return nil
		}
		// Reasoning can produce many tokens per second. Report activity at most
		// ten times per second, but always report a phase transition immediately.
		if p.Phase == "thinking" && lastProgress.Phase == p.Phase && time.Since(lastProgressAt) < 100*time.Millisecond {
			return nil
		}
		lastProgress, lastProgressAt = p, time.Now()
		return progress(p)
	}
	err = consumeSSE(resp.Body, func(data string) error {
		if data == "[DONE]" {
			if !finished || !answer {
				return errors.New("inference ended without a complete answer")
			}
			return errDone
		}
		var v struct {
			Error          json.RawMessage `json:"error"`
			PromptProgress *struct {
				Total     int `json:"total"`
				Processed int `json:"processed"`
			} `json:"prompt_progress"`
			Timings struct {
				Predicted int `json:"predicted_n"`
			} `json:"timings"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					Reasoning string `json:"reasoning_content"`
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
		if p := v.PromptProgress; p != nil && p.Total > 0 && p.Processed >= 0 && p.Processed <= p.Total && lastProgress.Phase != "thinking" {
			percent := int(100 * float64(p.Processed) / float64(p.Total))
			if lastProgress.Phase != "prefill" || percent >= lastProgress.Percent {
				if err := report(inferenceProgress{Phase: "prefill", Percent: percent}); err != nil {
					return err
				}
			}
		}
		for _, choice := range v.Choices {
			if choice.Delta.Reasoning != "" {
				tokens := max(0, v.Timings.Predicted, lastProgress.Tokens)
				if err := report(inferenceProgress{Phase: "thinking", Tokens: tokens}); err != nil {
					return err
				}
			}
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
		requestLogger(ctx).DebugContext(ctx, "inference completed")
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("inference disconnected before DONE")
}
func (a *App) message(w http.ResponseWriter, r *http.Request, u string) {
	var input struct {
		Content  string `json:"content"`
		Thinking string `json:"thinking"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Thinking != "" && input.Thinking != "quick" && input.Thinking != "thoughtful" {
		problem(w, 400, "Denkmodus ungültig.")
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
	if input.Thinking != "" {
		c.Thinking = input.Thinking
	}
	if !a.promptFits(r.Context(), c, input.Content) {
		problem(w, 413, "Kontext voll. Bitte eine neue Frage starten oder die Frage kürzen.")
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
		}, func(p inferenceProgress) error {
			return event(w, "progress", p)
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
		logFailure(r.Context(), "generate_reply", err)
		_ = event(w, "error", map[string]string{"error": "Antwort unterbrochen. Bitte erneut versuchen.", "status": reply.Status})
		return
	}
	requestLogger(r.Context()).DebugContext(r.Context(), "reply persisted")
	if err := event(w, "done", map[string]string{"message_id": reply.ID}); err != nil {
		logFailure(r.Context(), "complete_response", err)
	}
}
