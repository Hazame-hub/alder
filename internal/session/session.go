// Package session holds directory connections in memory, keyed by a cookie.
//
// Rule 5 of the project charter lives here: bind credentials never persist.
// They are held in this process's memory for the life of a session, they are
// not written to disk, not placed in a token the browser can read, and not
// returned by any endpoint. Restarting the server logs everyone out, which is
// the correct behaviour for a tool that holds a directory admin's password.
package session

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
)

// CookieName is the session cookie. The "__Host-" prefix is a browser-enforced
// guarantee: a cookie with it must be Secure, must have no Domain attribute,
// and must have Path=/, which means a subdomain cannot set or overwrite it.
const CookieName = "__Host-alder-session"

// CookieNameInsecure is used when the server is not serving over HTTPS, since
// the __Host- prefix requires Secure and a browser rejects the cookie outright
// without it. Running without TLS is a development convenience and the server
// says so loudly at startup.
const CookieNameInsecure = "alder-session"

// Default lifetimes.
const (
	// DefaultIdleTimeout closes a session that has not been used. A directory
	// connection holding an admin bind should not sit open overnight because
	// someone closed a laptop lid.
	DefaultIdleTimeout = 30 * time.Minute
	// DefaultMaxLifetime closes a session regardless of activity.
	DefaultMaxLifetime = 12 * time.Hour
	// sweepInterval is how often expired sessions are reaped.
	sweepInterval = time.Minute
)

// ErrNotFound reports an unknown, expired, or already-closed session.
var ErrNotFound = errors.New("session: no such session")

// Session is one user's connection to a directory.
type Session struct {
	ID string
	// Conn is the live directory session. It is safe for concurrent use.
	Conn directory.Session
	// Config is the connection configuration, with the password still in it.
	// Nothing outside this package reads Config.BindPassword.
	Config directory.ConnConfig

	CreatedAt time.Time
	lastUsed  time.Time
	ReadOnly  bool
	mu        sync.Mutex
	closed    bool
}

// Host, Port, TLS, BindDN and Verified expose the parts of the configuration
// that are safe to show. The password is deliberately not among them.
func (s *Session) Host() string   { return s.Config.Host }
func (s *Session) Port() int      { return s.Config.Port }
func (s *Session) TLS() string    { return string(s.Config.TLS) }
func (s *Session) BindDN() string { return s.Config.BindDN }

// Verified reports whether the connection verified the server certificate.
func (s *Session) Verified() bool {
	return s.Config.TLS != directory.TLSModePlaintext && !s.Config.InsecureSkipVerify
}

// Store holds live sessions.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	idleTimeout time.Duration
	maxLifetime time.Duration

	stop chan struct{}
	once sync.Once
}

// NewStore returns a running store. Call Close to stop its sweeper.
func NewStore(idleTimeout, maxLifetime time.Duration) *Store {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	if maxLifetime <= 0 {
		maxLifetime = DefaultMaxLifetime
	}
	s := &Store{
		sessions:    map[string]*Session{},
		idleTimeout: idleTimeout,
		maxLifetime: maxLifetime,
		stop:        make(chan struct{}),
	}
	go s.sweep()
	return s
}

// Add stores a connected session and returns its ID.
func (st *Store) Add(conn directory.Session, cfg directory.ConnConfig, readOnly bool) (*Session, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s := &Session{
		ID:        id,
		Conn:      conn,
		Config:    cfg,
		CreatedAt: now,
		lastUsed:  now,
		ReadOnly:  readOnly,
	}
	st.mu.Lock()
	st.sessions[id] = s
	st.mu.Unlock()
	return s, nil
}

// Get returns a session and marks it used. An expired session is removed and
// reported as absent.
func (st *Store) Get(id string) (*Session, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	st.mu.RLock()
	s, ok := st.sessions[id]
	st.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}

	s.mu.Lock()
	expired := s.closed ||
		time.Since(s.lastUsed) > st.idleTimeout ||
		time.Since(s.CreatedAt) > st.maxLifetime
	if !expired {
		s.lastUsed = time.Now()
	}
	s.mu.Unlock()

	if expired {
		st.Remove(id)
		return nil, ErrNotFound
	}
	return s, nil
}

// Remove closes and forgets a session. It is safe to call more than once.
func (st *Store) Remove(id string) {
	st.mu.Lock()
	s, ok := st.sessions[id]
	delete(st.sessions, id)
	st.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	already := s.closed
	s.closed = true
	// The password is cleared as well as the connection closed. It is a small
	// window and the memory may well still hold the bytes, but leaving a live
	// reference to a credential in a map value that something else might
	// retain is a worse habit than the clearing is theatre.
	s.Config.BindPassword = ""
	s.mu.Unlock()
	if !already {
		_ = s.Conn.Close()
	}
}

// Len reports the number of live sessions, for the health endpoint.
func (st *Store) Len() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.sessions)
}

// Close stops the sweeper and closes every session.
func (st *Store) Close() {
	st.once.Do(func() { close(st.stop) })
	st.mu.Lock()
	ids := make([]string, 0, len(st.sessions))
	for id := range st.sessions {
		ids = append(ids, id)
	}
	st.mu.Unlock()
	for _, id := range ids {
		st.Remove(id)
	}
}

func (st *Store) sweep() {
	t := time.NewTicker(sweepInterval)
	defer t.Stop()
	for {
		select {
		case <-st.stop:
			return
		case <-t.C:
			st.reap()
		}
	}
}

func (st *Store) reap() {
	now := time.Now()
	st.mu.RLock()
	var expired []string
	for id, s := range st.sessions {
		s.mu.Lock()
		dead := s.closed ||
			now.Sub(s.lastUsed) > st.idleTimeout ||
			now.Sub(s.CreatedAt) > st.maxLifetime
		s.mu.Unlock()
		if dead {
			expired = append(expired, id)
		}
	}
	st.mu.RUnlock()
	for _, id := range expired {
		st.Remove(id)
	}
}

// newID returns a 256-bit random session identifier.
//
// The identifier is the whole authentication. It is not a signed token and
// carries no claims, because it does not need to: it names an entry in a map
// held by this process, and a process restart invalidates every one of them.
func newID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
