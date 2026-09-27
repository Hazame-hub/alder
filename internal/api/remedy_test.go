package api

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// Where a refusal points. The hint says what the code usually means; this is
// the screen that answers it, and it must never point somewhere that would
// mislead: a remedy is a place to look, and a wrong place to look at 2am is
// worse than none.

func change(t *testing.T, target string, kind directory.ChangeType, attrs ...string) directory.ChangeRecord {
	t.Helper()
	parsed, err := dn.Parse(target)
	if err != nil {
		t.Fatalf("dn %q: %v", target, err)
	}
	record := directory.ChangeRecord{DN: parsed, Type: kind}
	for _, a := range attrs {
		record.Mods = append(record.Mods, directory.Mod{Op: directory.ModReplace, Name: a,
			Values: [][]byte{[]byte("x")}})
	}
	return record
}

func TestRemedyPointsAtTheScreenThatAnswers(t *testing.T) {
	alice := "uid=alice,ou=people,dc=alder,dc=test"
	caps := directory.Capabilities{Config: directory.ConfigAccess{DN: "cn=config"}}

	cases := []struct {
		name     string
		code     uint16
		record   directory.ChangeRecord
		caps     directory.Capabilities
		wantKind string
		wantDN   string
		wantAttr string
	}{
		{
			name: "refused for rights, so the access rules", code: rcInsufficientAccess,
			record: change(t, alice, directory.ChangeModify, "mail"), caps: caps,
			wantKind: remedyAccess, wantDN: alice,
		},
		{
			name: "a password refused on constraint, so the policy", code: rcConstraintViolation,
			record: change(t, alice, directory.ChangeModify, "userPassword"), caps: caps,
			wantKind: remedyPolicy, wantDN: alice, wantAttr: "userPassword",
		},
		{
			name: "an object class violation, so the schema", code: rcObjectClassViolation,
			record: change(t, alice, directory.ChangeModify, "title"), caps: caps,
			wantKind: remedySchema, wantDN: alice, wantAttr: "title",
		},
		{
			name: "an add with no parent, so the parent", code: rcNoSuchObject,
			record: change(t, "uid=bob,ou=new,dc=alder,dc=test", directory.ChangeAdd), caps: caps,
			wantKind: remedyParent, wantDN: "ou=new,dc=alder,dc=test",
		},
		{
			name: "no rights in the configuration tree, so a second identity", code: rcInsufficientAccess,
			record:   change(t, "cn=config", directory.ChangeModify, "olcIdleTimeout"),
			caps:     directory.Capabilities{Config: directory.ConfigAccess{DN: "cn=config"}},
			wantKind: remedyConfigIdentity,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ldapRemedy(c.code, c.record, c.caps)
			if got == nil {
				t.Fatalf("no remedy for code %d", c.code)
			}
			if string(got.Kind) != c.wantKind {
				t.Errorf("kind %q, want %q", got.Kind, c.wantKind)
			}
			if c.wantDN != "" && (got.Dn == nil || *got.Dn != c.wantDN) {
				t.Errorf("dn %v, want %q", got.Dn, c.wantDN)
			}
			if c.wantAttr != "" && (got.Attribute == nil || *got.Attribute != c.wantAttr) {
				t.Errorf("attribute %v, want %q", got.Attribute, c.wantAttr)
			}
			if got.Label == "" {
				t.Error("a way in with no words on it")
			}
		})
	}
}

func TestRemedyStaysQuietWhenItHasNowhereToSend(t *testing.T) {
	alice := "uid=alice,ou=people,dc=alder,dc=test"
	caps := directory.Capabilities{}

	// A constraint violation on something that is not a password could be any
	// rule the server keeps; pointing at the password policy would be a guess
	// dressed as help.
	if got := ldapRemedy(rcConstraintViolation, change(t, alice, directory.ChangeModify, "employeeNumber"), caps); got != nil {
		t.Errorf("constraint on a non-password: %+v", got)
	}
	// "No such object" on anything but an add is about the entry itself, and
	// its parent is not the answer.
	if got := ldapRemedy(rcNoSuchObject, change(t, alice, directory.ChangeModify, "mail"), caps); got != nil {
		t.Errorf("no such object on a modify: %+v", got)
	}
	// A code with nothing to add.
	if got := ldapRemedy(rcBusy, change(t, alice, directory.ChangeModify, "mail"), caps); got != nil {
		t.Errorf("busy: %+v", got)
	}
}

func TestRemedyFindsTheParentThroughAnEscapedRDN(t *testing.T) {
	// An RDN can hold a comma. Cutting the string at the first one would name
	// a parent that does not exist and send somebody hunting for it.
	record := change(t, `cn=Smith\, Jane,ou=people,dc=alder,dc=test`, directory.ChangeAdd)
	got := ldapRemedy(rcNoSuchObject, record, directory.Capabilities{})
	if got == nil || got.Dn == nil {
		t.Fatalf("no parent: %+v", got)
	}
	if *got.Dn != "ou=people,dc=alder,dc=test" {
		t.Errorf("parent %q", *got.Dn)
	}
}
