package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
)

func TestOIDCFlow(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, group, nonce, audience string
		expired                      bool
		status                       int
	}{{"allowed", "ai-users", "", "chat", false, 303}, {"denied", "unrelated", "", "chat", false, 403}, {"nonce", "ai-users", "wrong", "chat", false, 401}, {"audience", "ai-users", "", "other", false, 401}, {"expired", "ai-users", "", "chat", true, 401}} {
		t.Run(tc.name, func(t *testing.T) {
			var issuer, nonce, challenge string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					writeJSON(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
				case "/keys":
					writeJSON(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}}})
				case "/token":
					r.ParseForm()
					id, secret, ok := r.BasicAuth()
					if !ok || id != "chat" || secret != "secret" || r.Form.Get("redirect_uri") != "https://chat.example/auth/callback" || r.Form.Get("code") != "code" || hashToken(r.Form.Get("code_verifier")) != challenge {
						t.Error("invalid token exchange")
						http.Error(w, "bad", 400)
						return
					}
					n := nonce
					if tc.nonce != "" {
						n = tc.nonce
					}
					exp := time.Now().Add(time.Hour)
					if tc.expired {
						exp = time.Now().Add(-time.Hour)
					}
					signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "test"}}, nil)
					if err != nil {
						t.Fatal(err)
					}
					signed, err := jwt.Signed(signer).Claims(jwt.Claims{Issuer: issuer, Subject: "alice", Audience: jwt.Audience{tc.audience}, Expiry: jwt.NewNumericDate(exp), IssuedAt: jwt.NewNumericDate(time.Now())}).Claims(map[string]any{"nonce": n, "groups": []string{tc.group}}).Serialize()
					if err != nil {
						t.Fatal(err)
					}
					writeJSON(w, 200, map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": signed, "expires_in": 3600})
				default:
					http.NotFound(w, r)
				}
			}))
			defer provider.Close()
			issuer = provider.URL
			p, err := oidc.NewProvider(context.Background(), issuer)
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{PublicURL: "https://chat.example", Issuer: issuer, Groups: []string{"ai-users", "admins"}, SessionTTL: 12 * time.Hour}
			endpoint := p.Endpoint()
			endpoint.AuthStyle = oauth2.AuthStyleInHeader
			a := &Auth{cfg: cfg, oauth: oauth2.Config{ClientID: "chat", ClientSecret: "secret", RedirectURL: cfg.PublicURL + "/auth/callback", Endpoint: endpoint, Scopes: []string{"openid", "profile", "groups"}}, verifier: p.Verifier(&oidc.Config{ClientID: "chat"}), ctx: oidc.ClientContext(context.Background(), provider.Client()), sessions: map[string]Session{}, flows: map[string]loginFlow{}}
			w := httptest.NewRecorder()
			a.login(w, httptest.NewRequest("GET", "/auth/login", nil))
			location, err := url.Parse(w.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			q := location.Query()
			nonce = q.Get("nonce")
			challenge = q.Get("code_challenge")
			if q.Get("code_challenge_method") != "S256" || nonce == "" || challenge == "" {
				t.Fatal(q)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" {
				t.Fatal(cookies)
			}
			callback := httptest.NewRequest("GET", "/auth/callback?code=code&state="+q.Get("state"), nil)
			callback.AddCookie(cookies[0])
			w = httptest.NewRecorder()
			a.callback(w, callback)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.status == 303 {
				var sessionCookie *http.Cookie
				for _, c := range w.Result().Cookies() {
					if c.Name == "__Host-walter-session" {
						sessionCookie = c
					}
				}
				if sessionCookie == nil {
					t.Fatal("no session")
				}
				r := httptest.NewRequest("POST", "/auth/logout", nil)
				r.AddCookie(sessionCookie)
				s := a.session(r)
				if s == nil || s.User != userKey(issuer, "alice") {
					t.Fatal(s)
				}
				r.Header.Set("X-CSRF-Token", s.CSRF)
				w = httptest.NewRecorder()
				a.logout(w, r)
				if w.Code != 204 || a.session(r) != nil {
					t.Fatal("logout", w.Code)
				}
			}
			w = httptest.NewRecorder()
			a.callback(w, callback)
			if w.Code != 400 {
				t.Fatal("callback replay", w.Code)
			}
		})
	}
}
func TestLoginStateAndSessionExpiry(t *testing.T) {
	a := testApp(t)
	a.auth.sessions[hashToken(strings.Repeat("a", 43))] = Session{User: "a", Expires: time.Now().Add(-time.Second)}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "__Host-walter-session", Value: strings.Repeat("a", 43)})
	if a.auth.session(r) != nil {
		t.Fatal("expired session")
	}
	w := httptest.NewRecorder()
	a.auth.callback(w, httptest.NewRequest("GET", "/auth/callback?state=bad", nil))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	var data any
	if json.Unmarshal(w.Body.Bytes(), &data) != nil {
		t.Fatal("expected controlled error")
	}
}
