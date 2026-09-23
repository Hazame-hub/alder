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
