package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGermanConversations(t *testing.T) {
	a := testApp(t)
	for _, body := range []string{`{"thinking":"quick"}`, `{"thinking":"thoughtful"}`, `{"language":"en","thinking":"quick"}`} {
		w := request(a, "POST", "/api/conversations", body, "a")
		if w.Code != 201 || strings.Contains(w.Body.String(), `"language"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"thinking":"invalid"}`} {
		if w := request(a, "POST", "/api/conversations", body, "a"); w.Code != 400 {
			t.Fatal("accepted invalid thinking mode", w.Code)
		}
	}
	// Old on-disk language metadata is ignored without losing existing messages.
	c := fixture()
	if err := a.store.Save("a", c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.store.dir("a"), c.ID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), "{", `{"language":"en",`, 1))
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.store.Get("a", c.ID)
	if err != nil || loaded.ID != c.ID || loaded.Thinking != c.Thinking {
		t.Fatal(loaded, err)
	}
	w := request(a, "GET", "/api/conversations/"+c.ID, "", "a")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"language"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPublicSiteLinks(t *testing.T) {
	a := testApp(t)
	a.cfg.ImprintURL = "https://example.com/impressum"
	a.cfg.PrivacyURL = "https://example.com/datenschutz"
	a.cfg.ClientSecret = "private"
	w := request(a, "GET", "/api/site", "", "")
	var site map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &site) != nil || len(site) != 3 || site["imprint_url"] != a.cfg.ImprintURL || site["privacy_url"] != a.cfg.PrivacyURL || site["revision"] != buildRevision() {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private") {
		t.Fatal("configuration leaked")
	}
}
