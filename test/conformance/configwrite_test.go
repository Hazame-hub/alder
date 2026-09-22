//go:build conformance

package conformance

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.14: every setting Alder offers to change is one it has changed.
//
// The rule for the writable list is that a setting is on it only if Alder can
// write it, read it back and restore it on the harness, through the ordinary
// path: a comparison proposes the change, a plan judges it, an apply makes it.
// This test is that rule. It takes whatever a fresh capture calls writable --
// not a list kept here -- so a setting added to a model without a proof fails
// the suite rather than shipping.

// alternate is a value a setting can safely hold for the length of a test, and
// the server will store as written.
//
// Most settings take their current value plus one, or the other boolean. The
// exceptions are the ones where that would get in the test's own way -- an idle
// timeout of one second closes Alder's connection between two requests, a
// maximum entry size of one byte refuses every write -- or where the server
// rewrites the value it is given.
var alternate = map[string]string{
	"olcidletimeout":            "3600",
	"olcwritetimeout":           "3600",
	"olcloglevel":               "stats",
	"olcdbmaxsize":              "536870912",
	"olcdbmaxentrysize":         "1048576",
	"nsslapd-idletimeout":       "3600",
	"nsslapd-errorlog-level":    "24576", // the server keeps its default bit, 16384, in any level
	"nsslapd-accesslog-level":   "0",
	"nsslapd-securitylog-level": "0",
}

func alternateFor(key string, current []string) (string, bool) {
	if v, ok := alternate[strings.ToLower(key)]; ok {
		// A value the setting already holds is no change at all; step past
		// it rather than proving nothing.
		if len(current) == 1 && strings.EqualFold(current[0], v) {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return strconv.FormatInt(n+1, 10), true
			}
			return "", false
		}
		return v, true
	}
	if len(current) != 1 {
		return "", false
	}
	v := current[0]
	switch strings.ToLower(v) {
	case "true":
		return "FALSE", true
	case "false":
		return "TRUE", true
	case "on":
		return "off", true
	case "off":
		return "on", true
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return strconv.FormatInt(n+1, 10), true
	}
	return "", false
}

// seedAbsent is what the harness does not already set. The model lists them,
// so they are added outside Alder before the proof and removed after it: what
// is proved is Alder's write, not the harness's defaults.
var seedAbsent = map[string][]struct{ dn, key, value string }{
	"openldap": {
		{"olcDatabase={1}mdb,cn=config", "olcSizeLimit", "500"},
		{"olcDatabase={1}mdb,cn=config", "olcTimeLimit", "3600"},
	},
	"389ds": {
		{"cn=config", "nsslapd-pagedsizelimit", "0"},
	},
}

func TestEverySettingAlderOffersToChangeRoundTrips(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		for _, seed := range seedAbsent[s.name] {
			target := mustDN(t, seed.dn)
			entry, err := sess.Read(ctx(t), target, []string{seed.key})
			if err == nil && len(entry.GetStrings(seed.key)) > 0 {
				continue
			}
			if err := sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModAdd, Name: seed.key, Values: [][]byte{[]byte(seed.value)}}}}); err != nil {
				t.Fatalf("seeding %s on %s: %v", seed.key, seed.dn, err)
			}
			seed := seed
			t.Cleanup(func() {
				_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
					Mods: []directory.Mod{{Op: directory.ModDelete, Name: seed.key}}})
			})
		}

		client, base := alderSession(t, s, true)
		baseline := captureConfig(t, client, base)
		snap, _, err := snapshot.DecodeConfig([]byte(baseline))
		if err != nil {
			t.Fatal(err)
		}

		// Every key the model lists must be somewhere on the harness, or it
		// has not been proved.
		present := map[string]bool{}
		var writable []snapshot.ConfigSetting
		for _, setting := range snap.Settings {
			present[strings.ToLower(setting.Key)] = true
			if setting.Mutability == snapshot.MutabilityWritable {
				writable = append(writable, setting)
			}
		}
		var unproved []string
		for _, key := range config.WritableKeys(snap.Source.Provider) {
			if !present[key] {
				unproved = append(unproved, key)
			}
		}
		if len(unproved) > 0 {
			t.Fatalf("%s: listed as writable and not on the harness, so never proved: %v", s.name, unproved)
		}
		sort.Slice(writable, func(i, j int) bool { return writable[i].ID() < writable[j].ID() })

		proved := 0
		for _, setting := range writable {
			setting := setting
			t.Run(setting.ID(), func(t *testing.T) {
				alt, ok := alternateFor(setting.Key, setting.Values)
				if !ok {
					t.Fatalf("no safe alternate value for %s = %v; add one to the table", setting.Key, setting.Values)
				}
				target := mustDN(t, setting.DN)
				original := setting.Values
				t.Cleanup(func() {
					// Put it back directly if anything below failed half way.
					entry, err := sess.Read(ctx(t), target, []string{setting.Key})
					if err == nil && sameStrings(entry.GetStrings(setting.Key), original) {
						return
					}
					vals := make([][]byte, 0, len(original))
					for _, v := range original {
						vals = append(vals, []byte(v))
					}
					_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeModify,
						Mods: []directory.Mod{{Op: directory.ModReplace, Name: setting.Key, Values: vals}}})
				})

				// Change it: a document that wants the alternate value, compared
				// with the live server, planned and applied through Alder.
				wanted := rebuiltConfig(t, baseline, func(_ *snapshot.ConfigCapture,
					resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
					out := make([]snapshot.ConfigSetting, 0, len(settings))
					for _, other := range settings {
						if other.ID() == setting.ID() {
							other.Values = []string{alt}
						}
						out = append(out, other)
					}
					return resources, out
				})
				applyConfigCandidate(t, client, base, wanted, setting.ID())
				if got := readSetting(t, sess, target, setting.Key); !strings.EqualFold(strings.Join(got, ","), alt) {
					t.Fatalf("after applying, %s holds %v, want %s", setting.Key, got, alt)
				}

				// And restore it the same way, from the baseline.
				applyConfigCandidate(t, client, base, baseline, setting.ID())
				if got := readSetting(t, sess, target, setting.Key); !sameStrings(got, original) {
					t.Fatalf("after restoring, %s holds %v, want %v", setting.Key, got, original)
				}
			})
			proved++
		}
		t.Logf("%s: %d settings written, read back and restored through Alder", s.name, proved)
	})
}

// applyConfigCandidate compares the live server with a document, takes the
// one difference about id, and applies the change Alder proposes for it.
func applyConfigCandidate(t *testing.T, client *http.Client, base, document, id string) {
	t.Helper()
	d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+document+`}}`)
	for _, item := range d.Config.Items {
		if item.Id != id {
			continue
		}
		if item.Actionable != api.ConfigActionableWritable || item.Candidate == nil || len(item.Candidate.Changes) != 1 {
			t.Fatalf("%s is %s with candidate %+v; a writable setting must offer one change", id, item.Actionable, item.Candidate)
		}
		planAndApplyChanges(t, client, base, item.Candidate.Changes)
		return
	}
	t.Fatalf("the comparison has no difference about %s: %s", id, mustEncode(t, d.Config.Counts))
}

func readSetting(t *testing.T, sess directory.Session, target interface{ String() string }, key string) []string {
	t.Helper()
	entry, err := sess.Read(ctx(t), mustDN(t, target.String()), []string{key})
	if err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return entry.GetStrings(key)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}
