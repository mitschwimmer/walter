package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompletedConversationContext(t *testing.T) {
	a := testApp(t)
	a.cfg.APIKey = "metadata-key"
	a.cfg.Quick = map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}}
	a.cfg.Thoughtful = map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true}}
	var countCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer metadata-key" {
			t.Error("missing metadata authentication")
		}
		if r.URL.Query().Get("model") != "test" {
			t.Error("missing model routing", r.URL)
		}
		switch r.URL.Path {
		case "/llama/props":
			writeJSON(w, 200, map[string]any{"default_generation_settings": map[string]int{"n_ctx": 100}})
		case "/llama/v1/chat/completions/input_tokens":
			countCalls++
			var payload struct {
				Model    string              `json:"model"`
				Messages []map[string]string `json:"messages"`
				Template struct {
					Thinking bool `json:"enable_thinking"`
				} `json:"chat_template_kwargs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload.Model != "test" || len(payload.Messages) != 5 || payload.Messages[0]["content"] != systemPrompt || payload.Messages[4]["role"] != "user" || payload.Messages[4]["content"] != "" {
				t.Error("incorrect retained conversation", payload)
			}
			for _, m := range payload.Messages {
				if m["content"] == "incomplete" || m["content"] == "private thinking" {
					t.Error("included excluded content", payload.Messages)
				}
			}
			count := 40
			if payload.Template.Thinking {
				count = 45
			}
			writeJSON(w, 200, map[string]int{"input_tokens": count})
		default:
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL + "/llama/v1/chat/completions"
	c := fixture()
	c.Messages = []Message{
		{opaqueID(), "user", "Hi", "complete"},
		{opaqueID(), "assistant", "incomplete", "partial"},
		{opaqueID(), "user", "Noch einmal", "complete"},
		{opaqueID(), "assistant", "Hallo", "complete"},
	}
	if err := a.store.Save("a", c); err != nil {
		t.Fatal(err)
	}
	w := request(a, "GET", "/api/conversations/"+c.ID, "", "a")
	var view conversationHTML
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || view.Context == nil || view.Context.Capacity != 100 || view.Context.Quick == nil || *view.Context.Quick != 40 || view.Context.Thoughtful == nil || *view.Context.Thoughtful != 45 || countCalls != 2 {
		t.Fatal(w.Code, w.Body.String(), countCalls)
	}
	for _, status := range []string{"pending", "partial", "cancelled", "failed"} {
		c.Messages[len(c.Messages)-1].Status = status
		if a.conversationContext(context.Background(), c) != nil {
			t.Fatal("context shown before completion", status)
		}
	}
	if countCalls != 2 {
		t.Fatal("queried context while answer unavailable")
	}
}

func TestContextGuardAndUnknownMetadata(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		capacity, count          int
		propsStatus, countStatus int
		want                     int
	}{
		{"fits", 100, 99, 200, 200, 200},
		{"full", 100, 100, 200, 200, 413},
		{"over", 100, 101, 200, 200, 413},
		{"props unsupported", 100, 100, 404, 200, 200},
		{"count unsupported", 100, 100, 200, 404, 200},
		{"invalid capacity", 0, 100, 200, 200, 200},
		{"invalid count", 100, -1, 200, 200, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			// A maximum output allowance is not a required reservation.
			a.cfg.MaxOutput = 8192
			inferenceCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/props":
					writeJSON(w, tc.propsStatus, map[string]any{"default_generation_settings": map[string]int{"n_ctx": tc.capacity}})
				case "/v1/chat/completions/input_tokens":
					var payload struct {
						Messages []map[string]string `json:"messages"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload.Messages[len(payload.Messages)-1]["content"] != "Actual question" {
						t.Error("did not check submitted draft", payload)
					}
					writeJSON(w, tc.countStatus, map[string]int{"input_tokens": tc.count})
				case "/v1/chat/completions":
					inferenceCalls++
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				default:
					t.Error("unexpected endpoint", r.URL)
				}
			}))
			defer upstream.Close()
			a.cfg.Endpoint = upstream.URL + "/v1/chat/completions"
			c := fixture()
			if err := a.store.Save("a", c); err != nil {
				t.Fatal(err)
			}
			w := request(a, "POST", "/api/conversations/"+c.ID+"/messages", `{"content":"Actual question","thinking":"thoughtful"}`, "a")
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			saved, err := a.store.Get("a", c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == 413 {
				if inferenceCalls != 0 || len(saved.Messages) != 0 || saved.Thinking != "quick" {
					t.Fatal("rejected prompt changed conversation", saved, inferenceCalls)
				}
			} else if inferenceCalls != 1 || len(saved.Messages) != 2 || !strings.Contains(w.Body.String(), "event: done") {
				t.Fatal("unknown metadata blocked generation", saved, inferenceCalls, w.Body.String())
			}
		})
	}
}

func TestContextUnknownCounts(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			writeJSON(w, 200, map[string]any{"default_generation_settings": map[string]int{"n_ctx": 100}})
		} else {
			writeJSON(w, 200, map[string]any{})
		}
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL + "/chat/completions"
	c := fixture()
	c.Messages = []Message{{opaqueID(), "assistant", "Hallo", "complete"}}
	usage := a.conversationContext(context.Background(), c)
	if usage == nil || usage.Capacity != 100 || usage.Quick != nil || usage.Thoughtful != nil {
		t.Fatal(usage)
	}
	c.Model = "old-model"
	if a.conversationContext(context.Background(), c) != nil {
		t.Fatal("measured wrong model")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.contextCapacity(ctx, "test") != 0 {
		t.Fatal("ignored cancellation")
	}
}
