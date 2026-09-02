package session

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// fakeSession is a directory.Session that records whether it was closed. The
// store's whole job is holding these and letting go of them at the right time,
// so "was it closed" is the only behaviour worth faking.
type fakeSession struct {
	mu     sync.Mutex
	closed int
}

func (f *fakeSession) Capabilities() directory.Capabilities { return directory.Capabilities{} }
func (f *fakeSession) Schema(context.Context) (*schema.Schema, error) {
	return nil, errors.New("not used")
}
func (f *fakeSession) Search(context.Context, directory.SearchRequest) (*directory.SearchResult, error) {
	return nil, errors.New("not used")
}
func (f *fakeSession) Read(context.Context, dn.DN, []string) (*directory.Entry, error) {
	return nil, errors.New("not used")
}
func (f *fakeSession) Apply(context.Context, directory.ChangeRecord) error {
	return errors.New("not used")
}
func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}
func (f *fakeSession) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// testLogger discards output: these tests assert on behaviour, and a store
// that logs every open and close would bury the failures.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() directory.ConnConfig {
	return directory.ConnConfig{
		Host:         "directory.example.test",
		Port:         636,
		TLS:          directory.TLSModeLDAPS,
		BindDN:       "cn=admin,dc=example,dc=test",
		BindPassword: "hunter2",
	}
}

func TestAddAndGet(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	conn := &fakeSession{}
	sess, err := store.Add(conn, testConfig(), false)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("Add returned a session with no ID")
	}

	got, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != sess {
		t.Error("Get returned a different session")
	}
	if store.Len() != 1 {
		t.Errorf("Len = %d, want 1", store.Len())
	}
}

func TestGetRejectsUnknownAndEmptyIDs(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	for _, id := range []string{"", "not-a-session", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := store.Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

// TestSessionIDsAreUnpredictable is a weak test of a property that matters: the
// identifier is the whole authentication, so two sessions must never collide
// and an ID must carry enough entropy that guessing one is hopeless.
func TestSessionIDsAreUnpredictable(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		sess, err := store.Add(&fakeSession{}, testConfig(), false)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		if seen[sess.ID] {
			t.Fatalf("session ID %q was issued twice", sess.ID)
		}
		seen[sess.ID] = true
		// 32 random bytes, base64url without padding, is 43 characters.
		if len(sess.ID) < 40 {
			t.Fatalf("session ID %q is only %d characters", sess.ID, len(sess.ID))
		}
	}
}

func TestRemoveClosesTheConnectionAndClearsThePassword(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	conn := &fakeSession{}
	sess, err := store.Add(conn, testConfig(), false)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	store.Remove(sess.ID)

	if conn.closeCount() != 1 {
		t.Errorf("the directory connection was closed %d times, want exactly 1", conn.closeCount())
	}
	if sess.Config.BindPassword != "" {
		t.Error("the bind password survived Remove")
	}
	if _, err := store.Get(sess.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Remove = %v, want ErrNotFound", err)
	}
	if store.Len() != 0 {
		t.Errorf("Len = %d after Remove, want 0", store.Len())
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	// A user clicking Disconnect twice, or a disconnect racing the sweeper,
	// must not close the same connection twice.
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	conn := &fakeSession{}
	sess, _ := store.Add(conn, testConfig(), false)

	store.Remove(sess.ID)
	store.Remove(sess.ID)
	store.Remove("never-existed")

	if conn.closeCount() != 1 {
		t.Errorf("Close was called %d times, want exactly 1", conn.closeCount())
	}
}

func TestIdleTimeoutExpiresASession(t *testing.T) {
	store := NewStore(testLogger(), 20*time.Millisecond, time.Hour)
	defer store.Close()

	conn := &fakeSession{}
	sess, _ := store.Add(conn, testConfig(), false)

	time.Sleep(60 * time.Millisecond)

	if _, err := store.Get(sess.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after the idle timeout = %v, want ErrNotFound", err)
	}
	if conn.closeCount() != 1 {
		t.Errorf("an expired session did not close its connection (closed %d times)", conn.closeCount())
	}
}

func TestUseKeepsASessionAlive(t *testing.T) {
	// The idle timeout is idle, not absolute: a session in active use must not
	// be closed out from under someone mid-edit.
	store := NewStore(testLogger(), 80*time.Millisecond, time.Hour)
	defer store.Close()

	sess, _ := store.Add(&fakeSession{}, testConfig(), false)

	deadline := time.Now().Add(240 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := store.Get(sess.ID); err != nil {
			t.Fatalf("a session in continuous use expired: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMaxLifetimeExpiresAnActiveSession(t *testing.T) {
	// The absolute cap is what stops a tab left open all week from holding a
	// directory admin bind open all week.
	store := NewStore(testLogger(), time.Hour, 40*time.Millisecond)
	defer store.Close()

	conn := &fakeSession{}
	sess, _ := store.Add(conn, testConfig(), false)

	deadline := time.Now().Add(160 * time.Millisecond)
	expired := false
	for time.Now().Before(deadline) {
		if _, err := store.Get(sess.ID); errors.Is(err, ErrNotFound) {
			expired = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !expired {
		t.Fatal("a continuously used session outlived its maximum lifetime")
	}
	if conn.closeCount() != 1 {
		t.Errorf("the expired session did not close its connection")
	}
}

func TestCloseClosesEveryConnection(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)

	conns := make([]*fakeSession, 5)
	for i := range conns {
		conns[i] = &fakeSession{}
		if _, err := store.Add(conns[i], testConfig(), false); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	store.Close()

	for i, c := range conns {
		if c.closeCount() != 1 {
			t.Errorf("connection %d was closed %d times, want 1", i, c.closeCount())
		}
	}
	if store.Len() != 0 {
		t.Errorf("Len = %d after Close, want 0", store.Len())
	}
	// Closing twice must not panic on the already-closed stop channel.
	store.Close()
}

func TestReadOnlyIsCarried(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	sess, _ := store.Add(&fakeSession{}, testConfig(), true)
	if !sess.ReadOnly {
		t.Error("ReadOnly was not carried onto the session")
	}
}

func TestVerifiedReflectsTheConnection(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	tests := []struct {
		name string
		cfg  directory.ConnConfig
		want bool
	}{
		{
			name: "ldaps with verification",
			cfg:  directory.ConnConfig{TLS: directory.TLSModeLDAPS},
			want: true,
		},
		{
			name: "ldaps with verification skipped",
			cfg:  directory.ConnConfig{TLS: directory.TLSModeLDAPS, InsecureSkipVerify: true},
			want: false,
		},
		{
			name: "plaintext is never verified",
			cfg:  directory.ConnConfig{TLS: directory.TLSModePlaintext},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess, _ := store.Add(&fakeSession{}, tt.cfg, false)
			if got := sess.Verified(); got != tt.want {
				t.Errorf("Verified() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConcurrentUse runs the store the way HTTP handlers do. The race detector
// is the actual assertion; the counts only confirm the test did some work.
func TestConcurrentUse(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()

	const workers = 16
	var wg sync.WaitGroup
	ids := make(chan string, workers*4)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				sess, err := store.Add(&fakeSession{}, testConfig(), false)
				if err != nil {
					t.Errorf("Add: %v", err)
					return
				}
				ids <- sess.ID
			}
		}()
	}
	wg.Wait()
	close(ids)

	var all []string
	for id := range ids {
		all = append(all, id)
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, id := range all {
				_, _ = store.Get(id)
			}
		}()
	}
	for _, id := range all {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			store.Remove(id)
		}(id)
	}
	wg.Wait()

	if store.Len() != 0 {
		t.Errorf("Len = %d after removing every session, want 0", store.Len())
	}
}
