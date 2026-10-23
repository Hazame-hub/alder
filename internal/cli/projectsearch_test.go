package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const lockedSearchFile = `version: 1
title: Locked accounts
base: ou=people,dc=alder,dc=test
filter: (pwdAccountLockedTime=*)
attributes: [uid, mail, pwdAccountLockedTime]
limit: 50
`

const exportedLDIF = `# Exported by Alder
version: 1

dn: uid=alice,ou=people,dc=alder,dc=test
uid: alice

# 1 entries
# This export is complete.
`

func searchProject(t *testing.T, s *stub) string {
	return writeProject(t, s, map[string]string{
		"people/locked.search.yaml": lockedSearchFile,
		"all.search.yaml":           "version: 1\nbase: dc=alder,dc=test\nfilter: (objectClass=*)\n",
	})
}

func TestProjectSearchListsTheSearchesAndConnectsToNothing(t *testing.T) {
	s := newStub(t)
	dir := searchProject(t, s)
	r := s.run(t, projectRun, "project", "search", "--project", dir)
	expectCode(t, r, ExitOK)
	for _, want := range []string{
		"all\n  base dc=alder,dc=test (sub), limit 100\n  filter (objectClass=*)\n",
		"people/locked -- Locked accounts\n  base ou=people,dc=alder,dc=test (sub), limit 50\n" +
			"  filter (pwdAccountLockedTime=*)\n  attributes uid, mail, pwdAccountLockedTime\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout:\n%s\nwant %q", r.stdout, want)
		}
	}

	r = s.run(t, projectRun, "project", "search", "--project", dir, "--json")
	expectCode(t, r, ExitOK)
	var listed []searchListing
	if err := json.Unmarshal([]byte(r.stdout), &listed); err != nil || len(listed) != 2 || listed[1].Name != "people/locked" ||
		listed[1].Limit != 50 || listed[0].Attributes == nil {
		t.Errorf("--json: %v\n%s", err, r.stdout)
	}

	r = s.run(t, projectRun, "project", "validate", "--project", dir)
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stdout, "  2 saved searches: all, people/locked\n") {
		t.Errorf("validate:\n%s", r.stdout)
	}
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent; listing connects to nothing", len(s.calls))
	}
}

func TestProjectSearchRefusesWhatItCannotRun(t *testing.T) {
	s := newStub(t)
	dir := searchProject(t, s)
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"people/locked"}, ExitUsage, "--env is required to run people/locked"},
		{[]string{"--env", "dev"}, ExitUsage, "--env runs a search, and no search was named"},
		{[]string{"people/missing", "--env", "dev"}, ExitUsage, `has no search "people/missing"; it has all, people/locked`},
		{[]string{"people/locked", "--env", "staging"}, ExitUsage, `has no environment "staging"`},
		{[]string{"a", "b"}, ExitUsage, "expected at most one search name"},
	} {
		r := s.run(t, projectRun, append([]string{"project", "search", "--project", dir}, tc.args...)...)
		expectCode(t, r, tc.code)
		if !strings.Contains(r.stderr, tc.want) {
			t.Errorf("%v: stderr:\n%s\nwant %q", tc.args, r.stderr, tc.want)
		}
	}

	bad := writeProject(t, s, map[string]string{"x.search.yaml": "version: 1\nbase: dc=alder,dc=test\nfilter: (uid=*\n"})
	r := s.run(t, projectRun, "project", "search", "--project", bad)
	expectCode(t, r, ExitNotApplicable)
	if !strings.Contains(r.stderr, "x.search.yaml:3: filter is not a valid RFC 4515 filter") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if len(s.calls) != 0 {
		t.Errorf("%d request(s) sent for searches that could not run", len(s.calls))
	}
}

// exportStub answers GET /export/ldif with body and records the queries.
func exportStub(s *stub, status int, body string) func() []url.Values {
	var mu sync.Mutex
	var queries []url.Values
	s.on("GET /api/v1/export/ldif", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query())
		mu.Unlock()
		if status != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	return func() []url.Values {
		mu.Lock()
		defer mu.Unlock()
		return queries
	}
}

func TestProjectSearchWritesTheExportOfTheSearch(t *testing.T) {
	s := newStub(t)
	queries := exportStub(s, http.StatusOK, exportedLDIF)
	dir := searchProject(t, s)
	r := s.run(t, projectRun, "project", "search", "--project", dir, "people/locked", "--env", "dev")
	expectCode(t, r, ExitOK)
	if r.stdout != exportedLDIF {
		t.Errorf("stdout is not the export byte for byte:\n%s", r.stdout)
	}
	q := queries()
	if len(q) != 1 {
		t.Fatalf("%d export(s)", len(q))
	}
	if q[0].Get("dn") != "ou=people,dc=alder,dc=test" || q[0].Get("scope") != "sub" || q[0].Get("filter") != "(pwdAccountLockedTime=*)" ||
		q[0].Get("limit") != "50" || q[0].Get("attributes") != "uid,mail,pwdAccountLockedTime" || q[0].Has("includeSensitive") {
		t.Errorf("query = %v", q[0])
	}
	if !strings.Contains(r.stderr, "alder: searching ou=people,dc=alder,dc=test for people/locked in environment dev") {
		t.Errorf("stderr:\n%s", r.stderr)
	}

	// No attributes in the file: every user attribute, so none is named.
	r = s.run(t, projectRun, "project", "search", "--project", dir, "all", "--env", "dev")
	expectCode(t, r, ExitOK)
	if q := queries(); q[1].Has("attributes") {
		t.Errorf("a search without attributes named some: %v", q[1])
	}
}

func TestProjectSearchSaysWhenTheExportStoppedShort(t *testing.T) {
	s := newStub(t)
	exportStub(s, http.StatusOK, strings.Replace(exportedLDIF, "# This export is complete.\n",
		"# WARNING: the result was truncated at the export limit of 50.\n# This file does not contain the whole subtree.\n", 1))
	dir := searchProject(t, s)
	r := s.run(t, projectRun, "project", "search", "--project", dir, "people/locked", "--env", "dev")
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stderr, "people/locked stopped at its limit of 50 entries") {
		t.Errorf("stderr:\n%s", r.stderr)
	}

	// A value that says so is not the export saying so.
	s = newStub(t)
	exportStub(s, http.StatusOK, strings.Replace(exportedLDIF, "uid: alice\n",
		"uid: alice\ndescription: see # ERROR: and # WARNING: the result was truncated\n", 1))
	dir = searchProject(t, s)
	r = s.run(t, projectRun, "project", "search", "--project", dir, "people/locked", "--env", "dev")
	expectCode(t, r, ExitOK)
	if strings.Contains(r.stderr, "stopped at its limit") {
		t.Errorf("a value was read as the trailer:\n%s", r.stderr)
	}

	s = newStub(t)
	exportStub(s, http.StatusOK, "# Exported by Alder\nversion: 1\n\n# ERROR: this export stopped after 0 entries: gone\n"+
		"# This export is incomplete. Do not restore from it.\n")
	dir = searchProject(t, s)
	r = s.run(t, projectRun, "project", "search", "--project", dir, "people/locked", "--env", "dev")
	expectCode(t, r, ExitFailed)
}

// The export refuses to write an empty file; for a search, nothing matched
// is an answer.
func TestProjectSearchMatchingNothingSucceeds(t *testing.T) {
	s := newStub(t)
	exportStub(s, http.StatusBadRequest, `{"error":"bad_request","message":"Nothing matched, so there is nothing to export."}`)
	s.reply("POST /api/v1/search", http.StatusOK, `{"entries":[],"truncated":false}`)
	dir := searchProject(t, s)
	r := s.run(t, projectRun, "project", "search", "--project", dir, "people/locked", "--env", "dev")
	expectCode(t, r, ExitOK)
	if r.stdout != "" || !strings.Contains(r.stderr, "people/locked matched no entry") {
		t.Errorf("stdout %q, stderr:\n%s", r.stdout, r.stderr)
	}

	// The same refusal when something does match is the directory's, and fails.
	s = newStub(t)
	exportStub(s, http.StatusBadRequest, `{"error":"bad_request","message":"no"}`)
	s.reply("POST /api/v1/search", http.StatusOK, `{"entries":[{"dn":"uid=a"}],"truncated":false}`)
	dir = searchProject(t, s)
	r = s.run(t, projectRun, "project", "search", "--project", dir, "people/locked", "--env", "dev")
	expectCode(t, r, ExitFailed)
}

func TestProjectSearchJSONIsTheEntriesAsOneArray(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/search", http.StatusOK,
		`{"entries":[{"dn":"uid=alice,ou=people,dc=alder,dc=test","attributes":[{"name":"uid","values":["alice"]}]}],"truncated":true}`)
	dir := searchProject(t, s)
	r := s.run(t, projectRun, "project", "search", "--project", dir, "people/locked", "--env", "dev", "--json")
	expectCode(t, r, ExitOK)
	var entries []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &entries); err != nil || len(entries) != 1 {
		t.Fatalf("stdout: %v\n%s", err, r.stdout)
	}
	if !strings.Contains(r.stderr, "people/locked stopped at its limit of 50 entries") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	sent := s.called("POST /api/v1/search")
	var req map[string]any
	if err := json.Unmarshal(sent[0].Body, &req); err != nil {
		t.Fatal(err)
	}
	if req["baseDn"] != "ou=people,dc=alder,dc=test" || req["filter"] != "(pwdAccountLockedTime=*)" || req["limit"] != float64(50) ||
		len(req["attributes"].([]any)) != 3 {
		t.Errorf("request = %v", req)
	}
}

func TestProjectSearchMovesTheBaseForAnEnvironmentsSuffix(t *testing.T) {
	s := newStub(t)
	queries := exportStub(s, http.StatusOK, exportedLDIF)
	dir := writeProject(t, s, map[string]string{
		"people/managed.search.yaml": "version: 1\nbase: ou=people,dc=alder,dc=test\nfilter: (manager=uid=ana,ou=people,dc=alder,dc=test)\n",
	})
	rewriteFile(t, dir, "alder.yaml", func(text string) string {
		text = strings.Replace(text, "version: 1\n", "version: 1\nsuffix: dc=alder,dc=test\n", 1)
		return strings.Replace(text, "    bind-password-env: ALDER_DEV_BIND_PASSWORD\n",
			"    bind-password-env: ALDER_DEV_BIND_PASSWORD\n    suffix: dc=dev,dc=alder,dc=test\n", 1)
	})
	r := s.run(t, projectRun, "project", "search", "--project", dir, "people/managed", "--env", "dev")
	expectCode(t, r, ExitOK)
	q := queries()
	if q[0].Get("dn") != "ou=people,dc=dev,dc=alder,dc=test" || q[0].Get("filter") != "(manager=uid=ana,ou=people,dc=alder,dc=test)" {
		t.Errorf("query = %v", q[0])
	}
	for _, want := range []string{
		"alder: the base is moved from dc=alder,dc=test to dc=dev,dc=alder,dc=test",
		"alder: the filter names a DN below dc=alder,dc=test, and is sent as written (contains_dn)",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr:\n%s\nwant %q", r.stderr, want)
		}
	}
}

func rewriteFile(t *testing.T, dir, name string, edit func(string) string) {
	t.Helper()
	path := filepath.Join(dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(edit(string(raw))), 0o600); err != nil {
		t.Fatal(err)
	}
}
