package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInferenceProfiles(t *testing.T) {
	for k, v := range map[string]string{
		"WALTER_PUBLIC_URL": "https://chat.example", "WALTER_OIDC_ISSUER": "https://identity.example",
		"WALTER_OIDC_CLIENT_ID": "chat", "WALTER_OIDC_CLIENT_SECRET": "secret", "WALTER_OIDC_CLIENT_SECRET_FILE": "",
		"WALTER_MODEL": "qwen38-chat", "WALTER_QUICK_JSON": "", "WALTER_THOUGHTFUL_JSON": "",
		"WALTER_MAX_OUTPUT_TOKENS": "4096",
	} {
		t.Setenv(k, v)
	}
	for _, tc := range []struct {
		mode, settings string
	}{
		{"quick", `{"chat_template_kwargs":{"enable_thinking":false,"preserve_thinking":false},"temperature":0.7,"top_p":0.8,"top_k":20,"min_p":0,"presence_penalty":1.5,"repeat_penalty":1,"max_tokens":2048}`},
		{"thoughtful", `{"chat_template_kwargs":{"enable_thinking":true,"preserve_thinking":false,"reasoning_effort":"medium"},"temperature":1,"top_p":0.95,"top_k":20,"min_p":0,"presence_penalty":0,"repeat_penalty":1,"max_tokens":8192}`},
		{"override", `{"presence_penalty":0.5,"repeat_penalty":1.1,"max_tokens":512}`},
		{"fallback", `{"temperature":0.5}`},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			if tc.mode == "override" || tc.mode == "fallback" {
				t.Setenv("WALTER_QUICK_JSON", tc.settings)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got, want map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(tc.settings), &want); err != nil {
					t.Fatal(err)
				}
				if _, ok := want["max_tokens"]; !ok {
					want["max_tokens"] = float64(4096)
				}
				want["model"], want["stream"], want["reasoning_format"] = "qwen38-chat", true, "deepseek"
				// Message construction, including the German system prompt, is checked separately.
				delete(got, "messages")
				if !reflect.DeepEqual(got, want) {
					t.Errorf("request = %#v; want %#v", got, want)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			t.Setenv("WALTER_LLAMA_ENDPOINT", srv.URL)
			cfg, err := config()
			if err != nil {
				t.Fatal(err)
			}
			a := &App{cfg: cfg, client: srv.Client()}
			c := fixture()
			c.Model, c.Thinking = cfg.Model, tc.mode
			if err := a.infer(context.Background(), c, func(string) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInference(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{{"complete", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hidden\",\"content\":\"Hallo\"}}]}\r\n\r\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", true}, {"disconnect", "data: {\"choices\":[{\"delta\":{\"content\":\"Halb\"}}]}\n\n", false}, {"truncated", "data: {\"choices\":[{\"delta\":{\"content\":\"Halb\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", false}, {"empty", "data: [DONE]\n\n", false}, {"invalid", "data: broken\n\n", false}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var v map[string]any
				if json.NewDecoder(r.Body).Decode(&v) != nil {
					t.Error("body")
				}
				if v["model"] != "test" || v["reasoning_format"] != "deepseek" || v["reasoning_effort"] != "none" {
					t.Error(v)
				}
				messages := v["messages"].([]any)
				if messages[0].(map[string]any)["role"] != "system" || messages[0].(map[string]any)["content"] != systemPrompt {
					t.Error(messages)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			a := &App{cfg: Config{Endpoint: srv.URL, Quick: map[string]any{"reasoning_effort": "none"}}, client: srv.Client()}
			c := fixture()
			var output string
			err := a.infer(context.Background(), c, func(s string) error { output += s; return nil })
			if (err == nil) != tc.ok {
				t.Fatal(err)
			}
			if strings.Contains(output, "hidden") {
				t.Fatal("reasoning leaked")
			}
		})
	}
}
func TestSSEMultilineAndSize(t *testing.T) {
	var got string
	err := consumeSSE(strings.NewReader(": hi\r\ndata: one\r\ndata: two\r\n\r\n"), func(s string) error { got = s; return nil })
	if err != nil || got != "one\ntwo" {
		t.Fatal(got, err)
	}
	if consumeSSE(strings.NewReader("data: incomplete"), func(string) error { return nil }) == nil {
		t.Fatal("accepted incomplete frame")
	}
}
func testApp(t *testing.T) *App {
	t.Helper()
	cfg := Config{PublicURL: "https://chat.example", Model: "test", MaxHistory: 48000, GenerationTimeout: time.Minute}
	auth := &Auth{cfg: cfg, sessions: map[string]Session{}, flows: map[string]loginFlow{}}
	return &App{cfg: cfg, store: &Store{root: t.TempDir()}, auth: auth, active: map[string]bool{}, client: http.DefaultClient}
}
func request(a *App, method, path, body, user string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", a.cfg.PublicURL)
	if user != "" {
		value := strings.Repeat(user, 43)
		a.auth.mu.Lock()
		a.auth.sessions[hashToken(value)] = Session{User: user, CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
		a.auth.mu.Unlock()
		r.AddCookie(&http.Cookie{Name: "__Host-walter-session", Value: value})
		r.Header.Set("X-CSRF-Token", "csrf")
	}
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}
func TestAPIAuthorizationAndCSRF(t *testing.T) {
	a := testApp(t)
	if w := request(a, "GET", "/api/conversations", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request(a, "POST", "/api/conversations", `{"language":"de","thinking":"quick"}`, "a")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var c Conversation
	json.Unmarshal(w.Body.Bytes(), &c)
	if w = request(a, "GET", "/api/conversations/"+c.ID, "", "b"); w.Code != 404 {
		t.Fatal("ownership", w.Code)
	}
	r := httptest.NewRequest("POST", "/api/conversations", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("origin", w.Code)
	}
	r.Header.Set("Origin", a.cfg.PublicURL)
	r.AddCookie(&http.Cookie{Name: "__Host-walter-session", Value: strings.Repeat("a", 43)})
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	if !a.reserve("a") || a.reserve("a") {
		t.Fatal("concurrency")
	}
	if w = request(a, "DELETE", "/api/conversations/"+c.ID, "", "a"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	a.release("a")
}
func TestPersistedDoneAndFailure(t *testing.T) {
	for _, ok := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[ok], func(t *testing.T) {
			a := testApp(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"}}]}\n\n")
				if ok {
					io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer srv.Close()
			a.cfg.Endpoint = srv.URL
			c := fixture()
			a.store.Save("a", c)
			w := request(a, "POST", "/api/conversations/"+c.ID+"/messages", `{"content":"Hi"}`, "a")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			saved, err := a.store.Get("a", c.ID)
			if err != nil {
				t.Fatal(err)
			}
			status := "complete"
			if !ok {
				status = "partial"
			}
			if saved.Messages[1].Status != status || saved.Messages[1].Content != "Hallo" {
				t.Fatal(saved.Messages)
			}
			if strings.Contains(w.Body.String(), "event: done") != ok {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestDisconnectCancelsAndPersists(t *testing.T) {
	a := testApp(t)
	upstreamCancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Teil\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(upstreamCancelled)
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL
	c := fixture()
	if err := a.store.Save("a", c); err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("a", 43)
	a.auth.sessions[hashToken(value)] = Session{User: "a", CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	backend := httptest.NewServer(a.routes())
	defer backend.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", backend.URL+"/api/conversations/"+c.ID+"/messages", strings.NewReader(`{"content":"Hi"}`))
	req.Header.Set("Origin", a.cfg.PublicURL)
	req.Header.Set("X-CSRF-Token", "csrf")
	req.AddCookie(&http.Cookie{Name: "__Host-walter-session", Value: value})
	resp, err := backend.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Cancel after an actual delta, not merely before inference starts.
	err = consumeSSE(resp.Body, func(data string) error {
		if strings.Contains(data, "Teil") {
			cancel()
			return errDone
		}
		return nil
	})
	resp.Body.Close()
	if err != errDone {
		t.Fatal(err)
	}
	select {
	case <-upstreamCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not cancelled")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		saved, err := a.store.Get("a", c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Messages[1].Status == "cancelled" {
			if saved.Messages[1].Content != "Teil" {
				t.Fatal(saved.Messages)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation not persisted")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestSaveFailureNeverEmitsDone(t *testing.T) {
	a := testApp(t)
	c := fixture()
	if err := a.store.Save("a", c); err != nil {
		t.Fatal(err)
	}
	// Remove the conversation directory during inference and replace it with a
	// regular file, deterministically making the final atomic save fail.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dir := a.store.dir("a")
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(dir, []byte("blocked"), 0600); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL
	w := request(a, "POST", "/api/conversations/"+c.ID+"/messages", `{"content":"Hi"}`, "a")
	if strings.Contains(w.Body.String(), "event: done") || !strings.Contains(w.Body.String(), "event: error") {
		t.Fatal(w.Body.String())
	}
}

func TestFollowUpModes(t *testing.T) {
	a := testApp(t)
	a.cfg.Quick = map[string]any{"temperature": 0.7}
	a.cfg.Thoughtful = map[string]any{"temperature": 1.0}
	var temperatures []float64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Temperature float64             `json:"temperature"`
			Messages    []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		temperatures = append(temperatures, payload.Temperature)
		// Every switch retains all earlier questions and complete answers.
		if len(payload.Messages) != 2*len(temperatures) {
			t.Error("lost conversation history", payload.Messages)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL
	c := fixture()
	if err := a.store.Save("a", c); err != nil {
		t.Fatal(err)
	}
	path := "/api/conversations/" + c.ID + "/messages"
	for _, tc := range []struct{ body, mode string }{
		{`{"content":"Hi","thinking":"thoughtful"}`, "thoughtful"},
		{`{"content":"Weiter","thinking":"quick"}`, "quick"},
		{`{"content":"Alter Client"}`, "quick"},
	} {
		w := request(a, "POST", path, tc.body, "a")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") {
			t.Fatal(w.Code, w.Body.String())
		}
		saved, err := a.store.Get("a", c.ID)
		if err != nil || saved.Thinking != tc.mode {
			t.Fatal("mode not persisted", saved, err)
		}
	}
	if len(temperatures) != 3 || temperatures[0] != 1.0 || temperatures[1] != 0.7 || temperatures[2] != 0.7 {
		t.Fatal("wrong inference profiles", temperatures)
	}
	if w := request(a, "POST", path, `{"content":"Hi","thinking":"invalid"}`, "a"); w.Code != 400 {
		t.Fatal("invalid mode accepted", w.Code)
	}
	if !a.reserve("a") {
		t.Fatal("user busy after generation")
	}
	w := request(a, "POST", path, `{"content":"Hi","thinking":"thoughtful"}`, "a")
	a.release("a")
	if w.Code != 409 {
		t.Fatal("accepted queued prompt", w.Code)
	}
	saved, err := a.store.Get("a", c.ID)
	if err != nil || saved.Thinking != "quick" || len(saved.Messages) != 6 {
		t.Fatal("rejected prompt changed conversation", saved, err)
	}
}
