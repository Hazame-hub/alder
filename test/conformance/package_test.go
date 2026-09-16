//go:build conformance

package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.11: change packages against both servers.
//
// The package is built once and carried unchanged. Each server is asked what
// its intent means there, plans the changes that are ready, and applies its own
// plan. Nothing from one server's execution is replayed on the other: the
// schema modification that installs a definition looks different on each, and
// the package never held one.

const (
	packAttrOID  = "1.3.6.1.4.1.99997.1.60"
	packClassOID = "1.3.6.1.4.1.99997.2.60"
	packEntryDN  = "uid=pack-proof,ou=people," + suffix
)

func packAttrDef(desc string) string {
	return "( " + packAttrOID + " NAME 'alderPackProofTeam' DESC '" + desc +
		"' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"
}

const packClassDef = "( " + packClassOID +
	" NAME 'alderPackProofClass' SUP top AUXILIARY MUST alderPackProofTeam )"

// packageChanges are the changes a package is built from: a schema attribute
// type, the class that needs it, and an entry that uses both. The schema
// changes are expressed the way this server needs them, which is exactly what
// the package has to stop carrying.
func packageChanges(t *testing.T, sess directory.Session, target string) []api.ChangeRequest {
	t.Helper()
	write := sess.Capabilities().SchemaWrite
	attrAttr, err := write.Attribute(directory.SchemaDefAttributeType)
	if err != nil {
		t.Fatal(err)
	}
	classAttr, err := write.Attribute(directory.SchemaDefObjectClass)
	if err != nil {
		t.Fatal(err)
	}
	schemaChange := func(attribute, definition string) api.ChangeRequest {
		values := []api.AttributeValue{{Text: ptr(definition)}}
		return api.ChangeRequest{Dn: target, Type: api.ChangeRequestTypeModify,
			Mods: &[]api.ChangeMod{{Op: api.ChangeModOpAdd, Name: attribute, Values: &values}}}
	}
	entry := api.ChangeRequest{Dn: packEntryDN, Type: api.ChangeRequestTypeAdd,
		Attributes: &[]api.ChangeAttribute{
			{Name: "objectClass", Values: []api.AttributeValue{{Text: ptr("top")}, {Text: ptr("person")},
				{Text: ptr("organizationalPerson")}, {Text: ptr("inetOrgPerson")},
				{Text: ptr("alderPackProofClass")}}},
			{Name: "uid", Values: []api.AttributeValue{{Text: ptr("pack-proof")}}},
			{Name: "cn", Values: []api.AttributeValue{{Text: ptr("Pack Proof")}}},
			{Name: "sn", Values: []api.AttributeValue{{Text: ptr("Proof")}}},
			{Name: "alderPackProofTeam", Values: []api.AttributeValue{{Text: ptr("platform")}}},
		}}
	return []api.ChangeRequest{schemaChange(attrAttr, packAttrDef("package proof")),
		schemaChange(classAttr, packClassDef), entry}
}

func ptr[T any](v T) *T { return &v }

// buildPackage asks a server to turn changes into a package.
func buildPackage(t *testing.T, client *http.Client, base string, changes []api.ChangeRequest, title string) []byte {
	t.Helper()
	body, err := json.Marshal(api.PackageBuildRequest{Title: &title, Changes: changes})
	if err != nil {
		t.Fatal(err)
	}
	res := post(t, client, base+"/packages/build", string(body))
	if res.status != http.StatusOK {
		t.Fatalf("building the package: status %d\n%s", res.status, res.body)
	}
	return []byte(res.body)
}

func validatePackage(t *testing.T, client *http.Client, base string, document []byte,
	schemaTarget ...string) api.PackageValidation {
	t.Helper()
	body := `{"package":` + string(document)
	if len(schemaTarget) == 1 && schemaTarget[0] != "" {
		body += `,"schemaTarget":` + quote(schemaTarget[0])
	}
	body += `}`
	res := post(t, client, base+"/packages/validate", body)
	if res.status != http.StatusOK {
		t.Fatalf("validating: status %d\n%s", res.status, res.body)
	}
	return decodeInto[api.PackageValidation](t, res)
}

func packageStatuses(v api.PackageValidation) map[string]string {
	out := map[string]string{}
	for _, item := range v.Items {
		out[item.Id] = string(item.Status)
	}
	return out
}

// readyPackageChanges are the prepared changes of the ready items, in the
// order this target gave.
func readyPackageChanges(v api.PackageValidation) []api.ChangeRequest {
	byID := map[string]api.PackageValidationItem{}
	for _, item := range v.Items {
		byID[item.Id] = item
	}
	var out []api.ChangeRequest
	for _, id := range v.Order {
		item := byID[id]
		if item.Status != api.PackageStatusReady || item.Changes == nil {
			continue
		}
		out = append(out, *item.Changes...)
	}
	return out
}

// removePackageState puts a server back: the entry, then the class, then the
// attribute type, one definition per apply.
func removePackageState(t *testing.T, sess directory.Session, target string) {
	t.Helper()
	_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, packEntryDN), Type: directory.ChangeDelete})
	for _, d := range []struct {
		kind directory.SchemaDefKind
		oid  string
	}{{directory.SchemaDefObjectClass, packClassOID}, {directory.SchemaDefAttributeType, packAttrOID}} {
		_ = applySchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: d.kind,
			Op: directory.SchemaOpDelete, OID: d.oid})
	}
}

func TestPackageValidatePlanApplyOverHTTP(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)
		client, base := alderSession(t, s, true)
		removePackageState(t, sess, target)
		t.Cleanup(func() { removePackageState(t, sess, target) })

		document := buildPackage(t, client, base, packageChanges(t, sess, target), "package proof")

		// 1. What travels is intent: no baseline, no password, and no trace of
		// where this server keeps its schema.
		for _, forbidden := range []string{"baseline", "newPassword", target} {
			if strings.Contains(string(document), forbidden) {
				t.Fatalf("the package carries %q:\n%s", forbidden, document)
			}
		}

		// 2. This target can take all of it, in dependency order.
		v := validatePackage(t, client, base, document, target)
		if v.Counts.Ready != 3 || len(v.Order) != 3 {
			t.Fatalf("counts %+v\nitems %+v", v.Counts, v.Items)
		}
		ready := readyPackageChanges(v)
		if len(ready) != 3 {
			t.Fatalf("prepared %d changes", len(ready))
		}
		// The schema changes became this server's own modifications, built now.
		if ready[0].Dn != target || ready[0].Type != api.ChangeRequestTypeModify {
			t.Errorf("first prepared change %+v", ready[0])
		}
		if ready[2].Dn != packEntryDN || ready[2].Type != api.ChangeRequestTypeAdd {
			t.Errorf("last prepared change %+v", ready[2])
		}

		// 3. Plan and apply, one change at a time, as the plan issues its own
		// tokens for each.
		for _, change := range ready {
			planAndApplyChanges(t, client, base, []api.ChangeRequest{change})
		}

		// 4. The directory holds what the package intended.
		entry, err := sess.Read(ctx(t), mustDN(t, packEntryDN), []string{"alderPackProofTeam"})
		if err != nil {
			t.Fatalf("reading the entry the package created: %v", err)
		}
		if got := entry.GetOne("alderPackProofTeam"); got != "platform" {
			t.Errorf("the entry holds %q", got)
		}

		// 5. The same package, revalidated here, now has nothing to do.
		v = validatePackage(t, client, base, document, target)
		if v.Counts.AlreadySatisfied != 3 || v.Counts.Ready != 0 {
			t.Fatalf("after applying: counts %+v\nstatuses %+v", v.Counts, packageStatuses(v))
		}

		// 6. A package whose dependency is missing says so rather than failing
		// somewhere in the middle of an apply.
		orphan := buildPackage(t, client, base, []api.ChangeRequest{
			{Dn: "uid=pack-proof-orphan,ou=people," + suffix, Type: api.ChangeRequestTypeAdd,
				Attributes: &[]api.ChangeAttribute{
					{Name: "objectClass", Values: []api.AttributeValue{{Text: ptr("top")}, {Text: ptr("person")},
						{Text: ptr("organizationalPerson")}, {Text: ptr("inetOrgPerson")},
						{Text: ptr("alderNoSuchClass")}}},
					{Name: "uid", Values: []api.AttributeValue{{Text: ptr("pack-proof-orphan")}}},
					{Name: "cn", Values: []api.AttributeValue{{Text: ptr("Orphan")}}},
					{Name: "sn", Values: []api.AttributeValue{{Text: ptr("Orphan")}}},
				}},
		}, "a class nobody defines")
		v = validatePackage(t, client, base, orphan, target)
		if v.Counts.DependencyMissing != 1 {
			t.Errorf("a missing class: counts %+v items %+v", v.Counts, v.Items)
		}

		// 7. A destructive item is marked, and stays a deliberate choice.
		deletion := buildPackage(t, client, base, []api.ChangeRequest{
			{Dn: packEntryDN, Type: api.ChangeRequestTypeDelete}}, "remove the proof entry")
		v = validatePackage(t, client, base, deletion, target)
		if len(v.Items) != 1 || !v.Items[0].Destructive || v.Items[0].Status != api.PackageStatusReady {
			t.Fatalf("a deletion: %+v", v.Items)
		}

		// 8. A password change cannot be packaged at all.
		secret := post(t, client, base+"/packages/build", `{"changes":[{"dn":`+quote(packEntryDN)+
			`,"type":"setpassword","newPassword":"hunter2"}]}`)
		if secret.status != http.StatusBadRequest {
			t.Fatalf("a package of one password change: status %d\n%s", secret.status, secret.body)
		}
		if strings.Contains(secret.body, "hunter2") {
			t.Error("the refusal repeats the password")
		}
	})
}

func quote(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}

// One package, both servers: the same bytes, validated and planned separately.
// This is promotion, and it is why a package may not carry execution.
func TestOnePackageIsPromotedToBothServers(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	// Built on the first server, from its own way of writing schema changes.
	first, second := servers[0], servers[1]
	firstSession := connectForSchema(t, first)
	firstTarget := schemaTarget(t, firstSession)
	firstClient, firstBase := alderSession(t, first, true)
	removePackageState(t, firstSession, firstTarget)
	t.Cleanup(func() { removePackageState(t, firstSession, firstTarget) })

	document := buildPackage(t, firstClient, firstBase,
		packageChanges(t, firstSession, firstTarget), "promoted between servers")

	secondSession := connectForSchema(t, second)
	secondTarget := schemaTarget(t, secondSession)
	secondClient, secondBase := alderSession(t, second, true)
	removePackageState(t, secondSession, secondTarget)
	t.Cleanup(func() { removePackageState(t, secondSession, secondTarget) })

	// Before anything is applied, one side is given a different baseline: the
	// attribute type is already there, with the same definition.
	mustSchemaChange(t, secondSession, directory.SchemaChangeRequest{TargetDN: secondTarget,
		Kind: directory.SchemaDefAttributeType, Op: directory.SchemaOpAdd, Definition: packAttrDef("package proof")})

	firstValidation := validatePackage(t, firstClient, firstBase, document, firstTarget)
	secondValidation := validatePackage(t, secondClient, secondBase, document, secondTarget)

	// The same package. Different answers, because the directories differ.
	if firstValidation.Counts.Ready != 3 {
		t.Fatalf("%s: counts %+v", first.name, firstValidation.Counts)
	}
	if secondValidation.Counts.Ready != 2 || secondValidation.Counts.AlreadySatisfied != 1 {
		t.Fatalf("%s: counts %+v statuses %+v", second.name, secondValidation.Counts, packageStatuses(secondValidation))
	}
	// And the prepared changes are each server's own: the schema modification
	// names each one's schema entry.
	firstReady, secondReady := readyPackageChanges(firstValidation), readyPackageChanges(secondValidation)
	if firstReady[0].Dn != firstTarget || secondReady[0].Dn != secondTarget {
		t.Fatalf("prepared for %s: %s; for %s: %s", first.name, firstReady[0].Dn, second.name, secondReady[0].Dn)
	}
	if firstTarget != secondTarget {
		t.Logf("the same intent became a change to %s on %s and to %s on %s",
			firstTarget, first.name, secondTarget, second.name)
	}

	// Apply on both, each through its own plan.
	for _, change := range firstReady {
		planAndApplyChanges(t, firstClient, firstBase, []api.ChangeRequest{change})
	}
	for _, change := range secondReady {
		planAndApplyChanges(t, secondClient, secondBase, []api.ChangeRequest{change})
	}

	// Both now hold what the package intended, and the package says so.
	for _, env := range []struct {
		name   string
		client *http.Client
		base   string
		target string
	}{{first.name, firstClient, firstBase, firstTarget}, {second.name, secondClient, secondBase, secondTarget}} {
		v := validatePackage(t, env.client, env.base, document, env.target)
		if v.Counts.AlreadySatisfied != 3 {
			t.Errorf("%s after applying: counts %+v statuses %+v", env.name, v.Counts, packageStatuses(v))
		}
	}
}

// Recovery belongs to the apply that happened, not to the intent. The same
// package applied to two directories produces two different bundles.
func TestRecoveryIsTargetSpecificNotPackageSpecific(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	type run struct {
		name   string
		bundle string
	}
	var runs []run
	var document []byte

	for _, s := range servers {
		sess := connectForSchema(t, s)
		client, base := alderSession(t, s, true)
		dn := fmt.Sprintf("uid=pack-recovery,ou=people,%s", suffix)
		clean := func() {
			_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, dn), Type: directory.ChangeDelete})
		}
		clean()
		t.Cleanup(clean)

		// The entry each server starts with differs, which is the whole point:
		// what a compensation has to restore is what was there.
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, dn), Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("person"),
					[]byte("organizationalPerson"), []byte("inetOrgPerson")}},
				{Name: "uid", Values: [][]byte{[]byte("pack-recovery")}},
				{Name: "cn", Values: [][]byte{[]byte("Pack Recovery")}},
				{Name: "sn", Values: [][]byte{[]byte("Recovery")}},
				{Name: "description", Values: [][]byte{[]byte("as " + s.name + " had it")}},
			}})

		// One package, built once, used for both.
		if document == nil {
			document = buildPackage(t, client, base, []api.ChangeRequest{
				{Dn: dn, Type: api.ChangeRequestTypeModify, Mods: &[]api.ChangeMod{{
					Op: api.ChangeModOpReplace, Name: "description",
					Values: &[]api.AttributeValue{{Text: ptr("as the package intends")}},
				}}}}, "one description everywhere")
		}

		v := validatePackage(t, client, base, document)
		ready := readyPackageChanges(v)
		if len(ready) != 1 {
			t.Fatalf("%s: prepared %+v", s.name, ready)
		}
		item, _ := planOneOverHTTP(t, client, base, mustEncode(t, ready[0]))
		if item.Baseline == nil {
			t.Fatalf("%s: the plan issued no token", s.name)
		}
		applied := post(t, client, base+"/changeset/apply",
			`{"recovery":true,"changes":[`+withBaseline(t, ready[0], *item.Baseline)+`]}`)
		if applied.status != http.StatusOK {
			t.Fatalf("%s: applying: status %d\n%s", s.name, applied.status, applied.body)
		}
		result := decodeInto[api.ChangesetResult](t, applied)
		if result.Recovery == nil {
			t.Fatalf("%s: no recovery bundle", s.name)
		}
		runs = append(runs, run{name: s.name, bundle: mustEncode(t, *result.Recovery)})
	}

	if len(runs) != 2 {
		t.Fatalf("ran against %d servers", len(runs))
	}
	if runs[0].bundle == runs[1].bundle {
		t.Error("the same package produced the same recovery bundle on two directories, whose entries differed")
	}
	for _, r := range runs {
		if !strings.Contains(r.bundle, "as "+r.name+" had it") {
			t.Errorf("%s: the bundle does not restore what that directory held:\n%s", r.name, r.bundle)
		}
	}
}

func mustEncode(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func withBaseline(t *testing.T, change api.ChangeRequest, baseline string) string {
	t.Helper()
	change.Baseline = &baseline
	return mustEncode(t, change)
}
