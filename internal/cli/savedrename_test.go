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

const renameLDIF = "dn: " + aliceDN + "\nchangetype: modrdn\nnewrdn: uid=alicia\ndeleteoldrdn: 1\n" +
	"newsuperior: ou=staff,dc=example,dc=test\n"

// renameStub answers alder plan for one move of alice. subordinates is the
// plan item's count of what is below her (nil: a leaf), identity the
// attribute the entry is read with (empty: none), and sent collects each
// plan request's body.
func renameStub(t *testing.T, subordinates map[string]any, identity string, sent *[]string) *stub {
	t.Helper()
	s := newStub(t)
	stubAttributeNames(s)
	record := map[string]any{"dn": aliceDN, "type": "modrdn", "newRdn": "uid=alicia", "deleteOldRdn": true,
		"newSuperior": "ou=staff,dc=example,dc=test"}
	item := func(baseline string) map[string]any {
		it := map[string]any{"index": 0, "dn": aliceDN, "action": "rename", "exists": true, "intent": "exact",
			"kind": "data", "record": record, "baseline": baseline}
		if subordinates != nil {
			it["subordinates"] = subordinates
		}
		return it
	}
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if sent != nil {
			*sent = append(*sent, string(body))
		}
		baseline := "plan-alice"
		if strings.Contains(string(body), `"expect"`) {
			baseline = "saved-alice"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, planJSON(t, counts{rename: 1}, item(baseline)))
	})
	s.on("GET /api/v1/entry", func(w http.ResponseWriter, r *http.Request) {
		attrs := []any{
			map[string]any{"name": "uid", "values": []any{map[string]any{"text": "alice"}}},
			map[string]any{"name": "cn", "values": []any{map[string]any{"text": "Alice"}}},
		}
		if identity != "" && r.URL.Query().Get("includeOperational") == "true" {
			attrs = append(attrs, map[string]any{"name": identity,
				"values": []any{map[string]any{"text": "6f1c4a8e-2b1d-4c1e-9a7f-1d2e3f4a5b6c"}}})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, mustJSON(t, map[string]any{"dn": aliceDN, "attributes": attrs}))
	})
	s.reply("POST /api/v1/changeset/apply", http.StatusOK,
		`{"appliedCount":1,"outcomes":[{"index":0,"dn":"`+aliceDN+`","applied":true,"summary":"renamed"}]}`)
	return s
}

func saveRename(t *testing.T, s *stub, want int) (string, result) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "move.plan.json")
	r := s.run(t, runOpts{}, "plan", writeTemp(t, dir, "move.ldif", renameLDIF), "--out", out)
	expectCode(t, r, want)
	return out, r
}

func TestPlanOutSavesARenameBoundToItsEntrysIdentity(t *testing.T) {
	for _, tc := range []struct {
		name         string
		identity     string
		subordinates map[string]any
		below        float64
	}{
		{"a leaf on OpenLDAP", "entryUUID", nil, 0},
		{"a branch on 389 DS", "nsUniqueId", map[string]any{"count": 3, "truncated": false}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := saveRename(t, renameStub(t, tc.subordinates, tc.identity, nil), ExitOK)
			changes, _ := readSaved(t, out)["changes"].([]any)
			if len(changes) != 1 {
				t.Fatalf("changes = %v", changes)
			}
			change := changes[0].(map[string]any)
			if change["type"] != "modrdn" || change["newSuperior"] != "ou=staff,dc=example,dc=test" {
				t.Errorf("saved change = %v", change)
			}
			expect, _ := change["expect"].(map[string]any)
			if expect["subordinates"] != tc.below || expect["exhaustive"] != nil {
				t.Errorf("expect = %v, want %v entries below and not exhaustive", expect, tc.below)
			}
			names := map[string]bool{}
			for _, a := range expect["attributes"].([]any) {
				names[a.(map[string]any)["name"].(string)] = true
			}
			if !names[tc.identity] || !names["uid"] || names["cn"] || len(names) != 2 {
				t.Errorf("expected attributes %v, want %s and uid only", names, tc.identity)
			}
		})
	}
}

func TestPlanOutDoesNotSaveARenameItCannotBind(t *testing.T) {
	for _, tc := range []struct {
		name, identity, want string
		subordinates         map[string]any
	}{
		{"no identity this bind can read", "", "no entryUUID or nsUniqueId", nil},
		{"more below it than a plan counts", "entryUUID", "at least 10000 entries below it",
			map[string]any{"count": 10000, "truncated": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, r := saveRename(t, renameStub(t, tc.subordinates, tc.identity, nil), ExitFailed)
			if !strings.Contains(r.stderr, tc.want) || !strings.Contains(r.stderr, "apply this one with alder apply") {
				t.Errorf("stderr does not say %q and what to do instead:\n%s", tc.want, r.stderr)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Error("the plan was saved")
			}
		})
	}
}

func TestPlanOutDoesNotSaveARenameWhoseSubtreeChangedSinceItWasShown(t *testing.T) {
	s := newStub(t)
	stubAttributeNames(s)
	record := map[string]any{"dn": aliceDN, "type": "modrdn", "newRdn": "uid=alicia", "deleteOldRdn": true,
		"newSuperior": "ou=staff,dc=example,dc=test"}
	item := func(count int) map[string]any {
		return map[string]any{"index": 0, "dn": aliceDN, "action": "rename", "exists": true, "intent": "exact",
			"kind": "data", "record": record, "baseline": "b", "subordinates": map[string]any{"count": count, "truncated": false}}
	}
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		count := 3
		if strings.Contains(string(body), `"expect"`) {
			count = 4
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, planJSON(t, counts{rename: 1}, item(count)))
	})
	s.reply("GET /api/v1/entry", http.StatusOK, mustJSON(t, map[string]any{"dn": aliceDN, "attributes": []any{
		map[string]any{"name": "uid", "values": []any{map[string]any{"text": "alice"}}},
		map[string]any{"name": "entryUUID", "values": []any{map[string]any{"text": "u"}}},
	}}))
	_, r := saveRename(t, s, ExitStale)
	if !strings.Contains(r.stderr, "number of entries below it") {
		t.Errorf("stderr does not say what moved:\n%s", r.stderr)
	}
}

func TestApplyPlanAppliesASavedRename(t *testing.T) {
	var sent []string
	s := renameStub(t, map[string]any{"count": 3, "truncated": false}, "entryUUID", &sent)
	out, _ := saveRename(t, s, ExitOK)
	sent = nil
	r := s.run(t, runOpts{}, "apply", "--plan", out, "--yes")
	expectCode(t, r, ExitOK)
	if len(sent) != 1 {
		t.Fatalf("plan requests on apply: %d", len(sent))
	}
	var req struct {
		Changes []struct {
			Expect struct {
				Subordinates *int `json:"subordinates"`
			} `json:"expect"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(sent[0]), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Changes) != 1 || req.Changes[0].Expect.Subordinates == nil || *req.Changes[0].Expect.Subordinates != 3 {
		t.Errorf("apply planned %s; want the rename with its expectation of 3 entries below", sent[0])
	}
}

func TestASavedRenameIsReadStrictly(t *testing.T) {
	s := renameStub(t, nil, "entryUUID", nil)
	for _, tc := range []struct{ name, from, to string }{
		{"no count of what is below", `"subordinates": 0`, `"exhaustive": false`},
		{"no identity", `"name": "entryUUID"`, `"name": "description"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := saveRename(t, s, ExitOK)
			edit(t, out, tc.from, tc.to)
			r := s.run(t, runOpts{}, "apply", "--plan", out, "--yes")
			expectCode(t, r, ExitNotApplicable)
			if !strings.Contains(r.stderr, "does not expect the entry's identity") {
				t.Errorf("stderr:\n%s", r.stderr)
			}
		})
	}
}
