// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package session manages server-side sessions through a pluggable Store.
// Each session is keyed by a random opaque ID stored in a cookie.
//
// Storage is pluggable via [Store]. The package ships [MemoryStore] for tests;
// applications provide their own Redis or Postgres implementation.
//
// Usage:
//
//	mgr, err := session.NewManager(store, session.Options{
//	    CookieName: "sid",
//	    TTL:        24 * time.Hour,
//	})
//	if err != nil { /* handle configuration error */ }
//
//	// In a handler:
//	sess, err := mgr.Load(r)
//	sess.Set("user_id", "u-123")
//	mgr.SaveContext(r.Context(), w, sess)
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
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
	// AllowInsecureCookie disables the Secure flag for isolated HTTP
	// development. Cookies are secure by default.
	AllowInsecureCookie bool
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

// NewManager returns a Manager after validating its store and lifetime.
func NewManager(store Store, opts Options) (*Manager, error) {
	if validate.IsNil(store) {
		return nil, errors.New("session: store is required")
	}
	if opts.TTL < 0 {
		return nil, errors.New("session: TTL must not be negative")
	}
	return &Manager{store: store, opts: opts.withDefaults()}, nil
}

// Load reads the session ID from the request cookie and fetches data
// from the store. If no cookie exists or the session is not found, a
// new empty session is returned; that path only fails when a fresh
// session ID cannot be generated.
func (m *Manager) Load(r *http.Request) (*Session, error) {
	cookie, err := r.Cookie(m.opts.CookieName)
	if err != nil {
		// Missing cookie is not an error; the caller gets a fresh session.
		return newEmptySession()
	}
	data, loadErr := m.store.Load(r.Context(), cookie.Value)
	if loadErr != nil {
		if isNotFound(loadErr) {
			return newEmptySession()
		}
		return nil, fmt.Errorf("session: load: %w", loadErr)
	}
	s := newSession(cookie.Value)
	s.data = data
	return s, nil
}

// SaveContext persists the session using ctx and sets the cookie on w.
func (m *Manager) SaveContext(ctx context.Context, w http.ResponseWriter, s *Session) error {
	if err := m.store.Save(ctx, s.ID, s.data, m.opts.TTL); err != nil {
		return fmt.Errorf("session: save: %w", err)
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is default-on with explicit development opt-out // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure defaults true; AllowInsecureCookie is explicit
		Name:     m.opts.CookieName,
		Value:    s.ID,
		Path:     m.opts.Path,
		MaxAge:   int(m.opts.TTL.Seconds()),
		HttpOnly: true,
		Secure:   !m.opts.AllowInsecureCookie,
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
	if delErr := m.store.Delete(r.Context(), cookie.Value); delErr != nil && !isNotFound(delErr) {
		return fmt.Errorf("session: destroy: %w", delErr)
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- Secure is default-on with explicit development opt-out // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure -- Secure defaults true; AllowInsecureCookie is explicit
		Name:     m.opts.CookieName,
		Value:    "",
		Path:     m.opts.Path,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   !m.opts.AllowInsecureCookie,
		SameSite: m.opts.SameSite,
	})
	return nil
}

// MemoryStore is an in-process store for tests. Not safe for
// multi-replica deployments.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]memEntry
	clk  clockwork.Clock
}

type memEntry struct {
	data    map[string]any
	expires time.Time
}

// NewMemoryStore returns an initialised in-memory store using the real clock.
func NewMemoryStore() *MemoryStore {
	return newMemoryStore(clockwork.NewRealClock())
}

// NewMemoryStoreWithClock returns an initialised in-memory store with an injected clock.
func NewMemoryStoreWithClock(clk clockwork.Clock) (*MemoryStore, error) {
	if validate.IsNil(clk) {
		return nil, errors.New("session/memory: clock is required")
	}
	return newMemoryStore(clk), nil
}

// Load implements [Store].
func (m *MemoryStore) Load(ctx context.Context, id string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session/memory: load: %w", err)
	}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("session/memory: load: %w", err)
	}
	e, ok := m.data[id]
	if !ok || !m.clk.Now().Before(e.expires) {
		if ok {
			delete(m.data, id)
		}
		m.mu.Unlock()
		return nil, gerr.NotFound("session not found")
	}
	m.mu.Unlock()
	// Deep-copy via JSON to prevent mutation.
	b, err := json.Marshal(e.data)
	if err != nil {
		return nil, fmt.Errorf("session/memory: marshal: %w", err)
	}
	var out map[string]any
	if unmarshalErr := json.Unmarshal(b, &out); unmarshalErr != nil {
		return nil, fmt.Errorf("session/memory: unmarshal: %w", unmarshalErr)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("session/memory: load: %w", err)
	}
	return out, nil
}

// Save implements [Store].
func (m *MemoryStore) Save(ctx context.Context, id string, data map[string]any, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("session/memory: save: %w", err)
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("session/memory: marshal: %w", err)
	}
	var cp map[string]any
	if unmarshalErr := json.Unmarshal(b, &cp); unmarshalErr != nil {
		return fmt.Errorf("session/memory: unmarshal: %w", unmarshalErr)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("session/memory: save: %w", err)
	}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("session/memory: save: %w", err)
	}
	m.data[id] = memEntry{data: cp, expires: m.clk.Now().Add(ttl)}
	m.mu.Unlock()
	return nil
}

// Delete implements [Store].
func (m *MemoryStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("session/memory: delete: %w", err)
	}
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("session/memory: delete: %w", err)
	}
	delete(m.data, id)
	m.mu.Unlock()
	return nil
}

// --- helpers ---

// newEmptySession mints a fresh session under a random ID.
func newEmptySession() (*Session, error) {
	id, err := genID()
	if err != nil {
		return nil, err
	}
	return newSession(id), nil
}

func genID() (string, error) {
	b := make([]byte, idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session: generate id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isNotFound(err error) bool {
	var e *gerr.Error
	return errors.As(err, &e) && e.Code == gerr.CodeNotFound
}

func newMemoryStore(clk clockwork.Clock) *MemoryStore {
	return &MemoryStore{data: make(map[string]memEntry), clk: clk}
}
