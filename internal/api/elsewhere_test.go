package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// Two attributes were writable in the general entry editor that the decisions
// log says Alder does not write.
//
// `aci` is operational and not NO-USER-MODIFICATION, so it arrived in the
// list of operational attributes a client may set and the picker offered it
// on every entry that did not already carry one. `olcAccess` is not
// operational at all -- it is an ordinary attribute of an OpenLDAP database
// entry -- so nothing filtered it anywhere: opening `olcDatabase={1}mdb` in
// the entry editor produced four text boxes containing the server's access
// rules, `{0}` ordering prefixes included.
//
// Reading access rules is 1.19. Writing them is out of scope, and not for
// tidiness: it is the one change that can lock every administrator out of the
// directory, and both servers evaluate rules first-match-wins, so an edit that
// looks right on its own line changes the meaning of every rule after it.

func TestTheEditorDoesNotOfferToWriteAnAccessRule(t *testing.T) {
	for _, name := range []string{"aci", "olcAccess", "ACI", "olcaccess;binary"} {
		why := editedElsewhere(name)
		if why == "" {
			t.Errorf("%s is offered in the entry editor, which writes access rules", name)
			continue
		}
		if !strings.Contains(why, "Access") {
			t.Errorf("%s: the reason does not say where to look: %q", name, why)
		}
	}
}

func TestTheEditorSendsSchemaDefinitionsToTheSchemaEditor(t *testing.T) {
	// The subschema entry carries a thousand of these on 389 DS, and a schema
	// change is an add or a delete of one definition. A text box holding all
	// of them can only express a replacement of the lot.
	for _, name := range []string{"objectClasses", "attributeTypes", "ldapSyntaxes", "nameForms"} {
		if editedElsewhere(name) == "" {
			t.Errorf("%s is offered as a text box in the entry editor", name)
		}
	}
}

func TestOrdinaryAttributesAreStillOffered(t *testing.T) {
	// The exclusion is four lines of switch, and a switch that grows by
	// accident takes the editor with it.
	for _, name := range []string{"cn", "sn", "userPassword", "nsAccountLock", "member", "olcDbDirectory"} {
		if why := editedElsewhere(name); why != "" {
			t.Errorf("%s is held back from the editor: %q", name, why)
		}
	}
}

func TestTheAccessAttributeIsNotOfferedAsSomethingToAdd(t *testing.T) {
	sch := schema.Load("cn=subschema", map[string][]string{
		"objectClasses": {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
		},
		"attributeTypes": {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.16.840.1.113730.3.1.55 NAME 'aci' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 USAGE directoryOperation )",
			"( 2.16.840.1.113730.3.1.610 NAME 'nsAccountLock' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 USAGE directoryOperation )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the test schema does not parse: %v", sch.Errors)
	}
	entry := directory.NewEntry(mustParse(t, "uid=alice,ou=people,dc=alder,dc=test"))
	entry.Set("objectClass", [][]byte{[]byte("top")})
	req := sch.Requirements([]string{"top"})

	// The schema still says the server would take it: this is Alder's
	// decision, not a claim about the directory.
	if !containsAnyCase(sch.SettableOperational(), "aci") {
		t.Fatal("the fixture is wrong: the schema does not call aci settable")
	}

	for _, k := range candidateKinds(entry, sch, req) {
		if strings.EqualFold(k.Name, "aci") {
			t.Error("the attribute picker offers aci, so an access rule can be written from the entry editor")
		}
	}
	if settable := deref(requirementsView(req, sch).SettableOperational); containsAnyCase(settable, "aci") {
		t.Errorf("the picker's list still carries aci: %v", settable)
	} else if !containsAnyCase(settable, "nsAccountLock") {
		t.Errorf("the filter took nsAccountLock with it: %v", settable)
	}
}

func TestTheReasonTravelsWithTheAttribute(t *testing.T) {
	// The viewer prints it. An attribute shown and not editable, with nothing
	// saying why, is the same dead end from the other direction: the audit
	// found nsAccountLock filed under "yours to set" with no field to set it
	// in, and spent seventeen interactions working out that there was none.
	k := attributeKind(schema.AttributeKind{Name: "olcAccess", Kind: "string", Known: true})
	if k.Elsewhere == nil || *k.Elsewhere == "" {
		t.Fatal("olcAccess carries no reason, so the viewer can only show an absence")
	}
	plain := attributeKind(schema.AttributeKind{Name: "cn", Kind: "string", Known: true})
	if plain.Elsewhere != nil {
		t.Errorf("cn carries a reason it should not: %q", *plain.Elsewhere)
	}
}

// containsAnyCase is the neighbouring contains() helpers less the case
// sensitivity: an attribute name is case-insensitive on the wire, and this is
// about which attribute, not how it was spelled.
func containsAnyCase(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
