package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerationProgress(t *testing.T) {
	for _, tc := range []struct {
		name, frames string
		want         []inferenceProgress
	}{
		{"reported", `{"prompt_progress":{"total":100,"cache":20,"processed":23},"choices":[{"delta":{"content":null}}]}
{"prompt_progress":{"total":100,"cache":20,"processed":23}}
{"prompt_progress":{"total":100,"cache":20,"processed":10}}
{"prompt_progress":{"total":100,"cache":20,"processed":100}}
{"timings":{"predicted_n":7},"choices":[{"delta":{"reasoning_content":"private thinking"}}]}
`, []inferenceProgress{{Phase: "prefill", Percent: 23}, {Phase: "prefill", Percent: 100}, {Phase: "thinking", Tokens: 7}}},
		{"legacy", "", nil},
		{"invalid", `{"prompt_progress":{"total":0,"processed":0}}
{"prompt_progress":{"total":100,"processed":-1}}
{"prompt_progress":{"total":100,"processed":101}}
`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload["return_progress"] != true || payload["timings_per_token"] != true {
					t.Error("missing progress request", payload)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, frame := range strings.Split(strings.TrimSpace(tc.frames), "\n") {
					if frame != "" {
						io.WriteString(w, "data: "+frame+"\n\n")
					}
				}
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"}}]}\n\n")
				// Late metadata must not replace an answer with a waiting status.
				io.WriteString(w, "data: {\"prompt_progress\":{\"total\":100,\"processed\":100},\"choices\":[{\"delta\":{\"reasoning_content\":\"private late thinking\"}}]}\n\n")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			a.cfg.Endpoint = upstream.URL
			c := fixture()
			if err := a.store.Save("a", c); err != nil {
				t.Fatal(err)
			}
			w := request(a, "POST", "/api/conversations/"+c.ID+"/messages", `{"content":"Hi"}`, "a")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
			var got []inferenceProgress
			snapshot := false
			for _, frame := range strings.Split(w.Body.String(), "\n\n") {
				if strings.HasPrefix(frame, "event: snapshot\n") {
					snapshot = true
				}
				if strings.HasPrefix(frame, "event: progress\n") {
					if snapshot {
						t.Fatal("progress after answer")
					}
					var p inferenceProgress
					if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: progress\ndata: ")), &p); err != nil {
						t.Fatal(err)
					}
					got = append(got, p)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatal("progress", got, "want", tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatal("progress", got, "want", tc.want)
				}
			}
			saved, err := a.store.Get("a", c.ID)
			if err != nil || saved.Messages[1].Content != "Hallo" {
				t.Fatal("reasoning or metadata persisted", saved, err)
			}
		})
	}
}

func TestProgressWriteFailureCancelsInference(t *testing.T) {
	a := testApp(t)
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"prompt_progress\":{\"total\":100,\"processed\":23}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stop := errors.New("client write failed")
	var reply Message
	err := a.streamReply(ctx, fixture(), &reply, func(string) error {
		t.Error("unexpected answer")
		return nil
	}, func(inferenceProgress) error { return stop })
	if !errors.Is(err, stop) || reply.Content != "" {
		t.Fatal(err, reply)
	}
	select {
	case <-cancelled:
	case <-ctx.Done():
		t.Fatal("upstream was not cancelled")
	}
}
