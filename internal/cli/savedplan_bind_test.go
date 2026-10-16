package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/plan"
)

// A saved project plan is bound to the project: the digest says the files
// are the ones it was made from, and an apply checks that every saved change
// is one those files ask for. Editing the file alone changes nothing that
// runs.

func textValue(v string) map[string]any { return map[string]any{"text": v} }

// tamper rewrites a saved plan's first change and writes it back.
func tamper(t *testing.T, path string, change func(c map[string]any)) {
	t.Helper()
	doc := readSaved(t, path)
	change(doc["changes"].([]any)[0].(map[string]any))
	if err := os.WriteFile(path, []byte(mustJSON(t, doc)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectApplyPlanRefusesAChangeTheProjectDoesNotMake(t *testing.T) {
	const admins = "cn=admins,ou=groups,dc=alder,dc=test"
	const mallory = "uid=mallory,ou=people,dc=alder,dc=test"
	for _, tc := range []struct {
		name   string
		change func(c map[string]any)
		// connects is whether the refusal needs the directory: the schema
		// says which names are one attribute, and the environment's names.
		connects bool
	}{
		{"an entry outside the project, expecting nothing", func(c map[string]any) {
			c["dn"] = admins
			c["mods"] = []any{map[string]any{"op": "add", "name": "member", "values": []any{textValue(mallory)}}}
			c["expect"] = map[string]any{"attributes": []any{}}
		}, false},
		{"an entry outside the project", func(c map[string]any) {
			c["dn"] = admins
			c["mods"] = []any{map[string]any{"op": "add", "name": "member", "values": []any{textValue(mallory)}}}
			c["expect"] = map[string]any{"attributes": []any{map[string]any{"name": "member", "values": []any{}}}}
		}, true},
		{"another value than the project's", func(c map[string]any) {
			c["mods"] = []any{map[string]any{"op": "replace", "name": "title", "values": []any{textValue("Owner")}}}
		}, true},
		{"one value more than the project's", func(c map[string]any) {
			c["mods"] = []any{map[string]any{"op": "replace", "name": "title", "values": []any{textValue("After"), textValue("Owner")}}}
		}, true},
		{"an add of a value instead of a replace", func(c map[string]any) {
			c["mods"] = []any{map[string]any{"op": "add", "name": "title", "values": []any{textValue("After")}}}
		}, true},
		{"a delete instead of a replace", func(c map[string]any) {
			c["mods"] = []any{map[string]any{"op": "delete", "name": "title"}}
		}, true},
		{"an attribute the project does not name", func(c map[string]any) {
			c["mods"] = []any{map[string]any{"op": "replace", "name": "mail", "values": []any{textValue("a@x.test")}}}
			c["expect"] = map[string]any{"attributes": []any{map[string]any{"name": "mail", "values": []any{}}}}
		}, true},
		{"an attribute the expectation does not name", func(c map[string]any) {
			c["mods"] = []any{
				map[string]any{"op": "replace", "name": "title", "values": []any{textValue("After")}},
				map[string]any{"op": "replace", "name": "sn", "values": []any{textValue("A")}},
			}
		}, true},
		{"an add the project does not describe", func(c map[string]any) {
			c["dn"] = mallory
			c["type"] = "add"
			delete(c, "mods")
			delete(c, "expect")
			c["attributes"] = []any{map[string]any{"name": "objectClass", "values": []any{textValue("inetOrgPerson")}}}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := savedPlanStub(t, true)
			dir := writeSavedPlanProject(t, s)
			saved := savePlanFor(t, s, dir)
			tamper(t, saved, tc.change)
			before, planned := len(s.calls), len(s.called("POST /api/v1/plan"))
			r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", saved, "--yes")
			expectCode(t, r, ExitNotApplicable)
			if len(s.called("POST /api/v1/changeset/apply")) != 0 {
				t.Fatal("a change the project does not make was applied")
			}
			if n := len(s.called("POST /api/v1/plan")) - planned; n != 0 {
				t.Errorf("planned %d times; a refused file is never planned", n)
			}
			if !tc.connects && len(s.calls) != before {
				t.Errorf("%d request(s) sent for a file that is not a saved plan", len(s.calls)-before)
			}
		})
	}
}

// The add of an entry the project describes has to be the project's entry.
func TestProjectApplyPlanRefusesAnAddThatIsNotTheProjectsEntry(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeSavedPlanProject(t, s)
	saved := savePlanFor(t, s, dir)
	doc := readSaved(t, saved)
	bob := doc["changes"].([]any)[1].(map[string]any)
	attrs := bob["attributes"].([]any)
	bob["attributes"] = append(attrs, map[string]any{"name": "description", "values": []any{textValue("admin")}})
	if err := os.WriteFile(saved, []byte(mustJSON(t, doc)), 0o600); err != nil {
		t.Fatal(err)
	}
	r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", saved, "--yes")
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "uid=bob,ou=people,dc=alder,dc=test") {
		t.Errorf("stderr does not name the entry:\n%s", r.stderr)
	}
	if len(s.called("POST /api/v1/changeset/apply")) != 0 {
		t.Fatal("an add the project does not make was applied")
	}
}

// alder apply --plan has no project to bind the changes to, but a
// modification still has to expect every attribute it changes.
func TestApplyPlanRefusesAModificationThatDoesNotExpectWhatItChanges(t *testing.T) {
	for name, expect := range map[string]map[string]any{
		"nothing":           {"attributes": []any{}},
		"another attribute": {"attributes": []any{map[string]any{"name": "cn", "values": []any{textValue("alice")}}}},
	} {
		t.Run(name, func(t *testing.T) {
			s := inputPlanStub(t, true)
			saved := saveInput(t, s)
			tamper(t, saved, func(c map[string]any) { c["expect"] = expect })
			r := s.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
			expectCode(t, r, ExitNotApplicable)
			if len(s.called("POST /api/v1/changeset/apply")) != 0 {
				t.Fatal("a modification expecting nothing of what it changes was applied")
			}
		})
	}
}

// The expectation names the attribute as the directory returns it, whatever
// name the change used: an alias or the OID is one attribute.
func TestApplyPlanAcceptsAnExpectationUnderTheAttributesOwnName(t *testing.T) {
	s := inputPlanStubNamed(t, true, "2.5.4.12")
	saved := saveInput(t, s)
	r := s.run(t, runOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
	expectCode(t, r, ExitOK)
}

// reconcilingStub is a directory that plans the way Alder does: an entry the
// LDIF describes and the directory lacks is added as written, one it holds is
// reconciled into the replacements that make it match. live is keyed by the
// lowercased DN, and holds attribute names as the directory returns them.
type reconcilingStub struct {
	*stub
	mu      sync.Mutex
	live    map[string]*directory.Entry
	applied []json.RawMessage
}

func newReconcilingStub(t *testing.T, entries ...*directory.Entry) *reconcilingStub {
	t.Helper()
	rs := &reconcilingStub{stub: newStub(t), live: map[string]*directory.Entry{}}
	for _, e := range entries {
		rs.live[strings.ToLower(e.DN.String())] = e
	}
	stubSchemaWithNames(rs.stub)
	request := func(c directory.ChangeRecord) map[string]any {
		values := func(vs [][]byte) []any {
			out := []any{}
			for _, v := range vs {
				out = append(out, textValue(string(v)))
			}
			return out
		}
		out := map[string]any{"dn": c.DN.String(), "type": string(c.Type)}
		if c.Type == directory.ChangeAdd {
			var attrs []any
			for _, a := range c.Attrs {
				attrs = append(attrs, map[string]any{"name": a.Name, "values": values(a.Values)})
			}
			out["attributes"] = attrs
		} else {
			var mods []any
			for _, m := range c.Mods {
				mods = append(mods, map[string]any{"op": string(m.Op), "name": m.Name, "values": values(m.Values)})
			}
			out["mods"] = mods
		}
		return out
	}
	rs.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Ldif    *string           `json:"ldif"`
			Changes []json.RawMessage `json:"changes"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		rs.mu.Lock()
		defer rs.mu.Unlock()
		var items []map[string]any
		var c counts
		if req.Ldif != nil {
			records, err := ldif.Unmarshal([]byte(*req.Ldif))
			if err != nil {
				t.Errorf("the plan request's LDIF does not read: %v", err)
			}
			for i, rec := range records {
				add := directory.ChangeRecord{DN: rec.DN, Type: directory.ChangeAdd}
				for _, a := range rec.Attrs {
					add.Attrs = append(add.Attrs, directory.Attribute{Name: a.Name, Values: a.Values})
				}
				item := map[string]any{"index": i, "dn": rec.DN.String(), "intent": "desired", "kind": "data",
					"baseline": fmt.Sprintf("desired-%d", i)}
				live := rs.live[strings.ToLower(rec.DN.String())]
				switch outcome := plan.Reconcile(add, live, nil); {
				case live == nil:
					item["action"], item["exists"], item["record"] = "add", false, request(add)
					c.add++
				case outcome.Changed:
					item["action"], item["exists"], item["record"] = "modify", true, request(outcome.Change)
					c.modify++
				default:
					item = map[string]any{"index": i, "dn": rec.DN.String(), "action": "unchanged", "exists": true,
						"intent": "desired", "kind": "data"}
					c.unchanged++
				}
				items = append(items, item)
			}
		} else {
			for i, raw := range req.Changes {
				var change struct {
					Dn   string `json:"dn"`
					Type string `json:"type"`
				}
				_ = json.Unmarshal(raw, &change)
				items = append(items, map[string]any{"index": i, "dn": change.Dn, "action": change.Type,
					"exists": change.Type == "modify", "intent": "exact", "kind": "data",
					"record": raw, "baseline": fmt.Sprintf("saved-%d", i)})
				if change.Type == "add" {
					c.add++
				} else {
					c.modify++
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, planJSON(t, c, items...))
	})
	rs.on("GET /api/v1/entry", func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		defer rs.mu.Unlock()
		e := rs.live[strings.ToLower(dn.MustParse(r.URL.Query().Get("dn")).String())]
		if e == nil {
			http.NotFound(w, r)
			return
		}
		var attrs []any
		for _, name := range e.Order {
			var values []any
			for _, v := range e.Attributes[name] {
				values = append(values, textValue(string(v)))
			}
			attrs = append(attrs, map[string]any{"name": name,
				"kind": map[string]any{"name": name, "kind": "string", "known": true}, "values": values})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, mustJSON(t, map[string]any{"dn": e.DN.String(), "attributes": attrs}))
	})
	rs.on("POST /api/v1/changeset/apply", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Changes []json.RawMessage `json:"changes"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		var outcomes []any
		for i, raw := range req.Changes {
			var change struct {
				Dn string `json:"dn"`
			}
			_ = json.Unmarshal(raw, &change)
			outcomes = append(outcomes, map[string]any{"index": i, "dn": change.Dn, "applied": true, "summary": "applied"})
		}
		rs.mu.Lock()
		rs.applied = req.Changes
		rs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, mustJSON(t, map[string]any{"appliedCount": len(outcomes), "outcomes": outcomes}))
	})
	return rs
}

// stubSchemaWithNames is GET /schema with the syntaxes a rebase reads and the
// names an expectation is written under.
func stubSchemaWithNames(s *stub) {
	const dirString, dnSyntax, oid, phone = "1.3.6.1.4.1.1466.115.121.1.15", "1.3.6.1.4.1.1466.115.121.1.12",
		"1.3.6.1.4.1.1466.115.121.1.38", "1.3.6.1.4.1.1466.115.121.1.50"
	types := []map[string]any{
		{"name": "objectClass", "oid": "2.5.4.0", "syntax": oid},
		{"name": "uid", "names": []string{"uid", "userid"}, "oid": "0.9.2342.19200300.100.1.1", "syntax": dirString},
		{"name": "cn", "names": []string{"cn", "commonName"}, "oid": "2.5.4.3", "syntax": dirString},
		{"name": "sn", "names": []string{"sn", "surname"}, "oid": "2.5.4.4", "syntax": dirString},
		{"name": "title", "oid": "2.5.4.12", "syntax": dirString},
		{"name": "telephoneNumber", "oid": "2.5.4.20", "syntax": phone},
		{"name": "manager", "oid": "0.9.2342.19200300.100.1.10", "syntax": dnSyntax},
		{"name": "member", "oid": "2.5.4.31", "syntax": dnSyntax},
	}
	body, _ := json.Marshal(map[string]any{"subschemaDn": "cn=Subschema", "counts": map[string]any{}, "attributeTypes": types})
	s.reply("GET /api/v1/schema", http.StatusOK, string(body))
}

func liveEntry(d string, attrs ...string) *directory.Entry {
	e := directory.NewEntry(dn.MustParse(d))
	for i := 0; i < len(attrs); i += 2 {
		e.Set(attrs[i], append(e.Get(attrs[i]), []byte(attrs[i+1])))
	}
	return e
}

// A realistic project: written under one suffix and held by dev under
// another, with names in another case or by an alias than the directory
// returns them, values of several, a DN value that moves with the suffix,
// and an entry to add. Its saved plan applies.
func TestASavedProjectPlanAppliesAsTheProjectDescribesIt(t *testing.T) {
	const dev = "ou=people,dc=dev,dc=alder,dc=test"
	rs := newReconcilingStub(t,
		liveEntry("uid=alice,"+dev, "objectClass", "inetOrgPerson", "uid", "alice", "cn", "Alice", "sn", "A",
			"title", "Before", "telephoneNumber", "+1 555 0100", "manager", "uid=carol,"+dev),
		liveEntry("cn=staff,"+dev, "objectClass", "groupOfNames", "cn", "staff", "member", "uid=alice,"+dev),
		liveEntry("uid=carol,"+dev, "objectClass", "inetOrgPerson", "uid", "carol", "cn", "Carol", "sn", "C"),
	)
	dir := writeProject(t, rs.stub, map[string]string{
		"alder.yaml": `version: 1
suffix: dc=alder,dc=test
managed:
  - base: ou=people,dc=alder,dc=test
    files: [people/*.ldif]
environments:
  dev:
    api-url: ` + rs.srv.URL + `
    host: ldap.dev.test
    bind-dn: cn=alder,ou=services,dc=dev,dc=alder,dc=test
    bind-password-env: ALDER_DEV_BIND_PASSWORD
    suffix: dc=dev,dc=alder,dc=test
`,
		"people/alice.ldif": `dn: UID=Alice,OU=People,DC=alder,DC=test
objectClass: inetOrgPerson
uid: alice
CN: Alice
surname: A
Title: After
telephonenumber: +1 555 0100
telephonenumber: +1 555 0101
manager: uid=bob,ou=people,dc=alder,dc=test
`,
		"people/bob.ldif": projectBob + "commonName: Robert\n",
		"people/carol.ldif": `dn: uid=carol,ou=people,dc=alder,dc=test
objectClass: inetOrgPerson
uid: carol
cn: Carol
sn: C
`,
		"people/staff.ldif": `dn: cn=staff,ou=people,dc=alder,dc=test
objectClass: groupOfNames
cn: staff
member: uid=alice,ou=people,dc=alder,dc=test
member: uid=bob,ou=people,dc=alder,dc=test
member: uid=carol,ou=people,dc=alder,dc=test
`,
	})
	saved := filepath.Join(t.TempDir(), "dev.plan.json")
	r := rs.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", saved)
	expectCode(t, r, ExitOK)
	changes, _ := readSaved(t, saved)["changes"].([]any)
	if len(changes) != 3 {
		t.Fatalf("saved %d changes, want alice and staff modified and bob added: %v", len(changes), changes)
	}

	r = rs.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", saved, "--yes")
	expectCode(t, r, ExitOK)
	if len(rs.applied) != 3 {
		t.Fatalf("applied %d changes, want 3:\n%s", len(rs.applied), r.stderr)
	}
}

// Saving a plan reads what each modification expects after the plan has been
// shown. A directory that moved in between is refused, not written into the
// expectation: the plan saved would not be the plan reviewed.
func TestAPlanIsNotSavedWhenWhatItShowedMovedBeforeTheExpectationWasRead(t *testing.T) {
	const group = "cn=staff,ou=people,dc=alder,dc=test"
	member := func(removed ...string) []any {
		return []any{map[string]any{"attribute": "member", "gained": []any{}, "removed": removed}}
	}
	item := func(intent, baseline string, removed ...string) map[string]any {
		return map[string]any{"index": 0, "dn": group, "action": "modify", "exists": true, "intent": intent, "kind": "data",
			"membership": member(removed...),
			"record": map[string]any{"dn": group, "type": "modify", "mods": []any{
				map[string]any{"op": "replace", "name": "member", "values": []any{textValue("uid=x")}}}},
			"baseline": baseline}
	}
	for _, tc := range []struct {
		name string
		// fresh is what the saved changes' plan finds removed.
		fresh []string
		want  int
	}{
		{"the same members", []string{"uid=y"}, ExitOK},
		{"a member added meanwhile", []string{"uid=y", "uid=z"}, ExitStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t)
			stubAttributeNames(s)
			shown := planJSON(t, counts{modify: 1}, item("desired", "b", "uid=y"))
			fresh := planJSON(t, counts{modify: 1}, item("exact", "b2", tc.fresh...))
			s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(string(body), `"mode":"desired"`) {
					_, _ = io.WriteString(w, shown)
				} else {
					_, _ = io.WriteString(w, fresh)
				}
			})
			var values []any
			for _, v := range append([]string{"uid=x"}, tc.fresh...) {
				values = append(values, textValue(v))
			}
			s.reply("GET /api/v1/entry", http.StatusOK, mustJSON(t, map[string]any{"dn": group, "attributes": []any{
				map[string]any{"name": "member", "kind": map[string]any{"name": "member", "kind": "dn", "known": true},
					"values": values}}}))
			dir := writeProject(t, s, nil)
			out := filepath.Join(t.TempDir(), "dev.plan.json")
			r := s.run(t, projectRun, "project", "plan", "--project", dir, "--env", "dev", "--out", out)
			expectCode(t, r, tc.want)
			_, err := os.Stat(out)
			if written := err == nil; written != (tc.want == ExitOK) {
				t.Errorf("written = %v, want %v:\n%s", written, tc.want == ExitOK, r.stderr)
			}
			if tc.want == ExitStale && !strings.Contains(r.stderr, group) {
				t.Errorf("stderr does not name the entry that moved:\n%s", r.stderr)
			}
		})
	}
}

// The same holds for alder plan --out.
func TestAnInputPlanIsNotSavedWhenWhatItShowedMovedBeforeTheExpectationWasRead(t *testing.T) {
	const group = "cn=staff,ou=groups,dc=example,dc=test"
	item := func(baseline string, removed ...string) map[string]any {
		return map[string]any{"index": 0, "dn": group, "action": "modify", "exists": true, "intent": "exact", "kind": "data",
			"membership": []any{map[string]any{"attribute": "member", "gained": []any{}, "removed": removed}},
			"record": map[string]any{"dn": group, "type": "modify", "mods": []any{
				map[string]any{"op": "delete", "name": "member", "values": []any{textValue("uid=y")}}}},
			"baseline": baseline}
	}
	s := newStub(t)
	stubAttributeNames(s)
	shown := planJSON(t, counts{modify: 1}, item("b", "uid=y"))
	// Planned again, the change removes nothing: uid=y left meanwhile.
	fresh := planJSON(t, counts{modify: 1}, item("b2"))
	s.on("POST /api/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(body), `"expect"`) {
			_, _ = io.WriteString(w, fresh)
		} else {
			_, _ = io.WriteString(w, shown)
		}
	})
	s.reply("GET /api/v1/entry", http.StatusOK, mustJSON(t, map[string]any{"dn": group, "attributes": []any{
		map[string]any{"name": "member", "kind": map[string]any{"name": "member", "kind": "dn", "known": true},
			"values": []any{textValue("uid=x")}}}}))
	dir := t.TempDir()
	out := filepath.Join(dir, "p.json")
	in := writeTemp(t, dir, "c.ldif", "dn: "+group+"\nchangetype: modify\ndelete: member\nmember: uid=y\n-\n")
	r := s.run(t, runOpts{}, "plan", in, "--out", out)
	expectCode(t, r, ExitStale)
	if _, err := os.Stat(out); err == nil {
		t.Errorf("%s was written for a plan that is not the one shown", out)
	}
}

// A project apply without a terminal and without --yes refuses a file made
// for another environment as that (3), as it does with --yes, not as
// unconfirmed (5).
func TestProjectApplyPlanForAnotherEnvironmentIsRefusedAsSuchWithoutYes(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeSavedPlanProject(t, s)
	saved := savePlanFor(t, s, dir)
	opts := projectRun
	opts.env = map[string]string{"ALDER_DEV_BIND_PASSWORD": testSecret, "ALDER_PROD_BIND_PASSWORD": testSecret}
	r := s.run(t, opts, "project", "apply", "--project", dir, "--env", "prod", "--plan", saved)
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "was made for environment dev, not prod") {
		t.Errorf("stderr:\n%s", r.stderr)
	}

	// A file that is this environment's still needs someone to confirm it.
	r = s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", saved)
	expectCode(t, r, ExitNotConfirmed)
}

// The reader is strict about keys as well as values: Go's decoder matches a
// key in any case and keeps the last of two, so either would let a file say
// one thing to a person reading it and another to Alder.
func TestASavedPlansKeysAreReadExactly(t *testing.T) {
	s := savedPlanStub(t, true)
	dir := writeSavedPlanProject(t, s)
	saved := savePlanFor(t, s, dir)
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	good := string(data)
	if _, err := decodeSavedPlan(data); err != nil {
		t.Fatalf("the saved plan does not read: %v", err)
	}
	const smuggled = `[{"dn":"cn=x,dc=alder,dc=test","type":"add","attributes":[{"name":"cn","values":[{"text":"x"}]}]}]`
	for name, tc := range map[string]struct{ from, to string }{
		"a key in another case":            {`"changes": [`, `"CHANGES": ` + smuggled + `, "changes": [`},
		"a key twice":                      {`"changes": [`, `"changes": ` + smuggled + `, "changes": [`},
		"the environment's key in a case":  {`"name": "dev"`, `"Name": "dev"`},
		"the environment's key twice":      {`"name": "dev"`, `"name": "prod", "name": "dev"`},
		"a change's key in another case":   {`"dn": "uid=alice`, `"DN": "uid=alice`},
		"a modification's key twice":       {`"op": "replace"`, `"op": "add", "op": "replace"`},
		"an expectation's key in a case":   {`"expect": {`, `"Expect": {`},
		"an expected attribute's key case": {`"values": [`, `"Values": [`},
		"a value's key in another case":    {`"text": "Before"`, `"TEXT": "Before"`},
		"the project's key twice":          {`"digest": "`, `"digest": "sha256:00", "digest": "`},
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(good, tc.from) {
				t.Fatalf("the saved plan has no %q:\n%s", tc.from, good)
			}
			bad := strings.Replace(good, tc.from, tc.to, 1)
			if _, err := decodeSavedPlan([]byte(bad)); err == nil {
				t.Fatalf("read:\n%s", bad)
			}
			path := filepath.Join(t.TempDir(), "bad.plan.json")
			if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			before := len(s.calls)
			r := s.run(t, projectRun, "project", "apply", "--project", dir, "--env", "dev", "--plan", path, "--yes")
			expectCode(t, r, ExitNotApplicable)
			if len(s.calls) != before {
				t.Error("requests were sent for a file that is not a saved plan")
			}
		})
	}
}
