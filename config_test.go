package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfig(t *testing.T) {
	values := map[string]string{"WALTER_PUBLIC_URL": "https://chat.example/", "WALTER_OIDC_ISSUER": "https://identity.example/oauth2/openid/chat", "WALTER_OIDC_CLIENT_ID": "chat", "WALTER_OIDC_CLIENT_SECRET": "secret", "WALTER_LLAMA_ENDPOINT": "http://inference.example:8080/v1/chat/completions", "WALTER_MODEL": "test", "WALTER_QUICK_JSON": `{"reasoning_effort":"none"}`, "WALTER_THOUGHTFUL_JSON": `{"chat_template_kwargs":{"enable_thinking":true}}`, "WALTER_IMPRINT_URL": "https://example.com/impressum", "WALTER_PRIVACY_URL": "https://example.com/datenschutz"}
	for k, v := range values {
		t.Setenv(k, v)
	}
	c, err := config()
	if err != nil || c.PublicURL != "https://chat.example" || c.ImprintURL != values["WALTER_IMPRINT_URL"] || c.PrivacyURL != values["WALTER_PRIVACY_URL"] {
		t.Fatal(c, err)
	}
	for _, tc := range []struct{ k, v string }{{"WALTER_PUBLIC_URL", "http://chat.example"}, {"WALTER_PUBLIC_URL", "https://chat.example/path"}, {"WALTER_QUICK_JSON", "{}"}, {"WALTER_QUICK_JSON", `{"model":"override"}`}, {"WALTER_THOUGHTFUL_JSON", "null"}, {"WALTER_QUICK_JSON", `{"max_tokens":0}`}, {"WALTER_QUICK_JSON", `{"max_tokens":-1}`}, {"WALTER_QUICK_JSON", `{"max_tokens":1.5}`}, {"WALTER_QUICK_JSON", `{"max_tokens":"2048"}`}, {"WALTER_SESSION_TTL", "0s"}, {"WALTER_MAX_OUTPUT_TOKENS", "0"}, {"WALTER_ALLOWED_GROUPS", ","}, {"WALTER_LLAMA_ENDPOINT", "file:///etc/passwd"}, {"WALTER_MODEL", ""}} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			t.Setenv(tc.k, tc.v)
			if _, err := config(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	for _, key := range []string{"WALTER_IMPRINT_URL", "WALTER_PRIVACY_URL"} {
		for _, raw := range []string{"javascript:alert(1)", "http://example.com", "https://user:password@example.com", "/impressum"} {
			t.Run(key+raw, func(t *testing.T) {
				t.Setenv(key, raw)
				if _, err := config(); err == nil {
					t.Fatal("accepted unsafe footer URL")
				}
			})
		}
		t.Setenv(key, "")
	}
	if _, err := config(); err != nil {
		t.Fatal("footer URLs should be optional", err)
	}
}

func TestClientSecretFile(t *testing.T) {
	t.Setenv("WALTER_OIDC_CLIENT_SECRET", "")
	path := filepath.Join(t.TempDir(), "client-secret")
	if err := os.WriteFile(path, []byte("mounted-secret\r\n"), 0400); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WALTER_OIDC_CLIENT_SECRET_FILE", path)
	value, err := clientSecret()
	if err != nil || value != "mounted-secret" {
		t.Fatal("secret file not loaded")
	}
	t.Setenv("WALTER_OIDC_CLIENT_SECRET", "other")
	if _, err := clientSecret(); err == nil {
		t.Fatal("ambiguous secret sources accepted")
	}
	t.Setenv("WALTER_OIDC_CLIENT_SECRET", "")
	t.Setenv("WALTER_OIDC_CLIENT_SECRET_FILE", path+".missing")
	if _, err := clientSecret(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing secret file cause not preserved")
	}
}

func TestProfileValidationAtStartup(t *testing.T) {
	backendConfig(t)
	for _, tc := range []struct {
		name, backend, profile string
		valid                  bool
	}{
		{"temperature type", "openai", `{"temperature":"hot"}`, false},
		{"temperature null", "openai", `{"temperature":null}`, false},
		{"temperature range", "openai", `{"temperature":2.1}`, false},
		{"temperature lower", "openai", `{"temperature":-0.1}`, false},
		{"probability type", "openai", `{"top_p":true}`, false},
		{"probability lower", "llamacpp", `{"min_p":-0.1}`, false},
		{"probability upper", "llamacpp", `{"top_p":1.1}`, false},
		{"penalty type", "llamacpp", `{"repeat_penalty":[]}`, false},
		{"openai penalty", "openai", `{"presence_penalty":2.1}`, false},
		{"openai negative penalty", "openai", `{"presence_penalty":-2.1}`, false},
		{"fractional top k", "llamacpp", `{"top_k":1.5}`, false},
		{"large top k", "llamacpp", `{"top_k":2147483648}`, false},
		{"large token budget", "openai", `{"max_tokens":1e100}`, false},
		{"template type", "llamacpp", `{"chat_template_kwargs":false}`, false},
		{"template null", "llamacpp", `{"chat_template_kwargs":null}`, false},
		{"template thinking type", "llamacpp", `{"chat_template_kwargs":{"enable_thinking":"false"}}`, false},
		{"template preserve type", "llamacpp", `{"chat_template_kwargs":{"preserve_thinking":1}}`, false},
		{"template effort type", "llamacpp", `{"chat_template_kwargs":{"reasoning_effort":false}}`, false},
		{"effort type", "openai", `{"reasoning_effort":1}`, false},
		{"effort blank", "openai", `{"reasoning_effort":" "}`, false},
		{"openai edges", "openai", `{"temperature":0,"top_p":1,"presence_penalty":-2,"max_tokens":1}`, true},
		{"openai other edges", "openai", `{"temperature":2,"top_p":0,"presence_penalty":2}`, true},
		{"llama extensions", "llamacpp", `{"temperature":3,"presence_penalty":3,"top_k":-1,"min_p":0,"repeat_penalty":1}`, true},
		{"llama greedy", "llamacpp", `{"temperature":-1,"top_k":0,"min_p":1}`, true},
		{"template extension", "llamacpp", `{"chat_template_kwargs":{"enable_thinking":false,"custom_template_flag":"value"}}`, true},
		{"effort extension", "openai", `{"reasoning_effort":"provider-specific"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WALTER_INFERENCE_BACKEND", tc.backend)
			// Validate both profile entry points, without changing the other default.
			for _, key := range []string{"WALTER_QUICK_JSON", "WALTER_THOUGHTFUL_JSON"} {
				t.Run(key, func(t *testing.T) {
					t.Setenv(key, tc.profile)
					_, err := config()
					if (err == nil) != tc.valid {
						t.Fatalf("valid=%v: %v", tc.valid, err)
					}
				})
			}
		})
	}
}
