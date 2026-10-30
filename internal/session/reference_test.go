package session

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A session's reference server (B6) lives and dies with its session, and is a
// connection the store counts.

func TestAReferenceClosesWithItsSession(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	conn, refConn := &fakeSession{}, &fakeSession{}
	sess, err := store.Add(conn, testConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachReference(context.Background(), sess, refConn, testConfig()); err != nil {
		t.Fatalf("attaching: %v", err)
	}
	if sess.Reference() == nil {
		t.Fatal("the session has no reference after attaching one")
	}
	store.Remove(sess.ID)
	if refConn.closeCount() != 1 {
		t.Errorf("the reference was closed %d times when its session was, want 1", refConn.closeCount())
	}
	if store.Connections() != 0 {
		t.Errorf("%d connections held after the only session closed", store.Connections())
	}
}

func TestAReferenceExpiresWithItsSession(t *testing.T) {
	store := NewStore(testLogger(), 10*time.Millisecond, time.Hour)
	defer store.Close()
	refConn := &fakeSession{}
	sess, err := store.Add(&fakeSession{}, testConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachReference(context.Background(), sess, refConn, testConfig()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	store.reap()
	if refConn.closeCount() != 1 {
		t.Errorf("an idle session's reference was closed %d times, want 1", refConn.closeCount())
	}
}

func TestASessionHoldsOneReference(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	sess, err := store.Add(&fakeSession{}, testConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachReference(context.Background(), sess, &fakeSession{}, testConfig()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachReference(context.Background(), sess, &fakeSession{}, testConfig()); !errors.Is(err, ErrReferenceAttached) {
		t.Errorf("a second reference = %v, want ErrReferenceAttached", err)
	}
	if !store.DetachReference(sess) {
		t.Error("detaching reported there was no reference")
	}
	if store.DetachReference(sess) {
		t.Error("detaching twice reported a reference the second time")
	}
}

func TestAReferenceIsACountedConnection(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	store.SetLimit(2)
	sess, err := store.Add(&fakeSession{}, testConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachReference(context.Background(), sess, &fakeSession{}, testConfig()); err != nil {
		t.Fatal(err)
	}
	if !store.Full() {
		t.Error("Full = false with a session and its reference against a limit of two")
	}
	if _, err := store.Add(&fakeSession{}, testConfig(), false); !errors.Is(err, ErrFull) {
		t.Fatalf("a second session beside a reference = %v, want ErrFull", err)
	}
	store.DetachReference(sess)
	if store.Full() {
		t.Error("still full after the reference was detached")
	}
}

func TestAReferenceToAClosedSessionIsRefused(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	sess, err := store.Add(&fakeSession{}, testConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	store.Remove(sess.ID)
	if _, err := store.AttachReference(context.Background(), sess, &fakeSession{}, testConfig()); !errors.Is(err, ErrNotFound) {
		t.Errorf("attaching to a closed session = %v, want ErrNotFound", err)
	}
}

// Opened and closed are logged as a reference, and the password is never in
// the log at any level.
func TestAReferenceIsLoggedWithoutItsPassword(t *testing.T) {
	var buf bytes.Buffer
	store := NewStore(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), time.Minute, time.Hour)
	defer store.Close()
	sess, err := store.Add(&fakeSession{}, testConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.BindPassword = "reference-secret-91c2"
	cfg.ConfigBindPassword = "reference-config-secret-0ab4"
	ref, err := store.AttachReference(context.Background(), sess, &fakeSession{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store.DetachReference(sess)

	log := buf.String()
	if strings.Count(log, "role=reference") != 2 {
		t.Errorf("want an open and a close logged as role=reference:\n%s", log)
	}
	for _, secret := range []string{cfg.BindPassword, cfg.ConfigBindPassword} {
		if strings.Contains(log, secret) {
			t.Fatalf("the reference's password is in the log:\n%s", log)
		}
	}
	if ref.config.BindPassword != "" || ref.config.ConfigBindPassword != "" {
		t.Error("the reference's passwords were not cleared when it closed")
	}
}
