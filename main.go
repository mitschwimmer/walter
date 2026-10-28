package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var assets embed.FS

type App struct {
	cfg    Config
	store  *Store
	auth   *Auth
	client *http.Client
	mu     sync.Mutex
	active map[string]bool
}

func (a *App) reserve(user string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active[user] {
		return false
	}
	a.active[user] = true
	return true
}
func (a *App) release(user string) { a.mu.Lock(); delete(a.active, user); a.mu.Unlock() }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		if response, ok := w.(*recordedResponse); ok {
			response.recorder.fail("response_encoding")
		}
	}
}
func problem(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		problem(w, 400, "Ungültige Anfrage.")
		return false
	}
	return true
}
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/site", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"imprint_url": a.cfg.ImprintURL, "privacy_url": a.cfg.PrivacyURL, "revision": buildRevision()})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /auth/login", a.auth.login)
	mux.HandleFunc("GET /auth/callback", a.auth.callback)
	mux.HandleFunc("POST /auth/logout", a.auth.logout)
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		s := a.auth.session(r)
		if s == nil {
			problem(w, 401, "Bitte anmelden.")
			return
		}
		writeJSON(w, 200, map[string]string{"csrf": s.CSRF})
	})
	mux.HandleFunc("GET /api/conversations", a.api(a.list))
	mux.HandleFunc("POST /api/conversations", a.api(a.create))
	mux.HandleFunc("GET /api/conversations/{id}", a.api(a.get))
	mux.HandleFunc("DELETE /api/conversations/{id}", a.api(a.remove))
	mux.HandleFunc("POST /api/conversations/{id}/messages", a.api(a.message))
	web, _ := fs.Sub(assets, "web")
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	return recordRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != a.cfg.PublicURL {
			problem(w, 403, "Ungültiger Ursprung.")
			return
		}
		mux.ServeHTTP(w, r)
	}), slog.Default().Handler())
}
func (a *App) api(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := a.auth.session(r)
		if s == nil {
			problem(w, 401, "Bitte erneut anmelden.")
			return
		}
		if r.Method != "GET" && r.Header.Get("X-CSRF-Token") != s.CSRF {
			problem(w, 403, "Ungültige Sitzung.")
			return
		}
		next(w, r, s.User)
	}
}
func (a *App) list(w http.ResponseWriter, r *http.Request, u string) {
	cs, err := a.store.List(u)
	if err != nil {
		logFailure(r.Context(), "list_conversations", err)
		problem(w, 500, "Chats konnten nicht geladen werden.")
		return
	}
	writeJSON(w, 200, cs)
}
func (a *App) create(w http.ResponseWriter, r *http.Request, u string) {
	var v struct {
		// Accept legacy clients; all new replies are German regardless of this field.
		Language string `json:"language"`
		Thinking string `json:"thinking"`
	}
	if !decode(w, r, &v) {
		return
	}
	if v.Thinking != "quick" && v.Thinking != "thoughtful" {
		problem(w, 400, "Denkmodus ungültig.")
		return
	}
	now := time.Now().UTC()
	c := &Conversation{ID: opaqueID(), Title: "Neue Frage", Thinking: v.Thinking, Model: a.cfg.Model, Created: now, Updated: now, Messages: []Message{}}
	if err := a.store.Save(u, c); err != nil {
		logFailure(r.Context(), "create_conversation", err)
		problem(w, 500, "Chat konnte nicht gespeichert werden.")
		return
	}
	writeJSON(w, 201, c)
}
func (a *App) get(w http.ResponseWriter, r *http.Request, u string) {
	c, err := a.store.Get(u, r.PathValue("id"))
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	view, err := renderConversation(c)
	if err != nil {
		logFailure(r.Context(), "render_conversation", err)
		problem(w, 500, "Chat konnte nicht dargestellt werden.")
		return
	}
	view.Context = a.conversationContext(r.Context(), c)
	writeJSON(w, 200, view)
}
func (a *App) remove(w http.ResponseWriter, r *http.Request, u string) {
	if !a.reserve(u) {
		problem(w, 409, "Bitte zuerst die laufende Antwort stoppen.")
		return
	}
	defer a.release(u)
	if err := a.store.Delete(u, r.PathValue("id")); err != nil {
		a.storeError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (a *App) storeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errMissing) {
		problem(w, 404, "Chat nicht gefunden.")
	} else {
		logFailure(r.Context(), "access_conversation", err)
		problem(w, 500, "Speicherfehler.")
	}
}
func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config()
	if err != nil {
		logFailure(context.Background(), "load_configuration", err)
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	auth, err := newAuth(cfg)
	if err != nil {
		logFailure(ctx, "discover_oidc_provider", err)
		return err
	}
	store := &Store{root: cfg.DataDir}
	if err = store.Recover(); err != nil {
		logFailure(ctx, "recover_conversations", err)
		return err
	}
	a := &App{cfg: cfg, store: store, auth: auth, client: inferenceClient(cfg.GenerationTimeout), active: map[string]bool{}}
	srv := &http.Server{Addr: cfg.Listen, Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, BaseContext: func(_ net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		logFailure(ctx, "listen_http", err)
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			logFailure(shutdown, "shutdown_http", err)
			return err
		}
		return nil
	}
}
