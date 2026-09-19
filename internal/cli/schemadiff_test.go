package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1.10: schema snapshots and schema comparisons on the command line.

const schemaSnapshotDoc = `{
  "format": "alder-snapshot",
  "version": 1,
  "kind": "schema",
  "createdAt": "2026-09-14T10:00:00Z",
  "source": {"vendor": "389 Project", "subschemaEntry": "cn=schema", "collections": false},
  "completeness": "complete",
  "counts": {"attributeTypes": 2, "objectClasses": 1, "ldapSyntaxes": 0, "matchingRules": 0, "matchingRuleUse": 0,
    "ditContentRules": 0, "nameForms": 0, "unparsed": 0},
  "checksum": "sha256:abc"
}`

const (
	siteCodeOID = "1.3.6.1.4.1.99999.1.3"
	siteOID     = "1.3.6.1.4.1.99999.2.2"
	goneOID     = "1.3.6.1.4.1.99999.1.9"
)

func schemaChange(op, attr, def string) map[string]any {
	return map[string]any{"dn": "cn=schema", "type": "modify", "mods": []any{
		map[string]any{"op": op, "name": attr, "values": []any{map[string]any{"text": def}}},
	}}
}

func schemaItems() ([]map[string]any, []string) {
	at := map[string]any{
		"key": "attributeType:" + siteCodeOID, "element": "attributeType", "oid": siteCodeOID,
		"names": []any{"alderSiteCode"}, "kind": "added",
		"candidate": map[string]any{"destructive": false, "changes": []any{
			schemaChange("add", "attributeTypes", "( "+siteCodeOID+" NAME 'alderSiteCode' )")}},
	}
	oc := map[string]any{
		"key": "objectClass:" + siteOID, "element": "objectClass", "oid": siteOID,
		"names": []any{"alderSite"}, "kind": "added",
		"candidate": map[string]any{"destructive": false, "requires": []any{"attributeType:" + siteCodeOID}, "changes": []any{
			schemaChange("add", "objectClasses", "( "+siteOID+" NAME 'alderSite' MUST alderSiteCode )")}},
	}
	gone := map[string]any{
		"key": "attributeType:" + goneOID, "element": "attributeType", "oid": goneOID,
		"names": []any{"alderGone"}, "kind": "removed",
		"candidate": map[string]any{"destructive": true, "impact": []any{"usage_unknown"}, "changes": []any{
			schemaChange("delete", "attributeTypes", "( "+goneOID+" NAME 'alderGone' )")}},
	}
	// Another element kind with the same OID, which a bare OID cannot name.
	twin := map[string]any{
		"key": "objectClass:" + goneOID, "element": "objectClass", "oid": goneOID,
		"kind": "added", "candidate": map[string]any{"destructive": false, "changes": []any{
			schemaChange("add", "objectClasses", "( "+goneOID+" NAME 'alderTwin' )")}},
	}
	meta := map[string]any{
		"key": "attributeType:2.5.4.3", "element": "attributeType", "oid": "2.5.4.3",
		"names": []any{"cn\u202etxt.exe"}, "kind": "metadata_only",
		"fields": []any{map[string]any{"field": "X-ORIGIN", "category": "extension",
			"source": []any{"RFC 4519"}, "target": []any{"evil \x1b]0;owned\x07"}}},
		"candidate": map[string]any{"destructive": false, "changes": []any{}, "blocked": "metadata_only"},
	}
	items := []map[string]any{at, gone, meta, oc, twin}
	order := []string{"attributeType:" + siteCodeOID, "objectClass:" + goneOID, "objectClass:" + siteOID, "attributeType:" + goneOID}
	return items, order
}

func schemaDiffJSON(t *testing.T, items []map[string]any, order []string) string {
	t.Helper()
	side := func(kind string) map[string]any {
		return map[string]any{"kind": kind, "base": "cn=schema", "scope": "base", "filter": "(objectClass=subschema)",
			"operationalAttributes": true, "entryCount": 1, "definitionCount": 12, "vendor": "389 Project"}
	}
	n := map[string]int{}
	for _, it := range items {
		n[it["kind"].(string)]++
	}
	counts := map[string]any{"compared": len(items), "added": n["added"], "removed": n["removed"], "modified": n["modified"],
		"metadataOnly": n["metadata_only"], "unchanged": 0, "unknown": 0}
	doc := map[string]any{
		"kind": "schema", "source": side("live"), "target": side("snapshot"), "complete": true, "crossVendor": false,
		"operationalIgnored": false, "items": []any{},
		"counts": map[string]any{"compared": len(items), "added": n["added"], "removed": n["removed"], "modified": n["modified"],
			"renamed": 0, "unchanged": n["metadata_only"], "unknown": 0},
		"schema": map[string]any{"attributeTypes": counts, "objectClasses": counts, "items": items, "order": order},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestASchemaDiffIsShownByDefinitionAndSafely(t *testing.T) {
	items, order := schemaItems()
	s := newStub(t)
	s.reply("POST /api/v1/diff", http.StatusOK, schemaDiffJSON(t, items, order))
	snap := writeTemp(t, t.TempDir(), "schema.json", schemaSnapshotDoc)
	r := s.run(t, runOpts{}, "diff", "@live", snap)
	expectCode(t, r, ExitDifferences)
	for _, want := range []string{
		"the schema at cn=schema",
		"attribute types",
		"added         attributeType " + siteCodeOID + " alderSiteCode",
		"(select with --stage " + siteCodeOID + ")",
		"removes the definition (select with --stage-deletion " + goneOID + ")",
		"impact: usage_unknown",
		"stage together with: attributeType:" + siteCodeOID,
		"no change offered: metadata_only",
		"Metadata differences are in X- extensions only",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, r.stdout)
		}
	}
	if strings.ContainsAny(r.stdout, "\x1b\x07\u202e") {
		t.Errorf("a control or bidi character reached the terminal:\n%q", r.stdout)
	}
}

func TestSchemaDifferencesAreStagedByOIDInDependencyOrder(t *testing.T) {
	items, order := schemaItems()
	body := schemaDiffJSON(t, items, order)
	snap := writeTemp(t, t.TempDir(), "schema.json", schemaSnapshotDoc)
	run := func(t *testing.T, args ...string) (result, string) {
		s := newStub(t)
		s.reply("POST /api/v1/diff", http.StatusOK, body)
		out := filepath.Join(t.TempDir(), "changes.json")
		return s.run(t, runOpts{}, append([]string{"diff", "@live", snap, "--changes-out", out}, args...)...), out
	}
	written := func(t *testing.T, out string) []string {
		t.Helper()
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var changes []struct {
			Mods []struct {
				Values []struct {
					Text string `json:"text"`
				} `json:"values"`
			} `json:"mods"`
		}
		if err := json.Unmarshal(data, &changes); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		var defs []string
		for _, c := range changes {
			defs = append(defs, c.Mods[0].Values[0].Text)
		}
		return defs
	}

	t.Run("named out of order, written in order", func(t *testing.T) {
		r, out := run(t, "--stage", siteOID, "--stage", "attributeType:"+siteCodeOID)
		expectCode(t, r, ExitDifferences)
		defs := written(t, out)
		if len(defs) != 2 || !strings.Contains(defs[0], siteCodeOID) || !strings.Contains(defs[1], siteOID) {
			t.Fatalf("written: %v", defs)
		}
	})
	t.Run("a removal named as one", func(t *testing.T) {
		r, out := run(t, "--stage-deletion", "attributeType:"+goneOID)
		expectCode(t, r, ExitDifferences)
		if defs := written(t, out); len(defs) != 1 || !strings.Contains(defs[0], "alderGone") {
			t.Fatalf("written: %v", defs)
		}
	})
	for name, args := range map[string][]string{
		"a class without the attribute type it needs": {"--stage", siteOID},
		"a removal selected as a change":              {"--stage", "attributeType:" + goneOID},
		"an OID two definitions share":                {"--stage-deletion", goneOID},
		"a NAME instead of an OID":                    {"--stage", "alderSiteCode"},
		"a difference only in metadata":               {"--stage", "2.5.4.3"},
	} {
		t.Run(name, func(t *testing.T) {
			r, out := run(t, args...)
			expectCode(t, r, ExitNotApplicable)
			assertAbsent(t, out)
		})
	}
}

func TestASchemaCaptureAsksForTheSchemaAndNothingElse(t *testing.T) {
	s := newStub(t)
	var sent map[string]any
	s.on("POST /api/v1/snapshots/capture", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, schemaSnapshotDoc)
	})
	out := filepath.Join(t.TempDir(), "schema.json")
	r := s.run(t, runOpts{}, "snapshot", "--kind", "schema", "--output", out)
	expectCode(t, r, ExitOK)
	if sent["kind"] != "schema" || sent["base"] != nil || sent["scope"] != nil {
		t.Errorf("request: %v", sent)
	}
	if !strings.Contains(r.stderr, "Captured the schema at cn=schema (2 attribute types, 1 object classes, 0 unparsed; complete)") {
		t.Errorf("stderr: %s", r.stderr)
	}
	if data, _ := os.ReadFile(out); string(data) != schemaSnapshotDoc {
		t.Errorf("the file is not what Alder sent:\n%s", data)
	}

	for name, args := range map[string][]string{
		"a base":        {"snapshot", "--kind", "schema", "--base", "dc=example,dc=test", "--output", "x.json"},
		"a live target": {"diff", "schema.json", "@live", "--schema-target", "cn=schema"},
	} {
		t.Run(name, func(t *testing.T) {
			expectCode(t, newStub(t).run(t, runOpts{}, args...), ExitUsage)
		})
	}
}
