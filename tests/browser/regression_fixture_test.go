package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var fullAnswer = `<p>Marmelade ist ein süßer Aufstrich aus Früchten und Zucker.</p><h2>Wichtige Merkmale</h2><ul><li>Früchte</li><li>Zucker</li></ul><p><strong>Gut zu wissen:</strong> Walter kann auch Tabellen und Code darstellen.</p><table><thead><tr><th>Zutat</th><th>Menge</th></tr></thead><tbody><tr><td>Früchte</td><td>1 kg</td></tr></tbody></table><pre><code>&lt;script&gt;kein ausführbarer Code&lt;/script&gt;` + "\n" + strings.Repeat("langes_wort_", 30) + `</code></pre>`

type regressionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	HTML    string `json:"html,omitempty"`
	Status  string `json:"status"`
}
type regressionConversation struct {
	ID        string              `json:"id"`
	Title     string              `json:"title"`
	Thinking  string              `json:"thinking"`
	UpdatedAt string              `json:"updated_at"`
	Messages  []regressionMessage `json:"messages"`
}
type regressionFixture struct {
	t                                    *testing.T
	root                                 string
	server                               *httptest.Server
	mu                                   sync.Mutex
	signedIn, footerLinks, contextKnown  bool
	expireNext, rejectNext, rejectDelete bool
	contextQuick, contextThoughtful      int
	seq                                  int
	order                                []string
	conversations                        map[string]*regressionConversation
	submissions                          []string
	creations                            []string
	sessionHold, reloadHold, answerHold  chan struct{}
	progress                             chan any
	handlerErrors                        []error
}

func newRegressionFixture(t *testing.T) *regressionFixture {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	f := &regressionFixture{
		t: t, root: root, signedIn: true, footerLinks: true, contextKnown: true,
		contextQuick: 4096, contextThoughtful: 8192,
		conversations: make(map[string]*regressionConversation),
		sessionHold:   make(chan struct{}), progress: make(chan any, 16),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}
func (f *regressionFixture) close()      { f.server.CloseClientConnections(); f.server.Close() }
func (f *regressionFixture) url() string { return f.server.URL }
func (f *regressionFixture) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func (f *regressionFixture) bad(w http.ResponseWriter, code int, message string) {
	f.json(w, code, map[string]string{"error": message})
}
func (f *regressionFixture) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlerErrors = append(f.handlerErrors, err)
}
func (f *regressionFixture) errors() []error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]error(nil), f.handlerErrors...)
}
func (f *regressionFixture) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/site" {
		f.mu.Lock()
		links := f.footerLinks
		f.mu.Unlock()
		site := map[string]string{"revision": "10d95b1"}
		if links {
			site["imprint_url"] = "https://example.com/impressum"
			site["privacy_url"] = "https://example.com/datenschutz"
		}
		f.json(w, 200, site)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/auth/") {
		filename := strings.TrimPrefix(r.URL.Path, "/")
		if filename == "" {
			filename = "index.html"
		}
		contentType, ok := browserAssets[filename]
		if !ok {
			http.NotFound(w, r)
			return
		}
		bytes, err := os.ReadFile(filepath.Join(f.root, filename))
		if err != nil {
			f.fail(err)
			http.Error(w, "Missing asset", 500)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(bytes)
		return
	}
	f.mu.Lock()
	if f.expireNext {
		f.expireNext = false
		f.signedIn = false
	}
	signed := f.signedIn
	f.mu.Unlock()
	if !signed {
		f.bad(w, 401, "Bitte erneut anmelden.")
		return
	}
	if r.URL.Path == "/api/session" {
		f.mu.Lock()
		hold := f.sessionHold
		f.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		f.json(w, 200, map[string]string{"csrf": "csrf"})
		return
	}
	if r.Method == "POST" && r.URL.Path == "/auth/logout" {
		f.mu.Lock()
		f.signedIn = false
		f.mu.Unlock()
		f.json(w, 200, map[string]string{})
		return
	}
	if r.Method != http.MethodGet && r.Header.Get("X-CSRF-Token") != "csrf" {
		f.fail(fmt.Errorf("missing CSRF header for %s %s", r.Method, r.URL.Path))
		f.bad(w, 403, "CSRF token missing")
		return
	}
	var input map[string]any
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		payload, err := io.ReadAll(io.LimitReader(r.Body, 40000))
		if err != nil {
			f.fail(err)
			f.bad(w, 400, "Bad request")
			return
		}
		if len(payload) > 0 && json.Unmarshal(payload, &input) != nil {
			f.fail(fmt.Errorf("bad JSON"))
			f.bad(w, 400, "Bad JSON")
			return
		}
	}
	if r.URL.Path == "/api/conversations" {
		f.mu.Lock()
		if r.Method == http.MethodGet {
			conversations := make([]*regressionConversation, 0, len(f.order))
			for i := len(f.order) - 1; i >= 0; i-- {
				if c := f.conversations[f.order[i]]; c != nil {
					conversations = append(conversations, c)
				}
			}
			out, _ := json.Marshal(conversations)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(out)
			return
		}
		thinking, _ := input["thinking"].(string)
		if len(input) != 1 || (thinking != "quick" && thinking != "thoughtful") {
			f.mu.Unlock()
			f.fail(fmt.Errorf("invalid creation payload: %v", input))
			f.bad(w, 400, "Invalid creation")
			return
		}
		f.creations = append(f.creations, thinking)
		f.seq++
		id := fmt.Sprintf("%032d", f.seq)
		c := &regressionConversation{ID: id, Title: "Neue Frage", Thinking: thinking, UpdatedAt: "2026-10-16T18:00:00Z", Messages: []regressionMessage{}}
		f.conversations[id] = c
		f.order = append(f.order, id)
		f.mu.Unlock()
		f.json(w, 201, c)
		return
	}
	const prefix = "/api/conversations/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		f.bad(w, 404, "Unknown URL")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, prefix)
	isMessage := strings.HasSuffix(suffix, "/messages")
	if isMessage {
		suffix = strings.TrimSuffix(suffix, "/messages")
	}
	if strings.Contains(suffix, "/") {
		f.bad(w, 404, "Unknown URL")
		return
	}
	f.mu.Lock()
	c := f.conversations[suffix]
	if c == nil {
		f.mu.Unlock()
		f.bad(w, 404, "Gespräch nicht gefunden.")
		return
	}
	if r.Method == http.MethodGet && !isMessage {
		hold := f.reloadHold
		f.mu.Unlock()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		f.mu.Lock()
		c = f.conversations[suffix]
		if c == nil {
			f.mu.Unlock()
			f.bad(w, 404, "Gespräch nicht gefunden.")
			return
		}
		response := map[string]any{"id": c.ID, "title": c.Title, "thinking": c.Thinking, "updated_at": c.UpdatedAt, "messages": c.Messages}
		if f.contextKnown && len(c.Messages) > 0 && c.Messages[len(c.Messages)-1].Status == "complete" {
			response["context"] = map[string]int{"capacity": 16384, "quick": f.contextQuick, "thoughtful": f.contextThoughtful}
		}
		f.mu.Unlock()
		f.json(w, 200, response)
		return
	}
	if r.Method == http.MethodDelete && !isMessage {
		if f.rejectDelete {
			f.rejectDelete = false
			f.mu.Unlock()
			f.bad(w, 503, "Löschen fehlgeschlagen.")
			return
		}
		delete(f.conversations, suffix)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost || !isMessage {
		f.mu.Unlock()
		f.bad(w, 405, "Unsupported method")
		return
	}
	if f.rejectNext {
		f.rejectNext = false
		f.mu.Unlock()
		f.bad(w, 503, "Bitte später erneut versuchen.")
		return
	}
	thinking, _ := input["thinking"].(string)
	content, _ := input["content"].(string)
	if len(input) != 2 || (thinking != "quick" && thinking != "thoughtful") || content == "" {
		f.mu.Unlock()
		f.fail(fmt.Errorf("invalid message input: %v", input))
		f.bad(w, 400, "Invalid message")
		return
	}
	c.Thinking = thinking
	c.Title = content
	if len(c.Title) > 60 {
		c.Title = c.Title[:60]
	}
	f.submissions = append(f.submissions, content)
	c.Messages = append(c.Messages, regressionMessage{Role: "user", Content: content, Status: "complete"}, regressionMessage{Role: "assistant", Content: "Marmelade", HTML: "<p>Marmelade</p>", Status: "pending"})
	replyIndex := len(c.Messages) - 1
	hold := f.answerHold
	f.answerHold = nil
	progress := f.progress
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	writer := func(event string, payload any) bool {
		b, err := json.Marshal(payload)
		if err != nil {
			f.fail(err)
			return false
		}
		if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return true
	}
	writer("start", map[string]any{})
	if hold != nil {
		for {
			select {
			case <-hold:
				goto released
			case data := <-progress:
				writer("progress", data)
			case <-r.Context().Done():
				f.markCancelled(suffix, replyIndex)
				return
			}
		}
	}
released:
	if r.Context().Err() != nil {
		f.markCancelled(suffix, replyIndex)
		return
	}
	if !writer("snapshot", map[string]string{"html": "<p>Marmelade</p>"}) {
		f.markCancelled(suffix, replyIndex)
		return
	}
	select {
	case <-time.After(90 * time.Millisecond):
	case <-r.Context().Done():
		f.markCancelled(suffix, replyIndex)
		return
	}
	if !writer("snapshot", map[string]string{"html": "<p><strong>Marmelade</strong> ist ein Aufstrich.</p>"}) {
		f.markCancelled(suffix, replyIndex)
		return
	}
	select {
	case <-time.After(260 * time.Millisecond):
	case <-r.Context().Done():
		f.markCancelled(suffix, replyIndex)
		return
	}
	f.mu.Lock()
	if active := f.conversations[suffix]; active != nil && replyIndex < len(active.Messages) {
		active.Messages[replyIndex].HTML = fullAnswer
		active.Messages[replyIndex].Status = "complete"
	}
	f.mu.Unlock()
	writer("snapshot", map[string]string{"html": fullAnswer})
	writer("done", map[string]any{})
}
func (f *regressionFixture) markCancelled(id string, index int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.conversations[id]; c != nil && index < len(c.Messages) && c.Messages[index].Status == "pending" {
		c.Messages[index].Status = "cancelled"
	}
}
func (f *regressionFixture) setContext(known bool, quick, thoughtful int) {
	f.mu.Lock()
	f.contextKnown = known
	f.contextQuick = quick
	f.contextThoughtful = thoughtful
	f.mu.Unlock()
}
func (f *regressionFixture) setSignedIn(v bool)    { f.mu.Lock(); f.signedIn = v; f.mu.Unlock() }
func (f *regressionFixture) setExpire()            { f.mu.Lock(); f.expireNext = true; f.mu.Unlock() }
func (f *regressionFixture) setRejectNext()        { f.mu.Lock(); f.rejectNext = true; f.mu.Unlock() }
func (f *regressionFixture) setRejectDelete()      { f.mu.Lock(); f.rejectDelete = true; f.mu.Unlock() }
func (f *regressionFixture) setFooterLinks(v bool) { f.mu.Lock(); f.footerLinks = v; f.mu.Unlock() }
func (f *regressionFixture) holdNextAnswer() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := make(chan struct{})
	f.answerHold = c
	return c
}
func (f *regressionFixture) holdNextReload() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := make(chan struct{})
	f.reloadHold = c
	return c
}
func (f *regressionFixture) clearReload() { f.mu.Lock(); f.reloadHold = nil; f.mu.Unlock() }
func (f *regressionFixture) createHistoryItem(title string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	id := strings.Repeat("0", 32-len(strconv.Itoa(f.seq))) + strconv.Itoa(f.seq)
	f.conversations[id] = &regressionConversation{ID: id, Title: title, Thinking: "quick", UpdatedAt: "2026-10-16T18:00:00Z", Messages: []regressionMessage{}}
	f.order = append(f.order, id)
	return id
}
func (f *regressionFixture) stats() (creations int, submissions []string, convs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.creations), append([]string(nil), f.submissions...), len(f.conversations)
}
func (f *regressionFixture) lastConversation() regressionConversation {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.order) - 1; i >= 0; i-- {
		if c := f.conversations[f.order[i]]; c != nil {
			return *c
		}
	}
	return regressionConversation{}
}
