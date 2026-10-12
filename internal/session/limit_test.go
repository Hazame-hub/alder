package session

import (
	"errors"
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
