package replication

import (
	"testing"
	"time"
)

// Every row of the proposal's tables (docs/proposals/replication-health.md),
// in both servers' change sequence forms.

func openldapCursor(t *testing.T, csn string) Cursor {
	t.Helper()
	c, ok := parseOpenLDAPCSN(csn)
	if !ok {
		t.Fatalf("%q was not read", csn)
	}
	return c
}

func ruvCursor(t *testing.T, value string) Cursor {
	t.Helper()
	c, ok := parseRUV(value)
	if !ok {
		t.Fatalf("%q was not read", value)
	}
	return c
}

func report(provider string, suffixes ...Suffix) Report {
	return Report{Provider: provider, Suffixes: suffixes}
}

func TestTwoServersHoldingTheSameChangeAreInStep(t *testing.T) {
	c := openldapCursor(t, "20261030140211.000000Z#000000#001#000000")
	got := Compare(
		report("openldap", Suffix{DN: "dc=alder,dc=test", Cursors: []Cursor{c}}),
		report("openldap", Suffix{DN: "DC=alder,DC=test", Cursors: []Cursor{c}}),
		CompareOptions{})
	if got.Verdict != VerdictInStep || len(got.Suffixes) != 1 || got.Suffixes[0].Verdict != VerdictInStep {
		t.Fatalf("got %+v", got)
	}
	if got.Caveat != InStepCaveat {
		t.Error("in step was said without its caveat")
	}
}

func TestA389VectorIsComparedOnItsLastChangeNotWhereItStarted(t *testing.T) {
	// The first CSN is where each server started with that origin, and
	// differs between two servers that hold exactly the same changes.
	a := ruvCursor(t, "{replica 1 ldap://ds389:3389} 6ab8d208000000010000 6ab8d2b6000b00010000")
	b := ruvCursor(t, "{replica 1 ldap://ds389:3389} 6ab8d240000000010000 6ab8d2b6000b00010000")
	got := Compare(report("389", Suffix{DN: "dc=a", Cursors: []Cursor{a}}),
		report("389", Suffix{DN: "dc=a", Cursors: []Cursor{b}}), CompareOptions{})
	if got.Verdict != VerdictInStep {
		t.Fatalf("got %s: %+v", got.Verdict, got.Suffixes)
	}
}

func TestBehindIsASpanByTheOriginsOwnClock(t *testing.T) {
	newer := openldapCursor(t, "20261030140211.000000Z#000000#002#000000")
	older := openldapCursor(t, "20261030140131.000000Z#000000#002#000000")
	got := Compare(report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{newer}}),
		report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{older}}), CompareOptions{})
	if got.Verdict != VerdictBBehind {
		t.Fatalf("verdict %s", got.Verdict)
	}
	o := got.Suffixes[0].Origins[0]
	if o.Verdict != VerdictBBehind || o.Span != 40*time.Second || o.Origin != "2" {
		t.Errorf("origin %+v", o)
	}

	// The other way round.
	got = Compare(report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{older}}),
		report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{newer}}), CompareOptions{})
	if got.Verdict != VerdictABehind || got.Suffixes[0].Origins[0].Span != 40*time.Second {
		t.Errorf("reversed: %+v", got)
	}
}

func TestTwoChangesInTheSameSecondAreStillOrdered(t *testing.T) {
	// 389 DS stamps whole seconds, so the span is zero; the sequence number
	// inside the CSN still says which is later, and the verdict follows it.
	a := ruvCursor(t, "{replica 1 ldap://ds389:3389} 6ab8d208000000010000 6ab8d2b6000c00010000")
	b := ruvCursor(t, "{replica 1 ldap://ds389:3389} 6ab8d208000000010000 6ab8d2b6000b00010000")
	got := Compare(report("389", Suffix{DN: "dc=a", Cursors: []Cursor{a}}),
		report("389", Suffix{DN: "dc=a", Cursors: []Cursor{b}}), CompareOptions{})
	o := got.Suffixes[0].Origins[0]
	if o.Verdict != VerdictBBehind || o.Span != 0 {
		t.Errorf("got %+v", o)
	}
}

func TestAnOriginOneSideHasNothingFromIsBehind(t *testing.T) {
	one := openldapCursor(t, "20261030140211.000000Z#000000#001#000000")
	two := openldapCursor(t, "20261030140211.000000Z#000000#002#000000")
	got := Compare(report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{one, two}}),
		report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{one}}), CompareOptions{})
	s := got.Suffixes[0]
	if s.Verdict != VerdictBBehind || len(s.Origins) != 2 {
		t.Fatalf("got %+v", s)
	}
	if s.Origins[1].Origin != "2" || s.Origins[1].B != nil || s.Origins[1].Verdict != VerdictBBehind {
		t.Errorf("origin 2: %+v", s.Origins[1])
	}
}

func TestEachAheadOnADifferentOriginHaveDiverged(t *testing.T) {
	a1 := openldapCursor(t, "20261030140211.000000Z#000000#001#000000")
	b1 := openldapCursor(t, "20261030140201.000000Z#000000#001#000000")
	a2 := openldapCursor(t, "20261030140201.000000Z#000000#002#000000")
	b2 := openldapCursor(t, "20261030140211.000000Z#000000#002#000000")
	got := Compare(report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{a1, a2}}),
		report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{b1, b2}}), CompareOptions{})
	if got.Verdict != VerdictDiverged || got.Suffixes[0].Verdict != VerdictDiverged {
		t.Errorf("got %+v", got)
	}
}

func TestToleranceCountsASmallSpanAsInStep(t *testing.T) {
	newer := openldapCursor(t, "20261030140211.000000Z#000000#001#000000")
	older := openldapCursor(t, "20261030140201.000000Z#000000#001#000000")
	a := report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{newer}})
	b := report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{older}})
	got := Compare(a, b, CompareOptions{Tolerate: 10 * time.Second})
	o := got.Suffixes[0].Origins[0]
	if got.Verdict != VerdictInStep || !o.Tolerated || o.Span != 10*time.Second {
		t.Errorf("within tolerance: %+v", got)
	}
	if got := Compare(a, b, CompareOptions{Tolerate: 9 * time.Second}); got.Verdict != VerdictBBehind {
		t.Errorf("beyond tolerance: %s", got.Verdict)
	}
	// A side holding nothing from an origin has no span to tolerate.
	none := report("openldap", Suffix{DN: "dc=a", Cursors: nil})
	none.Suffixes[0].Cursors = []Cursor{openldapCursor(t, "20261030140211.000000Z#000000#002#000000")}
	if got := Compare(a, none, CompareOptions{Tolerate: time.Hour}); got.Verdict != VerdictDiverged {
		t.Errorf("missing origins: %s", got.Verdict)
	}
}

func TestRecordsThatAreNotOneHistoryAreNotCompared(t *testing.T) {
	c := openldapCursor(t, "20261030140211.000000Z#000000#001#000000")
	if got := Compare(report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{c}}),
		report("389", Suffix{DN: "dc=a", Cursors: []Cursor{c}}), CompareOptions{}); got.Verdict != VerdictNotComparable ||
		got.Why == "" || len(got.Suffixes) != 0 {
		t.Errorf("two models: %+v", got)
	}

	r := ruvCursor(t, "{replica 1 ldap://ds389:3389} 6ab8d208000000010000 6ab8d2b6000b00010000")
	got := Compare(report("389", Suffix{DN: "dc=a", Generation: "6ab8d208000000010000", Cursors: []Cursor{r}}),
		report("389", Suffix{DN: "dc=a", Generation: "6ab90000000000010000", Cursors: []Cursor{r}}), CompareOptions{})
	if got.Verdict != VerdictNotComparable || got.Suffixes[0].Verdict != VerdictNotComparable {
		t.Errorf("two generations: %+v", got)
	}

	got = Compare(report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{c}}),
		report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{c}}, Suffix{DN: "dc=b", Cursors: []Cursor{c}}),
		CompareOptions{})
	if got.Verdict != VerdictNotComparable || got.Suffixes[1].Verdict != VerdictNotComparable ||
		got.Suffixes[0].Verdict != VerdictInStep {
		t.Errorf("one side only: %+v", got)
	}

	if got := Compare(report("openldap"), report("openldap"), CompareOptions{}); got.Verdict != VerdictNotComparable {
		t.Errorf("nothing replicated: %s", got.Verdict)
	}
}

func TestWhatCouldNotBeReadIsIncompleteNeverInStep(t *testing.T) {
	c := openldapCursor(t, "20261030140211.000000Z#000000#001#000000")
	a := report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{c}})
	b := report("openldap", Suffix{DN: "dc=a"})
	b.Unread = []Unread{{Where: "dc=a", Reason: "how far along this server is could not be read"}}
	if got := Compare(a, b, CompareOptions{}); got.Verdict != VerdictIncomplete || got.Suffixes[0].Verdict != VerdictIncomplete {
		t.Errorf("unread suffix: %+v", got)
	}

	b = report("openldap", Suffix{DN: "dc=a", Cursors: []Cursor{c}})
	b.Unread = []Unread{{Where: "cn=config", Reason: "the configuration could not be read"}}
	if got := Compare(a, b, CompareOptions{}); got.Verdict != VerdictIncomplete || got.Suffixes[0].Verdict != VerdictInStep {
		t.Errorf("unread elsewhere: %+v", got)
	}

	if got := Compare(report("openldap", Suffix{DN: "dc=a"}), report("openldap", Suffix{DN: "dc=a"}),
		CompareOptions{}); got.Verdict != VerdictIncomplete {
		t.Errorf("no cursors: %s", got.Verdict)
	}
}

func TestTheGenerationIsReadFromTheVector(t *testing.T) {
	if g, ok := parseGeneration("{replicageneration} 6AB8D208000000010000"); !ok || g != "6ab8d208000000010000" {
		t.Errorf("got %q %v", g, ok)
	}
	if _, ok := parseGeneration("{replica 1 ldap://ds389:3389} 6ab8d208000000010000 6ab8d2b6000b00010000"); ok {
		t.Error("a replica line was read as the generation")
	}
}

func TestOriginsAreOrderedAsNumbers(t *testing.T) {
	ids := []string{"10", "2", "9", "1"}
	sortOrigins(ids)
	if ids[0] != "1" || ids[1] != "2" || ids[2] != "9" || ids[3] != "10" {
		t.Errorf("got %v", ids)
	}
}
