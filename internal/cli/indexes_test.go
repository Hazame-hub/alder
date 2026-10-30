package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.41: the index advisor on the command line, which contacts nothing.

const adviceLog = `[30/Oct/2026:14:02:11.200000000 +0000] conn=12 op=1 SRCH base="ou=people,dc=example,dc=test" scope=2 filter="(title=Engineer)" attrs=ALL
[30/Oct/2026:14:02:11.700000000 +0000] conn=12 op=1 RESULT err=0 tag=101 nentries=3 wtime=0.0001 optime=0.5 etime=0.5001 notes=A
[30/Oct/2026:14:02:12.000000000 +0000] conn=12 op=2 SRCH base="cn=config" scope=2 filter="(cn=x)" attrs=ALL
[30/Oct/2026:14:02:12.900000000 +0000] conn=12 op=2 RESULT err=0 tag=101 nentries=0 wtime=0.0001 optime=0.9 etime=0.9 notes=A
`

func TestTheAdvisorReadsALogAgainstASnapshotWithNothingContacted(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte(configSnapshotWithIndexes(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "access")
	if err := os.WriteFile(log, []byte(adviceLog), 0o600); err != nil {
		t.Fatal(err)
	}

	r := newStub(t).run(t, runOpts{noConnection: true}, "indexes", "advise", "--log", log, "--config", config)
	// One search under cn=config, which no backend in the snapshot serves.
	expectCode(t, r, ExitIncomplete)
	if !strings.Contains(r.stdout, "title eq on dc=example,dc=test  can be added") ||
		!strings.Contains(r.stdout, "e.g. (title=…)") || strings.Contains(r.stdout, "Engineer") {
		t.Errorf("stdout:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "Adding an index is not building it") {
		t.Errorf("no caveat:\n%s", r.stdout)
	}

	only := filepath.Join(dir, "only")
	if err := os.WriteFile(only, []byte(strings.Join(strings.Split(adviceLog, "\n")[:2], "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = newStub(t).run(t, runOpts{noConnection: true, stdin: adviceLog[:strings.Index(adviceLog, "[30/Oct/2026:14:02:12.000")]},
		"indexes", "advise", "--log", "-", "--config", config, "--json")
	expectCode(t, r, ExitDifferences)
	var out struct {
		Format      string `json:"format"`
		Suggestions []struct {
			Attribute string   `json:"attribute"`
			Status    string   `json:"status"`
			Examples  []string `json:"examples"`
		} `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.Format != "389-access" ||
		len(out.Suggestions) != 1 || out.Suggestions[0].Attribute != "title" || out.Suggestions[0].Status != "candidate" {
		t.Errorf("json: %v\n%s", err, r.stdout)
	}

	r = newStub(t).run(t, runOpts{noConnection: true}, "indexes", "advise", "--log", only, "--config", config, "--slower-than", "1s")
	expectCode(t, r, ExitDifferences) // notes=A counts whatever the threshold
}

func TestTheAdvisorNeedsBothInputs(t *testing.T) {
	r := newStub(t).run(t, runOpts{noConnection: true}, "indexes", "advise", "--log", "x")
	expectCode(t, r, ExitUsage)
	r = newStub(t).run(t, runOpts{noConnection: true}, "indexes", "advise", "--config", "x")
	expectCode(t, r, ExitUsage)
}

// configSnapshotWithIndexes is a real configuration snapshot of one OpenLDAP
// database indexing uid, built the way a capture builds one.
func configSnapshotWithIndexes(t *testing.T) string {
	t.Helper()
	snap, err := snapshot.BuildConfig(snapshot.ConfigCapture{Provider: "openldap", Root: "cn=config", CreatedAt: time.Now()},
		[]snapshot.ConfigResource{
			{Section: "backend", Kind: "database", Name: "dc=example,dc=test", DN: "olcDatabase={1}mdb,cn=config", Label: "{1}mdb"},
			{Section: "performance", Kind: "index", Name: "dc=example,dc=test/uid", DN: "olcDatabase={1}mdb,cn=config", Label: "uid"},
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := snapshot.EncodeConfig(&b, snap); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
