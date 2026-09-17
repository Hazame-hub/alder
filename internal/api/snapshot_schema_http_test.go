package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.10: schema snapshots and schema comparisons over HTTP.

const (
	siteCodeAT = "( 1.3.6.1.4.1.99999.1.3 NAME 'alderSiteCode' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	siteOC     = "( 1.3.6.1.4.1.99999.2.2 NAME 'alderSite' SUP top AUXILIARY MUST alderSiteCode )"
)

func baseSchemaAttrs(extraAT, extraOC []string) map[string][]string {
	return map[string][]string{
		schema.AttrAttributeTypes: append([]string{
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.41 NAME 'name' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.3 NAME 'cn' SUP name )",
			"( 2.5.21.5 NAME 'attributeTypes' SYNTAX 1.3.6.1.4.1.1466.115.121.1.3 USAGE directoryOperation )",
			"( 2.5.21.6 NAME 'objectClasses' SYNTAX 1.3.6.1.4.1.1466.115.121.1.37 USAGE directoryOperation )",
		}, extraAT...),
		schema.AttrObjectClasses: append([]string{
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.20.1 NAME 'subschema' AUXILIARY MAY ( attributeTypes $ objectClasses ) )",
		}, extraOC...),
	}
}

func schemaValues(defs []string) [][]byte {
	out := make([][]byte, 0, len(defs))
	for _, d := range defs {
		out = append(out, []byte(d))
	}
	return out
}

// schemaRig is a session on a server with one directly writable schema entry
// publishing attrs.
func schemaRig(t *testing.T, attrs map[string][]string) *testRig {
	t.Helper()
	caps := defaultCaps()
	caps.VendorName = "389 Project"
	caps.SubschemaSubentry = "cn=schema"
	caps.SchemaWrite = directory.SchemaWrite{
		Style:           directory.SchemaStyleSubschema,
		Targets:         []directory.SchemaTarget{{DN: "cn=schema", Name: "cn=schema"}},
		ObjectClassAttr: "objectClasses", AttributeTypeAttr: "attributeTypes",
	}
	entry := directory.NewEntry(mustParse(t, "cn=schema"))
	entry.Set("objectClass", [][]byte{[]byte("top"), []byte("subschema")})
	entry.Set("attributeTypes", schemaValues(attrs[schema.AttrAttributeTypes]))
	entry.Set("objectClasses", schemaValues(attrs[schema.AttrObjectClasses]))
	return newRig(t, Config{}, &fakeSession{
		caps: caps, sch: schema.Load("cn=schema", attrs),
		byDN: map[string]*directory.Entry{"cn=schema": entry},
		schemaDefs: map[string][]string{
			"cn=schema|" + string(directory.SchemaDefAttributeType): attrs[schema.AttrAttributeTypes],
			"cn=schema|" + string(directory.SchemaDefObjectClass):   attrs[schema.AttrObjectClasses],
		},
	})
}

func schemaDocument(t *testing.T, vendor string, attrs map[string][]string) string {
	t.Helper()
	s, err := snapshot.BuildSchema(snapshot.SchemaCapture{
		Vendor: vendor, SubschemaEntry: "cn=schema", CreatedAt: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
	}, schema.Load("cn=schema", attrs))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := snapshot.EncodeSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func post(t *testing.T, rig *testRig, path, body string) response {
	t.Helper()
	return rig.do(t, http.MethodPost, path, strings.NewReader(body))
}

func TestASchemaCaptureIsTheSameDocumentTwice(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	first := post(t, rig, "/api/v1/snapshots/capture", `{"kind":"schema"}`)
	if first.Status != http.StatusOK {
		t.Fatalf("capture: %d %s", first.Status, first.Body)
	}
	if got := first.Header.Get("Content-Disposition"); !strings.Contains(got, "alder-schema-snapshot-cn-schema-") {
		t.Errorf("filename: %q", got)
	}
	time.Sleep(1100 * time.Millisecond)
	second := post(t, rig, "/api/v1/snapshots/capture", `{"kind":"schema"}`)

	withoutTime := func(doc string) (map[string]any, string) {
		var m map[string]any
		if err := json.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "createdAt")
		out, _ := json.Marshal(m)
		return m, string(out)
	}
	a, aText := withoutTime(first.Body)
	_, bText := withoutTime(second.Body)
	if aText != bText {
		t.Errorf("two captures of one schema differ beyond createdAt:\n%s\n%s", aText, bText)
	}
	if a["kind"] != "schema" || a["checksum"] == "" {
		t.Errorf("document head: kind %v checksum %v", a["kind"], a["checksum"])
	}
	if _, ok := a["entries"]; ok {
		t.Error("a schema snapshot carries entries")
	}
	if n := rig.fake.searchCount(); n != 0 {
		t.Errorf("capturing the schema searched the directory %d times", n)
	}
}

func TestACaptureNamesItsKind(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	for body, code := range map[string]ErrorError{
		// 1.13 added kind config. This server has no configuration tree to
		// read, which is a different answer from "there is no such kind".
		`{"kind":"config"}`:   ErrorErrorConfigModelUnavailable,
		`{"kind":"nonsense"}`: ErrorErrorBadRequest,
		`{"kind":"data"}`:     ErrorErrorBadRequest,
		`{}`:                  ErrorErrorBadRequest,
	} {
		res := post(t, rig, "/api/v1/snapshots/capture", body)
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d", body, res.Status)
			continue
		}
		if got := errorCode(t, res); got != code {
			t.Errorf("%s: code %s, want %s", body, got, code)
		}
	}
}

func TestInspectDescribesASchemaSnapshot(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs([]string{siteCodeAT}, []string{siteOC}))
	doc := schemaDocument(t, "389 Project", baseSchemaAttrs([]string{siteCodeAT}, []string{siteOC}))
	res := post(t, rig, "/api/v1/snapshots/inspect", doc)
	if res.Status != http.StatusOK {
		t.Fatalf("inspect: %d %s", res.Status, res.Body)
	}
	in := decode[SnapshotInspection](t, res)
	if in.Kind != "schema" || in.Schema == nil || in.Integrity != SnapshotIntegrityVerified {
		t.Fatalf("inspection: %+v", in)
	}
	if in.Schema.Counts.AttributeTypes != 6 || in.Schema.Counts.ObjectClasses != 3 || in.Source.Base != "cn=schema" ||
		in.Schema.Completeness != "complete" {
		t.Errorf("summary: %+v source %+v", in.Schema, in.Source)
	}

	tampered := strings.Replace(doc, "NAME 'cn' SUP name", "NAME 'cn' SUP objectClass", 1)
	if tampered == doc {
		t.Fatal("the tamper did not apply")
	}
	if res := post(t, rig, "/api/v1/snapshots/inspect", tampered); res.Status != http.StatusBadRequest {
		t.Errorf("a definition edited apart from its fields was accepted: %d", res.Status)
	}
}

func TestTwoSchemaSnapshotsCompareWithoutASession(t *testing.T) {
	app, driver := directorylessServer(t)
	before := schemaDocument(t, "389 Project", baseSchemaAttrs(nil, nil))
	after := schemaDocument(t, "OpenLDAP", baseSchemaAttrs([]string{siteCodeAT}, nil))
	res := postDiffWithoutSession(t, app, diffOfTwoSnapshots(before, after))
	if res.Status != http.StatusOK {
		t.Fatalf("diff: %d %s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	if d.Kind != StateKindSchema || d.Schema == nil || !d.CrossVendor || !d.Complete {
		t.Fatalf("diff: %+v", d)
	}
	if d.Schema.AttributeTypes.Added != 1 || d.Counts.Added != 1 || len(d.Items) != 0 {
		t.Errorf("counts: %+v / %+v", d.Schema.AttributeTypes, d.Counts)
	}
	for _, it := range d.Schema.Items {
		if it.Candidate != nil {
			t.Errorf("a snapshot source proposed a change for %s", it.Key)
		}
	}
	if n := driver.connects.Load(); n != 0 {
		t.Errorf("comparing two schema snapshots connected %d times", n)
	}

	data := snapshotWithTitle(t, "Engineer")
	if res := postDiffWithoutSession(t, app, diffOfTwoSnapshots(data, after)); res.Status != http.StatusBadRequest {
		t.Errorf("data compared with schema: %d %s", res.Status, res.Body)
	}
}

func TestALiveSchemaSideIsReadWhole(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	doc := schemaDocument(t, "389 Project", baseSchemaAttrs(nil, nil))
	for name, body := range map[string]string{
		"a base":                      `{"source":{"live":{"base":"cn=schema"}},"target":{"snapshot":` + doc + `}}`,
		"an unlisted target":          `{"source":{"live":{"schemaTarget":"cn=elsewhere"}},"target":{"snapshot":` + doc + `}}`,
		"a target on the live target": `{"source":{"snapshot":` + doc + `},"target":{"live":{"schemaTarget":"cn=schema"}}}`,
		"configuration":               `{"source":{"live":{"kind":"config"}},"target":{"snapshot":` + doc + `}}`,
		"data against schema":         `{"source":{"live":{"kind":"data"}},"target":{"snapshot":` + doc + `}}`,
	} {
		if res := post(t, rig, "/api/v1/diff", body); res.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", name, res.Status, res.Body)
		}
	}
	res := post(t, rig, "/api/v1/diff", `{"source":{"live":{"kind":"schema"}},"target":{"snapshot":`+doc+`}}`)
	if res.Status != http.StatusOK {
		t.Fatalf("an equal schema: %d %s", res.Status, res.Body)
	}
	if d := decode[Diff](t, res); d.Counts.Added+d.Counts.Removed+d.Counts.Modified+d.Counts.Unknown != 0 || !d.Complete {
		t.Errorf("the live schema differs from its own snapshot: %+v", d.Counts)
	}
}

func planOf(t *testing.T, rig *testRig, changes ...ChangeRequest) Plan {
	t.Helper()
	body, err := json.Marshal(PlanRequest{Changes: &changes})
	if err != nil {
		t.Fatal(err)
	}
	return planFor(t, rig, string(body))
}

func TestALiveSchemaDifferenceBecomesAnOrderedPlan(t *testing.T) {
	rig := schemaRig(t, baseSchemaAttrs(nil, nil))
	target := schemaDocument(t, "389 Project", baseSchemaAttrs([]string{siteCodeAT}, []string{siteOC}))
	res := post(t, rig, "/api/v1/diff", `{"source":{"live":{}},"target":{"snapshot":`+target+`}}`)
	if res.Status != http.StatusOK {
		t.Fatalf("diff: %d %s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	if d.Kind != StateKindSchema || d.Schema == nil {
		t.Fatalf("diff: %+v", d)
	}
	atKey, ocKey := "attributeType:1.3.6.1.4.1.99999.1.3", "objectClass:1.3.6.1.4.1.99999.2.2"
	if got := strings.Join(d.Schema.Order, " "); got != atKey+" "+ocKey {
		t.Fatalf("order: %s", got)
	}
	byKey := map[string]SchemaDiffItem{}
	for _, it := range d.Schema.Items {
		byKey[it.Key] = it
	}
	var changes []ChangeRequest
	for _, key := range d.Schema.Order {
		c := byKey[key].Candidate
		if c == nil || c.Blocked != nil || c.Destructive || len(c.Changes) != 1 {
			t.Fatalf("%s: candidate %+v", key, c)
		}
		changes = append(changes, c.Changes...)
	}
	if c := byKey[ocKey].Candidate; c.Requires == nil || !slices.Contains(*c.Requires, atKey) {
		t.Errorf("the class does not say it needs the attribute type: %+v", c.Requires)
	}

	for _, it := range planOf(t, rig, changes...).Items {
		if it.Action != PlanActionModify {
			t.Errorf("in order: %s %+v %s", it.Action, it.Problem, deref(it.Reason))
		}
	}
	reversed := planOf(t, rig, changes[1], changes[0])
	if p := reversed.Items[0].Problem; reversed.Items[0].Action != PlanActionConflict || p == nil || p.Code != PlanProblemDependencyRequired {
		t.Errorf("the class before its attribute type: %s %+v", reversed.Items[0].Action, p)
	}
}

func TestALiveSchemaRemovalIsDestructiveAndNeverAssumedUnused(t *testing.T) {
	gone := "( 1.3.6.1.4.1.99999.1.9 NAME 'alderGone' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 X-ORIGIN 'user defined' )"
	builtin := "( 2.5.4.13 NAME 'description' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 X-ORIGIN 'RFC 4519' )"
	rig := schemaRig(t, baseSchemaAttrs([]string{gone, builtin}, nil))
	target := schemaDocument(t, "389 Project", baseSchemaAttrs(nil, nil))
	res := post(t, rig, "/api/v1/diff", `{"source":{"live":{}},"target":{"snapshot":`+target+`}}`)
	if res.Status != http.StatusOK {
		t.Fatalf("diff: %d %s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	var sawGone, sawBuiltin bool
	for _, it := range d.Schema.Items {
		switch it.Oid {
		case "1.3.6.1.4.1.99999.1.9":
			sawGone = true
			c := it.Candidate
			if it.Kind != DiffKindRemoved || c == nil || !c.Destructive || c.Blocked != nil || len(c.Changes) != 1 {
				t.Fatalf("user-defined removal: %+v", c)
			}
			if c.Impact == nil || !slices.Contains(*c.Impact, SchemaProblemUsageUnknown) || slices.Contains(*c.Impact, SchemaProblemUsedByEntries) {
				t.Errorf("impact: %+v", c.Impact)
			}
		case "2.5.4.13":
			sawBuiltin = true
			if c := it.Candidate; c == nil || c.Blocked == nil || *c.Blocked != DiffBlockedServerDefined {
				t.Errorf("server-defined removal: %+v", c)
			}
		}
	}
	if !sawGone || !sawBuiltin {
		t.Fatalf("items: %+v", d.Schema.Items)
	}
}
