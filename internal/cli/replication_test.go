package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 1.41: one server's replication record as a document, and two documents
// compared with nothing contacted.

func replicationReportJSON(csn, at string) string {
	return `{"provider":"openldap","role":"both","why":"w","disclaimer":"d","suffixes":[{"dn":"dc=alder,dc=test","role":"both",
	  "cursors":[{"origin":"1","at":"` + at + `","raw":"` + csn + `","csn":"` + csn + `"}],"links":[]}]}`
}

func writeReplicationDoc(t *testing.T, s *stub, path, report string) {
	t.Helper()
	s.reply("GET /api/v1/replication", http.StatusOK, report)
	r := s.run(t, runOpts{}, "replication", "--output", path)
	expectCode(t, r, ExitOK)
}

func TestReplicationIsWrittenAsADocumentAndComparedOffline(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	newer := replicationReportJSON("20261030140211.000000z#000000#001#000000", "2026-10-30T14:02:11Z")
	older := replicationReportJSON("20261030140131.000000z#000000#001#000000", "2026-10-30T14:01:31Z")
	writeReplicationDoc(t, newStub(t), a, newer)
	writeReplicationDoc(t, newStub(t), b, older)

	raw, err := os.ReadFile(a)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || doc["format"] != "alder-replication" || doc["version"] != float64(1) {
		t.Fatalf("not an alder-replication document: %v\n%s", err, raw)
	}

	// No connection flags: the comparison asks nobody.
	r := newStub(t).run(t, runOpts{noConnection: true}, "replication", "compare", a, b)
	expectCode(t, r, ExitDifferences)
	if !strings.Contains(r.stdout, "B lacks its changes made in the 40s before") {
		t.Errorf("stdout:\n%s", r.stdout)
	}

	r = newStub(t).run(t, runOpts{noConnection: true}, "replication", "compare", "--tolerate", "1m", a, b)
	expectCode(t, r, ExitOK)
	r = newStub(t).run(t, runOpts{noConnection: true}, "replication", "compare", a, a)
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stdout, "a link that stopped working looks the same") {
		t.Errorf("in step without its caveat:\n%s", r.stdout)
	}

	r = newStub(t).run(t, runOpts{noConnection: true}, "replication", "compare", "--json", a, b)
	expectCode(t, r, ExitDifferences)
	var out struct {
		Verdict  string `json:"verdict"`
		Suffixes []struct {
			Origins []struct {
				SpanSeconds float64 `json:"spanSeconds"`
			} `json:"origins"`
		} `json:"suffixes"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.Verdict != "b_behind" ||
		out.Suffixes[0].Origins[0].SpanSeconds != 40 {
		t.Errorf("json: %v\n%s", err, r.stdout)
	}
}

func TestAReplicationDocumentIsReadStrictly(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	writeReplicationDoc(t, newStub(t), good, replicationReportJSON("20261030140211.000000z#000000#001#000000", "2026-10-30T14:02:11Z"))
	for name, body := range map[string]string{
		"snapshot":      `{"format":"alder-snapshot","version":1}`,
		"future":        `{"format":"alder-replication","version":2,"server":"x","readAt":"2026-10-30T14:02:11Z","report":{"role":"none","why":"","disclaimer":"","suffixes":[]}}`,
		"unknown field": `{"format":"alder-replication","version":1,"extra":true}`,
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		r := newStub(t).run(t, runOpts{noConnection: true}, "replication", "compare", good, path)
		expectCode(t, r, ExitFailed)
	}
	r := newStub(t).run(t, runOpts{noConnection: true}, "replication", "compare", "-", "-")
	expectCode(t, r, ExitUsage)
}
