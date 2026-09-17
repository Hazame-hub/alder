//go:build conformance

package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.12: migration preflight against both servers.
//
// Artifacts are made on one server and preflighted against the other, in both
// directions. Equivalent custom schema and ordinary data are portable; each
// deliberate incompatibility is then introduced on its own and must produce
// its exact finding code. Nothing is written by any preflight, which the last
// test proves by comparing the whole directory before and after.

const (
	flightAttrOID  = "1.3.6.1.4.1.99997.1.80"
	flightClassOID = "1.3.6.1.4.1.99997.2.80"
	flightEntryDN  = "uid=flight-proof,ou=people," + suffix
	flightGroupDN  = "cn=flight-proof-group,ou=groups," + suffix
	seededUser     = "uid=user0001,ou=people," + suffix
)

func flightAttrDef(extra string) string {
	return "( " + flightAttrOID + " NAME 'alderFlightTeam' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15" + extra + " )"
}

const flightClassDef = "( " + flightClassOID + " NAME 'alderFlightClass' SUP top AUXILIARY MUST alderFlightTeam )"

func textValue(s string) []api.AttributeValue { return []api.AttributeValue{{Text: ptr(s)}} }

// flightChanges are schema, an entry using it, and a group naming the entry and
// a seeded user -- expressed the way the server the package is built on writes
// schema.
func flightChanges(t *testing.T, sess directory.Session, target string, members ...string) []api.ChangeRequest {
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
		values := textValue(definition)
		return api.ChangeRequest{Dn: target, Type: api.ChangeRequestTypeModify,
			Mods: &[]api.ChangeMod{{Op: api.ChangeModOpAdd, Name: attribute, Values: &values}}}
	}
	if len(members) == 0 {
		members = []string{flightEntryDN, seededUser}
	}
	memberValues := make([]api.AttributeValue, 0, len(members))
	for _, m := range members {
		memberValues = append(memberValues, api.AttributeValue{Text: ptr(m)})
	}
	return []api.ChangeRequest{
		schemaChange(attrAttr, flightAttrDef("")),
		schemaChange(classAttr, flightClassDef),
		{Dn: flightEntryDN, Type: api.ChangeRequestTypeAdd, Attributes: &[]api.ChangeAttribute{
			{Name: "objectClass", Values: []api.AttributeValue{{Text: ptr("top")}, {Text: ptr("person")},
				{Text: ptr("organizationalPerson")}, {Text: ptr("inetOrgPerson")}, {Text: ptr("alderFlightClass")}}},
			{Name: "uid", Values: textValue("flight-proof")},
			{Name: "cn", Values: textValue("Flight Proof")},
			{Name: "sn", Values: textValue("Proof")},
			{Name: "alderFlightTeam", Values: textValue("platform")},
		}},
		{Dn: flightGroupDN, Type: api.ChangeRequestTypeAdd, Attributes: &[]api.ChangeAttribute{
			{Name: "objectClass", Values: []api.AttributeValue{{Text: ptr("top")}, {Text: ptr("groupOfNames")}}},
			{Name: "cn", Values: textValue("flight-proof-group")},
			{Name: "member", Values: memberValues},
		}},
	}
}

func removeFlightState(t *testing.T, sess directory.Session, target string) {
	t.Helper()
	for _, d := range []string{flightGroupDN, flightEntryDN} {
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeDelete})
	}
	for _, d := range []struct {
		kind directory.SchemaDefKind
		oid  string
	}{{directory.SchemaDefObjectClass, flightClassOID}, {directory.SchemaDefAttributeType, flightAttrOID}} {
		_ = applySchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: d.kind, Op: directory.SchemaOpDelete, OID: d.oid})
	}
}

func preflightOverHTTP(t *testing.T, client *http.Client, base string, artifact []byte, schemaTarget string) api.PreflightReport {
	t.Helper()
	body := `{"artifact":` + string(artifact)
	if schemaTarget != "" {
		body += `,"schemaTarget":` + quote(schemaTarget)
	}
	body += `}`
	res := post(t, client, base+"/preflight", body)
	if res.status != http.StatusOK {
		t.Fatalf("preflight: status %d\n%s", res.status, res.body)
	}
	return decodeInto[api.PreflightReport](t, res)
}

func findingsWith(r api.PreflightReport, code string) []api.PreflightFinding {
	var out []api.PreflightFinding
	for _, f := range r.Findings {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func codeCounts(r api.PreflightReport) map[string]int {
	out := map[string]int{}
	for _, f := range r.Findings {
		out[f.Code]++
	}
	return out
}

func sourceText(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// directions yields each ordered pair of servers: source, then target.
func directions() [][2]server {
	if len(servers) < 2 {
		return nil
	}
	return [][2]server{{servers[0], servers[1]}, {servers[1], servers[0]}}
}

// Equivalent custom schema and ordinary data, made on one server, are portable
// on the other -- in both directions. A vendor difference alone decides
// nothing.
func TestAPackageFromOneServerIsPortableOnTheOther(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	for _, pair := range directions() {
		source, target := pair[0], pair[1]
		t.Run(source.name+"_to_"+target.name, func(t *testing.T) {
			sourceSess := connectForSchema(t, source)
			sourceTarget := schemaTarget(t, sourceSess)
			sourceClient, sourceBase := alderSession(t, source, true)
			targetSess := connectForSchema(t, target)
			targetEntry := schemaTarget(t, targetSess)
			targetClient, targetBase := alderSession(t, target, true)
			removeFlightState(t, targetSess, targetEntry)
			t.Cleanup(func() { removeFlightState(t, targetSess, targetEntry) })

			document := buildPackage(t, sourceClient, sourceBase, flightChanges(t, sourceSess, sourceTarget), "flight proof")
			r := preflightOverHTTP(t, targetClient, targetBase, document, targetEntry)
			counts := codeCounts(r)
			if r.Overall != api.PreflightCompatible || !r.Complete {
				t.Fatalf("overall %s complete %v reasons %v\ncodes %v\n%s", r.Overall, r.Complete, r.Reasons, counts, mustEncode(t, r.Findings))
			}
			if !r.Target.CrossVendor {
				t.Errorf("the servers were not reported as different products")
			}
			if counts["definition_portable"] != 2 || counts["change_portable"] != 2 || counts["reference_ready"] != 1 ||
				counts["reference_provided_by_source"] != 1 {
				t.Errorf("codes %v", counts)
			}
			t.Logf("%s -> %s: %v", source.name, target.name, counts)
		})
	}
}

// One incompatibility at a time, each with its exact code.
func TestEachIncompatibilityHasItsOwnFinding(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	source, target := servers[0], servers[1]
	sourceSess := connectForSchema(t, source)
	sourceTarget := schemaTarget(t, sourceSess)
	sourceClient, sourceBase := alderSession(t, source, true)
	targetSess := connectForSchema(t, target)
	targetEntry := schemaTarget(t, targetSess)
	targetClient, targetBase := alderSession(t, target, true)
	removeFlightState(t, targetSess, targetEntry)
	t.Cleanup(func() { removeFlightState(t, targetSess, targetEntry) })

	t.Run("semantic OID conflict", func(t *testing.T) {
		mustSchemaChange(t, targetSess, directory.SchemaChangeRequest{TargetDN: targetEntry,
			Kind: directory.SchemaDefAttributeType, Op: directory.SchemaOpAdd, Definition: flightAttrDef(" SINGLE-VALUE")})
		t.Cleanup(func() { removeFlightState(t, targetSess, targetEntry) })
		document := buildPackage(t, sourceClient, sourceBase, flightChanges(t, sourceSess, sourceTarget), "conflict")
		r := preflightOverHTTP(t, targetClient, targetBase, document, targetEntry)
		conflict := findingsWith(r, "definition_conflict")
		if len(conflict) != 1 || conflict[0].Target == nil || !strings.Contains(sourceText(conflict[0].Target.Detail), "singleValue") {
			t.Fatalf("conflict %+v\n%v", conflict, codeCounts(r))
		}
		blocked := findingsWith(r, "dependency_blocked")
		if len(blocked) == 0 || r.Overall != api.PreflightNotCompatible {
			t.Fatalf("the class and entry should be blocked by the conflict: %v overall %s", codeCounts(r), r.Overall)
		}
		linked := false
		for _, f := range blocked {
			if f.Causes != nil && len(*f.Causes) > 0 && (*f.Causes)[0] == conflict[0].Id {
				linked = true
			}
		}
		if !linked {
			t.Errorf("no blocked finding names the conflict as its cause")
		}
	})

	t.Run("missing parent and naming context", func(t *testing.T) {
		person := func(dn string) api.ChangeRequest {
			return api.ChangeRequest{Dn: dn, Type: api.ChangeRequestTypeAdd, Attributes: &[]api.ChangeAttribute{
				{Name: "objectClass", Values: []api.AttributeValue{{Text: ptr("top")}, {Text: ptr("person")}}},
				{Name: "cn", Values: textValue("x")}, {Name: "sn", Values: textValue("y")}}}
		}
		document := buildPackage(t, sourceClient, sourceBase, []api.ChangeRequest{
			person("cn=orphan,ou=flight-nowhere," + suffix),
		}, "parent")
		r := preflightOverHTTP(t, targetClient, targetBase, document, "")
		if len(findingsWith(r, "parent_missing")) != 1 {
			t.Fatalf("parent: %v\n%s", codeCounts(r), mustEncode(t, r.Findings))
		}

		// A DN under a suffix the target does not hold. Built by hand: the
		// source server would refuse to package what it does not hold either.
		outside := strings.Replace(string(document), "ou=flight-nowhere,"+suffix, "ou=people,dc=elsewhere,dc=example", 1)
		outside = stripChecksum(outside)
		r = preflightOverHTTP(t, targetClient, targetBase, []byte(outside), "")
		if len(findingsWith(r, "naming_context_mismatch")) != 1 || r.Overall != api.PreflightNotCompatible {
			t.Fatalf("naming context: %v overall %s", codeCounts(r), r.Overall)
		}
		for _, f := range r.Findings {
			if strings.Contains(sourceText(f.Source.Dn), suffix) && strings.Contains(sourceText(f.Source.Dn), "orphan") {
				t.Fatalf("a DN was rewritten into the target's suffix: %+v", f)
			}
		}
	})

	t.Run("unsupported schema prerequisite", func(t *testing.T) {
		write := sourceSess.Capabilities().SchemaWrite
		attrAttr, _ := write.Attribute(directory.SchemaDefAttributeType)
		// A syntax neither server implements.
		values := textValue("( 1.3.6.1.4.1.99997.1.81 NAME 'alderFlightOdd' SYNTAX 1.3.6.1.4.1.99997.9.9 )")
		document := buildPackage(t, sourceClient, sourceBase, []api.ChangeRequest{{Dn: sourceTarget, Type: api.ChangeRequestTypeModify,
			Mods: &[]api.ChangeMod{{Op: api.ChangeModOpAdd, Name: attrAttr, Values: &values}}}}, "odd syntax")
		r := preflightOverHTTP(t, targetClient, targetBase, document, targetEntry)
		if len(findingsWith(r, "syntax_unavailable")) != 1 || r.Overall != api.PreflightNotCompatible {
			t.Fatalf("syntax: %v overall %s\n%s", codeCounts(r), r.Overall, mustEncode(t, r.Findings))
		}
	})

	t.Run("vendor operational fields", func(t *testing.T) {
		res := post(t, sourceClient, sourceBase+"/snapshots/capture",
			fmt.Sprintf(`{"base":%q,"scope":"base","operationalAttributes":true}`, seededUser))
		if res.status != http.StatusOK {
			t.Fatalf("capture: %d %s", res.status, res.body)
		}
		r := preflightOverHTTP(t, targetClient, targetBase, []byte(res.body), "")
		counts := codeCounts(r)
		if counts["ignored_operational"]+counts["server_owned_attribute"] == 0 || counts["target_generated"] != 1 {
			t.Fatalf("operational: %v\n%s", counts, mustEncode(t, r.Findings))
		}
		for _, f := range r.Findings {
			if (f.Code == "ignored_operational" || f.Code == "server_owned_attribute") && f.Classification != api.PreflightExcluded {
				t.Errorf("an operational field was judged, not excluded: %+v", f)
			}
		}
		if r.Overall == api.PreflightNotCompatible {
			t.Errorf("operational fields alone made the entry incompatible: %v", counts)
		}
		t.Logf("operational findings for %s from %s: %v", seededUser, source.name, counts)
	})

	t.Run("missing reference", func(t *testing.T) {
		document := buildPackage(t, sourceClient, sourceBase, []api.ChangeRequest{
			{Dn: flightGroupDN, Type: api.ChangeRequestTypeAdd, Attributes: &[]api.ChangeAttribute{
				{Name: "objectClass", Values: []api.AttributeValue{{Text: ptr("top")}, {Text: ptr("groupOfNames")}}},
				{Name: "cn", Values: textValue("flight-proof-group")},
				{Name: "member", Values: textValue("uid=flight-ghost,ou=people," + suffix)},
			}}}, "ghost member")
		r := preflightOverHTTP(t, targetClient, targetBase, document, "")
		missing := findingsWith(r, "reference_missing")
		if len(missing) != 1 || sourceText(missing[0].Source.Value) != "uid=flight-ghost,ou=people,"+suffix {
			t.Fatalf("reference: %v\n%s", codeCounts(r), mustEncode(t, r.Findings))
		}
	})
}

// stripChecksum removes a package's checksum so a hand-edited copy is read as
// unverified rather than refused. It is how an operator's edited file arrives.
func stripChecksum(document string) string {
	var doc map[string]any
	_ = json.Unmarshal([]byte(document), &doc)
	delete(doc, "checksum")
	out, _ := json.Marshal(doc)
	return string(out)
}

// The same package bytes, preflighted against both servers: one already has
// the attribute type, the other does not. The package is unchanged.
func TestOnePackageIsPreflightedAgainstBothServers(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	first, second := servers[0], servers[1]
	firstSess := connectForSchema(t, first)
	firstEntry := schemaTarget(t, firstSess)
	firstClient, firstBase := alderSession(t, first, true)
	secondSess := connectForSchema(t, second)
	secondEntry := schemaTarget(t, secondSess)
	secondClient, secondBase := alderSession(t, second, true)
	removeFlightState(t, firstSess, firstEntry)
	removeFlightState(t, secondSess, secondEntry)
	t.Cleanup(func() { removeFlightState(t, firstSess, firstEntry) })
	t.Cleanup(func() { removeFlightState(t, secondSess, secondEntry) })

	document := buildPackage(t, firstClient, firstBase, flightChanges(t, firstSess, firstEntry), "promoted preflight")
	before := sha256.Sum256(document)

	mustSchemaChange(t, secondSess, directory.SchemaChangeRequest{TargetDN: secondEntry,
		Kind: directory.SchemaDefAttributeType, Op: directory.SchemaOpAdd, Definition: flightAttrDef("")})

	firstReport := preflightOverHTTP(t, firstClient, firstBase, document, firstEntry)
	secondReport := preflightOverHTTP(t, secondClient, secondBase, document, secondEntry)
	if sha256.Sum256(document) != before {
		t.Fatal("the package bytes changed")
	}
	if n := len(findingsWith(firstReport, "definition_portable")); n != 2 {
		t.Errorf("%s: %d definitions portable, want 2: %v", first.name, n, codeCounts(firstReport))
	}
	present := findingsWith(secondReport, "definition_present")
	if len(present) != 1 || sourceText(present[0].Source.Oid) != flightAttrOID {
		t.Errorf("%s: the attribute type should already be present: %v", second.name, codeCounts(secondReport))
	}
	if firstReport.Source.Checksum == nil || secondReport.Source.Checksum == nil || *firstReport.Source.Checksum != *secondReport.Source.Checksum {
		t.Errorf("the two reports are not about the same artifact")
	}
	t.Logf("same package %s: %s %v; %s %v", hex.EncodeToString(before[:4]),
		first.name, codeCounts(firstReport), second.name, codeCounts(secondReport))
}

// A snapshot captured on one server, preflighted on the other: the harness's
// own schema is already there, and the seeded users are too.
func TestSnapshotsFromOneServerPreflightOnTheOther(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	for _, pair := range directions() {
		source, target := pair[0], pair[1]
		t.Run(source.name+"_to_"+target.name, func(t *testing.T) {
			sourceClient, sourceBase := alderSession(t, source, true)
			targetClient, targetBase := alderSession(t, target, true)

			schemaDoc := post(t, sourceClient, sourceBase+"/snapshots/capture", `{"kind":"schema"}`)
			if schemaDoc.status != http.StatusOK {
				t.Fatalf("schema capture: %d", schemaDoc.status)
			}
			r := preflightOverHTTP(t, targetClient, targetBase, []byte(schemaDoc.body), "")
			for _, f := range r.Findings {
				oid := sourceText(f.Source.Oid)
				if !strings.HasPrefix(oid, "1.3.6.1.4.1.99999.") {
					continue
				}
				if f.Classification != api.PreflightAlreadySatisfied {
					t.Errorf("the harness definition %s is %s (%s) on %s", oid, f.Classification, f.Code, target.name)
				}
			}
			t.Logf("schema %s -> %s: overall %s, %+v", source.name, target.name, r.Overall, r.Counts)

			dataDoc := post(t, sourceClient, sourceBase+"/snapshots/capture", fmt.Sprintf(`{"base":%q,"scope":"sub"}`, "ou=groups,"+suffix))
			if dataDoc.status != http.StatusOK {
				t.Fatalf("data capture: %d", dataDoc.status)
			}
			r = preflightOverHTTP(t, targetClient, targetBase, []byte(dataDoc.body), "")
			counts := codeCounts(r)
			if counts["entry_present_equivalent"] == 0 {
				t.Errorf("no seeded group was recognised as already present: %v", counts)
			}
			if r.Overall == api.PreflightNotCompatible {
				t.Errorf("the seeded groups are incompatible across servers: %v\n%s", counts, mustEncode(t, r.Findings))
			}
			t.Logf("data %s -> %s: overall %s, %v", source.name, target.name, r.Overall, counts)
		})
	}
}

// A bind that may not read part of the directory. A hidden parent is unknown
// where the server admits it exists; where the server answers exactly as for a
// missing entry, the finding says it is the server's account to this bind. In
// neither case does the report claim to be a clean pass.
func TestARestrictedBindCannotProduceACleanResult(t *testing.T) {
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			admin, adminBase := alderSession(t, s, false)
			document := buildPackage(t, admin, adminBase, []api.ChangeRequest{
				{Dn: "cn=flight-hidden-child," + "ou=services," + suffix, Type: api.ChangeRequestTypeAdd,
					Attributes: &[]api.ChangeAttribute{
						{Name: "objectClass", Values: []api.AttributeValue{{Text: ptr("top")}, {Text: ptr("person")}}},
						{Name: "cn", Values: textValue("flight-hidden-child")}, {Name: "sn", Values: textValue("x")}}},
			}, "under a hidden subtree")

			restricted, restrictedBase := restrictedAlderSession(t, s)
			r := preflightOverHTTP(t, restricted, restrictedBase, document, "")
			counts := codeCounts(r)
			if r.Overall == api.PreflightCompatible {
				t.Fatalf("a bind that cannot see the parent produced a clean result: %v", counts)
			}
			switch {
			case counts["parent_unknown"] == 1:
				if r.Overall != api.PreflightIncomplete {
					t.Errorf("unknown parent, overall %s", r.Overall)
				}
			case counts["parent_missing"] == 1:
				f := findingsWith(r, "parent_missing")[0]
				if !strings.Contains(f.Explanation, "as the server reports it to this bind") {
					t.Errorf("a concealed parent is stated as fact: %q", f.Explanation)
				}
			default:
				t.Fatalf("no parent finding: %v\n%s", counts, mustEncode(t, r.Findings))
			}
			t.Logf("%s restricted bind: overall %s, %v", s.name, r.Overall, counts)
		})
	}
}

func restrictedAlderSession(t *testing.T, s server) (*http.Client, string) {
	t.Helper()
	base := startAlder(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	caPEM, err := os.ReadFile(filepath.Join("..", "compose", "certs", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"host": s.host, "port": s.port, "tls": "ldaps", "caCertificate": string(caPEM),
		"serverName": "localhost", "bindDn": s.restrictedDN, "bindPassword": s.restrictedPW})
	res := post(t, client, base+"/session", string(body))
	if res.status != http.StatusCreated {
		t.Fatalf("restricted connect: %d %s", res.status, res.body)
	}
	return client, base
}

// Every preflight mode, against both servers, leaves the directory exactly as
// it was: the data and the schema are captured before and after, and must be
// the same documents.
func TestPreflightLeavesTheDirectoryUntouched(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		entry := schemaTarget(t, sess)
		client, base := alderSession(t, s, true)
		removeFlightState(t, sess, entry)

		state := func() string {
			data := post(t, client, base+"/snapshots/capture", fmt.Sprintf(`{"base":%q,"scope":"sub"}`, suffix))
			sch := post(t, client, base+"/snapshots/capture", `{"kind":"schema"}`)
			if data.status != http.StatusOK || sch.status != http.StatusOK {
				t.Fatalf("capture: %d %d", data.status, sch.status)
			}
			var d, c struct {
				Checksum string `json:"checksum"`
			}
			_ = json.Unmarshal([]byte(data.body), &d)
			_ = json.Unmarshal([]byte(sch.body), &c)
			return d.Checksum + " " + c.Checksum
		}
		before := state()

		pkg := buildPackage(t, client, base, flightChanges(t, sess, entry, "uid=flight-ghost,ou=people,"+suffix), "untouched")
		schemaDoc := post(t, client, base+"/snapshots/capture", `{"kind":"schema"}`)
		dataDoc := post(t, client, base+"/snapshots/capture", fmt.Sprintf(`{"base":%q,"scope":"sub"}`, "ou=groups,"+suffix))
		for name, artifact := range map[string]string{
			"package":         string(pkg),
			"schema snapshot": schemaDoc.body,
			"data snapshot":   dataDoc.body,
			"malformed":       `{"format":"alder-change-package","version":1,"id":"x","smuggled":true}`,
			"unsupported":     `{"format":"alder-snapshot","version":1,"kind":"config"}`,
		} {
			res := post(t, client, base+"/preflight", `{"artifact":`+artifact+`,"schemaTarget":`+quote(entry)+`}`)
			if name == "malformed" || name == "unsupported" {
				if res.status != http.StatusBadRequest {
					t.Errorf("%s: status %d", name, res.status)
				}
			} else if res.status != http.StatusOK {
				t.Errorf("%s: status %d %s", name, res.status, res.body)
			}
		}
		if after := state(); after != before {
			t.Fatalf("the directory changed during preflight:\nbefore %s\nafter  %s", before, after)
		}
	})
}
