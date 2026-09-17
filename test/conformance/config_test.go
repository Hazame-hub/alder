//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.13: configuration snapshots and comparisons against both servers.
//
// Configuration is provider-specific, so almost everything here is asserted
// per server against that server's own model. The one cross-server case is the
// one that matters most: two providers' configuration is not compared setting
// by setting, and Alder says so instead of inventing hundreds of differences.

func captureConfig(t *testing.T, client *http.Client, base string) string {
	t.Helper()
	res := post(t, client, base+"/snapshots/capture", `{"kind":"config"}`)
	if res.status != http.StatusOK {
		t.Fatalf("capturing configuration: status %d\n%s", res.status, res.body)
	}
	return res.body
}

func configDiff(t *testing.T, client *http.Client, base, body string) api.Diff {
	t.Helper()
	d := diffOverHTTP(t, client, base, body)
	if d.Kind != api.StateKindConfig || d.Config == nil {
		t.Fatalf("the comparison is %s, not a configuration comparison", d.Kind)
	}
	return d
}

func configItem(t *testing.T, d api.Diff, key string) api.ConfigDiffItem {
	t.Helper()
	var found []api.ConfigDiffItem
	for _, item := range d.Config.Items {
		if strings.EqualFold(item.Key, key) {
			found = append(found, item)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one item for %s, got %d: %s", key, len(found), mustEncode(t, d.Config.Items))
	}
	return found[0]
}

// A configuration capture is the same document twice, and holds no secret.
func TestConfigSnapshotIsTheSameDocumentTwice(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skipf("the configuration tree is not readable: %s", sess.Capabilities().Config.Reason)
		}
		client, base := alderSession(t, s, true)
		first, second := captureConfig(t, client, base), captureConfig(t, client, base)

		a, _, err := snapshot.DecodeConfig([]byte(first))
		if err != nil {
			t.Fatalf("the capture does not decode: %v", err)
		}
		b, _, err := snapshot.DecodeConfig([]byte(second))
		if err != nil {
			t.Fatal(err)
		}
		if a.Checksum != b.Checksum {
			t.Errorf("two captures of an unchanged configuration have different checksums:\n%s\n%s", a.Checksum, b.Checksum)
		}
		if a.Source.Provider == "" || a.Completeness != snapshot.ConfigComplete {
			t.Errorf("provider %q completeness %q", a.Source.Provider, a.Completeness)
		}
		if a.Counts.Settings == 0 || a.Counts.Resources == 0 {
			t.Fatalf("the capture is empty: %+v", a.Counts)
		}
		t.Logf("%s: provider %s, %d resources, %d settings, %d withheld, %d read-only, %d operational, %d bytes",
			s.name, a.Source.Provider, a.Counts.Resources, a.Counts.Settings, a.Counts.Withheld,
			a.Counts.ReadOnly, a.Counts.Operational, len(first))

		// Every secret this server keeps in its configuration, by the value it
		// actually holds. None of them may be in the document.
		for _, secret := range []string{s.bindPW, s.schemaBindPW, s.restrictedPW} {
			if secret == "" {
				continue
			}
			if strings.Contains(first, secret) {
				t.Fatalf("the configuration snapshot carries a password this server holds in its configuration")
			}
		}
		withheld := 0
		for _, setting := range a.Settings {
			if setting.Sensitive {
				withheld++
				if len(setting.Values) != 0 {
					t.Errorf("%s is sensitive and carries values", setting.ID())
				}
			}
			// The schema has its own snapshot kind, and a monitor entry is not
			// configuration.
			if strings.Contains(strings.ToLower(setting.DN), "cn=schema,cn=config") ||
				strings.Contains(strings.ToLower(setting.DN), "cn=monitor") {
				t.Errorf("%s came from %s, which a configuration snapshot does not capture", setting.ID(), setting.DN)
			}
		}
		if withheld == 0 {
			t.Errorf("%s holds no secret at all in its configuration, which is unexpected", s.name)
		}
	})
}

// One setting changed on the server, and exactly one difference reported --
// then put back through the ordinary plan, because that setting is one Alder
// already knows how to change.
func TestConfigDiffSeesOneChangedSetting(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.configWriteDN == "" {
			t.Skip("no readable configuration, or no setting nominated for this server")
		}
		client, base := alderSession(t, s, true)
		target := mustDN(t, s.configWriteDN)

		read := func() []string {
			entry, err := sess.Read(ctx(t), target, []string{s.configWriteAttr})
			if err != nil {
				t.Fatalf("reading %s: %v", s.configWriteDN, err)
			}
			return entry.GetStrings(s.configWriteAttr)
		}
		set := func(values []string) error {
			vals := make([][]byte, 0, len(values))
			for _, v := range values {
				vals = append(vals, []byte(v))
			}
			return sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.configWriteAttr, Values: vals}}})
		}

		before := read()
		if len(before) == 0 {
			t.Fatalf("%s holds no %s", s.configWriteDN, s.configWriteAttr)
		}
		t.Cleanup(func() { _ = set(before) })

		baseline := captureConfig(t, client, base)

		// Nothing changed yet: the capture compares equal with itself and with
		// the live server.
		same := configDiff(t, client, base, `{"source":{"snapshot":`+baseline+`},"target":{"snapshot":`+baseline+`}}`)
		if same.Config.Counts.Compared == 0 || same.Config.Counts.Modified != 0 || len(same.Config.Items) != 0 {
			t.Fatalf("a capture differs from itself: %+v", same.Config.Counts)
		}
		live := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+baseline+`}}`)
		if live.Config.Counts.Modified != 0 || live.Config.Counts.Added != 0 || live.Config.Counts.Removed != 0 {
			t.Fatalf("the live server differs from a capture just taken: %s", mustEncode(t, live.Config.Items))
		}

		// One setting changed, outside Alder.
		if err := set([]string{s.configWriteValue}); err != nil {
			t.Fatalf("changing %s: %v", s.configWriteAttr, err)
		}
		changed := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+baseline+`}}`)
		if changed.Config.Counts.Modified != 1 || changed.Config.Counts.Added != 0 || changed.Config.Counts.Removed != 0 {
			t.Fatalf("want exactly one modified setting, got %+v:\n%s", changed.Config.Counts, mustEncode(t, changed.Config.Items))
		}
		item := configItem(t, changed, s.configWriteAttr)
		if item.Kind != api.DiffKindModified || item.Source == nil || item.Target == nil {
			t.Fatalf("item %+v", item)
		}
		if item.Actionable != api.ConfigActionableWritable {
			t.Fatalf("%s is %s, and this suite only nominates settings Alder can change", item.Key, item.Actionable)
		}
		if item.Candidate == nil || len(item.Candidate.Changes) != 1 {
			t.Fatalf("no candidate change: %+v", item.Candidate)
		}
		if item.Candidate.Changes[0].Dn != s.configWriteDN {
			t.Errorf("the candidate changes %s, not %s", item.Candidate.Changes[0].Dn, s.configWriteDN)
		}

		// Put it back the way every change is made: plan, then apply the plan.
		planAndApplyChanges(t, client, base, item.Candidate.Changes)
		if after := read(); len(after) != len(before) || after[0] != before[0] {
			t.Fatalf("after applying the candidate, %s is %v and was %v", s.configWriteAttr, after, before)
		}
		restored := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+baseline+`}}`)
		if restored.Config.Counts.Modified != 0 || len(restored.Config.Items) != 0 {
			t.Fatalf("after restoring: %+v\n%s", restored.Config.Counts, mustEncode(t, restored.Config.Items))
		}
	})
}

// A setting Alder has no proven way to change is reported and never offered.
func TestAReadOnlyConfigSettingIsNeverStaged(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		doc := captureConfig(t, client, base)
		snap, _, err := snapshot.DecodeConfig([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}

		// Take a read-only setting from the capture and edit the document, so
		// the comparison sees a difference Alder must refuse to act on.
		var readOnly *snapshot.ConfigSetting
		for i := range snap.Settings {
			if snap.Settings[i].Mutability == snapshot.MutabilityReadOnly && len(snap.Settings[i].Values) > 0 {
				readOnly = &snap.Settings[i]
				break
			}
		}
		if readOnly == nil {
			t.Skip("this server's configuration has no read-only setting with a value")
		}
		edited := editedConfig(t, doc, readOnly.ID(), []string{"something-else"})
		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+edited+`}}`)
		item := configItem(t, d, readOnly.Key)
		if item.Actionable == api.ConfigActionableWritable {
			t.Fatalf("%s is read-only and was offered as a change: %+v", readOnly.ID(), item)
		}
		if item.Candidate != nil && len(item.Candidate.Changes) > 0 {
			t.Fatalf("a read-only setting produced a candidate change: %+v", item.Candidate)
		}
		if d.Config.Counts.Actionable != 0 {
			t.Errorf("counts say %d actionable", d.Config.Counts.Actionable)
		}
	})
}

// editedConfig rewrites one setting's values in a captured document, and drops
// the checksum: a document somebody edited is unverified, not forged.
func editedConfig(t *testing.T, document, id string, values []string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(document), &doc); err != nil {
		t.Fatal(err)
	}
	settings, _ := doc["settings"].([]any)
	found := false
	for _, raw := range settings {
		setting, _ := raw.(map[string]any)
		section, _ := setting["section"].(string)
		resource, _ := setting["resource"].(string)
		key, _ := setting["key"].(string)
		if section+"/"+resource+"/"+strings.ToLower(key) != id {
			continue
		}
		next := make([]any, 0, len(values))
		for _, v := range values {
			next = append(next, v)
		}
		setting["values"] = next
		found = true
	}
	if !found {
		t.Fatalf("no setting %s in the document", id)
	}
	delete(doc, "checksum")
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Two providers' configuration is not one configuration. Alder says so, and
// does not produce a list of differences that would all be artefacts.
func TestConfigurationOfTwoProvidersIsNotCompared(t *testing.T) {
	if len(servers) < 2 {
		t.Skip("this proof needs two servers")
	}
	documents := map[string]string{}
	var client *http.Client
	var base string
	for _, s := range servers {
		sess := connectForSchema(t, s)
		if !sess.Capabilities().Config.Readable {
			t.Skipf("%s: the configuration tree is not readable", s.name)
		}
		client, base = alderSession(t, s, true)
		documents[s.name] = captureConfig(t, client, base)
	}
	first, second := servers[0].name, servers[1].name

	d := configDiff(t, client, base,
		`{"source":{"snapshot":`+documents[first]+`},"target":{"snapshot":`+documents[second]+`}}`)
	if !d.Config.ProviderMismatch {
		t.Fatalf("two providers' configuration was compared as though it were one: %+v", d.Config.Counts)
	}
	if len(d.Config.Items) != 0 || d.Config.Counts.Added != 0 || d.Config.Counts.Removed != 0 {
		t.Fatalf("a provider mismatch produced %d items and %d added, %d removed",
			len(d.Config.Items), d.Config.Counts.Added, d.Config.Counts.Removed)
	}
	if d.Config.Complete {
		t.Error("a provider mismatch is not a complete comparison")
	}
	if d.Config.Source.Provider == d.Config.Target.Provider {
		t.Fatalf("both sides report provider %s", d.Config.Source.Provider)
	}
	if d.Config.Source.Settings == 0 || d.Config.Target.Settings == 0 {
		t.Errorf("the summaries say nothing: %+v %+v", d.Config.Source, d.Config.Target)
	}
	t.Logf("%s (%d settings in %d sections) vs %s (%d settings in %d sections): %s",
		first, d.Config.Source.Settings, len(d.Config.Source.Sections),
		second, d.Config.Target.Settings, len(d.Config.Target.Sections), reasonText(d))
}

func reasonText(d api.Diff) string {
	if d.Reasons == nil || len(*d.Reasons) == 0 {
		return ""
	}
	return (*d.Reasons)[0].Detail
}

// A session that cannot read the configuration tree is told so, rather than
// given a smaller configuration.
func TestConfigCaptureNeedsToReadTheConfigurationTree(t *testing.T) {
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			sess := connect(t, s, false)
			if sess.Capabilities().Config.Readable {
				t.Skipf("this server's data bind can read %s", sess.Capabilities().Config.DN)
			}
			client, base := alderSession(t, s, false)
			res := post(t, client, base+"/snapshots/capture", `{"kind":"config"}`)
			if res.status != http.StatusBadRequest {
				t.Fatalf("status %d\n%s", res.status, res.body)
			}
			if !strings.Contains(res.body, string(api.ErrorErrorConfigModelUnavailable)) {
				t.Errorf("body %s", res.body)
			}
		})
	}
}

// The identity of a repeated resource is what the provider calls it, not where
// it appeared in a list.
func TestConfigResourcesAreIdentifiedByName(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		snap, _, err := snapshot.DecodeConfig([]byte(captureConfig(t, client, base)))
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]string{}
		repeated := 0
		for _, resource := range snap.Resources {
			if previous, ok := seen[resource.ID()]; ok {
				t.Fatalf("two resources share the identity %s: %s and %s", resource.ID(), previous, resource.DN)
			}
			seen[resource.ID()] = resource.DN
			if resource.Kind != "global" {
				repeated++
			}
			if strings.Contains(resource.Name, "{") {
				t.Errorf("%s is identified by a position: %q", resource.DN, resource.Name)
			}
		}
		if repeated < 2 {
			t.Errorf("%s: only %d repeated resources, so identity is barely exercised", s.name, repeated)
		}
		// The suffix a backend serves is what names it on both servers.
		wanted := strings.ToLower(suffix)
		named := false
		for _, resource := range snap.Resources {
			if strings.EqualFold(resource.Name, wanted) {
				named = true
			}
		}
		if !named {
			t.Errorf("no resource is named after the suffix %s: %s", wanted, mustEncode(t, snap.Resources))
		}
	})
}
