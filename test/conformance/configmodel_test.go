//go:build conformance

package conformance

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.18: the entry editor and a comparison read one model.
//
// The editor marks each field of a configuration entry from what the model
// says about it. A second implementation of "what does the model say" written
// for the editor would drift from the one a comparison uses, and the two would
// disagree on screen: a field marked writable that a comparison then refuses
// to stage. So the rule is that both answers come from the same model, over
// the same tree, and this proves it against both servers -- with the real
// restart list and the real plugin set, which no fixture has.

func TestConfigModelAgreesWithACapture(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("this session cannot read the server's configuration")
		}
		snap, err := config.Capture(ctx(t), sess, config.Options{})
		if err != nil {
			t.Fatalf("capturing the configuration: %v", err)
		}

		// One Describe per entry, not per setting: the answer is about an
		// entry, and a capture of this server has hundreds of settings on it.
		described := map[string]*config.EntryModel{}
		checked := 0
		for _, setting := range snap.Settings {
			if setting.DN == "" {
				continue
			}
			key := strings.ToLower(setting.DN)
			model, seen := described[key]
			if !seen {
				target, err := dn.Parse(setting.DN)
				if err != nil {
					t.Fatalf("the capture recorded a DN that does not parse: %q: %v", setting.DN, err)
				}
				model, err = config.Describe(ctx(t), sess, target)
				if err != nil {
					t.Fatalf("describing %s: %v", setting.DN, err)
				}
				described[key] = model
			}

			attribute, ok := model.Attribute(setting.Key)
			if !ok {
				t.Errorf("%s: the model does not mention %s, which a capture of the same server recorded",
					setting.DN, setting.Key)
				continue
			}
			if attribute.Mutability != setting.Mutability {
				t.Errorf("%s %s: the editor would be told %q and a comparison says %q",
					setting.DN, setting.Key, attribute.Mutability, setting.Mutability)
			}
			if attribute.Section != setting.Section {
				t.Errorf("%s %s: section %q here, %q in the capture", setting.DN, setting.Key,
					attribute.Section, setting.Section)
			}
			if attribute.Sensitive != setting.Sensitive {
				t.Errorf("%s %s: sensitive %v here, %v in the capture", setting.DN, setting.Key,
					attribute.Sensitive, setting.Sensitive)
			}
			checked++
		}
		if checked == 0 {
			t.Fatal("nothing was compared: no captured setting carried the entry it came from")
		}
		t.Logf("%s: %d settings across %d entries answer the same either way", s.name, checked, len(described))

		// Every setting a capture calls writable is a setting the editor marks
		// as one Alder changes, and there is at least one: a model that marked
		// nothing would pass everything above by saying "read_only" twice.
		writable := 0
		for _, setting := range snap.Settings {
			if setting.Mutability == snapshot.MutabilityWritable {
				writable++
			}
		}
		if writable == 0 {
			t.Error("this capture calls nothing writable, so the agreement above proves little")
		}
	})
}

func TestConfigModelRefusesWhatIsNotConfiguration(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("this session cannot read the server's configuration")
		}
		// Directory data is not configuration, whatever the bind may read.
		target, err := dn.Parse(suffix)
		if err != nil {
			t.Fatalf("dn: %v", err)
		}
		if _, err := config.Describe(ctx(t), sess, target); err == nil {
			t.Errorf("%s: the model answered for %s, which is directory data", s.name, suffix)
		}
	})
}
