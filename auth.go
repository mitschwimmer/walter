package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Session struct {
	User, CSRF string
	Expires    time.Time
}
type loginFlow struct {
	Nonce, Verifier string
	Expires         time.Time
}
type Auth struct {
	cfg      Config
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	mu       sync.Mutex
	sessions map[string]Session
	flows    map[string]loginFlow
	ctx      context.Context
}

func token() string { return base64.RawURLEncoding.EncodeToString(randomBytes(32)) }
func hashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func newAuth(c Config) (*Auth, error) {
	// Remote JWKS refresh outlives discovery; bound each outbound HTTP call.
	ctx := oidc.ClientContext(context.Background(), &http.Client{Timeout: 30 * time.Second})
	p, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	endpoint := p.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	return &Auth{cfg: c, oauth: oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: endpoint, RedirectURL: c.PublicURL + "/auth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "groups"}}, verifier: p.Verifier(&oidc.Config{ClientID: c.ClientID}), sessions: map[string]Session{}, flows: map[string]loginFlow{}, ctx: ctx}, nil
}
func (a *Auth) prune() {
	now := time.Now()
	for k, v := range a.sessions {
		if !now.Before(v.Expires) {
			delete(a.sessions, k)
		}
	}
	for k, v := range a.flows {
		if !now.Before(v.Expires) {
			delete(a.flows, k)
		}
	}
}
func cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age})
}
// Authentication diagnostics use fixed reasons, never cookie or token values.
func authRejected(r *http.Request, reason string) {
	requestLogger(r.Context()).DebugContext(r.Context(), "authentication rejected", "auth_reason", reason)
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	if a.session(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	state, nonce, verifier := token(), token(), oauth2.GenerateVerifier()
	a.mu.Lock()
	a.prune()
	if len(a.flows) >= 256 {
		a.mu.Unlock()
		authRejected(r, "login_flows_full")
		problem(w, 503, "Anmeldung vorübergehend ausgelastet.")
		return
	}
	a.flows[hashToken(state)] = loginFlow{nonce, verifier, time.Now().Add(10 * time.Minute)}
	a.mu.Unlock()
	cookie(w, "__Host-walter-login", state, 600)
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusSeeOther)
}
func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	c, err := r.Cookie("__Host-walter-login")
	if err != nil || len(state) != 43 || subtle.ConstantTimeCompare([]byte(state), []byte(c.Value)) != 1 {
		authRejected(r, "login_state_invalid")
		problem(w, 400, "Anmeldung ungültig oder abgelaufen.")
		return
	}
	a.mu.Lock()
	flow, ok := a.flows[hashToken(state)]
	delete(a.flows, hashToken(state))
	a.mu.Unlock()
	cookie(w, "__Host-walter-login", "", -1)
	if !ok || !time.Now().Before(flow.Expires) {
		if !ok {
			authRejected(r, "login_flow_missing")
		} else {
			authRejected(r, "login_flow_expired")
		}
		problem(w, 400, "Anmeldung abgelaufen.")
		return
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, a.ctx.Value(oauth2.HTTPClient))
	t, err := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		logFailure(ctx, "exchange_oidc_code", err)
		authRejected(r, "token_exchange_failed")
		problem(w, 401, "Anmeldung fehlgeschlagen.")
		return
	}
	raw, ok := t.Extra("id_token").(string)
	if !ok {
		authRejected(r, "identity_missing")
		problem(w, 401, "Identität fehlt.")
		return
	}
	id, err := a.verifier.Verify(ctx, raw)
	if err != nil || id.Subject == "" || subtle.ConstantTimeCompare([]byte(id.Nonce), []byte(flow.Nonce)) != 1 {
		if err != nil {
			logFailure(ctx, "verify_oidc_identity", err)
		}
		authRejected(r, "identity_invalid")
		problem(w, 401, "Identität ungültig.")
		return
	}
	var claims struct {
		Groups []string `json:"groups"`
	}
	if id.Claims(&claims) != nil {
		authRejected(r, "groups_invalid")
		problem(w, 403, "Kein Zugriff.")
		return
	}
	if !a.allowedGroup(claims.Groups) {
		authRejected(r, "groups_denied")
		problem(w, 403, "Kein Zugriff.")
		return
	}
	// The ID token is verified at login; its expiry is not the lifetime of
	// Walter's independently managed application session.
	expires := time.Now().Add(a.cfg.SessionTTL)
	value := token()
	a.mu.Lock()
	a.prune()
	if len(a.sessions) >= 128 {
		a.mu.Unlock()
		authRejected(r, "sessions_full")
		problem(w, 503, "Zu viele Sitzungen.")
		return
	}
	if old, err := r.Cookie("__Host-walter-session"); err == nil {
		delete(a.sessions, hashToken(old.Value))
	}
	a.sessions[hashToken(value)] = Session{userKey(id.Issuer, id.Subject), token(), expires}
	a.mu.Unlock()
	cookie(w, "__Host-walter-session", value, int(time.Until(expires).Seconds()))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *Auth) session(r *http.Request) *Session {
	s, _ := a.sessionWithReason(r)
	return s
}

// Distinguish an expired session from one lost across a process restart.
// Only fixed reasons are returned; raw cookie values are never logged.
func (a *Auth) sessionWithReason(r *http.Request) (*Session, string) {
	c, err := r.Cookie("__Host-walter-session")
	if errors.Is(err, http.ErrNoCookie) {
		return nil, "cookie_missing"
	}
	if err != nil || len(c.Value) != 43 {
		return nil, "cookie_invalid"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[hashToken(c.Value)]
	if ok && !time.Now().Before(s.Expires) {
		a.prune()
		return nil, "session_expired"
	}
	a.prune()
	if !ok {
		return nil, "session_unknown"
	}
	return &s, ""
}
func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	s, reason := a.sessionWithReason(r)
	if s == nil {
		authRejected(r, reason)
		problem(w, 401, "Bitte anmelden.")
		return
	}
	if r.Header.Get("X-CSRF-Token") != s.CSRF {
		authRejected(r, "csrf_invalid")
		problem(w, 403, "Ungültige Sitzung.")
		return
	}
	c, _ := r.Cookie("__Host-walter-session")
	a.mu.Lock()
	delete(a.sessions, hashToken(c.Value))
	a.mu.Unlock()
	cookie(w, "__Host-walter-session", "", -1)
	w.WriteHeader(204)
}

func (a *Auth) allowedGroup(groups []string) bool {
	for _, group := range groups {
		if slices.Contains(a.cfg.Groups, group) {
			return true
		}
	}
	return false
}
