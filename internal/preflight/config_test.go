package preflight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/snapshot"
)

// A configuration preflight answers only for one server's own software, and
// within it says, setting by setting, what an operator would have to do for
// the target to match the snapshot.

func cfgSetting(section, resource, key string, values []string, mutability string) snapshot.ConfigSetting {
	return snapshot.ConfigSetting{Section: section, Resource: resource, Key: key, Values: values, Type: "string",
		Mutability: mutability, Comparison: snapshot.ComparisonNormalised, DN: "cn=config"}
}

func cfgDoc(t *testing.T, provider string, partial bool, resources []snapshot.ConfigResource, settings ...snapshot.ConfigSetting) *snapshot.ConfigSnapshot {
	t.Helper()
	c := snapshot.ConfigCapture{Provider: provider, Vendor: "OpenLDAP", Root: "cn=config",
		Sections:  []string{"server", "limits", "backend", "plugins", "logging", "access_control", "password_policy"},
		CreatedAt: time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)}
	if partial {
		c.Incomplete = []snapshot.ConfigIncomplete{{Reason: snapshot.IncompleteAccess, Scope: "cn=config"}}
	}
	s, err := snapshot.BuildConfig(c, resources, settings)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	return s
}

func runConfig(t *testing.T, source, live *snapshot.ConfigSnapshot, captureErr error) *Report {
	t.Helper()
	target := &fakeTarget{}
	r, err := ConfigSnapshot(context.Background(), source, "verified", target, Options{
		Now: func() time.Time { return time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC) },
		CaptureConfig: func(context.Context) (*snapshot.ConfigSnapshot, error) {
			if captureErr != nil {
				return nil, captureErr
			}
			return live, nil
		},
	})
	if err != nil {
		t.Fatalf("ConfigSnapshot: %v", err)
	}
	if target.writes != 0 {
		t.Fatalf("a configuration preflight wrote %d times", target.writes)
	}
	return r
}

func findingFor(t *testing.T, r *Report, setting string) Finding {
	t.Helper()
	for _, f := range r.Findings {
		if f.Source.Setting == setting {
			return f
		}
	}
	t.Fatalf("no finding about %s; findings: %+v", setting, r.Findings)
	return Finding{}
}

var database = snapshot.ConfigResource{Section: "backend", Kind: "database", Name: "dc=alder,dc=test", Label: "{1}mdb"}
var overlay = snapshot.ConfigResource{Section: "plugins", Kind: "overlay", Name: "dc=alder,dc=test/memberof", Label: "memberof"}

func TestAConfigurationPreflightSaysWhatEachSettingNeeds(t *testing.T) {
	source := cfgDoc(t, snapshot.ProviderOpenLDAP, false, []snapshot.ConfigResource{database, overlay},
		cfgSetting("limits", "", "olcIdleTimeout", []string{"1800"}, snapshot.MutabilityWritable),
		cfgSetting("limits", "", "olcSizeLimit", []string{"500"}, snapshot.MutabilityWritable),
		cfgSetting("server", "", "olcThreads", []string{"16"}, snapshot.MutabilityWritable),
		cfgSetting("backend", "database:dc=alder,dc=test", "olcDbMaxSize", []string{"1"}, snapshot.MutabilityReadOnly),
		cfgSetting("plugins", "overlay:dc=alder,dc=test/memberof", "olcMemberOfRefint", []string{"TRUE"}, snapshot.MutabilityUnknown),
		cfgSetting("logging", "", "olcLogFile", []string{"/var/log/a"}, snapshot.MutabilityUnknown),
		cfgSetting("access_control", "", "olcAccess", []string{"to * by * read"}, snapshot.MutabilityUnknown),
		snapshot.ConfigSetting{Section: "backend", Resource: "database:dc=alder,dc=test", Key: "olcRootPW", Sensitive: true,
			Withheld: 1, Type: "string", Mutability: snapshot.MutabilityUnknown, Comparison: snapshot.ComparisonNormalised},
	)
	for i := range source.Settings {
		source.Settings[i].Operational = source.Settings[i].Key == "olcLogFile"
	}

	live := cfgDoc(t, snapshot.ProviderOpenLDAP, false, []snapshot.ConfigResource{database},
		cfgSetting("limits", "", "olcIdleTimeout", []string{"0"}, snapshot.MutabilityWritable),
		cfgSetting("server", "", "olcThreads", []string{"16"}, snapshot.MutabilityWritable),
		cfgSetting("backend", "database:dc=alder,dc=test", "olcDbMaxSize", []string{"2"}, snapshot.MutabilityReadOnly),
		cfgSetting("logging", "", "olcLogFile", []string{"/var/log/b"}, snapshot.MutabilityUnknown),
		cfgSetting("access_control", "", "olcAccess", []string{"to * by * none"}, snapshot.MutabilityUnknown),
		snapshot.ConfigSetting{Section: "backend", Resource: "database:dc=alder,dc=test", Key: "olcRootPW", Sensitive: true,
			Withheld: 1, Type: "string", Mutability: snapshot.MutabilityUnknown, Comparison: snapshot.ComparisonNormalised},
	)
	for i := range live.Settings {
		live.Settings[i].Operational = live.Settings[i].Key == "olcLogFile"
	}

	r := runConfig(t, source, live, nil)

	cases := []struct {
		setting string
		code    string
		class   Classification
		manual  bool
	}{
		{"limits//olcidletimeout", CodeConfigChangeable, PrerequisiteRequired, false},
		{"limits//olcsizelimit", CodeConfigMissing, PrerequisiteRequired, true},
		{"server//olcthreads", CodeConfigPresent, AlreadySatisfied, false},
		{"backend/database:dc=alder,dc=test/olcdbmaxsize", CodeConfigManual, PrerequisiteRequired, true},
		{"plugins/overlay:dc=alder,dc=test/memberof/olcmemberofrefint", CodeConfigMissing, PrerequisiteRequired, true},
		{"logging//olclogfile", CodeConfigEnvironment, Excluded, false},
		{"backend/database:dc=alder,dc=test/olcrootpw", CodeSensitiveNotMigratable, Excluded, false},
	}
	for _, c := range cases {
		f := findingFor(t, r, c.setting)
		if f.Code != c.code || f.Classification != c.class || f.ManualAction != c.manual {
			t.Errorf("%s: %s/%s manual=%v, want %s/%s manual=%v", c.setting, f.Code, f.Classification, f.ManualAction,
				c.code, c.class, c.manual)
		}
	}

	// The overlay the target lacks is its own finding, and the setting on it
	// names that finding as its cause.
	var resourceFinding Finding
	for _, f := range r.Findings {
		if f.Code == CodeConfigResourceMissing {
			resourceFinding = f
		}
	}
	if resourceFinding.Source.Resource != "overlay:dc=alder,dc=test/memberof" {
		t.Fatalf("no finding about the missing overlay: %+v", r.Findings)
	}
	refint := findingFor(t, r, "plugins/overlay:dc=alder,dc=test/memberof/olcmemberofrefint")
	if len(refint.Causes) != 1 || refint.Causes[0] != resourceFinding.ID {
		t.Fatalf("the overlay's setting does not name the missing overlay: %v", refint.Causes)
	}

	// Access rules are not evaluated here, as in every report.
	for _, f := range r.Findings {
		if f.Source.Setting == "access_control//olcaccess" {
			t.Fatalf("access control produced a finding: %+v", f)
		}
	}
	notEvaluated := map[string]bool{}
	for _, n := range r.NotEvaluated {
		notEvaluated[n.Area] = true
	}
	if !notEvaluated["access_control"] || notEvaluated["server_configuration"] {
		t.Fatalf("not evaluated: %+v", r.NotEvaluated)
	}

	if r.Overall != CompatibleWithPrerequisite || !r.Complete {
		t.Fatalf("overall %s complete %v %v", r.Overall, r.Complete, r.Reasons)
	}
}

func TestTwoProvidersConfigurationIsNotEvaluated(t *testing.T) {
	source := cfgDoc(t, snapshot.ProviderOpenLDAP, false, nil,
		cfgSetting("limits", "", "olcIdleTimeout", []string{"0"}, snapshot.MutabilityWritable))
	live := cfgDoc(t, snapshot.Provider389DS, false, nil,
		cfgSetting("limits", "", "nsslapd-idletimeout", []string{"0"}, snapshot.MutabilityWritable))
	r := runConfig(t, source, live, nil)
	if r.Overall != Incomplete || r.Complete {
		t.Fatalf("overall %s complete %v", r.Overall, r.Complete)
	}
	if len(r.Findings) != 1 || r.Findings[0].Code != CodeConfigProviderMismatch {
		t.Fatalf("findings: %+v", r.Findings)
	}
	listed := false
	for _, n := range r.NotEvaluated {
		if n.Area == "server_configuration" {
			listed = true
		}
	}
	if !listed {
		t.Fatal("configuration was compared across providers and not listed as not evaluated")
	}
}

func TestATargetWhoseConfigurationCannotBeReadIsUnknown(t *testing.T) {
	source := cfgDoc(t, snapshot.ProviderOpenLDAP, false, nil,
		cfgSetting("limits", "", "olcIdleTimeout", []string{"0"}, snapshot.MutabilityWritable))
	r := runConfig(t, source, nil, errors.New("insufficient access"))
	if r.Overall != Incomplete || len(r.Findings) != 1 || r.Findings[0].Code != CodeConfigUnreadable {
		t.Fatalf("overall %s findings %+v", r.Overall, r.Findings)
	}
}

func TestAPartialTargetMakesMissingSettingsUnknown(t *testing.T) {
	source := cfgDoc(t, snapshot.ProviderOpenLDAP, false, nil,
		cfgSetting("limits", "", "olcIdleTimeout", []string{"0"}, snapshot.MutabilityWritable),
		cfgSetting("limits", "", "olcSizeLimit", []string{"500"}, snapshot.MutabilityWritable))
	live := cfgDoc(t, snapshot.ProviderOpenLDAP, true, nil,
		cfgSetting("limits", "", "olcIdleTimeout", []string{"0"}, snapshot.MutabilityWritable))
	r := runConfig(t, source, live, nil)
	if f := findingFor(t, r, "limits//olcsizelimit"); f.Code != CodeConfigUnknown || f.Classification != Unknown {
		t.Fatalf("a setting a partial target lacks is %s/%s, want unknown", f.Code, f.Classification)
	}
	if r.Complete || r.Overall != Incomplete {
		t.Fatalf("overall %s complete %v", r.Overall, r.Complete)
	}
}

func TestASettingOnlyTheTargetHasIsNotAFinding(t *testing.T) {
	source := cfgDoc(t, snapshot.ProviderOpenLDAP, false, nil,
		cfgSetting("limits", "", "olcIdleTimeout", []string{"0"}, snapshot.MutabilityWritable))
	live := cfgDoc(t, snapshot.ProviderOpenLDAP, false, nil,
		cfgSetting("limits", "", "olcIdleTimeout", []string{"0"}, snapshot.MutabilityWritable),
		cfgSetting("limits", "", "olcSizeLimit", []string{"500"}, snapshot.MutabilityWritable))
	r := runConfig(t, source, live, nil)
	for _, f := range r.Findings {
		if f.Source.Setting == "limits//olcsizelimit" {
			t.Fatalf("a setting only the target has became a finding: %+v", f)
		}
	}
	if r.Overall != Compatible {
		t.Fatalf("overall %s", r.Overall)
	}
}
