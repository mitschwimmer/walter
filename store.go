package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var errMissing = errors.New("conversation not found")

type Message struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
	Status  string `json:"status"`
}
type Conversation struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Thinking string    `json:"thinking"`
	Model    string    `json:"model"`
	Created  time.Time `json:"created_at"`
	Updated  time.Time `json:"updated_at"`
	Messages []Message `json:"messages"`
}

// A single process owns this directory. The mutex also makes list and restart
// recovery consistent with atomic replacements. Callers never hold file handles.
type Store struct {
	root string
	mu   sync.Mutex
}

func opaqueID() string { return hex.EncodeToString(randomBytes(16)) }
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func userKey(issuer, subject string) string {
	b, _ := json.Marshal([]string{issuer, subject})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Store) dir(user string) string { return filepath.Join(s.root, "users", user, "conversations") }
func (s *Store) load(user, id string) (*Conversation, error) {
	if !validID.MatchString(id) {
		return nil, errMissing
	}
	b, err := os.ReadFile(filepath.Join(s.dir(user), id+".json"))
	if os.IsNotExist(err) {
		return nil, errMissing
	}
	if err != nil {
		return nil, err
	}
	var c Conversation
	err = json.Unmarshal(b, &c)
	if err == nil && c.ID != id {
		err = errors.New("invalid stored id")
	}
	return &c, err
}
func (s *Store) Get(user, id string) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(user, id)
}
func (s *Store) Save(user string, c *Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(user, c)
}
func (s *Store) save(user string, c *Conversation) error {
	if !validID.MatchString(c.ID) {
		return errors.New("invalid id")
	}
	dir := s.dir(user)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, c.ID+".json")); err != nil {
		return err
	}
	return syncDir(dir)
}
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *Store) List(user string) ([]Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list(user)
}
func (s *Store) list(user string) ([]Conversation, error) {
	out := []Conversation{}
	es, err := os.ReadDir(s.dir(user))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		if !validID.MatchString(id) {
			continue
		}
		c, err := s.load(user, id)
		if err != nil {
			return nil, err
		}
		c.Messages = nil
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}
func (s *Store) Delete(user, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.load(user, id); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(s.dir(user), id+".json")); err != nil {
		return err
	}
	return syncDir(s.dir(user))
}

// Pending generations cannot survive process restart. Recover before accepting traffic.
func (s *Store) Recover() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, err := os.ReadDir(filepath.Join(s.root, "users"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, u := range users {
		if !u.IsDir() {
			continue
		}
		cs, err := s.list(u.Name())
		if err != nil {
			return err
		}
		for _, v := range cs {
			if err := s.recoverConversation(u.Name(), v.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Caller holds s.mu throughout startup recovery.
func (s *Store) recoverConversation(user, id string) error {
	c, err := s.load(user, id)
	if err != nil {
		return err
	}
	changed := false
	for i := range c.Messages {
		if c.Messages[i].Status != "pending" {
			continue
		}
		c.Messages[i].Status = "failed"
		changed = true
	}
	if !changed {
		return nil
	}
	c.Updated = time.Now().UTC()
	return s.save(user, c)
}
