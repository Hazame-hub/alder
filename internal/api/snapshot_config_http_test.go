package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.13: configuration snapshots and configuration comparisons over HTTP.

const harnessRootPW = "alder-admin"

func configEntry(t *testing.T, text string, attrs ...string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, text))
	for i := 0; i+1 < len(attrs); i += 2 {
		e.Set(attrs[i], append(e.Get(attrs[i]), []byte(attrs[i+1])))
	}
	return e
}

// configRig is a session on an OpenLDAP-shaped server whose configuration tree
// can be read.
func configRig(t *testing.T, idleTimeout string) *testRig {
	t.Helper()
	caps := defaultCaps()
	caps.VendorName = "OpenLDAP"
	caps.VendorVersion = "2.6.7"
	caps.ConfigContext = "cn=config"
	caps.Config = directory.ConfigAccess{DN: "cn=config", Readable: true}
	entries := []*directory.Entry{
		configEntry(t, "cn=config",
			"objectClass", "olcGlobal", "cn", "config",
			"olcIdleTimeout", idleTimeout,
			"olcArgsFile", "/run/slapd/slapd.args",
			"olcLogLevel", "stats"),
		configEntry(t, "cn=schema,cn=config", "objectClass", "olcSchemaConfig", "cn", "schema"),
		configEntry(t, "cn={0}core,cn=schema,cn=config", "objectClass", "olcSchemaConfig", "cn", "{0}core",
			"olcAttributeTypes", "( 2.5.4.3 NAME 'cn' SUP name )"),
		configEntry(t, "olcDatabase={1}mdb,cn=config",
			"objectClass", "olcMdbConfig", "olcDatabase", "{1}mdb",
			"olcSuffix", "dc=alder,dc=test",
			"olcRootDN", "cn=admin,dc=alder,dc=test",
			"olcRootPW", harnessRootPW,
			"olcDbMaxSize", "1073741824"),
	}
	byDN := map[string]*directory.Entry{}
	for _, e := range entries {
		byDN[strings.ToLower(e.DN.String())] = e
	}
	return newRig(t, Config{}, &fakeSession{caps: caps, entries: entries, byDN: byDN})
}

func captureConfig(t *testing.T, rig *testRig) string {
	t.Helper()
	res := post(t, rig, "/api/v1/snapshots/capture", `{"kind":"config"}`)
	if res.Status != http.StatusOK {
		t.Fatalf("capture: %d %s", res.Status, res.Body)
	}
	return res.Body
}

func TestAConfigCaptureIsTheSameDocumentTwiceAndHoldsNoSecret(t *testing.T) {
	rig := configRig(t, "0")
	first := captureConfig(t, rig)
	time.Sleep(1100 * time.Millisecond)
	second := captureConfig(t, rig)

	withoutTime := func(doc string) (map[string]any, string) {
		var m map[string]any
		if err := json.Unmarshal([]byte(doc), &m); err != nil {
			t.Fatalf("%v: %s", err, doc)
		}
		delete(m, "createdAt")
		out, _ := json.Marshal(m)
		return m, string(out)
	}
	head, a := withoutTime(first)
	_, b := withoutTime(second)
	if a != b {
		t.Errorf("two captures of one configuration differ beyond createdAt:\n%s\n%s", a, b)
	}
	if head["kind"] != "config" || head["checksum"] == "" {
		t.Errorf("document head: kind %v checksum %v", head["kind"], head["checksum"])
	}
	source, _ := head["source"].(map[string]any)
	if source["provider"] != "openldap" {
		t.Errorf("provider: %v", source["provider"])
	}
	if strings.Contains(first, harnessRootPW) {
		t.Error("the document holds the server's root password")
	}
	if strings.Contains(first, "olcAttributeTypes") {
		t.Error("the document holds schema definitions, which are a schema snapshot's subject")
	}
	counts, _ := head["counts"].(map[string]any)
	if counts["withheld"].(float64) < 1 {
		t.Errorf("nothing was withheld, and the configuration holds a password: %v", counts)
	}
}

func TestAConfigCaptureNamesItselfInTheFilename(t *testing.T) {
	rig := configRig(t, "0")
	res := post(t, rig, "/api/v1/snapshots/capture", `{"kind":"config"}`)
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, "alder-config-snapshot-") {
		t.Errorf("filename: %q", got)
	}
}

func TestAConfigCaptureNeedsAConfigurationTreeItCanRead(t *testing.T) {
	// A server that announces no configuration tree, or a bind that cannot
	// read it, is told so rather than handed an empty configuration.
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps()})
	res := post(t, rig, "/api/v1/snapshots/capture", `{"kind":"config"}`)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("capture: %d %s", res.Status, res.Body)
	}
	if got := errorCode(t, res); got != ErrorErrorConfigModelUnavailable {
		t.Fatalf("code %s, want config_model_unavailable", got)
	}
}

func TestAConfigCaptureTakesNoSubtreeOptions(t *testing.T) {
	rig := configRig(t, "0")
	for _, body := range []string{
		`{"kind":"config","base":"dc=alder,dc=test"}`,
		`{"kind":"config","scope":"one"}`,
		`{"kind":"config","filter":"(objectClass=*)"}`,
		`{"kind":"config","operationalAttributes":true}`,
	} {
		res := post(t, rig, "/api/v1/snapshots/capture", body)
		if res.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d %s", body, res.Status, res.Body)
		}
	}
}

func TestAConfigDiffSeesOneChangedSettingAndOffersIt(t *testing.T) {
	baseline := captureConfig(t, configRig(t, "0"))

	// The same server with one setting changed, compared with the document.
	changed := configRig(t, "1800")
	res := post(t, changed, "/api/v1/diff",
		`{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+baseline+`}}`)
	if res.Status != http.StatusOK {
		t.Fatalf("diff: %d %s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	if d.Config == nil {
		t.Fatal("the comparison has no configuration")
	}
	if d.Config.ProviderMismatch {
		t.Fatal("one server was compared with itself and reported a provider mismatch")
	}
	if d.Config.Counts.Modified != 1 {
		t.Fatalf("modified is %d, want 1: %+v", d.Config.Counts.Modified, itemKeys(d.Config.Items))
	}
	item := d.Config.Items[0]
	if !strings.EqualFold(item.Key, "olcIdleTimeout") {
		t.Fatalf("the difference is about %q", item.Key)
	}
	if item.Actionable != ConfigActionableWritable || item.Candidate == nil || len(item.Candidate.Changes) != 1 {
		t.Fatalf("no change was offered for a setting Alder changes: %+v", item)
	}
	change := item.Candidate.Changes[0]
	if change.Dn != "cn=config" || change.Type != ChangeRequestTypeModify {
		t.Fatalf("the change is %v on %q", change.Type, change.Dn)
	}
	if strings.Contains(res.Body, harnessRootPW) {
		t.Error("the comparison holds the server's root password")
	}
}

func TestAReadOnlyConfigSettingIsReportedAndNeverOffered(t *testing.T) {
	baseline := captureConfig(t, configRig(t, "0"))
	// olcArgsFile is the server's own; changing the document does not make it
	// something Alder writes.
	edited := strings.Replace(baseline, "/run/slapd/slapd.args", "/var/run/slapd/slapd.args", 1)
	if edited == baseline {
		t.Fatal("the document did not hold the args file")
	}
	// The checksum no longer matches, which is itself the right answer: a
	// document edited after capture is refused.
	res := post(t, configRig(t, "0"), "/api/v1/diff",
		`{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+edited+`}}`)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("an edited document was accepted: %d %s", res.Status, res.Body)
	}
	if got := errorCode(t, res); got != ErrorErrorSnapshotChecksumMismatch {
		t.Fatalf("code %s, want a checksum mismatch", got)
	}
}

func TestTwoProvidersConfigurationIsNotComparedOverHTTP(t *testing.T) {
	openldap := captureConfig(t, configRig(t, "0"))
	other := ds389Document(t)

	res := post(t, configRig(t, "0"), "/api/v1/diff",
		`{"source":{"snapshot":`+openldap+`},"target":{"snapshot":`+other+`}}`)
	if res.Status != http.StatusOK {
		t.Fatalf("diff: %d %s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	if d.Config == nil || !d.Config.ProviderMismatch {
		t.Fatalf("two providers were compared: %+v", d.Config)
	}
	if len(d.Config.Items) != 0 {
		t.Fatalf("a cross-provider comparison listed %d differences", len(d.Config.Items))
	}
	if d.Config.Counts.Added != 0 || d.Config.Counts.Removed != 0 {
		t.Fatal("a cross-provider comparison invented additions and removals")
	}
	if d.Complete {
		t.Fatal("a comparison that compared nothing claimed to be complete")
	}
	if d.Config.Source.Provider != ConfigProviderOpenLDAP || d.Config.Target.Provider != ConfigProvider389DS {
		t.Fatalf("the sides are %v and %v", d.Config.Source.Provider, d.Config.Target.Provider)
	}
}

func TestTwoConfigurationSnapshotsAreComparedWithoutADirectory(t *testing.T) {
	doc := captureConfig(t, configRig(t, "0"))
	app, driver := directorylessServer(t)
	res := postDiffWithoutSession(t, app, `{"source":{"snapshot":`+doc+`},"target":{"snapshot":`+doc+`}}`)
	if res.Status != http.StatusOK {
		t.Fatalf("diff: %d %s", res.Status, res.Body)
	}
	if n := driver.connects.Load(); n != 0 {
		t.Fatalf("comparing two files opened %d connections", n)
	}
	d := decode[Diff](t, res)
	if d.Config == nil || len(d.Config.Items) != 0 {
		t.Fatalf("a configuration differs from itself: %+v", d.Config)
	}
}

func TestAConfigurationIsOnlyComparedWithAConfiguration(t *testing.T) {
	rig := configRig(t, "0")
	doc := captureConfig(t, rig)
	schemaDoc := schemaDocument(t, "389 Project", baseSchemaAttrs(nil, nil))
	res := post(t, rig, "/api/v1/diff",
		`{"source":{"snapshot":`+doc+`},"target":{"snapshot":`+schemaDoc+`}}`)
	if res.Status != http.StatusBadRequest {
		t.Fatalf("a configuration was compared with a schema: %d %s", res.Status, res.Body)
	}
	if !strings.Contains(res.Body, "kind") {
		t.Errorf("the refusal does not say why: %s", res.Body)
	}
}

// ds389Document is a 389 DS configuration snapshot, captured from a fake of
// that server so the document is one the code actually produces.
func ds389Document(t *testing.T) string {
	t.Helper()
	caps := defaultCaps()
	caps.VendorName = "389 Project"
	caps.ConfigContext = "cn=config"
	caps.Config = directory.ConfigAccess{DN: "cn=config", Readable: true}
	entries := []*directory.Entry{
		configEntry(t, "cn=config", "objectClass", "nsslapdConfig", "cn", "config",
			"nsslapd-port", "389", "nsslapd-idletimeout", "0",
			"nsslapd-rootpw", "{PBKDF2-SHA512}10000$abc$def"),
		configEntry(t, "cn=userRoot,cn=ldbm database,cn=plugins,cn=config",
			"objectClass", "nsBackendInstance", "cn", "userRoot",
			"nsslapd-suffix", "dc=alder,dc=test", "nsslapd-cachememsize", "536870912"),
	}
	fake := &fakeSession{caps: caps, entries: entries}
	s, err := config.Capture(t.Context(), fake, config.Options{
		Now: func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("capture 389 DS configuration: %v", err)
	}
	var buf bytes.Buffer
	if err := snapshot.EncodeConfig(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func itemKeys(items []ConfigDiffItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Id)
	}
	return out
}
