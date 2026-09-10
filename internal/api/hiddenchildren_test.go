package api

import (
	"context"
	"errors"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// parentWith builds a fake whose parent entry carries the given
// numSubordinates, or none when the value is empty.
func parentWith(t *testing.T, count string) *fakeSession {
	t.Helper()
	parent := mustParse(t, "dc=alder,dc=test")
	e := directory.NewEntry(parent)
	e.Set("objectClass", [][]byte{[]byte("domain")})
	if count != "" {
		e.Set("numSubordinates", [][]byte{[]byte(count)})
	}
	return &fakeSession{byDN: map[string]*directory.Entry{"dc=alder,dc=test": e}}
}

func hiddenFor(t *testing.T, f *fakeSession, shown int, truncated bool) (int, bool) {
	t.Helper()
	return hiddenChildCount(context.Background(), f,
		mustParse(t, "dc=alder,dc=test"), shown, truncated)
}

// The case the whole thing exists for: the server counts more children than the
// session could enumerate, and the difference is what the access rules hid.
func TestHiddenChildrenAreCountedWhereTheServerPublishesThem(t *testing.T) {
	got, ok := hiddenFor(t, parentWith(t, "3"), 2, false)
	if !ok || got != 1 {
		t.Errorf("got %d, %v; want 1 hidden child", got, ok)
	}
}

// A server that does not publish the count says nothing, rather than the tree
// guessing. That is a capability, and there is no vendor name anywhere near it.
func TestNothingIsClaimedWhereTheServerPublishesNoCount(t *testing.T) {
	if got, ok := hiddenFor(t, parentWith(t, ""), 2, false); ok {
		t.Errorf("got %d hidden children from a server that counts none", got)
	}
}

// A truncated listing stopped at its limit, so the gap is paging. Reporting it
// as access would turn "there is more here" into "somebody is hiding this".
func TestATruncatedListingReportsNoHiddenChildren(t *testing.T) {
	if got, ok := hiddenFor(t, parentWith(t, "500"), 100, true); ok {
		t.Errorf("got %d hidden children from a listing that simply stopped early", got)
	}
}

func TestNothingHiddenWhenTheCountMatches(t *testing.T) {
	if got, ok := hiddenFor(t, parentWith(t, "3"), 3, false); ok {
		t.Errorf("got %d hidden children when the session saw all of them", got)
	}
}

// Seeing more than the server counts is not a negative number of hidden
// children. It should not happen; if it does, saying nothing is right.
func TestSeeingMoreThanTheCountClaimsNothing(t *testing.T) {
	if got, ok := hiddenFor(t, parentWith(t, "2"), 5, false); ok {
		t.Errorf("got %d hidden children when more were seen than counted", got)
	}
}

func TestAnUnparseableCountClaimsNothing(t *testing.T) {
	if got, ok := hiddenFor(t, parentWith(t, "lots"), 2, false); ok {
		t.Errorf("got %d hidden children from a count that is not a number", got)
	}
}

// A parent that cannot be read at all is not evidence of anything.
func TestAFailedReadClaimsNothing(t *testing.T) {
	f := parentWith(t, "3")
	f.readErr = errors.New("the server hung up")
	if got, ok := hiddenFor(t, f, 2, false); ok {
		t.Errorf("got %d hidden children after the read failed", got)
	}
}
