package plan

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// A server's own vocabulary is not a schema violation.
//
// 389 Directory Server keeps its configuration in attributes its published
// schema does not define. The server accepted them on the entry, so modifying
// one is a question for the server, not for a schema that says nothing about
// it -- and calling it invalid would withhold a change the directory accepts,
// which is the one failure this check must not have.
func TestAnAttributeTheEntryHoldsAndTheSchemaDoesNotDefineIsLeftToTheServer(t *testing.T) {
	sch := testSchema(t)
	target, err := dn.Parse("cn=config")
	if err != nil {
		t.Fatal(err)
	}
	live := directory.NewEntry(target)
	live.Set("objectClass", [][]byte{[]byte("top")})
	live.Set("nsslapd-sizelimit", [][]byte{[]byte("2000")})

	modify := directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "nsslapd-sizelimit", Values: [][]byte{[]byte("2001")}}}}
	if p := validate(sch, modify, live); p != nil {
		t.Fatalf("modifying an attribute the server already holds was refused: %+v", p)
	}

	// An attribute the entry does not hold and the schema does not define is
	// still a mistake the schema can point out.
	typo := directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "nsslapd-sizelimt", Values: [][]byte{[]byte("1")}}}}
	if p := validate(sch, typo, live); p == nil || p.Code != ProblemAttributeUndefined {
		t.Fatalf("a misspelt attribute was accepted: %+v", p)
	}

	// And a new entry gets no such allowance: nothing has accepted it yet.
	add := directory.ChangeRecord{DN: target, Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("top")}},
		{Name: "nsslapd-sizelimit", Values: [][]byte{[]byte("1")}},
	}}
	if p := validate(sch, add, nil); p == nil || p.Code != ProblemAttributeUndefined {
		t.Fatalf("an undefined attribute on a new entry was accepted: %+v", p)
	}
}

// A modify that empties a required attribute is refused by both servers, so
// the plan says so -- but only from values it read, never from an entry it
// could not see.
func TestAModifyThatLeavesARequiredAttributeEmptyIsInvalid(t *testing.T) {
	sch := testSchema(t)
	target, err := dn.Parse("uid=ana,ou=people,dc=alder,dc=test")
	if err != nil {
		t.Fatal(err)
	}
	live := directory.NewEntry(target)
	live.Set("objectClass", [][]byte{[]byte("top"), []byte("person")})
	live.Set("cn", [][]byte{[]byte("Ana")})
	live.Set("sn", [][]byte{[]byte("One"), []byte("Two")})

	modify := func(mods ...directory.Mod) directory.ChangeRecord {
		return directory.ChangeRecord{DN: target, Type: directory.ChangeModify, Mods: mods}
	}
	cases := []struct {
		name    string
		op      directory.ChangeRecord
		live    *directory.Entry
		invalid bool
	}{
		{"deleting every value", modify(directory.Mod{Op: directory.ModDelete, Name: "sn",
			Values: [][]byte{[]byte("One"), []byte("Two")}}), live, true},
		{"deleting the attribute", modify(directory.Mod{Op: directory.ModDelete, Name: "SN"}), live, true},
		{"replacing with nothing", modify(directory.Mod{Op: directory.ModReplace, Name: "sn"}), live, true},
		{"deleting one of two", modify(directory.Mod{Op: directory.ModDelete, Name: "sn",
			Values: [][]byte{[]byte("One")}}), live, false},
		{"deleting all, then adding one", modify(
			directory.Mod{Op: directory.ModDelete, Name: "sn"},
			directory.Mod{Op: directory.ModAdd, Name: "sn", Values: [][]byte{[]byte("Three")}}), live, false},
		{"an optional attribute", modify(directory.Mod{Op: directory.ModDelete, Name: "description"}), live, false},
		{"no live entry read", modify(directory.Mod{Op: directory.ModDelete, Name: "sn",
			Values: [][]byte{[]byte("One")}}), nil, false},
	}
	for _, c := range cases {
		p := validate(sch, c.op, c.live)
		switch {
		case c.invalid && (p == nil || p.Code != ProblemMissingRequired || p.Attribute != "sn"):
			t.Errorf("%s: got %+v, want missing_required_attribute on sn", c.name, p)
		case !c.invalid && p != nil && p.Code == ProblemMissingRequired:
			t.Errorf("%s: refused as %+v", c.name, p)
		}
	}
}
