package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfig(t *testing.T) {
	values := map[string]string{"WALTER_PUBLIC_URL": "https://chat.example/", "WALTER_OIDC_ISSUER": "https://identity.example/oauth2/openid/chat", "WALTER_OIDC_CLIENT_ID": "chat", "WALTER_OIDC_CLIENT_SECRET": "secret", "WALTER_LLAMA_ENDPOINT": "http://inference.example:8080/v1/chat/completions", "WALTER_MODEL": "test", "WALTER_QUICK_JSON": `{"reasoning_effort":"none"}`, "WALTER_THOUGHTFUL_JSON": `{"chat_template_kwargs":{"enable_thinking":true}}`}
	for k, v := range values {
		t.Setenv(k, v)
	}
	c, err := config()
	if err != nil || c.PublicURL != "https://chat.example" {
		t.Fatal(c, err)
	}
	for _, tc := range []struct{ k, v string }{{"WALTER_PUBLIC_URL", "http://chat.example"}, {"WALTER_PUBLIC_URL", "https://chat.example/path"}, {"WALTER_QUICK_JSON", "{}"}, {"WALTER_QUICK_JSON", `{"model":"override"}`}, {"WALTER_THOUGHTFUL_JSON", "null"}, {"WALTER_SESSION_TTL", "0s"}, {"WALTER_MAX_OUTPUT_TOKENS", "0"}, {"WALTER_ALLOWED_GROUPS", ","}, {"WALTER_LLAMA_ENDPOINT", "file:///etc/passwd"}, {"WALTER_MODEL", ""}} {
		t.Run(tc.k+tc.v, func(t *testing.T) {
			t.Setenv(tc.k, tc.v)
			if _, err := config(); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
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
