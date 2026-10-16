package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTheStoreHoldsNoMoreThanItsLimit(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	store.SetLimit(2)

	for i := range 2 {
		if _, err := store.Add(&fakeSession{}, testConfig(), false); err != nil {
			t.Fatalf("Add %d: %v", i+1, err)
		}
	}
	if !store.Full() {
		t.Error("Full = false with two of two sessions open")
	}
	if _, err := store.Add(&fakeSession{}, testConfig(), false); !errors.Is(err, ErrFull) {
		t.Fatalf("a third Add = %v, want ErrFull", err)
	}
	if store.Len() != 2 {
		t.Errorf("Len = %d, want 2", store.Len())
	}
}

// A session past its idle timeout holds nothing anyone can use, so it does
// not keep a new one out while it waits for the sweeper.
func TestAnExpiredSessionMakesRoom(t *testing.T) {
	store := NewStore(testLogger(), 20*time.Millisecond, time.Hour)
	defer store.Close()
	store.SetLimit(1)

	old := &fakeSession{}
	if _, err := store.Add(old, testConfig(), false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := store.Add(&fakeSession{}, testConfig(), false); err != nil {
		t.Fatalf("Add after the only session expired = %v, want room", err)
	}
	if old.closeCount() != 1 {
		t.Errorf("the expired session's connection was closed %d times, want 1", old.closeCount())
	}
}

func TestNoLimitMeansNoLimit(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	store.SetLimit(-1)
	for i := range 50 {
		if _, err := store.Add(&fakeSession{}, testConfig(), false); err != nil {
			t.Fatalf("Add %d: %v", i+1, err)
		}
	}
}

// Requests that were all told there was room still take only the places
// there are: the limit is checked again under the lock that inserts.
func TestConcurrentAddsDoNotPassTheLimit(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	store.SetLimit(3)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 64 {
		wg.Go(func() {
			<-start
			_, _ = store.Add(&fakeSession{}, testConfig(), false)
		})
	}
	close(start)
	wg.Wait()
	if n := store.Connections(); n != 3 {
		t.Errorf("%d connections held against a limit of 3", n)
	}
}

// Connecting again from the same browser replaces its session, and with it
// the session's reference: the connections about to be freed do not keep
// the replacement out.
func TestAReplacementDoesNotCountTheSessionItReplaces(t *testing.T) {
	store := NewStore(testLogger(), time.Minute, time.Hour)
	defer store.Close()
	store.SetLimit(2)
	old, err := store.Add(&fakeSession{}, testConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachReference(old, &fakeSession{}, testConfig()); err != nil {
		t.Fatal(err)
	}
	if !store.Full() {
		t.Fatal("Full = false with a session and its reference against a limit of two")
	}
	if store.FullReplacing(old.ID) {
		t.Error("FullReplacing(the session being replaced) = true, want room")
	}
	if _, err := store.AddReplacing(&fakeSession{}, testConfig(), false, old.ID); err != nil {
		t.Fatalf("replacing the session = %v, want room", err)
	}
	store.Remove(old.ID)
	if n := store.Connections(); n != 1 {
		t.Errorf("%d connections after the replacement, want 1", n)
	}
	// Someone else's session ID is not a replacement of anything this
	// request holds; only an ID in the store is subtracted, and an unknown
	// one changes nothing.
	if _, err := store.Add(&fakeSession{}, testConfig(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddReplacing(&fakeSession{}, testConfig(), false, "no-such-session"); !errors.Is(err, ErrFull) {
		t.Errorf("an unknown replaced ID = %v, want ErrFull", err)
	}
}
