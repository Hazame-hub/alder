package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// Plan 1.5 over HTTP: LDIF input and its two modes, typed problems, impact, and
// the refusals on apply.

func planLdif(t *testing.T, rig *testRig, mode, text string) response {
	t.Helper()
	body, err := json.Marshal(map[string]any{"ldif": text, "mode": mode})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	return rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(string(body)))
}

func mustPlan(t *testing.T, res response) Plan {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", res.Status, res.Body)
	}
	return decode[Plan](t, res)
}

func itemFor(t *testing.T, p Plan, dn string) PlanItem {
	t.Helper()
	for _, item := range p.Items {
		if strings.EqualFold(item.Dn, dn) {
			return item
		}
	}
	t.Fatalf("no plan item for %s in %+v", dn, p.Items)
	return PlanItem{}
}

const (
	bobDN   = "uid=bob,ou=people,dc=alder,dc=test"
	carolDN = "uid=carol,ou=people,dc=alder,dc=test"
	daveDN  = "uid=dave,ou=people,dc=alder,dc=test"
)

// --- LDIF modes ------------------------------------------------------------------

// A desired-state document over several entries: one absent, one matching, one
// differing -- and an entry the document does not mention, which must not be
// touched at all.
func TestADesiredStateDocumentIsReconciledAndAbsenceIsNotDeletion(t *testing.T) {
	rig := planRig(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}),
		personAt(t, daveDN, []string{"cn", "Dave"}, []string{"sn", "D"}),
	)
	doc := strings.Join([]string{
		"dn: " + planAlice,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Alice", "sn: L", "",
		"dn: " + bobDN,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Robert", "sn: B", "",
		"dn: " + carolDN,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Carol", "sn: C", "",
	}, "\n")

	p := mustPlan(t, planLdif(t, rig, "desired", doc))

	if got := itemFor(t, p, planAlice).Action; got != PlanActionUnchanged {
		t.Errorf("alice: %q, want unchanged", got)
	}
	bob := itemFor(t, p, bobDN)
	if bob.Action != PlanActionModify || bob.Intent == nil || *bob.Intent != PlanIntentDesired {
		t.Errorf("bob: action %q intent %v, want a desired-state modify", bob.Action, bob.Intent)
	}
	if got := itemFor(t, p, carolDN).Action; got != PlanActionAdd {
		t.Errorf("carol: %q, want add", got)
	}
	// Dave is in the directory and not in the document.
	for _, item := range p.Items {
		if strings.EqualFold(item.Dn, daveDN) {
			t.Fatalf("an entry the document does not mention was planned: %+v", item)
		}
	}
	if p.Counts.Delete != 0 {
		t.Errorf("a desired-state document planned %d deletions", p.Counts.Delete)
	}
}

// An explicit add is planned as written. It is never rewritten into a modify.
func TestAnExplicitAddInChangesModeConflictsWithAnExistingEntry(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
	doc := strings.Join([]string{
		"dn: " + planAlice, "changetype: add",
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Alice Liddell", "sn: L",
	}, "\n")

	item := itemFor(t, mustPlan(t, planLdif(t, rig, "changes", doc)), planAlice)
	if item.Action != PlanActionConflict {
		t.Fatalf("action = %q, want conflict", item.Action)
	}
	if item.Problem == nil || item.Problem.Code != PlanProblemEntryExists {
		t.Errorf("problem = %+v, want entry_exists", item.Problem)
	}
	if item.Record != nil || item.Baseline != nil {
		t.Error("a conflicting add still carries something to apply")
	}
}

// Explicit change LDIF is planned exactly: a modify of an absent entry is a
// typed conflict, a delete and a modify of present ones are what they say.
func TestExplicitChangeLdifIsPlannedAsWritten(t *testing.T) {
	rig := planRig(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"mail", "a@alder.test"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}),
	)
	doc := strings.Join([]string{
		"dn: " + planAlice, "changetype: modify",
		"replace: mail", "mail: alice@alder.test", "-", "",
		"dn: " + bobDN, "changetype: delete", "",
		"dn: " + carolDN, "changetype: modify",
		"replace: mail", "mail: carol@alder.test", "-", "",
	}, "\n")

	p := mustPlan(t, planLdif(t, rig, "changes", doc))
	if got := itemFor(t, p, planAlice).Action; got != PlanActionModify {
		t.Errorf("alice: %q, want modify", got)
	}
	if got := itemFor(t, p, bobDN).Action; got != PlanActionDelete {
		t.Errorf("bob: %q, want delete", got)
	}
	carol := itemFor(t, p, carolDN)
	if carol.Action != PlanActionConflict || carol.Problem == nil || carol.Problem.Code != PlanProblemEntryMissing {
		t.Errorf("carol: %q %+v, want conflict entry_missing", carol.Action, carol.Problem)
	}
	for _, item := range p.Items {
		if item.Intent == nil || *item.Intent != PlanIntentExact {
			t.Errorf("%s was not read as exact: %v", item.Dn, item.Intent)
		}
	}
}

// A desired-state document with a changetype in it has no single meaning, and
// is refused with a code and the record, not guessed at.
func TestADesiredStateDocumentWithAChangetypeIsRefused(t *testing.T) {
	rig := planRig(t)
	doc := strings.Join([]string{
		"dn: " + planAlice,
		"objectClass: top", "objectClass: person", "cn: Alice", "sn: L", "",
		"dn: " + bobDN, "changetype: delete", "",
	}, "\n")
	res := planLdif(t, rig, "desired", doc)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody: %s", res.Status, res.Body)
	}
	body := decode[Error](t, res)
	if body.Error != ErrorErrorLdifModeMismatch {
		t.Errorf("error = %q, want ldif_mode_mismatch", body.Error)
	}
	if body.Affected == nil || len(*body.Affected) != 1 || (*body.Affected)[0].Index != 1 {
		t.Errorf("affected = %+v, want the second record", body.Affected)
	}
}

func TestMalformedAndMisshapenPlanRequestsAreRefused(t *testing.T) {
	rig := planRig(t)
	cases := []struct {
		name string
		body string
	}{
		{"neither input", `{}`},
		{"both inputs", `{"changes":[{"dn":"` + planAlice + `","type":"delete"}],"ldif":"dn: ` + planAlice + `\nchangetype: delete\n"}`},
		{"LDIF that does not parse", `{"ldif":"this is not ldif"}`},
		{"an empty document", `{"ldif":"   "}`},
		{"a URL reference", `{"ldif":"dn: ` + planAlice + `\nchangetype: add\nobjectClass: person\njpegPhoto:< file:///etc/passwd\n"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(tc.body))
			if res.Status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400\nbody: %s", res.Status, res.Body)
			}
		})
	}
}

// Schema violations arrive typed over HTTP, as invalid rather than conflict.
func TestASchemaViolationIsInvalidAndTyped(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
	doc := strings.Join([]string{
		"dn: " + planAlice, "changetype: modify",
		"add: favouriteColour", "favouriteColour: teal", "-",
	}, "\n")
	item := itemFor(t, mustPlan(t, planLdif(t, rig, "changes", doc)), planAlice)
	if item.Action != PlanActionInvalid {
		t.Fatalf("action = %q, want invalid", item.Action)
	}
	if item.Problem == nil || item.Problem.Code != PlanProblemAttributeUndefined {
		t.Errorf("problem = %+v, want attribute_undefined", item.Problem)
	}
}

// --- the import endpoint's own fix -----------------------------------------------

// /import/ldif reconciles content records, as its contract always said, and no
// longer rewrites an explicit `changetype: add` into a modification.
func TestImportReconcilesContentRecordsOnly(t *testing.T) {
	rig := planRig(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}),
	)
	doc := strings.Join([]string{
		"dn: " + planAlice,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Alice Liddell", "sn: L", "",
		"dn: " + bobDN, "changetype: add",
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Robert", "sn: B", "",
	}, "\n")
	body, _ := json.Marshal(map[string]any{"ldif": doc, "reconcile": true})
	res := rig.do(t, http.MethodPost, "/api/v1/import/ldif", strings.NewReader(string(body)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\nbody: %s", res.Status, res.Body)
	}
	result := decode[ImportResult](t, res)
	if result.Requests == nil || len(*result.Requests) != 2 {
		t.Fatalf("requests = %+v, want two", result.Requests)
	}
	if got := (*result.Requests)[0].Type; got != ChangeRequestTypeModify {
		t.Errorf("the content record became %q, want modify", got)
	}
	if got := (*result.Requests)[1].Type; got != ChangeRequestTypeAdd {
		t.Errorf("the explicit changetype: add became %q; it must stay an add", got)
	}
	if result.Reconciled == nil || *result.Reconciled != 1 {
		t.Errorf("reconciled = %v, want 1", result.Reconciled)
	}
}

// --- impact ----------------------------------------------------------------------

// A deleted entry that groups still name: the references are found, and the
// ones the plan does not itself remove are dangling.
func TestDeletingAReferencedEntryReportsDanglingReferences(t *testing.T) {
	admins := "cn=admins,ou=groups,dc=alder,dc=test"
	legacy := "cn=legacy,ou=groups,dc=alder,dc=test"
	group := func(d string, members ...string) *directory.Entry {
		e := directory.NewEntry(mustParse(t, d))
		e.Set("objectClass", [][]byte{[]byte("top"), []byte("groupOfNames")})
		e.Set("cn", [][]byte{[]byte(d[3:strings.IndexByte(d, ',')])})
		values := make([][]byte, 0, len(members))
		for _, m := range members {
			values = append(values, []byte(m))
		}
		e.Set("member", values)
		return e
	}
	alice := personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"})
	adminsEntry := group(admins, planAlice, bobDN)
	legacyEntry := group(legacy, planAlice)

	fake := &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		byDN: map[string]*directory.Entry{
			strings.ToLower(planAlice): alice,
			strings.ToLower(admins):    adminsEntry,
			strings.ToLower(legacy):    legacyEntry,
		},
		entries: []*directory.Entry{alice, adminsEntry, legacyEntry},
	}
	rig := newRig(t, Config{}, fake)

	// Delete alice, and in the same plan take her out of admins -- but not out
	// of legacy.
	body := fmt.Sprintf(`{"changes":[
      {"dn":%q,"type":"modify","mods":[{"op":"delete","name":"member","values":[{"text":%q}]}]},
      {"dn":%q,"type":"delete"}
    ]}`, admins, planAlice, planAlice)
	res := rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(body))
	p := mustPlan(t, res)

	del := itemFor(t, p, planAlice)
	if del.References == nil {
		t.Fatalf("the delete carries no reference impact\nplan: %s", res.Body)
	}
	if del.References.Count != 2 {
		t.Errorf("references = %d, want 2 (admins and legacy)", del.References.Count)
	}
	if del.References.Dangling != 1 {
		t.Errorf("dangling = %d, want 1 (legacy; admins is removed by the plan)", del.References.Dangling)
	}
	if p.Impact == nil || !p.Impact.References.Analysed || p.Impact.References.Dangling != 1 {
		t.Errorf("impact.references = %+v", p.Impact)
	}

	// The modify of admins is a membership removal, reported as such.
	mod := itemFor(t, p, admins)
	if mod.Membership == nil || len(*mod.Membership) != 1 || len((*mod.Membership)[0].Removed) != 1 {
		t.Errorf("admins membership = %+v, want one removed", mod.Membership)
	}
	if p.Impact.Membership.Removed != 1 || p.Impact.Membership.Groups != 1 {
		t.Errorf("impact.membership = %+v", p.Impact.Membership)
	}
}

func TestAPlanClassifiesTheAreaEachChangeLandsIn(t *testing.T) {
	caps := defaultCaps()
	caps.ConfigContext = "cn=config"
	caps.SchemaWrite = directory.SchemaWrite{
		Style:   caps.SchemaWrite.Style,
		Targets: []directory.SchemaTarget{{DN: "cn=schema,cn=config", Name: "schema"}},
	}
	schemaEntry := directory.NewEntry(mustParse(t, "cn=schema,cn=config"))
	schemaEntry.Set("objectClass", [][]byte{[]byte("top")})
	configEntry := directory.NewEntry(mustParse(t, "cn=config"))
	configEntry.Set("objectClass", [][]byte{[]byte("top")})
	alice := personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"})

	rig := newRig(t, Config{}, &fakeSession{
		caps: caps, sch: testSchema(t),
		byDN: map[string]*directory.Entry{
			"cn=schema,cn=config":      schemaEntry,
			"cn=config":                configEntry,
			strings.ToLower(planAlice): alice,
		},
	})
	body := fmt.Sprintf(`{"changes":[
      {"dn":"cn=schema,cn=config","type":"modify","mods":[{"op":"add","name":"description","values":[{"text":"x"}]}]},
      {"dn":"cn=config","type":"modify","mods":[{"op":"add","name":"description","values":[{"text":"y"}]}]},
      {"dn":%q,"type":"modify","mods":[{"op":"add","name":"description","values":[{"text":"z"}]}]}
    ]}`, planAlice)
	p := mustPlan(t, rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(body)))

	want := map[string]PlanTargetKind{
		"cn=schema,cn=config": PlanTargetSchema,
		"cn=config":           PlanTargetConfig,
		planAlice:             PlanTargetData,
	}
	for d, kind := range want {
		item := itemFor(t, p, d)
		if item.Kind == nil || *item.Kind != kind {
			t.Errorf("%s: kind %v, want %s", d, item.Kind, kind)
		}
	}
}

// --- refusals on apply -------------------------------------------------------------

// A baseline lifted from one planned change and attached to another is not a
// plan, and is refused before anything runs.
func TestApplyRefusesAChangeThatIsNotItsPlan(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"},
		[]string{"mail", "old@alder.test"}))
	p := mustPlan(t, rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(fmt.Sprintf(
		`{"changes":[{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail","values":[{"text":"new@alder.test"}]}]}]}`,
		planAlice))))
	item := p.Items[0]

	substituted := *item.Record
	(*substituted.Mods)[0].Values = &[]AttributeValue{{Text: ptr("attacker@alder.test")}}
	substituted.Baseline = item.Baseline

	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply",
		strings.NewReader(fmt.Sprintf(`{"changes":[%s]}`, mustJSON(t, substituted))))
	if res.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody: %s", res.Status, res.Body)
	}
	if body := decode[Error](t, res); body.Error != ErrorErrorPlanMismatch {
		t.Errorf("error = %q, want plan_mismatch", body.Error)
	}
	if len(rig.fake.applied) != 0 {
		t.Errorf("%d changes were applied despite the refusal", len(rig.fake.applied))
	}

	// The same through the single-change endpoint.
	res = rig.do(t, http.MethodPost, "/api/v1/changes/apply", strings.NewReader(mustJSON(t, substituted)))
	if res.Status != http.StatusBadRequest {
		t.Fatalf("single change: status = %d, want 400\nbody: %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 0 {
		t.Error("the single-change endpoint applied a change that was not its plan")
	}
}

// Every stale change is named, so the operator knows the whole of what to look
// at again, and nothing runs.
func TestAStaleRefusalNamesEveryStaleChange(t *testing.T) {
	alice := personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"mail", "a@alder.test"})
	bob := personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}, []string{"mail", "b@alder.test"})
	carol := personAt(t, carolDN, []string{"cn", "Carol"}, []string{"sn", "C"}, []string{"mail", "c@alder.test"})
	rig := planRig(t, alice, bob, carol)

	var changes []string
	for _, d := range []string{planAlice, bobDN, carolDN} {
		changes = append(changes, fmt.Sprintf(
			`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail","values":[{"text":"new@alder.test"}]}]}`, d))
	}
	p := mustPlan(t, rig.do(t, http.MethodPost, "/api/v1/plan",
		strings.NewReader(`{"changes":[`+strings.Join(changes, ",")+`]}`)))

	alice.Set("mail", [][]byte{[]byte("moved@alder.test")})
	carol.Set("mail", [][]byte{[]byte("moved@alder.test")})

	var planned []string
	for _, item := range p.Items {
		planned = append(planned, mustJSON(t, withBaseline(*item.Record, *item.Baseline)))
	}
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply",
		strings.NewReader(`{"changes":[`+strings.Join(planned, ",")+`]}`))
	if res.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409\nbody: %s", res.Status, res.Body)
	}
	body := decode[Error](t, res)
	// conflict, exactly as 1.4 answered, with the precision in cause.
	if body.Error != ErrorErrorConflict {
		t.Errorf("error = %q, want conflict (the 1.4 code)", body.Error)
	}
	if body.Cause == nil || *body.Cause != ErrorCausePlanStale {
		t.Errorf("cause = %v, want plan_stale", body.Cause)
	}
	if body.Affected == nil || len(*body.Affected) != 2 {
		t.Fatalf("affected = %+v, want alice and carol", body.Affected)
	}
	if (*body.Affected)[0].Index != 0 || (*body.Affected)[1].Index != 2 {
		t.Errorf("affected = %+v, want changes 0 and 2", *body.Affected)
	}
	if len(rig.fake.applied) != 0 {
		t.Errorf("%d changes ran before the stale ones were found", len(rig.fake.applied))
	}
}

// A plan of nothing, applied the way a client applies a plan, writes nothing.
func TestApplyingANoOpPlanWritesNothing(t *testing.T) {
	rig := planRig(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}),
	)
	doc := strings.Join([]string{
		"dn: " + planAlice, "objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Alice", "sn: L", "",
		"dn: " + bobDN, "objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Bob", "sn: B", "",
	}, "\n")
	p := mustPlan(t, planLdif(t, rig, "desired", doc))
	if p.Counts.Unchanged != 2 || p.Counts.Modify+p.Counts.Add+p.Counts.Delete != 0 {
		t.Fatalf("counts = %+v, want two unchanged and nothing else", p.Counts)
	}
	for _, item := range p.Items {
		if item.Record != nil {
			t.Errorf("%s: an unchanged item offers a record to apply", item.Dn)
		}
	}
	if len(rig.fake.applied) != 0 {
		t.Errorf("planning wrote %d changes", len(rig.fake.applied))
	}
}

// --- secrets ---------------------------------------------------------------------

// A desired-state document carrying a password: the plan says the password
// would change and shows neither the old value nor the new one, in the record
// or in the preview.
func TestAPlanNeverReturnsASensitiveValue(t *testing.T) {
	const secret = "{SSHA}c2VjcmV0LXZhbHVlLXRoYXQtbXVzdC1ub3QtbGVhaw=="
	alice := personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"},
		[]string{"userPassword", "{SSHA}b2xkLXZhbHVl"})
	rig := planRig(t, alice)
	doc := strings.Join([]string{
		"dn: " + planAlice,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Alice", "sn: L", "userPassword: " + secret, "",
	}, "\n")

	res := planLdif(t, rig, "desired", doc)
	if strings.Contains(res.Body, secret) || strings.Contains(res.Body, "c2VjcmV0") {
		t.Fatalf("the plan response carries the new password:\n%s", res.Body)
	}
	if strings.Contains(res.Body, "b2xkLXZhbHVl") {
		t.Fatalf("the plan response carries the directory's current password:\n%s", res.Body)
	}
	item := itemFor(t, mustPlan(t, res), planAlice)
	if item.Action != PlanActionModify || item.Record == nil {
		t.Fatalf("action = %q, want a modify of userPassword", item.Action)
	}
	mods := *item.Record.Mods
	if len(mods) != 1 || !strings.EqualFold(mods[0].Name, "userPassword") {
		t.Fatalf("mods = %+v, want the one userPassword replace", mods)
	}
	v := (*mods[0].Values)[0]
	if v.Text != nil || v.Base64 != nil || v.Size == nil || *v.Size != len(secret) {
		t.Errorf("the withheld value is %+v, want its size alone", v)
	}
	if item.Preview == nil || !strings.Contains(item.Preview.Ldif, "withheld") {
		t.Errorf("the preview does not say the value is withheld: %+v", item.Preview)
	}
}

// Posting a withheld record straight back must not write an empty password in
// place of the one it never carried.
func TestAWithheldValueCannotBeAppliedAsAnEmptyOne(t *testing.T) {
	rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
	body := fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"userPassword","values":[{"size":42}]}]}`,
		planAlice)
	res := rig.do(t, http.MethodPost, "/api/v1/changes/apply", strings.NewReader(body))
	if res.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\nbody: %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 0 {
		t.Fatalf("a withheld value was applied: %+v", rig.fake.applied)
	}
}

// The token binds a sensitive attribute by shape, so the client can apply the
// planned change by supplying the value from its own copy -- and a different
// operation on the same entry is still caught.
func TestAPlannedPasswordChangeAppliesWithTheValueSuppliedByTheClient(t *testing.T) {
	const secret = "{SSHA}bmV3LXZhbHVl"
	rig := planRig(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"},
		[]string{"userPassword", "{SSHA}b2xk"}))
	change := fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"userPassword","values":[{"text":%q}]}]}`,
		planAlice, secret)
	p := mustPlan(t, rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(`{"changes":[`+change+`]}`)))
	baseline := *p.Items[0].Baseline

	var original ChangeRequest
	if err := json.Unmarshal([]byte(change), &original); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	original.Baseline = &baseline
	res := rig.do(t, http.MethodPost, "/api/v1/changes/apply", strings.NewReader(mustJSON(t, original)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\nbody: %s", res.Status, res.Body)
	}
	if len(rig.fake.applied) != 1 || string(rig.fake.applied[0].Mods[0].Values[0]) != secret {
		t.Fatalf("applied = %+v, want the client's value", rig.fake.applied)
	}
	if strings.Contains(res.Body, secret) {
		t.Error("the apply response echoes the password")
	}
}
