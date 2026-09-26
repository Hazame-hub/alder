package api

import (
	"net/http"
	"strings"
	"testing"
)

// GET /config/entry: what the model says about one configuration entry, which
// is what the editor marks its fields from. The answer has to be the model's,
// not a second opinion written for the editor.

func TestConfigEntryModelAnswersForOneEntry(t *testing.T) {
	rig := configRig(t, "0")
	res := rig.do(t, http.MethodGet, "/api/v1/config/entry?dn=olcDatabase%3D%7B1%7Dmdb%2Ccn%3Dconfig", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	model := decode[ConfigEntryModel](t, res)

	if model.Provider != ConfigProviderOpenLDAP {
		t.Errorf("provider %q", model.Provider)
	}
	if model.Resource.Kind != "database" || model.Resource.Name != "dc=alder,dc=test" {
		t.Errorf("resource %+v: a database is named by its suffix, never by its position", model.Resource)
	}

	byName := map[string]ConfigAttributeModel{}
	for _, a := range model.Attributes {
		byName[a.Name] = a
	}
	// Where the data lives is not a setting the editor should mark as one
	// Alder changes. The model says "unknown" rather than "read-only", which
	// is the honest answer: it has no opinion, and an opinion is what
	// "writable" would be.
	if a := byName["olcSuffix"]; a.Mutability == ConfigMutabilityWritable {
		t.Errorf("olcSuffix: %q", a.Mutability)
	}
	if a := byName["olcDbMaxSize"]; a.Mutability != ConfigMutabilityWritable {
		t.Errorf("olcDbMaxSize is a setting the round-trip proof covers: %q", a.Mutability)
	}
	if a := byName["olcRootPW"]; a.Sensitive == nil || !*a.Sensitive {
		t.Errorf("olcRootPW is a secret and the model must say so: %+v", a)
	}
	if a := byName["objectClass"]; a.Excluded == nil || !*a.Excluded {
		t.Errorf("objectClass is the directory's own: %+v", a)
	}
	if _, ok := byName["olcDatabase"]; !ok {
		t.Error("the entry's own naming attribute is missing from the answer")
	}
}

func TestConfigEntryModelRefusesWhatIsNotConfiguration(t *testing.T) {
	rig := configRig(t, "0")
	res := rig.do(t, http.MethodGet, "/api/v1/config/entry?dn=uid%3Dalice%2Cou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest", nil)
	if res.Status != http.StatusNotFound {
		t.Fatalf("directory data is not configuration: status %d, body %s", res.Status, res.Body)
	}
	if !strings.Contains(res.Body, "configuration model") {
		t.Errorf("the refusal should say why: %s", res.Body)
	}
}

func TestConfigEntryModelNeedsASession(t *testing.T) {
	rig := configRig(t, "0")
	res := rig.anonymous(t, http.MethodGet, "/api/v1/config/entry?dn=cn%3Dconfig")
	if res.Status != http.StatusUnauthorized {
		t.Fatalf("status %d: reading a configuration model needs a session", res.Status)
	}
}
