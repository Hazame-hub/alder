package diff

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

func testSchema(t testing.TB) *schema.Schema {
	t.Helper()
	return schema.Load("cn=schema", map[string][]string{
		"attributeTypes": {
			"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.3 NAME 'cn' SUP name )",
			"( 2.5.4.4 NAME 'sn' SUP name )",
			"( 2.5.4.12 NAME 'title' SUP name )",
			"( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 0.9.2342.19200300.100.1.1 NAME 'uid' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 0.9.2342.19200300.100.1.3 NAME 'mail' EQUALITY caseIgnoreIA5Match SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
			"( 2.5.4.31 NAME 'member' EQUALITY distinguishedNameMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
			"( 2.5.4.35 NAME 'userPassword' EQUALITY octetStringMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
			"( 2.5.18.2 NAME 'modifyTimestamp' EQUALITY generalizedTimeMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.24 SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )",
		},
	})
}

func entry(d string, pairs ...[]string) *directory.Entry {
	e := directory.NewEntry(dn.MustParse(d))
	for _, p := range pairs {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		e.Set(p[0], values)
	}
	return e
}

// drop removes an attribute from a fixture entry.
func drop(e *directory.Entry, name string) {
	for key := range e.Attributes {
		if strings.EqualFold(key, name) {
			delete(e.Attributes, key)
		}
	}
	kept := e.Order[:0]
	for _, key := range e.Order {
		if !strings.EqualFold(key, name) {
			kept = append(kept, key)
		}
	}
	e.Order = kept
}

const (
	alice  = "uid=alice,ou=people,dc=alder,dc=test"
	bob    = "uid=bob,ou=people,dc=alder,dc=test"
	admins = "cn=admins,ou=groups,dc=alder,dc=test"
)

type opts struct {
	vendor      string
	sch         *schema.Schema
	operational bool
	base        string
	filter      string
}

func snap(t testing.TB, o opts, entries ...*directory.Entry) *snapshot.Snapshot {
	t.Helper()
	if o.vendor == "" {
		o.vendor = "389 Project"
	}
	if o.base == "" {
		o.base = "dc=alder,dc=test"
	}
	if o.filter == "" {
		o.filter = "(objectClass=*)"
	}
	s, err := snapshot.Build(snapshot.Capture{Base: dn.MustParse(o.base), Scope: "sub", Filter: o.filter,
		Vendor: o.vendor, Operational: o.operational, CreatedAt: time.Date(2026, 9, 13, 22, 0, 0, 0, time.UTC)}, o.sch, entries)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func people(t testing.TB) []*directory.Entry {
	return []*directory.Entry{
		entry(alice, []string{"objectClass", "top", "person"}, []string{"uid", "alice"}, []string{"cn", "Alice"},
			[]string{"sn", "A"}, []string{"title", "Engineer"}, []string{"entryUUID", "id-alice"}),
		entry(bob, []string{"objectClass", "top", "person"}, []string{"uid", "bob"}, []string{"cn", "Bob"},
			[]string{"sn", "B"}, []string{"userPassword", "{SSHA}b2xk"}, []string{"entryUUID", "id-bob"}),
		entry(admins, []string{"objectClass", "top", "groupOfNames"}, []string{"cn", "admins"},
			[]string{"member", alice, bob}),
	}
}

func compare(t testing.TB, source, target Side, o Options) *Result {
	t.Helper()
	r, err := Compare(context.Background(), source, target, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func itemAt(t testing.TB, r *Result, d string) Item {
	t.Helper()
	for _, it := range r.Items {
		if strings.EqualFold(it.TargetDN, d) || strings.EqualFold(it.SourceDN, d) {
			return it
		}
	}
	t.Fatalf("no item for %s in %+v", d, r.Items)
	return Item{}
}

func TestIdenticalStatesHaveNoDifferences(t *testing.T) {
	sch := testSchema(t)
	a, b := snap(t, opts{sch: sch}, people(t)...), snap(t, opts{sch: sch}, people(t)...)
	r := compare(t, Side{Snapshot: a}, Side{Snapshot: b}, Options{})
	if !r.Complete || len(r.Items) != 0 || r.Counts.Unchanged != 3 || r.Counts.Compared != 3 {
		t.Fatalf("result = %+v", r)
	}
}

func TestAddedRemovedAndModified(t *testing.T) {
	sch := testSchema(t)
	source := snap(t, opts{sch: sch}, people(t)...)
	changed := people(t)
	changed[0].Set("title", [][]byte{[]byte("Senior Engineer")})                                      // value replaced
	changed[0].Set("mail", [][]byte{[]byte("alice@alder.test")})                                      // attribute added
	drop(changed[0], "sn")                                                                            // attribute removed
	changed[2].Set("member", [][]byte{[]byte(alice), []byte("uid=carol,ou=people,dc=alder,dc=test")}) // one out, one in
	changed = append(changed[:1], changed[2:]...)                                                     // bob removed
	changed = append(changed, entry("uid=dave,ou=people,dc=alder,dc=test",
		[]string{"objectClass", "top", "person"}, []string{"uid", "dave"}, []string{"cn", "Dave"}, []string{"sn", "D"}))
	target := snap(t, opts{sch: sch}, changed...)

	r := compare(t, Side{Snapshot: source}, Side{Snapshot: target}, Options{})
	if r.Counts.Added != 1 || r.Counts.Removed != 1 || r.Counts.Modified != 2 {
		t.Fatalf("counts = %+v", r.Counts)
	}
	if it := itemAt(t, r, bob); it.Kind != Removed {
		t.Errorf("bob = %s", it.Kind)
	}
	if it := itemAt(t, r, "uid=dave,ou=people,dc=alder,dc=test"); it.Kind != Added {
		t.Errorf("dave = %s", it.Kind)
	}
	got := map[string]AttributeChange{}
	for _, a := range itemAt(t, r, alice).Attributes {
		got[a.Name] = a
	}
	if got["title"].Kind != Modified || *got["title"].Added[0].Text != "Senior Engineer" || *got["title"].Removed[0].Text != "Engineer" {
		t.Errorf("title = %+v", got["title"])
	}
	if got["mail"].Kind != Added || got["sn"].Kind != Removed {
		t.Errorf("mail = %s, sn = %s", got["mail"].Kind, got["sn"].Kind)
	}
	member := itemAt(t, r, admins).Attributes[0]
	if member.Kind != Modified || len(member.Added) != 1 || len(member.Removed) != 1 || *member.Removed[0].Text != bob {
		t.Errorf("member = %+v", member)
	}
}

func TestEqualityFollowsTheRuleNotTheBytes(t *testing.T) {
	sch := testSchema(t)
	source := snap(t, opts{sch: sch}, people(t)...)
	respelled := people(t)
	respelled[0].Set("title", [][]byte{[]byte("  ENGINEER ")})
	respelled[2].Set("member", [][]byte{[]byte("UID=Alice, OU=People,DC=alder,DC=test"), []byte(bob)})
	r := compare(t, Side{Snapshot: source}, Side{Snapshot: snap(t, opts{sch: sch}, respelled...)}, Options{})
	if len(r.Items) != 0 {
		t.Fatalf("presentation differences reported as changes: %+v", r.Items)
	}
}

func TestWithoutASchemaValuesCompareByBytesAndSaySo(t *testing.T) {
	source := snap(t, opts{}, people(t)...)
	respelled := people(t)
	respelled[0].Set("title", [][]byte{[]byte("ENGINEER")})
	r := compare(t, Side{Snapshot: source}, Side{Snapshot: snap(t, opts{}, respelled...)}, Options{})
	if r.Complete || r.Reasons[0].Code != ReasonSchemaUnavailable {
		t.Errorf("reasons = %+v", r.Reasons)
	}
	if it := itemAt(t, r, alice); it.Kind != Modified || !it.Attributes[0].ComparedByBytes {
		t.Errorf("alice = %+v", it)
	}
}

func TestARenameNeedsAnIdentityBothSidesShare(t *testing.T) {
	sch := testSchema(t)
	source := snap(t, opts{sch: sch}, people(t)...)
	moved := people(t)
	moved[0] = entry("uid=alicia,ou=staff,dc=alder,dc=test", []string{"objectClass", "top", "person"},
		[]string{"uid", "alicia"}, []string{"cn", "Alice"}, []string{"sn", "A"}, []string{"title", "Engineer"},
		[]string{"entryUUID", "id-alice"})
	r := compare(t, Side{Snapshot: source}, Side{Snapshot: snap(t, opts{sch: sch}, moved...)}, Options{})
	it := itemAt(t, r, alice)
	if it.Kind != Renamed || it.TargetDN != "uid=alicia,ou=staff,dc=alder,dc=test" {
		t.Fatalf("with a shared id: %+v", it)
	}

	drop(moved[0], "entryUUID")
	r = compare(t, Side{Snapshot: source}, Side{Snapshot: snap(t, opts{sch: sch}, moved...)}, Options{})
	if itemAt(t, r, alice).Kind != Removed || itemAt(t, r, "uid=alicia,ou=staff,dc=alder,dc=test").Kind != Added {
		t.Errorf("without an id a move must be removed plus added: %+v", r.Items)
	}
}

func TestWhatCouldNotBeSeenIsUnknown(t *testing.T) {
	sch := testSchema(t)
	full := snap(t, opts{sch: sch}, people(t)...)
	reached := snap(t, opts{sch: sch}, people(t)[:1]...)

	t.Run("a truncated live read", func(t *testing.T) {
		r := compare(t, Side{Snapshot: reached, Live: true, Truncated: true}, Side{Snapshot: full}, Options{})
		if r.Complete || r.Counts.Added != 0 || r.Counts.Unknown != 2 {
			t.Errorf("counts = %+v, reasons = %+v", r.Counts, r.Reasons)
		}
	})
	t.Run("a different scope", func(t *testing.T) {
		narrow := snap(t, opts{sch: sch, base: "ou=people,dc=alder,dc=test"}, people(t)[:2]...)
		r := compare(t, Side{Snapshot: narrow}, Side{Snapshot: full}, Options{})
		if r.Complete || itemAt(t, r, admins).Kind != Unknown {
			t.Errorf("admins lies outside the source's scope and must be unknown: %+v", r.Items)
		}
	})
	t.Run("an attribute the live directory hides", func(t *testing.T) {
		hidden := people(t)
		drop(hidden[0], "title")
		live := snap(t, opts{sch: sch}, hidden...)
		probe := func(_ context.Context, _ dn.DN, attr string) (directory.AttributeVisibility, error) {
			return directory.VisibilityDenied, nil
		}
		r := compare(t, Side{Snapshot: live, Live: true}, Side{Snapshot: full}, Options{Probe: probe, ProbeBudget: 10})
		title := itemAt(t, r, alice).Attributes[0]
		if title.Kind != Unknown || title.UnknownReason != ReasonInsufficientAccess || r.Complete {
			t.Errorf("title = %+v, complete = %v", title, r.Complete)
		}
		if c := Derive(r, itemAt(t, r, alice)); len(c.Records) != 0 {
			t.Errorf("an unknown attribute produced plan input: %+v", c.Records)
		}
	})
}

func TestAComparisonAcrossVendorsSaysSo(t *testing.T) {
	sch := testSchema(t)
	r := compare(t, Side{Snapshot: snap(t, opts{sch: sch, vendor: "OpenLDAP"}, people(t)...)},
		Side{Snapshot: snap(t, opts{sch: sch}, people(t)...)}, Options{})
	if !r.CrossVendor || len(r.Items) != 0 {
		t.Errorf("crossVendor = %v, items = %+v", r.CrossVendor, r.Items)
	}
	unnamed := snap(t, opts{sch: sch}, people(t)...)
	unnamed.Source.Vendor = ""
	if r := compare(t, Side{Snapshot: unnamed}, Side{Snapshot: snap(t, opts{sch: sch}, people(t)...)}, Options{}); !r.CrossVendor {
		t.Error("a server that does not name itself was assumed to be the same product")
	}
	if r := compare(t, Side{Snapshot: snap(t, opts{sch: sch}, people(t)...)}, Side{Snapshot: snap(t, opts{sch: sch}, people(t)...)}, Options{}); r.CrossVendor {
		t.Error("one product compared with itself reported as cross-vendor")
	}
}

func TestSensitiveAttributesDifferByCountOnly(t *testing.T) {
	sch := testSchema(t)
	source := snap(t, opts{sch: sch}, people(t)...)
	changed := people(t)
	changed[1].Set("userPassword", [][]byte{[]byte("{SSHA}bmV3")}) // same count, different secret
	if r := compare(t, Side{Snapshot: source}, Side{Snapshot: snap(t, opts{sch: sch}, changed...)}, Options{}); len(r.Items) != 0 {
		t.Errorf("a secret's value leaked into the comparison: %+v", r.Items)
	}
	drop(changed[1], "userPassword")
	r := compare(t, Side{Snapshot: source}, Side{Snapshot: snap(t, opts{sch: sch}, changed...)}, Options{})
	pw := itemAt(t, r, bob).Attributes[0]
	if pw.Kind != Removed || !pw.Sensitive || pw.WithheldSource != 1 || len(pw.Removed) != 0 {
		t.Errorf("userPassword = %+v", pw)
	}
}

func TestDerivedCandidates(t *testing.T) {
	sch := testSchema(t)
	target := snap(t, opts{sch: sch, operational: true}, people(t)...)
	liveEntries := people(t)
	liveEntries[0].Set("title", [][]byte{[]byte("Senior Engineer")})
	liveEntries[0].Set("modifyTimestamp", [][]byte{[]byte("20260913230000Z")})
	liveEntries = append(liveEntries, entry("uid=eve,ou=people,dc=alder,dc=test",
		[]string{"objectClass", "top", "person"}, []string{"uid", "eve"}, []string{"cn", "Eve"}, []string{"sn", "E"}))
	liveEntries = append(liveEntries[:1], liveEntries[2:]...) // bob only in the target
	live := snap(t, opts{sch: sch, operational: true}, liveEntries...)

	t.Run("a diff between two files proposes nothing", func(t *testing.T) {
		r := compare(t, Side{Snapshot: live}, Side{Snapshot: target}, Options{})
		if c := Derive(r, itemAt(t, r, alice)); c.Blocked != BlockedSourceNotLive || len(c.Records) != 0 {
			t.Errorf("candidate = %+v", c)
		}
	})

	r := compare(t, Side{Snapshot: live, Live: true}, Side{Snapshot: target}, Options{})

	t.Run("modify restores values and never touches operational attributes", func(t *testing.T) {
		c := Derive(r, itemAt(t, r, alice))
		if len(c.Records) != 1 || c.Destructive {
			t.Fatalf("candidate = %+v", c)
		}
		mods := c.Records[0].Mods
		if len(mods) != 2 || mods[0].Op != directory.ModDelete || string(mods[0].Values[0]) != "Senior Engineer" ||
			mods[1].Op != directory.ModAdd || string(mods[1].Values[0]) != "Engineer" {
			t.Errorf("mods = %+v", mods)
		}
		for _, m := range mods {
			if strings.EqualFold(m.Name, "modifyTimestamp") {
				t.Error("an operational attribute was proposed for writing")
			}
		}
	})
	t.Run("an entry only in the target becomes an add without secrets", func(t *testing.T) {
		c := Derive(r, itemAt(t, r, bob))
		if len(c.Records) != 1 || c.Records[0].Type != directory.ChangeAdd {
			t.Fatalf("candidate = %+v", c)
		}
		for _, a := range c.Records[0].Attrs {
			if strings.EqualFold(a.Name, "userPassword") || strings.EqualFold(a.Name, "entryUUID") {
				t.Errorf("the add carries %s", a.Name)
			}
		}
	})
	t.Run("an entry only in the live directory is a destructive delete", func(t *testing.T) {
		c := Derive(r, itemAt(t, r, "uid=eve,ou=people,dc=alder,dc=test"))
		if !c.Destructive || len(c.Records) != 1 || c.Records[0].Type != directory.ChangeDelete {
			t.Fatalf("candidate = %+v", c)
		}
	})
	t.Run("but not from a partial comparison", func(t *testing.T) {
		partial := compare(t, Side{Snapshot: live, Live: true}, Side{Snapshot: target}, Options{})
		partial.Complete = false
		c := Derive(partial, itemAt(t, partial, "uid=eve,ou=people,dc=alder,dc=test"))
		if len(c.Records) != 0 || c.Blocked != BlockedIncomplete || !c.Destructive {
			t.Errorf("candidate = %+v", c)
		}
	})
	t.Run("a rename is a modrdn to the target's name", func(t *testing.T) {
		movedTarget := people(t)
		movedTarget[0] = entry("uid=alicia,ou=people,dc=alder,dc=test", []string{"objectClass", "top", "person"},
			[]string{"uid", "alicia"}, []string{"cn", "Alice"}, []string{"sn", "A"}, []string{"title", "Staff"},
			[]string{"entryUUID", "id-alice"})
		rr := compare(t, Side{Snapshot: snap(t, opts{sch: sch}, people(t)...), Live: true},
			Side{Snapshot: snap(t, opts{sch: sch}, movedTarget...)}, Options{})
		c := Derive(rr, itemAt(t, rr, alice))
		if len(c.Records) != 2 || c.Records[0].Type != directory.ChangeModRDN || c.Records[0].NewRDN != "uid=alicia" ||
			!c.Records[0].DeleteOldRDN || c.Records[1].DN.String() != "uid=alicia,ou=people,dc=alder,dc=test" {
			t.Fatalf("candidate = %+v", c)
		}
		for _, m := range c.Records[1].Mods {
			if strings.EqualFold(m.Name, "uid") {
				t.Error("the modification also rewrites the naming attribute the rename owns")
			}
		}
	})
}

func TestAnAttributeOnlyOneSideHoldsIsNotReportedAsComparedByBytes(t *testing.T) {
	sch := testSchema(t)
	source := snap(t, opts{sch: sch}, people(t)...)
	described := people(t)
	described[0].Set("description", [][]byte{[]byte("added after the snapshot")})
	r := compare(t, Side{Snapshot: source}, Side{Snapshot: snap(t, opts{sch: sch}, described...)}, Options{})
	if len(r.ComparedByBytes) != 0 || len(r.RuleDifferences) != 0 {
		t.Errorf("comparedByBytes = %v, ruleDifferences = %v", r.ComparedByBytes, r.RuleDifferences)
	}
	it := itemAt(t, r, alice)
	if it.Kind != Modified || len(it.Attributes) != 1 || it.Attributes[0].ComparedByBytes || len(it.Attributes[0].Added) != 1 {
		t.Errorf("alice = %+v", it)
	}
}
