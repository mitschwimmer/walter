package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// Run the shipped browser ES module itself, not a duplicate SSE parser in Go.
// Covers the same byte-split/UTF-8/CRLF/multiline/truncated/large-frame cases
// that formerly required Node's ReadableStream implementation.
func TestSSEBrowserModule(t *testing.T) {
	bin := os.Getenv("WALTER_BROWSER_EXECUTABLE")
	if bin == "" {
		if path, ok := launcher.LookPath(); ok {
			bin = path
		}
	}
	if bin == "" {
		t.Skip("install Chromium or set WALTER_BROWSER_EXECUTABLE")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Join(root, "web"))))
	defer srv.Close()
	l := launcher.New().Bin(bin).Headless(true).NoSandbox(os.Getenv("WALTER_BROWSER_NO_SANDBOX") == "1").Set("disable-dev-shm-usage")
	url, err := l.Launch()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Kill()
	browser := rod.New().ControlURL(url).NoDefaultDevice()
	if err := browser.Connect(); err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.Page(proto.TargetCreateTarget{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	result, err := page.Eval(`async () => {
      const {readEvents} = await import('/sse.js');
      const input = ': comment\r\nevent: delta\r\ndata: {"text":"Gründlich 😀"}\r\n\r\nevent: done\ndata: {"message_id":\n' + 'data: "123"}\n\n';
      const bytes = new TextEncoder().encode(input);
      for (let size = 1; size <= bytes.length; size++) {
        const got = [];
        const stream = new ReadableStream({start(c) {
          for (let i = 0; i < bytes.length; i += size) c.enqueue(bytes.slice(i, i + size));
          c.close();
        }});
        await readEvents(stream, (...event) => got.push(event));
        if (JSON.stringify(got) !== JSON.stringify([
          ['delta', {text:'Gründlich 😀'}], ['done', {message_id:'123'}]])) {
          throw new Error('corrupt SSE events at chunk size '+size);
        }
      }
      let truncated = false;
      try {
        await readEvents(new ReadableStream({start(c) {
          c.enqueue(new TextEncoder().encode('data: {}')); c.close();
        }}), () => {});
      } catch (err) { truncated = /unvollständig/.test(err.message); }
      if (!truncated) throw new Error('truncated frames not rejected');
      const html = '<p>' + '&lt;'.repeat(300000) + '</p>';
      const snapshot = new TextEncoder().encode('event: snapshot\ndata: ' +
        JSON.stringify({html}) + '\n\nevent: done\ndata: {}\n\n');
      const received = [];
      await readEvents(new ReadableStream({start(c) {c.enqueue(snapshot); c.close();}}),
        (...event) => received.push(event));
      if (received.length !== 2 || received[0][0] !== 'snapshot' ||
          received[0][1].html !== html || received[1][0] !== 'done') {
        throw new Error('large HTML snapshots were corrupted');
      }
      return true;
    }`)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Value.Bool() {
		t.Fatal("SSE test returned false")
	}
}
