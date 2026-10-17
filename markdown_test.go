package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMarkdown(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want, absent []string
	}{
		{"formatting", "# Heading\n\n**bold** and `code`\n\n- one\n- two\n\n> quote\n\n[Go](https://go.dev)\n\n```go\nfmt.Println(\"<hello>\")\n```\n\n| A | B |\n|---|---|\n| 1 | 2 |", []string{"<h1>Heading</h1>", "<strong>bold</strong>", "<code>code</code>", "<ul>", "<blockquote>", `href="https://go.dev"`, `<pre><code class="language-go">`, "&lt;hello&gt;", "<table>", "<td>2</td>"}, nil},
		{"unclosed fence", "```go\n<unfinished>", []string{"<pre><code", "&lt;unfinished&gt;", "</code></pre>"}, nil},
		{"unclosed emphasis", "**unfinished", []string{"**unfinished"}, nil},
		{"unsafe content", "<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[bad](javascript:alert%281%29)\n\n![bad](data:text/html,evil)", nil, []string{"<script", "<img src=x", "onerror", "javascript:", "data:text/html"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html, err := renderMarkdown(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Errorf("missing %q in %s", want, html)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(html, absent) {
					t.Errorf("unsafe %q in %s", absent, html)
				}
			}
		})
	}
}

func TestMarkdownStreamAndReload(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "complete", false: "partial"}[complete], func(t *testing.T) {
			a := testApp(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, text := range []string{"**Hel", "lo**\n\n```go\n", "<ok>\n```"} {
					b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": text}}}})
					io.WriteString(w, "data: "+string(b)+"\n\n")
				}
				if complete {
					io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer upstream.Close()
			a.cfg.Endpoint = upstream.URL
			c := fixture()
			if err := a.store.Save("a", c); err != nil {
				t.Fatal(err)
			}
			w := request(a, "POST", "/api/conversations/"+c.ID+"/messages", `{"content":"<b>Hi</b>"}`, "a")
			var snapshots []string
			var terminal bool
			for _, frame := range strings.Split(w.Body.String(), "\n\n") {
				if strings.HasPrefix(frame, "event: snapshot\n") {
					if terminal {
						t.Fatal("snapshot after terminal event")
					}
					var data struct{ HTML string }
					if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: snapshot\ndata: ")), &data); err != nil {
						t.Fatal(err)
					}
					snapshots = append(snapshots, data.HTML)
				} else if strings.HasPrefix(frame, "event: done\n") || strings.HasPrefix(frame, "event: error\n") {
					terminal = true
				}
			}
			if len(snapshots) < 2 || !terminal {
				t.Fatal("missing first/final snapshots or terminal event", w.Body.String())
			}
			last := snapshots[len(snapshots)-1]
			if !strings.Contains(last, "<strong>Hello</strong>") || !strings.Contains(last, "&lt;ok&gt;") {
				t.Fatal(last)
			}
			loaded := request(a, "GET", "/api/conversations/"+c.ID, "", "a")
			var view struct{ Messages []messageHTML }
			if err := json.Unmarshal(loaded.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			if view.Messages[1].HTML != last || view.Messages[0].HTML != "" || view.Messages[0].Content != "<b>Hi</b>" {
				t.Fatal(view)
			}
			raw, err := os.ReadFile(filepath.Join(a.store.dir("a"), c.ID+".json"))
			if err != nil || strings.Contains(string(raw), `"html"`) {
				t.Fatal("HTML must not be persisted", string(raw), err)
			}
		})
	}
}

func TestMarkdownFlushWhileUpstreamPaused(t *testing.T) {
	a := testApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"**Hel\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo**\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	a.cfg.Endpoint = upstream.URL
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var reply Message
	var snapshots []string
	stop := errors.New("client write failed")
	err := a.streamReply(ctx, fixture(), &reply, func(html string) error {
		snapshots = append(snapshots, html)
		if strings.Contains(html, "<strong>Hello</strong>") {
			return stop
		}
		return nil
	}, nil)
	if !errors.Is(err, stop) || len(snapshots) != 2 || reply.Content != "**Hello**" {
		t.Fatal(err, snapshots, reply)
	}
}
