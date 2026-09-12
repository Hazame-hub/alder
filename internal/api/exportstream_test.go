package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
)

// bulkExportEntries makes n plain entries for the export to render.
func bulkExportEntries(t *testing.T, n int) []*directory.Entry {
	t.Helper()
	out := make([]*directory.Entry, 0, n)
	for i := 0; i < n; i++ {
		e := directory.NewEntry(mustParse(t,
			fmt.Sprintf("uid=bulk%05d,ou=people,dc=alder,dc=test", i)))
		e.Set("objectClass", [][]byte{[]byte("top"), []byte("inetOrgPerson")})
		e.Set("uid", [][]byte{[]byte(fmt.Sprintf("bulk%05d", i))})
		e.Set("sn", [][]byte{[]byte("Bulk")})
		out = append(out, e)
	}
	return out
}

func exportLdif(t *testing.T, rig *testRig, query string) string {
	t.Helper()
	res := rig.do(t, http.MethodGet, "/api/v1/export/ldif?dn=dc%3Dalder%2Cdc%3Dtest"+query, nil)
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d: %s", res.Status, res.Body)
	}
	return res.Body
}

// The count moves to the end because a streamed export does not know it at the
// start — and a file that ends with its own summary is one you can tell arrived
// whole, where a count at the top of a download that died halfway cannot be
// told from an honest one.
func TestTheExportPutsItsCountAtTheEnd(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 30), pageSize: 10,
	})

	doc := exportLdif(t, rig, "&scope=sub&limit=1000")

	head, tail, found := strings.Cut(doc, "version: 1")
	if !found {
		t.Fatalf("no version header:\n%s", doc)
	}
	if strings.Contains(head, "30 entries") {
		t.Error("the count is in the header, which a streamed export cannot know")
	}
	if !strings.Contains(tail, "# 30 entries") {
		t.Errorf("the count is not at the end:\n%s", lastLines(tail, 6))
	}
	if !strings.Contains(tail, "This export is complete.") {
		t.Errorf("nothing says the export finished:\n%s", lastLines(tail, 6))
	}
}

// Every page is rendered, not just the first. A fake that returned everything
// at once would make a loop that runs once look right.
func TestTheExportStreamsEveryPage(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 250), pageSize: 25,
	})

	doc := exportLdif(t, rig, "&scope=sub&limit=1000")

	if got := strings.Count(doc, "\ndn: uid=bulk"); got != 250 {
		t.Errorf("%d entries in the document, want 250", got)
	}
	if !strings.Contains(doc, "# 250 entries") {
		t.Error("the footer count does not match what was streamed")
	}
	if rig.fake.searchCount() < 2 {
		t.Errorf("%d searches for 10 pages: the export is not paging", rig.fake.searchCount())
	}
}

// A truncated export that does not say so is a file somebody restores from and
// discovers the gap in much later.
func TestATruncatedExportSaysSoAtTheEnd(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 500), pageSize: 25,
	})

	doc := exportLdif(t, rig, "&scope=sub&limit=100")

	if got := strings.Count(doc, "\ndn: uid=bulk"); got != 100 {
		t.Errorf("%d entries, want the 100 asked for", got)
	}
	if !strings.Contains(doc, "WARNING: the result was truncated") {
		t.Errorf("no truncation warning:\n%s", lastLines(doc, 6))
	}
	if strings.Contains(doc, "This export is complete.") {
		t.Error("a truncated export claims to be complete")
	}
}

// Rule 6 survives the rewrite: a password is not in an export that did not ask
// for one.
func TestTheStreamedExportStillOmitsSecrets(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: []*directory.Entry{entryFixture(t)},
	})

	doc := exportLdif(t, rig, "&scope=base&limit=10")

	if strings.Contains(doc, "averyrealsecret") {
		t.Error("the export carries a password")
	}
	if !strings.Contains(doc, "Sensitive attributes such as userPassword were omitted") {
		t.Error("the export does not say it left anything out")
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// --- the outline ------------------------------------------------------------

// Asked for by an operator: an LDIF export is flat, and a flat list of three
// hundred records does not tell you the shape of what you exported.
func TestTheOutlineDrawsTheTree(t *testing.T) {
	entries := []*directory.Entry{}
	for _, d := range []string{
		"dc=alder,dc=test",
		"ou=people,dc=alder,dc=test",
		"uid=alice,ou=people,dc=alder,dc=test",
		"uid=bob,ou=people,dc=alder,dc=test",
	} {
		e := directory.NewEntry(mustParse(t, d))
		e.Set("objectClass", [][]byte{[]byte("top"), []byte("organizationalUnit")})
		entries = append(entries, e)
	}
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t), entries: entries,
	})

	res := rig.do(t, http.MethodGet,
		"/api/v1/export/outline?dn=dc%3Dalder%2Cdc%3Dtest&scope=sub", nil)
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d: %s", res.Status, res.Body)
	}

	if !strings.Contains(res.Body, "├── ") && !strings.Contains(res.Body, "└── ") {
		t.Errorf("nothing is drawn as a tree:\n%s", res.Body)
	}
	if !strings.Contains(res.Body, "uid=alice") {
		t.Errorf("an entry is missing from the outline:\n%s", res.Body)
	}
}

// It must not be mistakeable for an export you can apply. LDIF reads a leading
// space as a continuation, so an indented tree could never also be LDIF, and a
// file that looks like one and is not is worse than no file.
func TestTheOutlineSaysItIsNotSomethingYouApply(t *testing.T) {
	e := directory.NewEntry(mustParse(t, "dc=alder,dc=test"))
	e.Set("objectClass", [][]byte{[]byte("top")})
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t), entries: []*directory.Entry{e},
	})

	res := rig.do(t, http.MethodGet, "/api/v1/export/outline?dn=dc%3Dalder%2Cdc%3Dtest", nil)
	if !strings.Contains(res.Body, "not LDIF") {
		t.Errorf("the outline does not say what it is not:\n%s", res.Body)
	}
	if !strings.Contains(res.Header.Get("Content-Disposition"), "-outline.txt") {
		t.Errorf("the download is not named as an outline: %s",
			res.Header.Get("Content-Disposition"))
	}
}

// An outline carries no attribute values, so there is nothing in it to leak.
func TestTheOutlineCarriesNoAttributeValues(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: []*directory.Entry{entryFixture(t)},
	})

	res := rig.do(t, http.MethodGet,
		"/api/v1/export/outline?dn=dc%3Dalder%2Cdc%3Dtest&scope=sub", nil)

	for _, secret := range []string{"averyrealsecret", "alice@alder.test", "Liddell"} {
		if strings.Contains(res.Body, secret) {
			t.Errorf("the outline carries the value %q:\n%s", secret, res.Body)
		}
	}
}

// --- the YAML export --------------------------------------------------------

// Rule 6 on a new surface. A YAML file is destined for an editor, and a file
// destined for an editor is destined for a repository soon after.
func TestTheYamlExportOmitsSecretsUnlessAsked(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: []*directory.Entry{entryFixture(t)},
	})

	res := rig.do(t, http.MethodGet,
		"/api/v1/export/yaml?dn=dc%3Dalder%2Cdc%3Dtest&scope=sub", nil)
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d: %s", res.Status, res.Body)
	}
	if strings.Contains(res.Body, "averyrealsecret") {
		t.Error("the YAML carries a password")
	}

	asked := rig.do(t, http.MethodGet,
		"/api/v1/export/yaml?dn=dc%3Dalder%2Cdc%3Dtest&scope=sub&includeSensitive=true", nil)
	if !strings.Contains(asked.Body, "averyrealsecret") {
		t.Error("includeSensitive did not include it, so the option does nothing")
	}
}

// The nesting is the whole point: an editor folds it.
func TestTheYamlExportNestsChildrenUnderParents(t *testing.T) {
	entries := []*directory.Entry{}
	for _, d := range []string{
		"dc=alder,dc=test",
		"ou=people,dc=alder,dc=test",
		"uid=alice,ou=people,dc=alder,dc=test",
	} {
		e := directory.NewEntry(mustParse(t, d))
		e.Set("objectClass", [][]byte{[]byte("top")})
		e.Set("description", [][]byte{[]byte("a value")})
		entries = append(entries, e)
	}
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t), entries: entries,
	})

	res := rig.do(t, http.MethodGet,
		"/api/v1/export/yaml?dn=dc%3Dalder%2Cdc%3Dtest&scope=sub", nil)

	if !strings.Contains(res.Body, "children:") {
		t.Errorf("nothing is nested:\n%s", res.Body)
	}
	if !strings.Contains(res.Body, "nothing reads this back") {
		t.Errorf("the YAML does not say it is for reading:\n%s", res.Body)
	}
	if !strings.Contains(res.Header.Get("Content-Disposition"), ".yaml") {
		t.Errorf("the download is not named as YAML: %s", res.Header.Get("Content-Disposition"))
	}
}
