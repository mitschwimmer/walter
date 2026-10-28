package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func backendConfig(t *testing.T) Config {
	t.Helper()
	for k, v := range map[string]string{
		"WALTER_PUBLIC_URL": "https://chat.example", "WALTER_OIDC_ISSUER": "https://identity.example",
		"WALTER_OIDC_CLIENT_ID": "walter", "WALTER_OIDC_CLIENT_SECRET": "oidc-secret",
		"WALTER_OIDC_CLIENT_SECRET_FILE": "", "WALTER_MODEL": "qwen3.8-flash-next-iq3_s",
		"WALTER_INFERENCE_BACKEND": "openai", "WALTER_INFERENCE_ENDPOINT": "http://strata.internal:8080/v1/chat/completions",
		"WALTER_LLAMA_ENDPOINT": "", "WALTER_INFERENCE_API_KEY": "", "WALTER_INFERENCE_API_KEY_FILE": "",
		"WALTER_QUICK_JSON": "", "WALTER_THOUGHTFUL_JSON": "",
	} {
		t.Setenv(k, v)
	}
	cfg, err := config()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestBackendConfig(t *testing.T) {
	cfg := backendConfig(t)
	if cfg.Backend != "openai" || cfg.Quick["max_tokens"] != float64(2048) || cfg.Thoughtful["max_tokens"] != float64(4096) {
		t.Fatal("incorrect OpenAI defaults")
	}
	for _, tc := range []struct{ key, value string }{
		{"WALTER_INFERENCE_BACKEND", "anthropic"},
		{"WALTER_INFERENCE_ENDPOINT", ""},
		{"WALTER_INFERENCE_ENDPOINT", "file:///etc/passwd"},
		{"WALTER_INFERENCE_ENDPOINT", "http://user:secret@strata.internal/v1/chat/completions"},
		{"WALTER_LLAMA_ENDPOINT", "http://llama.internal/v1/chat/completions"},
		{"WALTER_INFERENCE_API_KEY", "key\ninjected"},
		{"WALTER_QUICK_JSON", `{"chat_template_kwargs":{"enable_thinking":false}}`},
		{"WALTER_THOUGHTFUL_JSON", `{"top_k":20}`},
		{"WALTER_QUICK_JSON", `{"min_p":0.1}`},
		{"WALTER_QUICK_JSON", `{"repeat_penalty":1.1}`},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := config(); err == nil {
				t.Fatal("accepted invalid backend configuration")
			}
		})
	}
	t.Setenv("WALTER_QUICK_JSON", `{"temperature":0.7,"reasoning_effort":"low","max_tokens":1024}`)
	if _, err := config(); err != nil {
		t.Fatal("rejected explicit portable settings", err)
	}
	t.Setenv("WALTER_INFERENCE_BACKEND", "llamacpp")
	t.Setenv("WALTER_QUICK_JSON", "")
	cfg, err := config()
	if err != nil || cfg.Quick["chat_template_kwargs"] == nil {
		t.Fatal("generic endpoint must also support llama.cpp", err)
	}
	t.Setenv("WALTER_INFERENCE_BACKEND", "")
	t.Setenv("WALTER_INFERENCE_ENDPOINT", "")
	t.Setenv("WALTER_LLAMA_ENDPOINT", "http://llama.internal/v1/chat/completions")
	cfg, err = config()
	if err != nil || cfg.Backend != "llamacpp" || cfg.Endpoint != os.Getenv("WALTER_LLAMA_ENDPOINT") {
		t.Fatal("legacy configuration must keep working", err)
	}
}

func TestInferenceAPIKeyFile(t *testing.T) {
	backendConfig(t)
	path := filepath.Join(t.TempDir(), "api-key")
	if err := os.WriteFile(path, []byte("mounted-key\r\n"), 0400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WALTER_INFERENCE_API_KEY_FILE", path)
	cfg, err := config()
	if err != nil || cfg.APIKey != "mounted-key" {
		t.Fatal("API key file not loaded")
	}
	t.Setenv("WALTER_INFERENCE_API_KEY", "another")
	if _, err := config(); err == nil {
		t.Fatal("ambiguous API key sources accepted")
	}
	t.Setenv("WALTER_INFERENCE_API_KEY", "")
	t.Setenv("WALTER_INFERENCE_API_KEY_FILE", path+".missing")
	if _, err := config(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing file cause not preserved")
	}
}

func TestOpenAIStreaming(t *testing.T) {
	cfg := backendConfig(t)
	for _, mode := range []string{"quick", "thoughtful"} {
		t.Run(mode, func(t *testing.T) {
			cfg.APIKey = "test-key"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/proxy/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("wrong inference endpoint or authentication")
					http.NotFound(w, r)
					return
				}
				var got map[string]any
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				messages := got["messages"]
				delete(got, "messages")
				limit := float64(2048)
				if mode == "thoughtful" {
					limit = 4096
				}
				want := map[string]any{"model": cfg.Model, "stream": true, "max_tokens": limit}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("payload = %#v; want %#v", got, want)
				}
				if first := messages.([]any)[0].(map[string]any); first["role"] != "system" || first["content"] != systemPrompt {
					t.Error("German system message missing")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":null}}]}\n\n")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private reasoning\"}}]}\n\n")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"}}]}\n\n")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"completion_tokens\":12}}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			cfg.Endpoint = srv.URL + "/proxy/v1/chat/completions"
			a := &App{cfg: cfg, client: inferenceClient(time.Second)}
			c := fixture()
			c.Model, c.Thinking = cfg.Model, mode
			var output string
			if err := a.infer(context.Background(), c, func(s string) error { output += s; return nil }, nil); err != nil || output != "Hallo" {
				t.Fatal("OpenAI streaming failed", err)
			}
			// Generic OpenAI servers need not implement llama.cpp metadata.
			c.Messages = []Message{{opaqueID(), "user", "Frage", "complete"}, {opaqueID(), "assistant", output, "complete"}}
			if got := a.conversationContext(context.Background(), c); got != nil {
				t.Fatal("invented context measurement")
			}
			if !a.promptFits(context.Background(), c, "Frage") {
				t.Fatal("missing metadata blocked generation")
			}
		})
	}
}

func TestInferenceRedirectDenied(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	a := &App{cfg: Config{Backend: "openai", Endpoint: source.URL, APIKey: "private-key"}, client: inferenceClient(time.Second)}
	err := a.infer(context.Background(), fixture(), func(string) error { return nil }, nil)
	var status *upstreamStatusError
	if !errors.As(err, &status) || status.status != 307 || called {
		t.Fatal("redirect forwarded inference credentials or prompt")
	}
}

func TestInferenceAuthenticationOptional(t *testing.T) {
	for _, key := range []string{"", "private-key"} {
		t.Run(key, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := ""
				if key != "" {
					want = "Bearer " + key
				}
				if r.Header.Get("Authorization") != want {
					t.Error("unexpected inference authorization")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hallo\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			a := &App{cfg: Config{Endpoint: srv.URL, APIKey: key}, client: inferenceClient(time.Second)}
			if err := a.infer(context.Background(), fixture(), func(string) error { return nil }, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
