package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
)

// The document a round trip produces: an export of one entry, unchanged.
const aliceDocument = `dn: uid=alice,ou=people,dc=alder,dc=test
objectClass: top
objectClass: inetOrgPerson
cn: Alice Liddell
sn: Liddell
uid: alice
mail: alice@alder.test
`

func importBody(t *testing.T, doc string, reconcile bool) *strings.Reader {
	t.Helper()
	body, err := json.Marshal(map[string]any{"ldif": doc, "reconcile": reconcile})
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	return strings.NewReader(string(body))
}

// Without reconciling, a content record is an add — which is what it has always
// been, and what a directory refuses for an entry that exists. Nothing about
// this behaviour changes.
func TestImportWithoutReconcileStillProducesAnAdd(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), entry: entryFixture(t)})

	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", importBody(t, aliceDocument, false))
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200: %s", res.Status, res.Body)
	}
	out := decode[ImportResult](t, res)

	if len(out.Changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(out.Changes))
	}
	if !strings.Contains(out.Changes[0].Ldif, "changetype: add") {
		t.Errorf("the record is not an add:\n%s", out.Changes[0].Ldif)
	}
	if out.Reconciled != nil {
		t.Error("a count was reported for a reconciliation that was not asked for")
	}
}

// The case the feature exists for: the same document, the entry already there
// and identical. There is nothing to confirm, so nothing is offered.
func TestReconcilingAnUnchangedDocumentProducesNoChanges(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps:  defaultCaps(),
		entry: aliceAsLive(t),
	})

	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", importBody(t, aliceDocument, true))
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200: %s", res.Status, res.Body)
	}
	out := decode[ImportResult](t, res)

	if len(out.Changes) != 0 {
		t.Errorf("got %d changes for an unchanged entry:\n%s",
			len(out.Changes), out.Changes[0].Ldif)
	}
	if out.Unchanged == nil || len(*out.Unchanged) != 1 {
		t.Fatalf("the unchanged entry was not reported: %v", out.Unchanged)
	}
	if (*out.Unchanged)[0] != "uid=alice,ou=people,dc=alder,dc=test" {
		t.Errorf("reported %q as unchanged", (*out.Unchanged)[0])
	}
}

// An edited value becomes a modify, and only for the attribute that differs.
func TestReconcilingAnEditedDocumentProducesAModify(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), entry: aliceAsLive(t)})

	edited := strings.Replace(aliceDocument,
		"mail: alice@alder.test", "mail: alice@new.test", 1)

	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", importBody(t, edited, true))
	out := decode[ImportResult](t, res)

	if len(out.Changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(out.Changes))
	}
	rendered := out.Changes[0].Ldif
	if !strings.Contains(rendered, "changetype: modify") {
		t.Errorf("the record is not a modify:\n%s", rendered)
	}
	if !strings.Contains(rendered, "replace: mail") {
		t.Errorf("the edited attribute is not replaced:\n%s", rendered)
	}
	// cn did not change, so it has no business in the modification.
	if strings.Contains(rendered, "replace: cn") {
		t.Errorf("an unchanged attribute was replaced:\n%s", rendered)
	}
	if out.Reconciled == nil || *out.Reconciled != 1 {
		t.Errorf("reconciled count is %v, want 1", out.Reconciled)
	}
}

// An entry the document describes that does not exist yet stays an add. This is
// the mixed document — some entries new, some already there — which is what an
// export of a subtree looks like after somebody adds a person to the file.
func TestAnAbsentEntryStaysAnAddWhenReconciling(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps:    defaultCaps(),
		readErr: &ldapdriver.Error{Code: 32, Message: "No Such Object"},
	})

	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", importBody(t, aliceDocument, true))
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, want 200: %s", res.Status, res.Body)
	}
	out := decode[ImportResult](t, res)

	if len(out.Changes) != 1 {
		t.Fatalf("got %d changes, want the add to survive", len(out.Changes))
	}
	if !strings.Contains(out.Changes[0].Ldif, "changetype: add") {
		t.Errorf("an absent entry did not stay an add:\n%s", out.Changes[0].Ldif)
	}
	if out.Reconciled == nil || *out.Reconciled != 0 {
		t.Errorf("reconciled count is %v, want 0", out.Reconciled)
	}
}

// A read that fails for any other reason is a real failure. Importing half a
// document because one read timed out is the worst available outcome.
func TestAReadFailureStopsTheImport(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps:    defaultCaps(),
		readErr: &ldapdriver.Error{Code: 51, Message: "Busy"},
	})

	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", importBody(t, aliceDocument, true))
	if res.Status < 400 {
		t.Errorf("got %d; a failed read should not produce a usable import", res.Status)
	}
}

// A changetype record already says what it wants. Reconciling one would be
// inventing an intent the document does not carry.
func TestReconcileLeavesChangetypeRecordsAlone(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), entry: aliceAsLive(t)})

	doc := `dn: uid=alice,ou=people,dc=alder,dc=test
changetype: modify
replace: mail
mail: explicit@alder.test
-
`
	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", importBody(t, doc, true))
	out := decode[ImportResult](t, res)

	if len(out.Changes) != 1 {
		t.Fatalf("got %d changes, want the record untouched", len(out.Changes))
	}
	if !strings.Contains(out.Changes[0].Ldif, "explicit@alder.test") {
		t.Errorf("the record was rewritten:\n%s", out.Changes[0].Ldif)
	}
	if out.Reconciled == nil || *out.Reconciled != 0 {
		t.Errorf("a changetype record was counted as reconciled: %v", out.Reconciled)
	}
}

// aliceAsLive is the entry the document describes, as the directory holds it.
func aliceAsLive(t *testing.T) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, "uid=alice,ou=people,dc=alder,dc=test"))
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("inetOrgPerson")})
	e.Set("cn", [][]byte{[]byte("Alice Liddell")})
	e.Set("sn", [][]byte{[]byte("Liddell")})
	e.Set("uid", [][]byte{[]byte("alice")})
	e.Set("mail", [][]byte{[]byte("alice@alder.test")})
	// Held by the directory and absent from every export. Reconciling must
	// never read its absence from the document as a request to remove it.
	e.Set("userPassword", [][]byte{[]byte("{SSHA}averyrealsecret")})
	return e
}

// The rule the whole design rests on, asserted through HTTP.
func TestReconcileNeverRemovesWhatTheDocumentOmits(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), entry: aliceAsLive(t)})

	edited := strings.Replace(aliceDocument,
		"mail: alice@alder.test", "mail: alice@new.test", 1)

	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", importBody(t, edited, true))
	body := res.Body

	// The document never mentions userPassword, so no modification may name it.
	if strings.Contains(body, "userPassword") {
		t.Errorf("reconciling touched an attribute the document does not name:\n%s", body)
	}
	if strings.Contains(body, "averyrealsecret") {
		t.Error("the stored hash reached the response")
	}
}
