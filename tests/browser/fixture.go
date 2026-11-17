package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// A fixture provides only synthetic API responses and the checkout's real web assets.
// Production code, OIDC and inference are never modified for browser tests.
var browserAssets = map[string]string{
	"index.html":           "text/html; charset=utf-8",
	"app.js":               "text/javascript; charset=utf-8",
	"sse.js":               "text/javascript; charset=utf-8",
	"style.css":            "text/css; charset=utf-8",
	"oat.css":              "text/css; charset=utf-8",
	"archaic-theme.css":    "text/css; charset=utf-8",
	"walter.svg":           "image/svg+xml",
	"headline-overlay.svg": "image/svg+xml",
}

type conversation struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Thinking  string `json:"thinking"`
	UpdatedAt string `json:"updated_at"`
	Messages  []any  `json:"messages"`
}

func fixtureServer(name, webRoot string) (*httptest.Server, error) {
	switch name {
	case "empty-chat", "streaming-reply", "history-with-conversations":
	default:
		return nil, fmt.Errorf("unknown fixture %q", name)
	}
	sample := conversation{
		ID: "00000000000000000000000000000003", Title: "Was ist Marmelade?",
		Thinking: "thoughtful", UpdatedAt: "2026-10-16T18:00:00Z", Messages: []any{},
	}
	history := []conversation{
		{ID: "00000000000000000000000000000001", Title: "Was ist Marmelade?", Thinking: "quick", UpdatedAt: "2026-10-16T18:00:00Z", Messages: []any{}},
		{ID: "00000000000000000000000000000002", Title: "Ein Beispiel mit Tabellen", Thinking: "thoughtful", UpdatedAt: "2026-10-15T09:00:00Z", Messages: []any{}},
	}
	var created atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
		respond := func(code int, data any) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(data)
		}
		switch {
		case r.URL.Path == "/api/site":
			respond(http.StatusOK, map[string]string{})
			return
		case r.URL.Path == "/api/session":
			respond(http.StatusOK, map[string]string{"csrf": "scenario-token"})
			return
		case r.URL.Path == "/api/conversations" && r.Method == http.MethodGet:
			if name == "history-with-conversations" {
				respond(http.StatusOK, history)
			} else {
				respond(http.StatusOK, []conversation{})
			}
			return
		case r.URL.Path == "/api/conversations" && r.Method == http.MethodPost && name == "streaming-reply":
			if r.Header.Get("X-CSRF-Token") != "scenario-token" {
				respond(http.StatusForbidden, map[string]string{"error": "CSRF token missing"})
				return
			}
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["thinking"] != "thoughtful" {
				respond(http.StatusBadRequest, map[string]string{"error": "Unexpected conversation creation"})
				return
			}
			created.Store(true)
			respond(http.StatusCreated, sample)
			return
		case r.URL.Path == "/api/conversations/"+sample.ID+"/messages" && r.Method == http.MethodPost && name == "streaming-reply" && created.Load():
			if r.Header.Get("X-CSRF-Token") != "scenario-token" {
				respond(http.StatusForbidden, map[string]string{"error": "CSRF token missing"})
				return
			}
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 2 || body["thinking"] != "thoughtful" || body["content"] != "Was ist Marmelade?" {
				respond(http.StatusBadRequest, map[string]string{"error": "Unexpected submitted message"})
				return
			}
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = io.WriteString(w, "event: start\ndata: {}\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done() // deterministic waiting state until browser closes
			return
		case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/"):
			respond(http.StatusNotFound, map[string]string{"error": "Unknown scenario request"})
			return
		}
		asset := strings.TrimPrefix(r.URL.Path, "/")
		if asset == "" {
			asset = "index.html"
		}
		contentType, ok := browserAssets[asset]
		if !ok {
			http.NotFound(w, r)
			return
		}
		content, err := os.ReadFile(filepath.Join(webRoot, asset))
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(os.Stderr, "fixture asset:", err)
			}
			http.Error(w, "Missing browser asset", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(content)
	}))
	return server, nil
}
