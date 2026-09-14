package cli

import (
	"encoding/json"
	"testing"
)

// Documents in the shapes Alder answers with. They are built from maps rather
// than from the generated types on purpose: a test that encoded api.Plan would
// agree with the client about any field the two had both got wrong.

const snapshotDoc = `{
  "format": "alder-snapshot",
  "version": 1,
  "kind": "data",
  "createdAt": "2026-09-14T10:00:00Z",
  "source": {"base": "ou=people,dc=example,dc=test", "scope": "sub", "filter": "(objectClass=*)"},
  "operationalAttributes": false,
  "schemaAvailable": true,
  "excluded": ["operational-attributes", "sensitive-values"],
  "completeness": "complete",
  "entryCount": 1,
  "attributes": [{"name": "userPassword", "sensitive": true}],
  "checksum": "sha256:0123",
  "entries": [
    {"dn": "uid=alice,ou=people,dc=example,dc=test", "attributes": [
      {"name": "objectClass", "values": [{"text": "person"}]},
      {"name": "userPassword", "withheld": 1}
    ]}
  ]
}
`

const restoreLDIF = "dn: uid=alice,ou=people,dc=example,dc=test\nchangetype: modify\nreplace: title\ntitle: Before\n-\n"

const aliceDN = "uid=alice,ou=people,dc=example,dc=test"

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type counts struct {
	examined, add, modify, delete, rename, setPassword, unchanged, conflict, invalid int
}

func planJSON(t *testing.T, c counts, items ...map[string]any) string {
	t.Helper()
	if c.examined == 0 {
		c.examined = len(items)
	}
	if items == nil {
		items = []map[string]any{}
	}
	return mustJSON(t, map[string]any{
		"counts": map[string]int{
			"examined": c.examined, "add": c.add, "modify": c.modify, "delete": c.delete, "rename": c.rename,
			"setPassword": c.setPassword, "unchanged": c.unchanged, "conflict": c.conflict, "invalid": c.invalid,
		},
		"items": items,
		"impact": map[string]any{
			"kinds":      map[string]int{"data": len(items), "schema": 0, "config": 0},
			"membership": map[string]int{"gained": 0, "removed": 0, "groups": 0},
			"references": map[string]any{"analysed": true, "found": 0, "dangling": 0, "truncated": false},
		},
	})
}

// exactModify is a planned modification of alice, with the plan's record
// withholding a sensitive value as a plan does.
func exactModify(baseline string) map[string]any {
	return map[string]any{
		"index": 0, "dn": aliceDN, "action": "modify", "exists": true, "intent": "exact", "kind": "data",
		"record": map[string]any{"dn": aliceDN, "type": "modify", "mods": []any{
			map[string]any{"op": "replace", "name": "title", "values": []any{map[string]any{"text": "Before"}}},
			map[string]any{"op": "replace", "name": "userPassword", "values": []any{map[string]any{"size": 26}}},
		}},
		"preview": map[string]any{
			"ldif":    "dn: " + aliceDN + "\nchangetype: modify\nreplace: title\ntitle: Before\n-\nreplace: userPassword\nuserPassword: withheld (26 bytes)\n-\n",
			"ansible": "", "summary": "modify alice",
		},
		"baseline": baseline,
	}
}

// importJSON is /import/ldif's answer: the client's own copy of the change,
// with the password in it.
func importJSON(t *testing.T) string {
	t.Helper()
	return mustJSON(t, map[string]any{
		"changes": []any{map[string]any{"ldif": "", "ansible": "", "summary": ""}},
		"requests": []any{map[string]any{"dn": aliceDN, "type": "modify", "mods": []any{
			map[string]any{"op": "replace", "name": "title", "values": []any{map[string]any{"text": "Before"}}},
			map[string]any{"op": "replace", "name": "userPassword", "values": []any{map[string]any{"text": testSecret}}},
		}}},
	})
}

type diffCounts struct {
	compared, added, modified, removed, renamed, unchanged, unknown int
}

func diffJSON(t *testing.T, complete bool, c diffCounts, items ...map[string]any) string {
	t.Helper()
	if items == nil {
		items = []map[string]any{}
	}
	side := func(kind string) map[string]any {
		return map[string]any{"kind": kind, "base": "ou=people,dc=example,dc=test", "scope": "sub",
			"filter": "(objectClass=*)", "operationalAttributes": false, "entryCount": c.compared}
	}
	doc := map[string]any{
		"source": side("live"), "target": side("snapshot"), "complete": complete,
		"counts": map[string]int{"compared": c.compared, "added": c.added, "modified": c.modified, "removed": c.removed,
			"renamed": c.renamed, "unchanged": c.unchanged, "unknown": c.unknown},
		"crossVendor": false, "operationalIgnored": false, "items": items,
	}
	if !complete {
		doc["reasons"] = []any{map[string]any{"code": "search_limit_reached", "detail": "the live read stopped at its bound"}}
	}
	return mustJSON(t, doc)
}

func apiErrorJSON(code, message string, extra map[string]any) string {
	doc := map[string]any{"error": code, "message": message}
	for k, v := range extra {
		doc[k] = v
	}
	b, _ := json.Marshal(doc)
	return string(b)
}
