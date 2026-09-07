package api

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

func addRecord(t *testing.T, d string, pairs ...[]string) directory.ChangeRecord {
	t.Helper()
	rec := directory.ChangeRecord{DN: mustParse(t, d), Type: directory.ChangeAdd}
	for _, p := range pairs {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		rec.Attrs = append(rec.Attrs, directory.Attribute{Name: p[0], Values: values})
	}
	return rec
}

func liveEntry(t *testing.T, d string, pairs ...[]string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, d))
	for _, p := range pairs {
		values := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			values = append(values, []byte(v))
		}
		e.Set(p[0], values)
	}
	return e
}

const alice = "uid=alice,ou=people,dc=alder,dc=test"

// The case the feature exists for, and the one that must be silent: export a
// subtree, change nothing, import it back.
func TestAnUnchangedRoundTripReconcilesToNothing(t *testing.T) {
	sch := testSchema(t)
	rec := addRecord(t, alice,
		[]string{"objectClass", "top", "inetOrgPerson"},
		[]string{"cn", "Alice Liddell"},
		[]string{"mail", "alice@alder.test"},
	)
	live := liveEntry(t, alice,
		[]string{"objectClass", "top", "inetOrgPerson"},
		[]string{"cn", "Alice Liddell"},
		[]string{"mail", "alice@alder.test"},
	)

	got := reconcile(rec, live, sch)
	if got.Changed {
		t.Errorf("an unchanged entry produced %d modifications: %+v",
			len(got.Change.Mods), got.Change.Mods)
	}
}

// The value order a directory returns is its own; an attribute is a set.
func TestValueOrderIsNotAChange(t *testing.T) {
	sch := testSchema(t)
	rec := addRecord(t, alice, []string{"mail", "a@x.test", "b@x.test"})
	live := liveEntry(t, alice, []string{"mail", "b@x.test", "a@x.test"})

	if reconcile(rec, live, sch).Changed {
		t.Error("reordered values were read as a change")
	}
}

func TestAnEditedValueBecomesAReplace(t *testing.T) {
	sch := testSchema(t)
	rec := addRecord(t, alice,
		[]string{"cn", "Alice Liddell"},
		[]string{"mail", "alice@new.test"},
	)
	live := liveEntry(t, alice,
		[]string{"cn", "Alice Liddell"},
		[]string{"mail", "alice@alder.test"},
	)

	got := reconcile(rec, live, sch)
	if !got.Changed {
		t.Fatal("an edited value produced no modification")
	}
	if len(got.Change.Mods) != 1 {
		t.Fatalf("got %d modifications, want only the edited attribute: %+v",
			len(got.Change.Mods), got.Change.Mods)
	}
	mod := got.Change.Mods[0]
	if mod.Op != directory.ModReplace || mod.Name != "mail" {
		t.Errorf("got %s %s, want replace mail", mod.Op, mod.Name)
	}
	if len(mod.Values) != 1 || string(mod.Values[0]) != "alice@new.test" {
		t.Errorf("the replacement is %q", mod.Values)
	}
	if got.Change.Type != directory.ChangeModify {
		t.Errorf("the record is a %s, want a modify", got.Change.Type)
	}
}

// The whole safety argument. An export omits userPassword always and
// operational attributes by default, so a document that does not mention an
// attribute is not a document asking for it to be removed.
func TestAttributesTheDocumentDoesNotNameAreLeftAlone(t *testing.T) {
	sch := testSchema(t)
	rec := addRecord(t, alice, []string{"cn", "Alice Elsewhere"})
	live := liveEntry(t, alice,
		[]string{"cn", "Alice Liddell"},
		[]string{"mail", "alice@alder.test"},
		[]string{"userPassword", "{SSHA}averyrealsecret"},
	)

	got := reconcile(rec, live, sch)
	for _, mod := range got.Change.Mods {
		if mod.Name != "cn" {
			t.Errorf("the document named only cn, and %s %s was produced",
				mod.Op, mod.Name)
		}
	}
	if len(got.Change.Mods) != 1 {
		t.Errorf("got %d modifications, want 1", len(got.Change.Mods))
	}
}

// A document exported with includeOperational carries attributes the server
// owns. Reconciling on one fails the whole record, so they are dropped — and
// reported, because silently ignoring part of a document is how a file comes to
// mean something other than it says.
func TestAttributesTheDirectoryOwnsAreSkippedAndReported(t *testing.T) {
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrAttributeTypes: {
			"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 1.3.6.1.1.16.4 NAME 'entryUUID' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 " +
				"NO-USER-MODIFICATION SINGLE-VALUE USAGE directoryOperation )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the test schema does not parse: %v", sch.Errors)
	}

	rec := addRecord(t, alice,
		[]string{"cn", "Alice Elsewhere"},
		[]string{"entryUUID", "11111111-2222-3333-4444-555555555555"},
	)
	live := liveEntry(t, alice,
		[]string{"cn", "Alice Liddell"},
		[]string{"entryUUID", "99999999-8888-7777-6666-555555555555"},
	)

	got := reconcile(rec, live, sch)
	for _, mod := range got.Change.Mods {
		if mod.Name == "entryUUID" {
			t.Error("a modification was produced for an attribute the directory owns")
		}
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != "entryUUID" {
		t.Errorf("skipped %v, want entryUUID reported", got.Skipped)
	}
	if !got.Changed {
		t.Error("the editable attribute changed and should still be applied")
	}
}

// An attribute in the document and absent from the entry is added by the same
// replace: "ends up as exactly this" covers "was not there".
func TestAnAttributeMissingFromTheEntryIsSet(t *testing.T) {
	sch := testSchema(t)
	rec := addRecord(t, alice, []string{"description", "on secondment"})
	live := liveEntry(t, alice, []string{"cn", "Alice Liddell"})

	got := reconcile(rec, live, sch)
	if !got.Changed || len(got.Change.Mods) != 1 {
		t.Fatalf("got %+v, want one replace", got.Change.Mods)
	}
	if got.Change.Mods[0].Name != "description" {
		t.Errorf("modified %q", got.Change.Mods[0].Name)
	}
}

// The attribute name in the document need not be spelled the way the server
// spells it back.
func TestTheLiveLookupMatchesTheWayLDAPDoes(t *testing.T) {
	sch := testSchema(t)
	rec := addRecord(t, alice, []string{"CN", "Alice Liddell"})
	live := liveEntry(t, alice, []string{"cn", "Alice Liddell"})

	if reconcile(rec, live, sch).Changed {
		t.Error("a difference in the attribute's spelling was read as a change")
	}
}

func TestReconcileKeepsTheDN(t *testing.T) {
	sch := testSchema(t)
	rec := addRecord(t, alice, []string{"cn", "Somebody Else"})
	live := liveEntry(t, alice, []string{"cn", "Alice Liddell"})

	got := reconcile(rec, live, sch)
	if got.Change.DN.String() != alice {
		t.Errorf("the modification is addressed to %q", got.Change.DN.String())
	}
}
