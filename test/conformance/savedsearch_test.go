//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// Saved searches against both real directories: examples/project's
// people/engineers, run by alder project search, returns what the web
// interface's link to it returns (POST /search with the same base, scope,
// filter, limit and attributes); its LDIF is GET /export/ldif's for the same
// parameters, byte for byte; and an environment with a suffix of its own
// runs it under that suffix.

const savedSearchBase = "ou=alder-savedsearch," + suffix

// savedSearchMoved is where the "moved" environment's suffix puts the
// project: an engineer lives there too, and nobody else.
const savedSearchMoved = "ou=moved," + savedSearchBase

func clearSavedSearch(t *testing.T, sess directory.Session) {
	t.Helper()
	deleteQuietly(t, sess,
		"uid=grace,ou=people,"+savedSearchMoved,
		"ou=people,"+savedSearchMoved,
		savedSearchMoved,
		"uid=ada,ou=people,"+savedSearchBase,
		"uid=margaret,ou=people,"+savedSearchBase,
		"ou=people,"+savedSearchBase,
		savedSearchBase)
}

func seedSavedSearch(t *testing.T, sess directory.Session) {
	t.Helper()
	clearSavedSearch(t, sess)
	t.Cleanup(func() { clearSavedSearch(t, sess) })
	ou := func(d, name string) directory.ChangeRecord {
		return directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte(name)}},
		}}
	}
	mustApply(t, sess, ou(savedSearchBase, "alder-savedsearch"))
	mustApply(t, sess, ou("ou=people,"+savedSearchBase, "people"))
	mustApply(t, sess, person(t, "uid=ada,ou=people,"+savedSearchBase, "ada",
		[2]string{"departmentNumber", "engineering"}, [2]string{"title", "Staff Engineer"}, [2]string{"mail", "ada@example.com"}))
	mustApply(t, sess, person(t, "uid=margaret,ou=people,"+savedSearchBase, "margaret",
		[2]string{"departmentNumber", "operations"}))
	mustApply(t, sess, ou(savedSearchMoved, "moved"))
	mustApply(t, sess, ou("ou=people,"+savedSearchMoved, "people"))
	mustApply(t, sess, person(t, "uid=grace,ou=people,"+savedSearchMoved, "grace",
		[2]string{"departmentNumber", "engineering"}))
}

// lowerDNs is the entries' DNs, lowered and sorted: the two servers spell a
// DN as they stored it, and an order is not what is being compared.
func lowerDNs(t *testing.T, entries []any) []string {
	t.Helper()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.ToLower(e.(map[string]any)["dn"].(string)))
	}
	sort.Strings(out)
	return out
}

func TestASavedSearchRunsAsItsLinkDoesOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		seedSavedSearch(t, sess)
		dir := writeExampleProjectUnder(t, s, base, savedSearchBase)
		// A suffix, and an environment that moves it.
		yamlPath := filepath.Join(dir, "alder.yaml")
		raw, err := os.ReadFile(yamlPath)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Replace(string(raw), "version: 1\n", "version: 1\nsuffix: "+savedSearchBase+"\n", 1)
		harness := text[strings.Index(text, "  harness:\n"):]
		text += strings.Replace(harness, "  harness:\n", "  moved:\n", 1) + "    suffix: " + savedSearchMoved + "\n"
		writeFile(t, dir, "alder.yaml", text)

		// Listed offline.
		r := runProject(t, s, "project", "search", "--project", dir, "--json")
		wantExit(t, r, cli.ExitOK, "alder project search --json")
		var listed []map[string]any
		if err := json.Unmarshal([]byte(r.stdout), &listed); err != nil || len(listed) != 2 {
			t.Fatalf("listing: %v\n%s", err, r.stdout)
		}
		engineers := listed[1]
		if engineers["name"] != "people/engineers" {
			t.Fatalf("listing = %v", listed)
		}

		// The web interface gets the same search back from Open project.
		res := post(t, client, base+"/project/plan", browserFiles(t, dir))
		if res.status != http.StatusOK {
			t.Fatalf("POST /project/plan: status %d\n%s", res.status, res.body)
		}
		searches, _ := decodeMap(t, res.body)["searches"].([]any)
		if len(searches) != 2 {
			t.Fatalf("searches = %v", decodeMap(t, res.body)["searches"])
		}
		fromWeb := searches[1].(map[string]any)
		for _, key := range []string{"name", "base", "scope", "filter", "limit"} {
			if fromWeb[key] != engineers[key] {
				t.Errorf("%s: web %v, client %v", key, fromWeb[key], engineers[key])
			}
		}

		// Run by the client, and by the link the web interface opens: the
		// same entries.
		r = runProject(t, s, "project", "search", "--project", dir, "people/engineers", "--env", "harness", "--json")
		wantExit(t, r, cli.ExitOK, "alder project search people/engineers --json")
		var fromCLI []any
		if err := json.Unmarshal([]byte(r.stdout), &fromCLI); err != nil {
			t.Fatalf("%v\n%s", err, r.stdout)
		}
		link, err := json.Marshal(map[string]any{
			"baseDn": fromWeb["base"], "scope": fromWeb["scope"], "filter": fromWeb["filter"],
			"limit": fromWeb["limit"], "pageSize": 200, "attributes": fromWeb["attributes"],
		})
		if err != nil {
			t.Fatal(err)
		}
		res = post(t, client, base+"/search", string(link))
		if res.status != http.StatusOK {
			t.Fatalf("POST /search: status %d\n%s", res.status, res.body)
		}
		fromLink, _ := decodeMap(t, res.body)["entries"].([]any)
		want := []string{strings.ToLower("uid=ada,ou=people," + savedSearchBase)}
		if got := lowerDNs(t, fromCLI); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("alder project search found %v, want %v", got, want)
		}
		if got := lowerDNs(t, fromLink); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("the link found %v, want %v", got, want)
		}

		// The LDIF is the export's, byte for byte.
		r = runProject(t, s, "project", "search", "--project", dir, "people/engineers", "--env", "harness")
		wantExit(t, r, cli.ExitOK, "alder project search people/engineers")
		q := url.Values{}
		q.Set("dn", "ou=people,"+savedSearchBase)
		q.Set("scope", "one")
		q.Set("filter", "(&(objectClass=inetOrgPerson)(departmentNumber=engineering))")
		q.Set("limit", "500")
		q.Set("attributes", "uid,cn,mail,title")
		export := get(t, client, base+"/export/ldif?"+q.Encode())
		if export.status != http.StatusOK {
			t.Fatalf("GET /export/ldif: status %d\n%s", export.status, export.body)
		}
		if r.stdout != export.body {
			t.Errorf("the search's LDIF is not the export's\n--- alder project search\n%s\n--- GET /export/ldif\n%s", r.stdout, export.body)
		}
		lower := strings.ToLower(r.stdout)
		if !strings.Contains(lower, "title: staff engineer") || strings.Contains(lower, "departmentnumber:") ||
			strings.Contains(lower, "margaret") {
			t.Errorf("the LDIF holds other than the attributes asked for, or other entries:\n%s", r.stdout)
		}

		// An environment with its own suffix runs the moved base.
		r = runProject(t, s, "project", "search", "--project", dir, "people/engineers", "--env", "moved", "--json")
		wantExit(t, r, cli.ExitOK, "alder project search people/engineers --env moved")
		var moved []any
		if err := json.Unmarshal([]byte(r.stdout), &moved); err != nil {
			t.Fatalf("%v\n%s", err, r.stdout)
		}
		if got := lowerDNs(t, moved); strings.Join(got, "|") != strings.ToLower("uid=grace,ou=people,"+savedSearchMoved) {
			t.Errorf("--env moved found %v", got)
		}
		if !strings.Contains(r.stderr, "the base is moved from "+savedSearchBase) {
			t.Errorf("stderr:\n%s", r.stderr)
		}
	})
}
