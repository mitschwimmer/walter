package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

const prompt = "Was ist Marmelade?"
const answer = "<p>Marmelade ist ein süßer Aufstrich aus Früchten und Zucker.</p><h2>Wichtige Merkmale</h2><ul><li>Früchte</li><li>Zucker</li></ul><table><thead><tr><th>Zutat</th><th>Menge</th></tr></thead><tbody><tr><td>Früchte</td><td>1 kg</td></tr></tbody></table><pre><code>" + "langes_wort_langes_wort_langes_wort_langes_wort_langes_wort_langes_wort_langes_wort_langes_wort_" + "</code></pre>"

type message struct {
	Role string `json:"role"`
	Content string `json:"content"`
	HTML string `json:"html,omitempty"`
	Status string `json:"status"`
}
type conversation struct {
	ID string `json:"id"`
	Title string `json:"title"`
	Thinking string `json:"thinking"`
	Updated string `json:"updated_at"`
	Messages []message `json:"messages"`
	Context map[string]int `json:"context,omitempty"`
}
type submission struct {
	Content string `json:"content"`
	Thinking string `json:"thinking"`
}
type fixture struct {
	mu sync.Mutex
	signedIn, rejectNext, rejectDelete, expireNext, omitContext bool
	sequence, creates int
	quick, thoughtful int
	chats map[string]*conversation
	submissions []submission
	stream chan string
	started chan struct{}
}

func newFixture(name string) *fixture {
	f := &fixture{signedIn: name != "signed-out", quick:4096, thoughtful:8192,
		chats:map[string]*conversation{}, stream:make(chan string, 8), started:make(chan struct{},8)}
	if name == "complete-reply" {
		c := f.create("thoughtful")
		c.Title = prompt
		c.Messages = []message{{Role:"user",Content:prompt,Status:"complete"},{Role:"assistant",Content:"Marmelade",HTML:answer,Status:"complete"}}
	}
	return f
}

func (f *fixture) create(thinking string) *conversation {
	f.sequence++
	f.creates++
	c := &conversation{ID:fmt.Sprintf("%032d", f.sequence), Title:"Neue Frage", Thinking:thinking, Updated:"2026-10-16T18:00:00Z", Messages:[]message{}}
	f.chats[c.ID] = c
	return c
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type","application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (f *fixture) serve(assets string) *httptest.Server {
	files := http.FileServer(http.Dir(assets))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		w.Header().Set("Content-Security-Policy","default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'")
		if r.URL.Path == "/api/site" {
			respond(w,200,map[string]string{"revision":"fixture","imprint_url":"https://example.com/impressum","privacy_url":"https://example.com/datenschutz"})
			return
		}
		if !strings.HasPrefix(r.URL.Path,"/api/") && !strings.HasPrefix(r.URL.Path,"/auth/") {
			files.ServeHTTP(w,r); return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.expireNext { f.expireNext=false; f.signedIn=false }
		if !f.signedIn { respond(w,401,map[string]string{"error":"Bitte erneut anmelden."}); return }
		if r.URL.Path=="/api/session" { respond(w,200,map[string]string{"csrf":"fixture-csrf"}); return }
		if r.Method!="GET" && r.Header.Get("X-CSRF-Token")!="fixture-csrf" { respond(w,403,nil); return }
		if r.URL.Path=="/auth/logout" && r.Method=="POST" { f.signedIn=false; respond(w,200,map[string]string{}); return }
		if r.URL.Path=="/api/conversations" {
			if r.Method=="GET" {
				items:=[]*conversation{}
				// Stable ordering, independent of Go's map iteration.
				for i:=f.sequence;i>0;i-- { if c:=f.chats[fmt.Sprintf("%032d",i)]; c!=nil { items=append(items,c) } }
				respond(w,200,items); return
			}
			var data submission
			if r.Method!="POST" || json.NewDecoder(r.Body).Decode(&data)!=nil || !validMode(data.Thinking) { respond(w,400,nil); return }
			respond(w,201,f.create(data.Thinking)); return
		}
		path:=strings.Split(strings.TrimPrefix(r.URL.Path,"/api/conversations/"),"/")
		c:=f.chats[path[0]]
		if c==nil { respond(w,404,map[string]string{"error":"Gespräch nicht gefunden."}); return }
		switch r.Method {
		case "GET":
			copy:=*c
			if !f.omitContext && len(c.Messages)>0 && c.Messages[len(c.Messages)-1].Status=="complete" {
				copy.Context=map[string]int{"capacity":16384,"quick":f.quick,"thoughtful":f.thoughtful}
			}
			respond(w,200,copy)
		case "DELETE":
			if f.rejectDelete { f.rejectDelete=false; respond(w,503,map[string]string{"error":"Löschen fehlgeschlagen."}); return }
			delete(f.chats,c.ID); w.WriteHeader(204)
		case "POST":
			if len(path)!=2 || path[1]!="messages" { respond(w,404,nil);return }
			if f.rejectNext { f.rejectNext=false;respond(w,503,map[string]string{"error":"Bitte später erneut versuchen."});return }
			var data submission
			if json.NewDecoder(r.Body).Decode(&data)!=nil || !validMode(data.Thinking) || strings.TrimSpace(data.Content)=="" { respond(w,400,nil);return }
			f.submissions=append(f.submissions,data)
			c.Thinking=data.Thinking; c.Title=data.Content
			c.Messages=append(c.Messages,message{Role:"user",Content:data.Content,Status:"complete"},message{Role:"assistant",Status:"pending"})
			index:=len(c.Messages)-1
			f.mu.Unlock()
			f.reply(w,r.Context(),c,index)
			f.mu.Lock()
		default: respond(w,405,nil)
		}
	}))
}
func validMode(value string) bool { return value=="quick" || value=="thoughtful" }

func (f *fixture) reply(w http.ResponseWriter, ctx context.Context, c *conversation, index int) {
	w.Header().Set("Content-Type","text/event-stream")
	w.WriteHeader(200)
	flush:=w.(http.Flusher)
	event:=func(name string,data any) {
		b,_:=json.Marshal(data)
		_,_=fmt.Fprintf(w,"event: %s\ndata: %s\n\n",name,b)
		flush.Flush()
	}
	event("start",map[string]string{})
	select { case f.started<-struct{}{}: case <-ctx.Done(): }
	for {
		select {
		case <-ctx.Done():
			f.mu.Lock(); c.Messages[index].Status="cancelled"; f.mu.Unlock(); return
		case command:=<-f.stream:
			switch command {
			case "progress": event("progress",map[string]any{"phase":"thinking","tokens":123})
			case "partial":
				f.mu.Lock(); c.Messages[index].HTML="<p>Marmelade</p>";c.Messages[index].Content="Marmelade";f.mu.Unlock()
				event("snapshot",map[string]string{"html":"<p>Marmelade</p>"})
			case "done":
				f.mu.Lock();c.Messages[index].HTML=answer;c.Messages[index].Content="Marmelade";c.Messages[index].Status="complete";f.mu.Unlock()
				event("snapshot",map[string]string{"html":answer});event("done",map[string]string{});return
			}
		}
	}
}
