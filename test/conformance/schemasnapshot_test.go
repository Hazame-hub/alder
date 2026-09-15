//go:build conformance

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.10: schema snapshots and schema comparisons against both servers, over HTTP.
//
// The flow is the data snapshot flow with definitions for entries: capture,
// change, compare, stage in dependency order, plan, apply, compare again and
// find nothing left. The definitions are disposable and live in the harness's
// own collection; everything is removed again, one definition per apply.

const (
	diffAttrOID  = "1.3.6.1.4.1.99997.1.20"
	diffClassOID = "1.3.6.1.4.1.99997.2.20"
	diffAttrKey  = "attributeType:" + diffAttrOID
	diffClassKey = "objectClass:" + diffClassOID
)

func diffAttrDef(desc string) string {
	return "( " + diffAttrOID + " NAME 'alderSchemaDiffAttr' DESC '" + desc +
		"' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"
}

const diffClassDef = "( " + diffClassOID + " NAME 'alderSchemaDiffClass' SUP top AUXILIARY MUST alderSchemaDiffAttr )"

// removeDiffSchema removes the disposable definitions, the class first because
// it names the attribute type. Errors are the definitions not being there.
func removeDiffSchema(t *testing.T, sess directory.Session, target string) {
	t.Helper()
	_ = applySchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: directory.SchemaDefObjectClass,
		Op: directory.SchemaOpDelete, OID: diffClassOID})
	_ = applySchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: directory.SchemaDefAttributeType,
		Op: directory.SchemaOpDelete, OID: diffAttrOID})
}

func mustSchemaChange(t *testing.T, sess directory.Session, req directory.SchemaChangeRequest) {
	t.Helper()
	if err := applySchemaChange(t, sess, req); err != nil {
		t.Fatalf("%s %s %s: %v", req.Op, req.Kind, req.OID, err)
	}
}

func schemaDifferences(d api.Diff) int {
	return d.Counts.Added + d.Counts.Removed + d.Counts.Modified + d.Counts.Unknown
}

func withoutCreatedAt(t *testing.T, doc string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("decoding a schema snapshot: %v", err)
	}
	delete(m, "createdAt")
	out, _ := json.Marshal(m)
	return string(out)
}

func schemaItem(t *testing.T, d api.Diff, key string) api.SchemaDiffItem {
	t.Helper()
	for _, it := range d.Schema.Items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no schema difference %s in %+v", key, d.Schema.Items)
	return api.SchemaDiffItem{}
}

// stageSchema selects the named differences' changes in the comparison's order.
func stageSchema(t *testing.T, d api.Diff, destructive bool, keys ...string) []api.ChangeRequest {
	t.Helper()
	var out []api.ChangeRequest
	for _, key := range d.Schema.Order {
		if !slices.Contains(keys, key) {
			continue
		}
		c := schemaItem(t, d, key).Candidate
		if c == nil || c.Blocked != nil || c.Destructive != destructive || len(c.Changes) == 0 {
			t.Fatalf("%s offers no usable change: %+v", key, c)
		}
		out = append(out, c.Changes...)
	}
	if len(out) == 0 {
		t.Fatalf("none of %v is in the order %v", keys, d.Schema.Order)
	}
	return out
}

func planChanges(t *testing.T, client *http.Client, base string, changes []api.ChangeRequest) api.Plan {
	t.Helper()
	encoded, _ := json.Marshal(changes)
	res := post(t, client, base+"/plan", `{"reconcile":false,"changes":`+string(encoded)+`}`)
	if res.status != http.StatusOK {
		t.Fatalf("plan: status %d\n%s", res.status, res.body)
	}
	return decodeInto[api.Plan](t, res)
}

// applyInOrder plans the whole set, which is where dependencies between its
// changes are checked, then applies it one change at a time, each against a
// fresh plan of itself.
func applyInOrder(t *testing.T, client *http.Client, base string, changes []api.ChangeRequest) {
	t.Helper()
	for i, item := range planChanges(t, client, base, changes).Items {
		if item.Baseline == nil {
			t.Fatalf("change %d of the set planned as %s (%+v): %s", i, item.Action, item.Problem, deref(item.Reason))
		}
	}
	for _, c := range changes {
		planAndApplyChanges(t, client, base, []api.ChangeRequest{c})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func TestSchemaSnapshotDiffPlanApplyOverHTTP(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)
		client, base := alderSession(t, s, true)
		removeDiffSchema(t, sess, target)
		t.Cleanup(func() { removeDiffSchema(t, sess, target) })

		capture := func() string {
			t.Helper()
			res := post(t, client, base+"/snapshots/capture", `{"kind":"schema"}`)
			if res.status != http.StatusOK {
				t.Fatalf("capture: status %d\n%s", res.status, res.body)
			}
			return res.body
		}
		liveTo := func(snap string) api.Diff {
			t.Helper()
			return diffOverHTTP(t, client, base,
				fmt.Sprintf(`{"source":{"live":{"schemaTarget":%q}},"target":{"snapshot":%s}}`, target, snap))
		}

		// 1. The same schema captured twice is the same document but for when.
		s0 := capture()
		if withoutCreatedAt(t, capture()) != withoutCreatedAt(t, s0) {
			t.Fatal("two captures of an unchanged schema differ beyond createdAt")
		}
		// 2. And compares equal with the directory it came from.
		d := liveTo(s0)
		if d.Kind != api.StateKindSchema || !d.Complete || schemaDifferences(d) != 0 {
			t.Fatalf("live -> S0: complete %v counts %+v reasons %+v", d.Complete, d.Counts, d.Reasons)
		}
		t.Logf("%s: S0 compares %d attribute types and %d object classes, %d metadata-only",
			s.name, d.Schema.AttributeTypes.Compared, d.Schema.ObjectClasses.Compared,
			d.Schema.AttributeTypes.MetadataOnly+d.Schema.ObjectClasses.MetadataOnly)

		// 3. A disposable attribute type and a class that needs it, captured as S1,
		// then removed outside this Alder session.
		mustSchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: directory.SchemaDefAttributeType,
			Op: directory.SchemaOpAdd, Definition: diffAttrDef("disposable")})
		mustSchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: directory.SchemaDefObjectClass,
			Op: directory.SchemaOpAdd, Definition: diffClassDef})
		s1 := capture()
		mustSchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: directory.SchemaDefObjectClass,
			Op: directory.SchemaOpDelete, OID: diffClassOID})
		mustSchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: directory.SchemaDefAttributeType,
			Op: directory.SchemaOpDelete, OID: diffAttrOID})

		// 4. live -> S1 sees two additions, the attribute type ordered first.
		d = liveTo(s1)
		if !d.Complete || d.Counts.Added != 2 || schemaDifferences(d) != 2 {
			t.Fatalf("live -> S1: counts %+v\nitems %+v", d.Counts, d.Schema.Items)
		}
		if got := strings.Join(d.Schema.Order, " "); got != diffAttrKey+" "+diffClassKey {
			t.Fatalf("order: %s", got)
		}
		if c := schemaItem(t, d, diffClassKey).Candidate; c == nil || c.Requires == nil || !slices.Contains(*c.Requires, diffAttrKey) {
			t.Fatalf("the class does not say it needs the attribute type: %+v", c)
		}
		adds := stageSchema(t, d, false, diffClassKey, diffAttrKey)

		// 5. Out of order, the plan refuses the class.
		p := planChanges(t, client, base, []api.ChangeRequest{adds[1], adds[0]})
		if pr := p.Items[0].Problem; p.Items[0].Action != api.PlanActionConflict || pr == nil || pr.Code != api.PlanProblemDependencyRequired {
			t.Fatalf("the class planned before its attribute type: %s %+v", p.Items[0].Action, pr)
		}

		// 6. In order, it plans and applies, and nothing is left.
		applyInOrder(t, client, base, adds)
		if d = liveTo(s1); schemaDifferences(d) != 0 {
			t.Fatalf("after adding: counts %+v\nitems %+v", d.Counts, d.Schema.Items)
		}

		// 7. The description changes outside Alder: one modification, of that field.
		mustSchemaChange(t, sess, directory.SchemaChangeRequest{TargetDN: target, Kind: directory.SchemaDefAttributeType,
			Op: directory.SchemaOpReplace, OID: diffAttrOID, Definition: diffAttrDef("changed elsewhere")})
		d = liveTo(s1)
		if d.Counts.Modified != 1 || schemaDifferences(d) != 1 {
			t.Fatalf("after the edit: counts %+v\nitems %+v", d.Counts, d.Schema.Items)
		}
		item := schemaItem(t, d, diffAttrKey)
		if item.Fields == nil || len(*item.Fields) != 1 || (*item.Fields)[0].Field != "desc" {
			t.Fatalf("modified fields: %+v", item.Fields)
		}
		applyInOrder(t, client, base, stageSchema(t, d, false, diffAttrKey))
		if d = liveTo(s1); schemaDifferences(d) != 0 {
			t.Fatalf("after restoring the description: counts %+v\nitems %+v", d.Counts, d.Schema.Items)
		}

		// 8. Back to S0: two removals, destructive, never assumed unused, the class first.
		d = liveTo(s0)
		if !d.Complete || d.Counts.Removed != 2 || schemaDifferences(d) != 2 {
			t.Fatalf("live -> S0: counts %+v\nitems %+v", d.Counts, d.Schema.Items)
		}
		if got := strings.Join(d.Schema.Order, " "); got != diffClassKey+" "+diffAttrKey {
			t.Fatalf("removal order: %s", got)
		}
		for _, key := range []string{diffClassKey, diffAttrKey} {
			c := schemaItem(t, d, key).Candidate
			if c == nil || !c.Destructive || c.Impact == nil ||
				!(slices.Contains(*c.Impact, api.SchemaProblemUsageUnknown) || slices.Contains(*c.Impact, api.SchemaProblemUsedByEntries)) {
				t.Fatalf("%s removal: %+v", key, c)
			}
		}
		if c := schemaItem(t, d, diffAttrKey).Candidate; c.Requires == nil || !slices.Contains(*c.Requires, diffClassKey) {
			t.Fatalf("the attribute type's removal does not say the class goes first: %+v", c.Requires)
		}
		applyInOrder(t, client, base, stageSchema(t, d, true, diffAttrKey, diffClassKey))
		if d = liveTo(s0); !d.Complete || schemaDifferences(d) != 0 {
			t.Fatalf("after removing: counts %+v\nitems %+v", d.Counts, d.Schema.Items)
		}
	})
}

// The harness's own schema is installed on both servers with the same meaning
// and different packaging. Compared across servers it has no core difference;
// one deliberate change makes exactly one.
func TestEquivalentSchemaOnBothServersComparesEqual(t *testing.T) {
	const arc = "1.3.6.1.4.1.99999."
	docs := map[string]string{}
	var client *http.Client
	var base string
	for _, s := range servers {
		client, base = alderSession(t, s, false)
		res := post(t, client, base+"/snapshots/capture", `{"kind":"schema"}`)
		if res.status != http.StatusOK {
			t.Fatalf("%s: capture status %d\n%s", s.name, res.status, res.body)
		}
		docs[s.name] = res.body
	}
	first, second := servers[0].name, servers[1].name

	compare := func(source, target string) api.Diff {
		t.Helper()
		return diffOverHTTP(t, client, base, `{"includeUnchanged":true,"source":{"snapshot":`+source+`},"target":{"snapshot":`+target+`}}`)
	}
	inArc := func(d api.Diff) (all, core []api.SchemaDiffItem) {
		for _, it := range d.Schema.Items {
			if !strings.HasPrefix(it.Oid, arc) {
				continue
			}
			all = append(all, it)
			if it.Kind != api.DiffKindUnchanged && it.Kind != api.DiffKindMetadataOnly {
				core = append(core, it)
			}
		}
		return all, core
	}

	d := compare(docs[first], docs[second])
	if !d.CrossVendor || d.Kind != api.StateKindSchema {
		t.Fatalf("cross-server comparison: crossVendor %v kind %s", d.CrossVendor, d.Kind)
	}
	all, core := inArc(d)
	if len(all) != 5 || len(core) != 0 {
		t.Fatalf("harness schema: %d definitions compared, %d with core differences: %+v", len(all), len(core), core)
	}
	t.Logf("%s -> %s: attribute types %+v, object classes %+v", first, second, d.Schema.AttributeTypes, d.Schema.ObjectClasses)

	// One deliberate change: alderNote becomes single-valued on the second side.
	snap, _, err := snapshot.DecodeSchema([]byte(docs[second]))
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for i := range snap.AttributeTypes {
		at := &snap.AttributeTypes[i]
		if at.OID == arc+"1.4" {
			at.SingleValue = true
			at.Definition = strings.Replace(at.Definition, "SYNTAX 1.3.6.1.4.1.1466.115.121.1.15",
				"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE", 1)
			changed = true
		}
	}
	if !changed {
		t.Fatal("alderNote is not in the second capture")
	}
	snap.Checksum = ""
	var buf bytes.Buffer
	if err := snapshot.EncodeSchema(&buf, snap); err != nil {
		t.Fatal(err)
	}
	d = compare(docs[first], buf.String())
	_, core = inArc(d)
	if len(core) != 1 || core[0].Oid != arc+"1.4" || core[0].Kind != api.DiffKindModified || core[0].Fields == nil {
		t.Fatalf("one deliberate change: %+v", core)
	}
	// The extensions the two servers record differently stay metadata; the
	// change is the one core field.
	var fields []string
	for _, f := range *core[0].Fields {
		if f.Category != api.SchemaFieldExtension {
			fields = append(fields, f.Field)
		}
	}
	if len(fields) != 1 || fields[0] != "singleValue" {
		t.Fatalf("fields of the deliberate change: %+v", *core[0].Fields)
	}
}
