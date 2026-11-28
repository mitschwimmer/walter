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
	"theme.js":             "text/javascript; charset=utf-8",
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
	case "empty-chat", "streaming-reply", "history-with-conversations", "completed-reply", "interrupted-reply":
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
	completed := conversation{
		ID: "00000000000000000000000000000004", Title: "Wie funktioniert eine Webseite?",
		Thinking: "thoughtful", UpdatedAt: "2026-10-16T18:00:00Z",
		Messages: []any{
			map[string]string{"role": "user", "content": "Wie funktioniert eine Webseite?", "status": "complete"},
			map[string]string{"role": "assistant", "status": "complete", "content": "HTML und CSS bilden die Grundlage einer Webseite.",
				"html": `<p>Eine Webseite besteht aus mehreren Bausteinen. <strong>HTML</strong> beschreibt den Inhalt, CSS das Aussehen.</p><h2>Die drei Bausteine</h2><table><thead><tr><th>Sprache</th><th>Aufgabe</th></tr></thead><tbody><tr><td>HTML</td><td>Inhalt und Struktur</td></tr><tr><td>CSS</td><td>Farben und Layout</td></tr><tr><td>JavaScript</td><td>Interaktion</td></tr></tbody></table><h2>Ein kleines Beispiel</h2><pre><code>&lt;h1&gt;Hallo Welt!&lt;/h1&gt;
&lt;p&gt;Das ist meine erste Webseite.&lt;/p&gt;</code></pre><p>Du kannst eine HTML-Datei direkt in deinem Browser öffnen.</p>`},
		},
	}
	interrupted := sample
	interrupted.Messages = []any{
		map[string]string{"role": "user", "content": "Was ist Marmelade?", "status": "complete"},
		map[string]string{"role": "assistant", "content": "Marmelade ist ein süßer Aufstrich.", "html": "<p>Marmelade ist ein süßer Aufstrich.</p>", "status": "cancelled"},
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
			if name == "interrupted-reply" {
				respond(http.StatusOK, map[string]string{"revision": "10d95b1"})
			} else {
				respond(http.StatusOK, map[string]string{})
			}
			return
		case r.URL.Path == "/api/session":
			respond(http.StatusOK, map[string]string{"csrf": "scenario-token"})
			return
		case r.URL.Path == "/api/conversations" && r.Method == http.MethodGet:
			if name == "history-with-conversations" {
				respond(http.StatusOK, history)
			} else if name == "completed-reply" {
				respond(http.StatusOK, []conversation{completed})
			} else {
				respond(http.StatusOK, []conversation{})
			}
			return
		case r.URL.Path == "/api/conversations/"+completed.ID && r.Method == http.MethodGet && name == "completed-reply":
			respond(http.StatusOK, completed)
			return
		case r.URL.Path == "/api/conversations/"+sample.ID && r.Method == http.MethodGet && name == "interrupted-reply" && created.Load():
			respond(http.StatusOK, interrupted)
			return
		case r.URL.Path == "/api/conversations" && r.Method == http.MethodPost && (name == "streaming-reply" || name == "interrupted-reply"):
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
		case r.URL.Path == "/api/conversations/"+sample.ID+"/messages" && r.Method == http.MethodPost && (name == "streaming-reply" || name == "interrupted-reply") && created.Load():
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
			if name == "interrupted-reply" {
				w.Header().Set("X-Walter-Request-ID", "00000000000000000000000000000042")
				w.Header().Set("X-Walter-Build", "10d95b1")
				// Return fewer bytes than promised, as with a broken HTTP stream.
				w.Header().Set("Content-Length", "4096")
				_, _ = io.WriteString(w, "event: start\ndata: {}\n\nevent: snapshot\ndata: {\"html\":\"<p>Marmelade ist ein süßer Aufstrich.</p>\"}\n\n")
				_ = http.NewResponseController(w).Flush()
				return
			}
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
