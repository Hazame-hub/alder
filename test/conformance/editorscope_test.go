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

		// An entry that actually holds one, on each server: the seed puts an
		// aci on uid=user0002, and OpenLDAP keeps its rules on the database
		// entry in the configuration tree.
		target, attribute := "uid=user0002,ou=people,"+suffix, "aci"
		if s.name == "openldap" {
			target, attribute = "olcDatabase={1}mdb,cn=config", "olcAccess"
		}

		view := entryOf(t, client, base, target)
		held := attributeOf(view, attribute)
		if held == nil {
			t.Fatalf("%s: %s holds no %s, so this case proves nothing about it",
				s.name, target, attribute)
		}
		if held.Kind.Elsewhere == nil || *held.Kind.Elsewhere == "" {
			t.Errorf("%s: %s on %s is editable with nothing saying otherwise",
				s.name, attribute, target)
		} else {
			t.Logf("%s: %s is shown and not editable -- %q", s.name, attribute, *held.Kind.Elsewhere)
		}

		// And neither name is offered as something to add, on either server.
		//
		// Both names on both servers, deliberately. They reach the picker by
		// different routes -- aci is operational and settable, so it arrives
		// in settableOperational; olcAccess is an ordinary attribute of a
		// configuration entry, so it arrives in may -- and checking only the
		// attribute a given server uses left each list unexercised on one of
		// the two, silently.
		lists := map[string][]string{
			"may":                 strs(view.Requirements.May),
			"settableOperational": strs(view.Requirements.SettableOperational),
			"must":                strs(view.Requirements.Must),
		}
		for where, names := range lists {
			for _, name := range names {
				if strings.EqualFold(name, "aci") || strings.EqualFold(name, "olcAccess") {
					t.Errorf("%s: the attribute picker offers %s (from %s), so an access "+
						"rule can be written from the entry editor", s.name, name, where)
				}
			}
		}

		// candidateKinds describes what the picker could add. An empty one
		// would make the loop below prove nothing, so say so.
		candidates := kinds(view.CandidateKinds)
		if len(candidates) == 0 {
			t.Errorf("%s: the entry view offers no candidate attributes at all, so this "+
				"assertion is vacuous", s.name)
		}
		for _, k := range candidates {
			if strings.EqualFold(k.Name, "aci") || strings.EqualFold(k.Name, "olcAccess") {
				t.Errorf("%s: %s is described as a candidate the editor could add", s.name, k.Name)
			}
		}
		t.Logf("%s: %d candidates offered, none of them an access rule", s.name, len(candidates))
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
			// Not a skip. Both servers publish it, the suite reads the schema
			// through it on every other case, and a skip here would mean this
			// case had quietly stopped running.
			t.Fatalf("%s does not say where its subschema is", s.name)
		}

		view := entryOf(t, client, base, caps.SubschemaSubentry)
		found := 0
		for _, name := range []string{"objectClasses", "attributeTypes"} {
			held := attributeOf(view, name)
			if held == nil {
				continue
			}
			found++
			if held.Kind.Elsewhere == nil || *held.Kind.Elsewhere == "" {
				t.Errorf("%s: %s on the subschema is offered as an editable field holding %d values",
					s.name, name, len(held.Values))
			}
		}
		if found == 0 {
			t.Errorf("%s: the subschema entry at %s carries neither objectClasses nor "+
				"attributeTypes, so this case proves nothing", s.name, caps.SubschemaSubentry)
		}
		t.Logf("%s: %s is read here and written by the schema editor", s.name, caps.SubschemaSubentry)
	})
}

// TestTheSchemaOpenLDAPActuallyWritesIsNotATextBoxEither.
//
// The case above reads the subschema, and on OpenLDAP the subschema is a
// generated view: its definition attributes are NO-USER-MODIFICATION and were
// never editable. The schema slapd actually writes lives in the configuration
// tree, carried by olcAttributeTypes and olcObjectClasses, which slapd's own
// configuration schema declares as ordinary user attributes -- no operational
// usage, no NO-USER-MODIFICATION, exactly the shape olcAccess had.
//
// Before this was closed, cn={0}core,cn=schema,cn=config opened in the entry
// editor as fifty-two text boxes holding the core attribute definitions, and
// one character changed in one of them produced a replace of the whole set.
func TestTheSchemaOpenLDAPActuallyWritesIsNotATextBoxEither(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, _ directory.Session) {
		if s.schemaBindDN == "" {
			t.Skipf("%s keeps no schema in a configuration tree Alder binds to separately", s.name)
		}
		client, base := alderSession(t, s, true)

		view := entryOf(t, client, base, "cn={0}core,cn=schema,cn=config")
		found := 0
		for _, name := range []string{"olcAttributeTypes", "olcObjectClasses"} {
			held := attributeOf(view, name)
			if held == nil {
				continue
			}
			found++
			if isTrue(held.Kind.ReadOnly) {
				// Then the premise is wrong and this case is pointless, which
				// is worth knowing.
				t.Errorf("%s: %s is NO-USER-MODIFICATION after all", s.name, name)
			}
			if held.Kind.Elsewhere == nil || *held.Kind.Elsewhere == "" {
				t.Errorf("%s: %s is editable, holding %d definitions -- one keystroke "+
					"in the entry editor replaces the whole schema", s.name, name, len(held.Values))
			}
		}
		if found == 0 {
			t.Errorf("%s: cn={0}core,cn=schema,cn=config carries neither olcAttributeTypes "+
				"nor olcObjectClasses, so this case proves nothing", s.name)
		}
		t.Logf("%s: the writable schema is read here and written by the schema editor", s.name)
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
