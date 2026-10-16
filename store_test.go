package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fixture() *Conversation {
	return &Conversation{ID: opaqueID(), Title: "Test", Language: "de", Thinking: "quick", Model: "test", Created: time.Now().UTC(), Updated: time.Now().UTC(), Messages: []Message{}}
}
func TestStoreOwnershipAtomicRecovery(t *testing.T) {
	s := &Store{root: t.TempDir()}
	u := userKey("issuer", "alice")
	other := userKey("issuer", "bob")
	c := fixture()
	if err := s.Save(u, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(other, c.ID); err != errMissing {
		t.Fatalf("ownership: %v", err)
	}
	for _, id := range []string{"../escape", "", c.ID + "/.."} {
		if _, err := s.Get(u, id); err != errMissing {
			t.Fatal(id, err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *c
			if err := s.Save(u, &copy); err != nil {
				t.Error(err)
			}
			if _, err := s.Get(u, c.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(s.dir(u), c.ID+".json"))
	if err != nil || !json.Valid(b) {
		t.Fatal("invalid file", err)
	}
	newer := fixture()
	newer.Updated = c.Updated.Add(time.Minute)
	if err := s.Save(u, newer); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(u)
	if err != nil || len(list) != 2 || list[0].ID != newer.ID {
		t.Fatal("order", list, err)
	}
	c.Messages = append(c.Messages, Message{opaqueID(), "assistant", "partial", "pending"})
	if err = s.Save(u, c); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Get(u, c.ID)
	if err != nil || loaded.Messages[0].Status != "failed" {
		t.Fatal("recovery", err)
	}
	if err = s.Delete(other, c.ID); err != errMissing {
		t.Fatal("foreign delete", err)
	}
	if err = s.Delete(u, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(u, c.ID); err != errMissing {
		t.Fatal(err)
	}
	if userKey("a:b", "c") == userKey("a", "b:c") {
		t.Fatal("identity collision")
	}
}
