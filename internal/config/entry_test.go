package config

import (
	"context"
	"errors"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// What the model says about one entry has to be the same thing a capture of
// the same server says about the same setting. Two answers from one model is
// the only way this is worth having; two models is how an editor ends up
// marking a field writable that a comparison refuses.

func describe(t *testing.T, r Reader, target string) *EntryModel {
	t.Helper()
	parsed, err := dn.Parse(target)
	if err != nil {
		t.Fatalf("parse %q: %v", target, err)
	}
	model, err := Describe(context.Background(), r, parsed)
	if err != nil {
		t.Fatalf("describe %q: %v", target, err)
	}
	return model
}

func TestDescribeAgreesWithACapture(t *testing.T) {
	for _, reader := range []*fakeReader{openldapReader(t), ds389Reader(t)} {
		snap, err := Capture(context.Background(), reader, Options{})
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		seen := 0
		for _, setting := range snap.Settings {
			if setting.DN == "" {
				continue
			}
			model := describe(t, reader, setting.DN)
			attribute, ok := model.Attribute(setting.Key)
			if !ok {
				t.Fatalf("%s: the model does not mention %s, which a capture recorded", setting.DN, setting.Key)
			}
			if attribute.Mutability != setting.Mutability {
				t.Errorf("%s %s: the entry model says %s, the capture says %s",
					setting.DN, setting.Key, attribute.Mutability, setting.Mutability)
			}
			if attribute.Section != setting.Section {
				t.Errorf("%s %s: section %s here, %s in the capture", setting.DN, setting.Key, attribute.Section, setting.Section)
			}
			if attribute.Sensitive != setting.Sensitive {
				t.Errorf("%s %s: sensitive %v here, %v in the capture", setting.DN, setting.Key, attribute.Sensitive, setting.Sensitive)
			}
			seen++
		}
		if seen == 0 {
			t.Fatal("no setting carried a DN, so nothing was compared")
		}
	}
}

func TestDescribeNamesTheResourceAndWhatIsNotConfiguration(t *testing.T) {
	model := describe(t, openldapReader(t), "cn=config")
	if model.Provider != snapshot.ProviderOpenLDAP {
		t.Errorf("provider %q", model.Provider)
	}
	if model.Resource.Kind != KindGlobal {
		t.Errorf("cn=config is the %s resource, not %s", KindGlobal, model.Resource.Kind)
	}
	// An attribute the directory owns is reported, and reported as not
	// configuration: the editor shows it, and nothing suggests writing it.
	if a, ok := model.Attribute("objectClass"); !ok || !a.Excluded || a.Mutability != snapshot.MutabilityReadOnly {
		t.Errorf("objectClass: %+v (ok=%v)", a, ok)
	}
	// And a setting the model states Alder changes stays writable.
	if a, ok := model.Attribute("olcIdleTimeout"); !ok || a.Mutability != snapshot.MutabilityWritable {
		t.Errorf("olcIdleTimeout: %+v (ok=%v)", a, ok)
	}
}

func TestDescribeRefusesWhatIsNotInTheModel(t *testing.T) {
	cases := []struct {
		reader *fakeReader
		dn     string
		why    string
	}{
		{openldapReader(t), "uid=alice,ou=people,dc=alder,dc=test", "directory data is not configuration"},
		{openldapReader(t), "cn=schema,cn=config", "the schema has its own kind of snapshot"},
		{ds389Reader(t), "cn=monitor,cn=userRoot,cn=ldbm database,cn=plugins,cn=config", "runtime state is not configuration"},
		{ds389Reader(t), "cn=import,cn=tasks,cn=config", "a task is not configuration"},
	}
	for _, c := range cases {
		parsed, err := dn.Parse(c.dn)
		if err != nil {
			t.Fatalf("parse %q: %v", c.dn, err)
		}
		if _, err := Describe(context.Background(), c.reader, parsed); !errors.Is(err, ErrNotConfiguration) {
			t.Errorf("%s (%s): err = %v, want ErrNotConfiguration", c.dn, c.why, err)
		}
	}
}

func TestDescribeSaysWhenASettingOnlyTakesEffectAtTheNextStart(t *testing.T) {
	// 389 DS publishes the list itself, on cn=config.
	reader := ds389Reader(t)
	reader.entries[0].Set("nsslapd-requiresrestart", [][]byte{[]byte("cn=config:nsslapd-port")})

	model := describe(t, reader, "cn=config")
	port, ok := model.Attribute("nsslapd-port")
	if !ok {
		t.Fatal("nsslapd-port is missing from the model")
	}
	if !port.RestartRequired {
		t.Error("the server named nsslapd-port as needing a restart, and the model does not say so")
	}
	if port.Mutability != snapshot.MutabilityReadOnly {
		t.Errorf("a setting that does nothing until a restart is not one Alder changes: %s", port.Mutability)
	}
}
