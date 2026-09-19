//go:build conformance

package conformance

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.13 invariants, against the live servers.
//
// Each of these states a rule the configuration feature must never break, and
// then breaks it on purpose to show the check is alive: a test that can only
// pass proves nothing. The deliberate break is always done to a document or to
// a comparison, never to a server -- the one write these tests make is the one
// the conformance suite already nominates and restores.

// rebuiltConfig makes a valid, checksummed document out of a captured one with
// the settings and resources a test wants, so a deliberate break is a real
// document rather than something the reader would refuse for being malformed.
func rebuiltConfig(t *testing.T, document string, edit func(c *snapshot.ConfigCapture,
	resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting)) string {
	t.Helper()
	snap, _, err := snapshot.DecodeConfig([]byte(document))
	if err != nil {
		t.Fatalf("decoding the capture: %v", err)
	}
	c := snapshot.ConfigCapture{
		Provider: snap.Source.Provider, Vendor: snap.Source.Vendor, VendorVersion: snap.Source.VendorVersion,
		Root: snap.Source.Root, Sections: snap.Coverage.Sections, Excluded: snap.Coverage.Excluded,
		Incomplete: snap.Incomplete, CreatedAt: time.Now().UTC(),
	}
	resources, settings := snap.Resources, snap.Settings
	if edit != nil {
		resources, settings = edit(&c, resources, settings)
	}
	rebuilt, err := snapshot.BuildConfig(c, resources, settings)
	if err != nil {
		t.Fatalf("rebuilding the document: %v", err)
	}
	return mustEncodeConfig(t, rebuilt)
}

func mustEncodeConfig(t *testing.T, s *snapshot.ConfigSnapshot) string {
	t.Helper()
	var sb strings.Builder
	if err := snapshot.EncodeConfig(&sb, s); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// Invariant 1: a configuration document never holds a secret, and a document
// that claims to is refused rather than read.
func TestInvariantAConfigDocumentNeverHoldsASecret(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		document := captureConfig(t, client, base)
		snap, _, err := snapshot.DecodeConfig([]byte(document))
		if err != nil {
			t.Fatal(err)
		}
		if snap.Counts.Withheld == 0 {
			t.Fatalf("%s: nothing was withheld, and this server's configuration holds a root password", s.name)
		}
		for _, secret := range []string{s.bindPW, s.schemaBindPW, s.restrictedPW} {
			if secret != "" && strings.Contains(document, secret) {
				t.Fatalf("%s: the document holds a password", s.name)
			}
		}
		for _, setting := range snap.Settings {
			if setting.Sensitive && len(setting.Values) > 0 {
				t.Fatalf("%s carries values and is a secret", setting.ID())
			}
		}

		// The break: a document that carries a value for a withheld setting.
		var withheld string
		for _, setting := range snap.Settings {
			if setting.Sensitive {
				withheld = setting.ID()
				break
			}
		}
		forged := editedConfig(t, document, withheld, []string{"a value that was never read"})
		res := post(t, client, base+"/diff", `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+forged+`}}`)
		if res.status == 200 {
			t.Fatalf("a document carrying a value for a withheld setting was read: %s", res.body)
		}
	})
}

// Invariant 2: identity is what the provider names a thing, so renumbering
// changes nothing -- and renaming changes everything.
func TestInvariantConfigIdentityIsNameNotPosition(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		document := captureConfig(t, client, base)

		// Every label -- the position, the display name -- replaced, with the
		// identities left alone. The comparison must see one configuration.
		relabelled := rebuiltConfig(t, document, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			out := make([]snapshot.ConfigResource, 0, len(resources))
			for i, r := range resources {
				r.Label = "renumbered-" + string(rune('a'+i%26))
				out = append(out, r)
			}
			return out, settings
		})
		same := configDiff(t, client, base, `{"source":{"snapshot":`+document+`},"target":{"snapshot":`+relabelled+`}}`)
		if len(same.Config.Items) != 0 {
			t.Fatalf("relabelling resources produced %d differences: %s", len(same.Config.Items), mustEncode(t, same.Config.Items))
		}

		// The break: one resource renamed. That is a different resource, and
		// its settings must stop pairing.
		renamed := rebuiltConfig(t, document, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			var target string
			outResources := make([]snapshot.ConfigResource, 0, len(resources))
			for _, r := range resources {
				if r.Kind != "global" && target == "" {
					target = r.ID()
					r.Name = r.Name + "-elsewhere"
				}
				outResources = append(outResources, r)
			}
			outSettings := make([]snapshot.ConfigSetting, 0, len(settings))
			for _, setting := range settings {
				if setting.Resource == target {
					setting.Resource = strings.Replace(target, ":", ":", 1) + "-elsewhere"
				}
				outSettings = append(outSettings, setting)
			}
			return outResources, outSettings
		})
		different := configDiff(t, client, base, `{"source":{"snapshot":`+document+`},"target":{"snapshot":`+renamed+`}}`)
		if len(different.Config.Items) == 0 {
			t.Fatal("renaming a resource produced no differences, so nothing here is pairing by name")
		}
	})
}

// Invariant 3: a document cannot make a setting writable. Both sides must say
// so, and the live side's answer comes from the provider's model.
func TestInvariantADocumentCannotMakeASettingWritable(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		document := captureConfig(t, client, base)
		snap, _, err := snapshot.DecodeConfig([]byte(document))
		if err != nil {
			t.Fatal(err)
		}
		var readOnly snapshot.ConfigSetting
		for _, setting := range snap.Settings {
			if setting.Mutability == snapshot.MutabilityReadOnly && len(setting.Values) > 0 && !setting.Sensitive {
				readOnly = setting
				break
			}
		}
		if readOnly.Key == "" {
			t.Skip("this server's configuration has no read-only setting with a value")
		}

		// The break: a document that says the setting is writable and holds a
		// different value for it.
		forged := rebuiltConfig(t, document, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			out := make([]snapshot.ConfigSetting, 0, len(settings))
			for _, setting := range settings {
				if setting.ID() == readOnly.ID() {
					setting.Mutability = snapshot.MutabilityWritable
					setting.Values = []string{"something-else"}
				}
				out = append(out, setting)
			}
			return resources, out
		})
		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+forged+`}}`)
		item := configItem(t, d, readOnly.Key)
		if item.Actionable == api.ConfigActionableWritable {
			t.Fatalf("a forged document made %s writable: %+v", readOnly.ID(), item)
		}
		if item.Candidate != nil && len(item.Candidate.Changes) > 0 {
			t.Fatalf("a forged document produced a change for %s", readOnly.ID())
		}
	})
}

// Invariant 4: runtime state is not configuration, and is never captured.
func TestInvariantRuntimeStateIsNotCaptured(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		snap, _, err := snapshot.DecodeConfig([]byte(captureConfig(t, client, base)))
		if err != nil {
			t.Fatal(err)
		}
		for _, setting := range snap.Settings {
			text := strings.ToLower(setting.DN)
			switch {
			case strings.Contains(text, "cn=monitor"), strings.Contains(text, "cn=tasks"):
				t.Fatalf("%s is runtime state and was captured: %s", setting.ID(), setting.DN)
			}
			switch strings.ToLower(setting.Key) {
			case "entryuuid", "entrycsn", "modifytimestamp", "createtimestamp", "nsuniqueid",
				"contextcsn", "numsubordinates", "hassubordinates", "objectclass":
				t.Fatalf("%s is maintained by the server and was captured as configuration", setting.Key)
			}
		}

		// The break, the other way round: the tree really does hold entries
		// that were excluded, so the rule is doing work rather than being
		// trivially true.
		if !strings.EqualFold(snap.Source.Provider, snapshot.ProviderOpenLDAP) {
			counted := countTreeEntries(t, sess, snap.Source.Root)
			if counted <= len(snap.Resources) {
				t.Fatalf("%s: the tree holds %d entries and the snapshot %d resources; nothing was excluded",
					s.name, counted, len(snap.Resources))
			}
		}
	})
}

// countTreeEntries reads the configuration tree directly, so a test can say
// how much of it a capture deliberately left out.
func countTreeEntries(t *testing.T, sess directory.Session, root string) int {
	t.Helper()
	res, err := sess.Search(ctx(t), directory.SearchRequest{
		BaseDN: mustDN(t, root), Scope: directory.ScopeSubtree, Filter: filter.Present("objectClass"),
		Attributes: []string{"1.1"}, Limit: 5000, PageSize: 500,
	})
	if err != nil {
		t.Fatalf("reading %s: %v", root, err)
	}
	return len(res.Entries)
}

// Invariant 5: what was not read is unknown, never removed.
func TestInvariantAnUnreadSettingIsUnknownNotRemoved(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		document := captureConfig(t, client, base)

		// A partial capture that is missing settings the live server has: the
		// shape of a bind that could read only part of the tree.
		var dropped string
		partial := rebuiltConfig(t, document, func(c *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			c.Incomplete = []snapshot.ConfigIncomplete{{Reason: snapshot.IncompleteAccess,
				Scope: c.Root, Detail: "this bind could read only part of the configuration"}}
			out := make([]snapshot.ConfigSetting, 0, len(settings))
			for _, setting := range settings {
				if dropped == "" && setting.Section != "" && !setting.Sensitive {
					dropped = setting.Key
					continue
				}
				out = append(out, setting)
			}
			return resources, out
		})
		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+partial+`}}`)
		if d.Config.Counts.Removed != 0 {
			t.Fatalf("a partial capture reported %d settings removed", d.Config.Counts.Removed)
		}
		if d.Config.Complete {
			t.Fatal("a comparison against a partial capture claimed to be complete")
		}
		item := configItem(t, d, dropped)
		if item.Kind != api.DiffKindUnknown {
			t.Fatalf("%s is %s, want unknown", dropped, item.Kind)
		}
		if item.Candidate != nil && len(item.Candidate.Changes) > 0 {
			t.Fatalf("something nobody read produced a change: %+v", item.Candidate)
		}
	})
}

// Invariant 6: a comparison never writes. The only writes in this suite are
// the ones a plan applies, and a comparison applies nothing.
func TestInvariantAConfigComparisonWritesNothing(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.configWriteDN == "" {
			t.Skip("no readable configuration, or no setting nominated for this server")
		}
		client, base := alderSession(t, s, true)
		target := mustDN(t, s.configWriteDN)
		before, err := sess.Read(ctx(t), target, []string{s.configWriteAttr})
		if err != nil {
			t.Fatalf("reading %s: %v", s.configWriteDN, err)
		}
		was := before.GetStrings(s.configWriteAttr)

		document := captureConfig(t, client, base)
		edited := editedConfig(t, document, settingIDFor(t, document, s.configWriteAttr), []string{s.configWriteValue})
		// Comparing, repeatedly, with a document that wants a different value.
		for i := 0; i < 3; i++ {
			configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+edited+`}}`)
		}

		after, err := sess.Read(ctx(t), target, []string{s.configWriteAttr})
		if err != nil {
			t.Fatalf("reading %s: %v", s.configWriteDN, err)
		}
		if got := after.GetStrings(s.configWriteAttr); len(got) != len(was) || (len(got) > 0 && got[0] != was[0]) {
			t.Fatalf("comparing changed the server: %s is %v and was %v", s.configWriteAttr, got, was)
		}
	})
}

// settingIDFor is the identity of the one setting with this key.
func settingIDFor(t *testing.T, document, key string) string {
	t.Helper()
	snap, _, err := snapshot.DecodeConfig([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range snap.Settings {
		if strings.EqualFold(setting.Key, key) {
			return setting.ID()
		}
	}
	t.Fatalf("no setting %s in the document", key)
	return ""
}

// Invariant 7: recovery is not available for configuration, and a
// configuration change is not a data change wearing another hat.
func TestInvariantRecoveryIsNotOfferedForConfiguration(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.configWriteDN == "" {
			t.Skip("no readable configuration, or no setting nominated for this server")
		}
		client, base := alderSession(t, s, true)
		document := captureConfig(t, client, base)
		edited := editedConfig(t, document, settingIDFor(t, document, s.configWriteAttr), []string{s.configWriteValue})
		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+edited+`}}`)
		item := configItem(t, d, s.configWriteAttr)
		if item.Candidate == nil || len(item.Candidate.Changes) != 1 {
			t.Fatalf("the nominated setting offered no change: %+v", item)
		}
		plan := planChanges(t, client, base, item.Candidate.Changes)
		if len(plan.Items) != 1 {
			t.Fatalf("the plan holds %d items", len(plan.Items))
		}
		planned := plan.Items[0]
		if planned.Kind == nil || *planned.Kind != api.PlanTargetConfig {
			t.Fatalf("the plan calls this a %v change, not a configuration one", planned.Kind)
		}
		if planned.Recovery == nil {
			t.Fatal("the plan says nothing about recovery")
		}
		if planned.Recovery.Recoverability != api.RecoverabilityUnavailable {
			t.Fatalf("recovery is %q for a configuration change; it is not available for configuration",
				planned.Recovery.Recoverability)
		}
	})
}

// Invariant 8: two providers are never compared setting by setting, whatever
// their configurations happen to hold.
func TestInvariantTwoProvidersAreNeverCompared(t *testing.T) {
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
		c, b := alderSession(t, s, true)
		client, base = c, b
		documents[s.name] = captureConfig(t, client, base)
	}
	first, second := servers[0].name, servers[1].name

	// The break: each document rewritten so the two sides share section names
	// and even setting keys. They still belong to different software, and the
	// answer must still be that they are not comparable.
	forged := rebuiltConfig(t, documents[second], func(c *snapshot.ConfigCapture,
		resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
		return resources, settings
	})
	d := configDiff(t, client, base,
		`{"source":{"snapshot":`+documents[first]+`},"target":{"snapshot":`+forged+`}}`)
	if !d.Config.ProviderMismatch || len(d.Config.Items) != 0 {
		t.Fatalf("two providers were compared: mismatch=%v items=%d", d.Config.ProviderMismatch, len(d.Config.Items))
	}
	if d.Config.Counts.Added != 0 || d.Config.Counts.Removed != 0 || d.Config.Counts.Modified != 0 {
		t.Fatalf("a provider mismatch produced counts: %+v", d.Config.Counts)
	}
	t.Logf("%s and %s: %s", first, second, reasonText(d))
}
