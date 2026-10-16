package main

import "testing"

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
