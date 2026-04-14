// Package session manages server-side sessions stored in Redis or
// Postgres. Each session is a JSON blob keyed by a random, opaque
// session ID. The ID is stored in a cookie; the data lives server-side.
//
// Storage is pluggable via [Store]. The package ships a [RedisStore]
// backed by rueidis and an [MemoryStore] for tests.
//
// Usage:
//
//	mgr := session.NewManager(store, session.Options{
//	    CookieName: "sid",
//	    TTL:        24 * time.Hour,
//	    Secure:     true,
//	})
//
//	// In a handler:
//	sess, err := mgr.Load(r)
//	sess.Set("user_id", "u-123")
//	mgr.Save(w, sess)
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/errors"
)

const (
	defaultCookieName = "sid"
	idBytes           = 32
)

// Session holds the session ID and its data map.
type Session struct {
	ID   string
	data map[string]any
}

func newSession(id string) *Session {
	return &Session{ID: id, data: make(map[string]any)}
}

// Get returns the value for key, or nil.
func (s *Session) Get(key string) any { return s.data[key] }

// Set stores key → val.
func (s *Session) Set(key string, val any) { s.data[key] = val }

// Delete removes key.
func (s *Session) Delete(key string) { delete(s.data, key) }

// Store is the backing store interface.
type Store interface {
	Load(ctx context.Context, id string) (map[string]any, error)
	Save(ctx context.Context, id string, data map[string]any, ttl time.Duration) error
	Delete(ctx context.Context, id string) error
}

// Options tune the session manager.
type Options struct {
	CookieName string
	TTL        time.Duration
	// Secure sets the Secure flag on the cookie (should be true in prod).
	Secure bool
	// SameSite sets the SameSite policy (default Lax).
	SameSite http.SameSite
	// Path is the cookie path (default "/").
	Path string
}

func (o Options) withDefaults() Options {
	if o.CookieName == "" {
		o.CookieName = defaultCookieName
	}
	if o.TTL == 0 {
		o.TTL = 24 * time.Hour
	}
	if o.SameSite == 0 {
		o.SameSite = http.SameSiteLaxMode
	}
	if o.Path == "" {
		o.Path = "/"
	}
	return o
}

// Manager loads, saves, and destroys sessions.
type Manager struct {
	store Store
	opts  Options
}

// NewManager returns a Manager. store must not be nil.
func NewManager(store Store, opts Options) *Manager {
	return &Manager{store: store, opts: opts.withDefaults()}
}

// Load reads the session ID from the request cookie and fetches data
// from the store. If no cookie exists or the session is not found, a
// new empty session is returned (no error).
func (m *Manager) Load(r *http.Request) (*Session, error) {
	cookie, err := r.Cookie(m.opts.CookieName)
	if err != nil {
		return newSession(genID()), nil //nolint:nilerr // missing cookie is not an error; caller gets a fresh session
	}
	data, loadErr := m.store.Load(r.Context(), cookie.Value)
	if loadErr != nil {
		if isNotFound(loadErr) {
			return newSession(genID()), nil
		}
		return nil, fmt.Errorf("session: load: %w", loadErr)
	}
	s := newSession(cookie.Value)
	s.data = data
	return s, nil
}

// Save persists the session and sets the cookie on w.
func (m *Manager) Save(w http.ResponseWriter, s *Session) error {
	if err := m.store.Save(context.Background(), s.ID, s.data, m.opts.TTL); err != nil {
		return fmt.Errorf("session: save: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.opts.CookieName,
		Value:    s.ID,
		Path:     m.opts.Path,
		MaxAge:   int(m.opts.TTL.Seconds()),
		HttpOnly: true,
		Secure:   m.opts.Secure,
		SameSite: m.opts.SameSite,
	})
	return nil
}

// Destroy deletes the session data and expires the cookie.
func (m *Manager) Destroy(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(m.opts.CookieName)
	if err != nil {
		return nil //nolint:nilerr // no cookie = no session to destroy
	}
	if delErr := m.store.Delete(context.Background(), cookie.Value); delErr != nil && !isNotFound(delErr) {
		return fmt.Errorf("session: destroy: %w", delErr)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     m.opts.CookieName,
		Value:    "",
		Path:     m.opts.Path,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   m.opts.Secure,
		SameSite: m.opts.SameSite,
	})
	return nil
}

// MemoryStore is an in-process store for tests. Not safe for
// multi-replica deployments.
type MemoryStore struct {
	data map[string]memEntry
	clk  clockwork.Clock
}

type memEntry struct {
	data    map[string]any
	expires time.Time
}

// NewMemoryStore returns an initialised in-memory store using the real clock.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(clockwork.NewRealClock())
}

// NewMemoryStoreWithClock returns an initialised in-memory store with an injected clock.
func NewMemoryStoreWithClock(clk clockwork.Clock) *MemoryStore {
	return &MemoryStore{data: make(map[string]memEntry), clk: clk}
}

// Load implements [Store].
func (m *MemoryStore) Load(_ context.Context, id string) (map[string]any, error) {
	e, ok := m.data[id]
	if !ok || m.clk.Now().After(e.expires) {
		return nil, gerr.NotFound("session not found")
	}
	// Deep-copy via JSON to prevent mutation.
	b, _ := json.Marshal(e.data)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

// Save implements [Store].
func (m *MemoryStore) Save(_ context.Context, id string, data map[string]any, ttl time.Duration) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("session/memory: marshal: %w", err)
	}
	var cp map[string]any
	_ = json.Unmarshal(b, &cp)
	m.data[id] = memEntry{data: cp, expires: m.clk.Now().Add(ttl)}
	return nil
}

// Delete implements [Store].
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	delete(m.data, id)
	return nil
}

// --- helpers ---

func genID() string {
	b := make([]byte, idBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func isNotFound(err error) bool {
	var e *gerr.Error
	return errors.As(err, &e) && e.Code == gerr.CodeNotFound
}
