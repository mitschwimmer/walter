package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var assets embed.FS

type Config struct {
	Listen, PublicURL, Issuer, ClientID, ClientSecret, DataDir, Endpoint, Model string
	Groups                                                                      []string
	Quick, Thoughtful                                                           map[string]any
	GenerationTimeout, SessionTTL                                               time.Duration
	MaxHistory, MaxOutput                                                       int
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func config() (Config, error) {
	c := Config{Listen: env("WALTER_LISTEN", "127.0.0.1:8080"), PublicURL: os.Getenv("WALTER_PUBLIC_URL"), Issuer: os.Getenv("WALTER_OIDC_ISSUER"), ClientID: os.Getenv("WALTER_OIDC_CLIENT_ID"), ClientSecret: os.Getenv("WALTER_OIDC_CLIENT_SECRET"), DataDir: env("WALTER_DATA_DIR", "data"), Endpoint: os.Getenv("WALTER_LLAMA_ENDPOINT"), Model: os.Getenv("WALTER_MODEL"), Groups: strings.Split(env("WALTER_ALLOWED_GROUPS", "walter-users"), ","), GenerationTimeout: 2 * time.Hour, SessionTTL: 12 * time.Hour, MaxHistory: 48000, MaxOutput: 4096}
	for _, v := range []struct {
		key string
		dst *time.Duration
	}{{"WALTER_GENERATION_TIMEOUT", &c.GenerationTimeout}, {"WALTER_SESSION_TTL", &c.SessionTTL}} {
		if raw := os.Getenv(v.key); raw != "" {
			d, err := time.ParseDuration(raw)
			if err != nil || d <= 0 {
				return c, fmt.Errorf("invalid %s", v.key)
			}
			*v.dst = d
		}
	}
	for _, v := range []struct {
		key string
		dst *int
	}{{"WALTER_MAX_HISTORY_BYTES", &c.MaxHistory}, {"WALTER_MAX_OUTPUT_TOKENS", &c.MaxOutput}} {
		if raw := os.Getenv(v.key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return c, fmt.Errorf("invalid %s", v.key)
			}
			*v.dst = n
		}
	}
	for _, v := range []struct {
		key string
		dst *map[string]any
	}{{"WALTER_QUICK_JSON", &c.Quick}, {"WALTER_THOUGHTFUL_JSON", &c.Thoughtful}} {
		if err := json.Unmarshal([]byte(os.Getenv(v.key)), v.dst); err != nil || len(*v.dst) == 0 {
			return c, fmt.Errorf("%s must be a nonempty JSON object of verified model settings", v.key)
		}
		for k := range *v.dst {
			switch k {
			case "reasoning_effort", "chat_template_kwargs", "temperature", "top_p", "top_k", "min_p":
			default:
				return c, fmt.Errorf("unsupported model setting %q", k)
			}
		}
	}
	for _, raw := range []string{c.PublicURL, c.Issuer} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("public URL and issuer must be HTTPS URLs")
		}
	}
	u, _ := url.Parse(c.PublicURL)
	if u.Path != "" && u.Path != "/" {
		return c, errors.New("public URL must be an origin")
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	endpoint, err := url.Parse(c.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
		return c, errors.New("invalid llama endpoint")
	}
	if c.ClientID == "" || c.ClientSecret == "" || c.Model == "" {
		return c, errors.New("OIDC client credentials and model required")
	}
	for i, g := range c.Groups {
		c.Groups[i] = strings.TrimSpace(g)
		if c.Groups[i] == "" {
			return c, errors.New("empty allowed group")
		}
	}
	return c, nil
}

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
	_ = json.NewEncoder(w).Encode(v)
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("Origin") != a.cfg.PublicURL {
			problem(w, 403, "Ungültiger Ursprung.")
			return
		}
		mux.ServeHTTP(w, r)
	})
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
		problem(w, 500, "Chats konnten nicht geladen werden.")
		return
	}
	writeJSON(w, 200, cs)
}
func (a *App) create(w http.ResponseWriter, r *http.Request, u string) {
	var v struct {
		Language string `json:"language"`
		Thinking string `json:"thinking"`
	}
	if !decode(w, r, &v) {
		return
	}
	if (v.Language != "de" && v.Language != "en") || (v.Thinking != "quick" && v.Thinking != "thoughtful") {
		problem(w, 400, "Sprache oder Denkmodus ungültig.")
		return
	}
	now := time.Now().UTC()
	c := &Conversation{ID: opaqueID(), Title: "Neuer Chat", Language: v.Language, Thinking: v.Thinking, Model: a.cfg.Model, Created: now, Updated: now, Messages: []Message{}}
	if err := a.store.Save(u, c); err != nil {
		problem(w, 500, "Chat konnte nicht gespeichert werden.")
		return
	}
	writeJSON(w, 201, c)
}
func (a *App) get(w http.ResponseWriter, r *http.Request, u string) {
	c, err := a.store.Get(u, r.PathValue("id"))
	if err != nil {
		a.storeError(w, err)
		return
	}
	writeJSON(w, 200, c)
}
func (a *App) remove(w http.ResponseWriter, r *http.Request, u string) {
	if !a.reserve(u) {
		problem(w, 409, "Bitte zuerst die laufende Antwort stoppen.")
		return
	}
	defer a.release(u)
	if err := a.store.Delete(u, r.PathValue("id")); err != nil {
		a.storeError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *App) storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errMissing) {
		problem(w, 404, "Chat nicht gefunden.")
	} else {
		problem(w, 500, "Speicherfehler.")
	}
}
func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	auth, err := newAuth(cfg)
	if err != nil {
		return err
	}
	store := &Store{root: cfg.DataDir}
	if err = store.Recover(); err != nil {
		return err
	}
	a := &App{cfg: cfg, store: store, auth: auth, client: &http.Client{Timeout: cfg.GenerationTimeout}, active: map[string]bool{}}
	srv := &http.Server{Addr: cfg.Listen, Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, BaseContext: func(_ net.Listener) context.Context { return ctx }}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
