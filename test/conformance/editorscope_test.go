//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// What the entry editor offers, against both servers.
//
// Two mistakes in opposite directions, and the same cause: the editor decided
// what to offer from whether an attribute was operational, which answers
// neither question it was being asked.
//
// One way, it refused what an administrator most needs. The lock attribute is
// operational on both servers and NO-USER-MODIFICATION on neither, and the
// viewer above the editor said so in a heading -- "kept by the directory,
// yours to set" -- while no field existed to set it in.
//
// The other way, it offered what Alder does not write. `aci` is operational
// and settable, so it appeared in the attribute picker on every 389 DS entry
// that did not already carry one; `olcAccess` is an ordinary attribute, so it
// was never filtered at all and an OpenLDAP database entry opened as four
// editable text boxes of access rules.

func entryOf(t *testing.T, client *http.Client, base, target string) api.EntryView {
	t.Helper()
	res := get(t, client, base+"/entry?dn="+url.QueryEscape(target)+"&includeOperational=true")
	if res.status != http.StatusOK {
		t.Fatalf("GET /entry %s: %d\n%s", target, res.status, res.body)
	}
	var view api.EntryView
	if err := json.Unmarshal([]byte(res.body), &view); err != nil {
		t.Fatalf("decoding the entry view: %v\n%s", err, res.body)
	}
	return view
}

func attributeOf(view api.EntryView, name string) *api.EntryAttribute {
	for i := range view.Attributes {
		if strings.EqualFold(view.Attributes[i].Name, name) {
			return &view.Attributes[i]
		}
	}
	return nil
}

// TestTheLockAttributeIsOfferedOnAnEntryThatAlreadyCarriesIt.
//
// The picker offers what an entry does not have, so on a *locked* account --
// the only account anybody needs to unlock -- the lock attribute was in
// neither list: not in the picker, because it was present, and not in the
// editor, because it was operational. The audit found exactly this and had to
// hand-write LDIF.
func TestTheLockAttributeIsOfferedOnAnEntryThatAlreadyCarriesIt(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alderSession(t, s, false)
		target := mustDN(t, "uid=user0107,ou=people,"+suffix)

		t.Cleanup(func() {
			_ = sess.Apply(ctx(t), directory.ChangeRecord{
				DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: s.lockAttr}},
			})
		})
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN: target, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.lockAttr,
				Values: [][]byte{[]byte(s.lockValue)}}},
		}); err != nil {
			t.Fatalf("%s: locking the account: %v", s.name, err)
		}

		view := entryOf(t, client, base, target.String())
		held := attributeOf(view, s.lockAttr)
		if held == nil {
			t.Fatalf("%s: the entry view does not carry %s at all", s.name, s.lockAttr)
		}
		if isTrue(held.Kind.ReadOnly) {
			t.Errorf("%s: %s is marked read-only, so no editor will offer it -- "+
				"and the server does not say NO-USER-MODIFICATION about it", s.name, s.lockAttr)
		}
		if held.Kind.Elsewhere != nil {
			t.Errorf("%s: %s is held back from the editor: %q", s.name, s.lockAttr, *held.Kind.Elsewhere)
		}
		if !isTrue(held.Kind.Operational) {
			t.Errorf("%s: %s is not marked operational, so it would be filed with "+
				"ordinary attributes and lose the warning that the server maintains it",
				s.name, s.lockAttr)
		}
		t.Logf("%s: %s is present, writable and offered", s.name, s.lockAttr)
	})
}

// TestTheEditorDoesNotOfferToRewriteAnAccessRule.
//
// Reading the rules a server holds is 1.19. Writing them is out of scope, and
// the reason is in the decisions log: an access rule is the one change that
// can lock every administrator out, and both servers evaluate rules
// first-match-wins, so a correct-looking edit to one line changes the meaning
// of every line after it.
func TestTheEditorDoesNotOfferToRewriteAnAccessRule(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		client, base := alderSession(t, s, s.schemaBindDN != "")

		// Where this server keeps its rules, and an entry that holds one.
		// An entry that actually holds one, on each server: the seed puts an
		// aci on uid=user0002, and OpenLDAP keeps its rules on the database
		// entry in the configuration tree.
		target, attribute := "uid=user0002,ou=people,"+suffix, "aci"
		if s.name == "openldap" {
			target, attribute = "olcDatabase={1}mdb,cn=config", "olcAccess"
		}

		view := entryOf(t, client, base, target)
		if held := attributeOf(view, attribute); held != nil {
			if held.Kind.Elsewhere == nil || *held.Kind.Elsewhere == "" {
				t.Errorf("%s: %s on %s is editable with nothing saying otherwise",
					s.name, attribute, target)
			} else {
				t.Logf("%s: %s is shown and not editable -- %q", s.name, attribute, *held.Kind.Elsewhere)
			}
		} else {
			t.Errorf("%s: %s holds no %s, so this case proves nothing about it",
				s.name, target, attribute)
		}

		// And it is not offered as something to add, either -- which is how
		// it was reachable on every entry that did not already have one.
		for _, name := range strs(view.Requirements.SettableOperational) {
			if strings.EqualFold(name, attribute) {
				t.Errorf("%s: the attribute picker offers %s, so an access rule can be "+
					"written from the entry editor", s.name, attribute)
			}
		}
		for _, name := range strs(view.Requirements.May) {
			if strings.EqualFold(name, attribute) {
				t.Errorf("%s: %s is in the may list the picker is built from", s.name, attribute)
			}
		}
		for _, k := range kinds(view.CandidateKinds) {
			if strings.EqualFold(k.Name, attribute) {
				t.Errorf("%s: %s is described as a candidate the editor could add", s.name, attribute)
			}
		}
	})
}

// TestTheSubschemaIsNotATextBox.
//
// The subschema entry carries over a thousand definitions on 389 DS. A schema
// change is an add or a delete of one of them; what a text box holding all of
// them can express is a replacement of the lot, which is not a schema edit at
// all. The schema editor exists and writes them one at a time.
func TestTheSubschemaIsNotATextBox(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alderSession(t, s, s.schemaBindDN != "")
		caps := sess.Capabilities()
		if caps.SubschemaSubentry == "" {
			t.Skip("this server does not say where its subschema is")
		}

		view := entryOf(t, client, base, caps.SubschemaSubentry)
		for _, name := range []string{"objectClasses", "attributeTypes"} {
			held := attributeOf(view, name)
			if held == nil {
				t.Errorf("%s: the subschema entry carries no %s, so this case proves nothing",
					s.name, name)
				continue
			}
			if held.Kind.Elsewhere == nil || *held.Kind.Elsewhere == "" {
				t.Errorf("%s: %s on the subschema is offered as an editable field holding %d values",
					s.name, name, len(held.Values))
			}
		}
		t.Logf("%s: %s is read here and written by the schema editor", s.name, caps.SubschemaSubentry)
	})
}

func isTrue(b *bool) bool { return b != nil && *b }

func strs(v *[]string) []string {
	if v == nil {
		return nil
	}
	return *v
}

func kinds(v *[]api.AttributeKind) []api.AttributeKind {
	if v == nil {
		return nil
	}
	return *v
}
