package main

import (
	"bytes"
	"context"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// Leave raw HTML, dangerous URLs and custom attributes disabled. Only this
// renderer's output may be inserted as HTML by the browser.
func renderMarkdown(source string) (string, error) {
	var out bytes.Buffer
	md := goldmark.New(goldmark.WithExtensions(extension.Table))
	err := md.Convert([]byte(source), &out)
	return out.String(), err
}

type messageHTML struct {
	Message
	HTML string `json:"html,omitempty"`
}

type conversationHTML struct {
	*Conversation
	Messages []messageHTML `json:"messages"`
}

func renderConversation(c *Conversation) (conversationHTML, error) {
	view := conversationHTML{Conversation: c, Messages: make([]messageHTML, len(c.Messages))}
	for i, m := range c.Messages {
		view.Messages[i].Message = m
		if m.Role == "assistant" {
			html, err := renderMarkdown(m.Content)
			if err != nil {
				return conversationHTML{}, err
			}
			view.Messages[i].HTML = html
		}
	}
	return view, nil
}

// The reader owns inference; this goroutine owns reply content and all writes.
// A bounded handoff and cancellation keep slow/disconnected clients from
// leaving a reader behind. The ticker also flushes text when upstream pauses.
func (a *App) streamReply(ctx context.Context, c *Conversation, reply *Message, snapshot func(string) error) error {
	ctx, cancel := context.WithCancel(ctx)
	chunks := make(chan string)
	result := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		err := a.infer(ctx, c, func(text string) error {
			select {
			case chunks <- text:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		result <- err
		close(chunks)
	}()
	defer func() { cancel(); <-stopped }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	dirty := false
	flush := func() error {
		if !dirty {
			return nil
		}
		html, err := renderMarkdown(reply.Content)
		if err != nil {
			return err
		}
		dirty = false
		return snapshot(html)
	}
	first := true
	for {
		select {
		case text, ok := <-chunks:
			if !ok {
				if err := flush(); err != nil {
					return err
				}
				return <-result
			}
			reply.Content += text
			dirty = true
			if first {
				first = false
				if err := flush(); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return err
			}
		case <-ctx.Done():
			// A timeout may still have a connected browser; preserve its last text.
			if err := flush(); err != nil {
				return err
			}
			return ctx.Err()
		}
	}
}
