package snapshot

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A configuration snapshot is a document before it is anything else, so what
// is tested here is the document: that two captures of one configuration are
// byte-identical, that identity is what the provider names a thing and never
// where it sat in a list, that a secret is never in it, that a partial capture
// cannot claim to be whole, and that a reader refuses anything it does not
// fully understand rather than reading part of it.

func configCapture() ConfigCapture {
	return ConfigCapture{
		Provider: ProviderOpenLDAP, Vendor: "OpenLDAP", VendorVersion: "2.6.7",
		Root: "cn=config", Sections: []string{"server", "backend", "limits", "plugins"},
		Excluded:  []string{ExcludedRuntime, ExcludedSchema, ExcludedSecrets, ExcludedServerOwned},
		CreatedAt: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC),
	}
}

func configResources() []ConfigResource {
	return []ConfigResource{
		{Section: "server", Kind: "global", Name: "global", DN: "cn=config", Label: "server"},
		{Section: "backend", Kind: "database", Name: "dc=alder,dc=test", DN: "olcDatabase={1}mdb,cn=config", Label: "{1}mdb"},
		{Section: "plugins", Kind: "overlay", Name: "dc=alder,dc=test/memberof",
			DN: "olcOverlay={0}memberof,olcDatabase={1}mdb,cn=config", Label: "memberof"},
	}
}

func configSettings() []ConfigSetting {
	return []ConfigSetting{
		{Section: "limits", Key: "olcIdleTimeout", Values: []string{"0"}, Type: "int",
			Mutability: MutabilityWritable, Comparison: ComparisonNormalised, DN: "cn=config"},
		{Section: "backend", Resource: "database:dc=alder,dc=test", Key: "olcRootPW",
			Type: "string", Mutability: MutabilityUnknown, Comparison: ComparisonNormalised,
			Sensitive: true, Withheld: 1, DN: "olcDatabase={1}mdb,cn=config"},
		{Section: "backend", Resource: "database:dc=alder,dc=test", Key: "olcSuffix",
			Values: []string{"dc=alder,dc=test"}, Type: "dn", Mutability: MutabilityUnknown,
			Comparison: ComparisonNormalised, DN: "olcDatabase={1}mdb,cn=config"},
		{Section: "server", Key: "olcArgsFile", Values: []string{"/run/slapd/slapd.args"}, Type: "path",
			Mutability: MutabilityReadOnly, Comparison: ComparisonNormalised, Operational: true, DN: "cn=config"},
	}
}

func buildConfig(t *testing.T) *ConfigSnapshot {
	t.Helper()
	s, err := BuildConfig(configCapture(), configResources(), configSettings())
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	return s
}

func encodeConfig(t *testing.T, s *ConfigSnapshot) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeConfig(&buf, s); err != nil {
		t.Fatalf("EncodeConfig: %v", err)
	}
	return buf.Bytes()
}

func TestAConfigSnapshotIsTheSameDocumentWhateverOrderTheServerAnswersIn(t *testing.T) {
	first := buildConfig(t)

	shuffledResources := []ConfigResource{configResources()[2], configResources()[0], configResources()[1]}
	shuffledSettings := []ConfigSetting{configSettings()[3], configSettings()[1], configSettings()[2], configSettings()[0]}
	second, err := BuildConfig(configCapture(), shuffledResources, shuffledSettings)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}

	if first.Checksum != second.Checksum {
		t.Fatalf("the same configuration in another order produced another checksum:\n%s\n%s", first.Checksum, second.Checksum)
	}
	if !bytes.Equal(encodeConfig(t, first), encodeConfig(t, second)) {
		t.Fatal("the same configuration in another order produced another document")
	}
}

func TestTheTimeOfCaptureIsNotPartOfTheChecksum(t *testing.T) {
	first := buildConfig(t)
	later := configCapture()
	later.CreatedAt = later.CreatedAt.Add(72 * time.Hour)
	second, err := BuildConfig(later, configResources(), configSettings())
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if first.Checksum != second.Checksum {
		t.Fatal("capturing the same configuration an hour later changed its checksum")
	}
	if first.CreatedAt == second.CreatedAt {
		t.Fatal("the two captures claim the same time")
	}
}

func TestASettingIsIdentifiedByWhatItIsAndNotByWhereItSat(t *testing.T) {
	setting := ConfigSetting{Section: "backend", Resource: "database:dc=alder,dc=test", Key: "OLCSUFFIX"}
	same := ConfigSetting{Section: "backend", Resource: "database:dc=alder,dc=test", Key: "olcSuffix"}
	if setting.ID() != same.ID() {
		t.Fatalf("the same setting spelled differently got two identities: %q and %q", setting.ID(), same.ID())
	}
	other := ConfigSetting{Section: "backend", Resource: "database:dc=other,dc=test", Key: "olcSuffix"}
	if setting.ID() == other.ID() {
		t.Fatal("the same key on two databases got one identity")
	}

	resource := ConfigResource{Kind: "database", Name: "dc=alder,dc=test", Label: "{1}mdb"}
	renumbered := ConfigResource{Kind: "database", Name: "dc=alder,dc=test", Label: "{4}mdb"}
	if resource.ID() != renumbered.ID() {
		t.Fatal("renumbering a database changed its identity")
	}
}

func TestTwoInstancesOfOneKindAreTwoSettings(t *testing.T) {
	c := configCapture()
	c.Sections = append(c.Sections, "backend")
	resources := append(configResources(), ConfigResource{Section: "backend", Kind: "database",
		Name: "dc=second,dc=test", DN: "olcDatabase={2}mdb,cn=config", Label: "{2}mdb"})
	settings := append(configSettings(),
		ConfigSetting{Section: "backend", Resource: "database:dc=second,dc=test", Key: "olcSuffix",
			Values: []string{"dc=second,dc=test"}, Type: "dn", Mutability: MutabilityUnknown,
			Comparison: ComparisonNormalised, DN: "olcDatabase={2}mdb,cn=config"})
	s, err := BuildConfig(c, resources, settings)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if s.Counts.Settings != 5 || s.Counts.Resources != 4 {
		t.Fatalf("got %d settings in %d resources, want 5 in 4", s.Counts.Settings, s.Counts.Resources)
	}
	if _, ok := s.SettingByID("backend/database:dc=alder,dc=test/olcsuffix"); !ok {
		t.Fatal("the first database's suffix is gone")
	}
	if _, ok := s.SettingByID("backend/database:dc=second,dc=test/olcsuffix"); !ok {
		t.Fatal("the second database's suffix is gone")
	}
}

func TestASecretIsCountedAndNeverRecorded(t *testing.T) {
	settings := configSettings()
	settings[1].Values = []string{"alder-admin"} // as a careless caller might pass it
	s, err := BuildConfig(configCapture(), configResources(), settings)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	document := string(encodeConfig(t, s))
	if strings.Contains(document, "alder-admin") {
		t.Fatal("the document holds a password")
	}
	rootpw, ok := s.SettingByID("backend/database:dc=alder,dc=test/olcrootpw")
	if !ok {
		t.Fatal("the setting itself is gone; a withheld value is still a setting that exists")
	}
	if len(rootpw.Values) != 0 || rootpw.Withheld != 1 || !rootpw.Sensitive {
		t.Fatalf("a secret was not withheld: %+v", *rootpw)
	}
	if s.Counts.Withheld != 1 {
		t.Fatalf("withheld count is %d, want 1", s.Counts.Withheld)
	}
	// Nothing derived from the value either: the document must not carry a
	// digest of it that could be compared against a guess.
	for _, line := range strings.Split(document, "\n") {
		if strings.Contains(strings.ToLower(line), "rootpw") && strings.Contains(line, "sha") {
			t.Fatalf("a withheld setting carries a digest: %s", line)
		}
	}
}

func TestAWithheldSettingWithAValueIsRefused(t *testing.T) {
	s := buildConfig(t)
	s.Settings = append(s.Settings, ConfigSetting{Section: "server", Key: "olcSecretThing",
		Values: []string{"hunter2"}, Sensitive: true, Withheld: 1, Type: "string",
		Mutability: MutabilityUnknown, Comparison: ComparisonNormalised})
	if err := s.validateConfig(); err == nil {
		t.Fatal("a sensitive setting carrying a value was accepted")
	}
}

func TestAPartialCaptureSaysSoAndSaysWhy(t *testing.T) {
	c := configCapture()
	c.Incomplete = []ConfigIncomplete{{Reason: IncompleteAccess, Scope: "cn=config", Detail: "this bind may not read it"}}
	s, err := BuildConfig(c, configResources(), configSettings())
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if s.Completeness != ConfigPartial {
		t.Fatalf("completeness is %q, want partial", s.Completeness)
	}

	// And the other way: a document that claims to be partial and says nothing
	// about why is not a document.
	s.Incomplete = nil
	if err := s.validateConfig(); err == nil {
		t.Fatal("a partial snapshot with no reason was accepted")
	}
}

func TestACompletenessThatContradictsTheDocumentIsRefused(t *testing.T) {
	c := configCapture()
	c.Incomplete = []ConfigIncomplete{{Reason: IncompleteLimit, Scope: "cn=config"}}
	s, err := BuildConfig(c, configResources(), configSettings())
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	raw := encodeConfig(t, s)
	edited := bytes.Replace(raw, []byte(`"completeness": "partial"`), []byte(`"completeness": "complete"`), 1)
	if bytes.Equal(raw, edited) {
		t.Fatal("the document did not say it was partial")
	}
	if _, _, err := DecodeConfig(edited); err == nil {
		t.Fatal("a document claiming to be complete while listing what it missed was accepted")
	}
}

func TestADecodedConfigSnapshotIsTheOneThatWasWritten(t *testing.T) {
	s := buildConfig(t)
	back, integrity, err := DecodeConfig(encodeConfig(t, s))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if integrity != IntegrityVerified {
		t.Fatalf("integrity is %q, want verified", integrity)
	}
	if back.Checksum != s.Checksum || back.Counts != s.Counts || len(back.Settings) != len(s.Settings) {
		t.Fatal("the document read back as something else")
	}
}

func TestAnEditedConfigSnapshotIsRefused(t *testing.T) {
	s := buildConfig(t)
	raw := encodeConfig(t, s)
	edited := bytes.Replace(raw, []byte(`"olcIdleTimeout"`), []byte(`"olcIdleTimeOut"`), 1)
	if bytes.Equal(raw, edited) {
		t.Fatal("nothing was edited")
	}
	_, _, err := DecodeConfig(edited)
	if codeOf(err) != CodeChecksumMismatch {
		t.Fatalf("editing a document gave %v, want a checksum mismatch", err)
	}
}

func TestConfigDocumentsAreReadStrictly(t *testing.T) {
	s := buildConfig(t)
	raw := string(encodeConfig(t, s))

	cases := []struct {
		name     string
		document string
		want     string
	}{
		{"an unknown field", strings.Replace(raw, `"version": 1`, `"version": 1,`+"\n"+`  "policy": "trust me"`, 1), CodeInvalid},
		{"another kind", strings.Replace(raw, `"kind": "config"`, `"kind": "schema"`, 1), CodeInvalid},
		{"a version from the future", strings.Replace(raw, `"version": 1`, `"version": 9`, 1), CodeUnsupportedVersion},
		{"a version that does not exist", strings.Replace(raw, `"version": 1`, `"version": 0`, 1), CodeUnsupportedVersion},
		{"another format", strings.Replace(raw, `"alder-snapshot"`, `"something-else"`, 1), CodeNotSnapshot},
		{"a provider with no model", strings.Replace(raw, `"provider": "openldap"`, `"provider": "activedirectory"`, 1), CodeInvalid},
		{"a second document", raw + raw, CodeInvalid},
		{"not a document at all", "olcIdleTimeout: 0\n", CodeNotSnapshot},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := DecodeConfig([]byte(c.document))
			if got := codeOf(err); got != c.want {
				t.Fatalf("%s gave %q (%v), want %q", c.name, got, err, c.want)
			}
		})
	}
}

func TestASettingThatNamesAResourceTheDocumentDoesNotHoldIsRefused(t *testing.T) {
	s := buildConfig(t)
	s.Settings[0].Resource = "database:dc=nowhere"
	if err := s.validateConfig(); err == nil {
		t.Fatal("a setting pointing at a resource that is not there was accepted")
	}
}

func TestOneSettingTwiceIsRefused(t *testing.T) {
	s := buildConfig(t)
	s.Settings = append(s.Settings, s.Settings[0])
	if err := s.validateConfig(); err == nil {
		t.Fatal("the same setting twice was accepted")
	}
}

func TestCountsThatDoNotMatchTheDocumentAreRefused(t *testing.T) {
	s := buildConfig(t)
	raw := encodeConfig(t, s)
	edited := bytes.Replace(raw, []byte(`"settings": 4`), []byte(`"settings": 3`), 1)
	if bytes.Equal(raw, edited) {
		t.Fatal("the counts were not where they were expected")
	}
	if _, _, err := DecodeConfig(edited); err == nil {
		t.Fatal("a document whose counts lie was accepted")
	}
}

func TestAHostileConfigDocumentIsRefusedRatherThanRead(t *testing.T) {
	s := buildConfig(t)

	t.Run("a value longer than anything is read", func(t *testing.T) {
		big := *s
		big.Settings = append([]ConfigSetting{}, s.Settings...)
		big.Settings[0].Values = []string{strings.Repeat("a", maxConfigValueLen+1)}
		if err := big.validateConfig(); err == nil {
			t.Fatal("an unbounded value was accepted")
		}
	})

	t.Run("a value that is not text", func(t *testing.T) {
		bad := *s
		bad.Settings = append([]ConfigSetting{}, s.Settings...)
		bad.Settings[0].Values = []string{"\xff\xfe"}
		if err := bad.validateConfig(); err == nil {
			t.Fatal("a value that is not UTF-8 was accepted")
		}
	})

	t.Run("more settings than are read", func(t *testing.T) {
		_, err := BuildConfig(configCapture(), configResources(), make([]ConfigSetting, MaxConfigSettings+1))
		if codeOf(err) != CodeTooLarge {
			t.Fatalf("got %v, want too_large", err)
		}
	})

	t.Run("a mutability nothing means", func(t *testing.T) {
		bad := *s
		bad.Settings = append([]ConfigSetting{}, s.Settings...)
		bad.Settings[0].Mutability = "whatever-you-like"
		if err := bad.validateConfig(); err == nil {
			t.Fatal("an invented mutability was accepted")
		}
	})
}

func TestAConfigSnapshotIsNotADataSnapshotAndTheOtherWayRound(t *testing.T) {
	config := encodeConfig(t, buildConfig(t))
	if _, _, err := Decode(config); err == nil {
		t.Fatal("the data reader read a configuration snapshot")
	}
	if _, _, err := DecodeSchema(config); err == nil {
		t.Fatal("the schema reader read a configuration snapshot")
	}

	var data bytes.Buffer
	plain, err := Build(capture(false), testSchema(t), fixture(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := Encode(&data, plain); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, _, err := DecodeConfig(data.Bytes()); err == nil {
		t.Fatal("the configuration reader read a data snapshot")
	}
}

func TestTheOrderOfValuesIsKeptOnlyWhereItMeansSomething(t *testing.T) {
	c := configCapture()
	c.Sections = []string{"access_control", "server"}
	ordered := ConfigSetting{Section: "access_control", Key: "olcAccess",
		Values: []string{"to * by users read", "to attrs=userPassword by self write"}, Ordered: true,
		Type: "structured", Mutability: MutabilityUnknown, Comparison: ComparisonRaw, DN: "cn=config"}
	unordered := ConfigSetting{Section: "server", Key: "olcObjectIdentifier",
		Values: []string{"zeta 1.2.3", "alpha 1.2.4"}, Type: "string",
		Mutability: MutabilityUnknown, Comparison: ComparisonNormalised, DN: "cn=config"}
	s, err := BuildConfig(c, nil, []ConfigSetting{ordered, unordered})
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	got, _ := s.SettingByID("access_control//olcaccess")
	if got.Values[0] != "to * by users read" {
		t.Fatalf("an ordered setting was reordered: %v", got.Values)
	}
	got, _ = s.SettingByID("server//olcobjectidentifier")
	if got.Values[0] != "alpha 1.2.4" {
		t.Fatalf("an unordered setting was not canonicalised: %v", got.Values)
	}
}

func TestAConfigSnapshotCarriesNoNullArrays(t *testing.T) {
	// A document read by anything else should not have to tell null from an
	// empty list, so every list is written as a list.
	s, err := BuildConfig(configCapture(), nil, nil)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(encodeConfig(t, s), &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"resources", "settings", "incomplete"} {
		if string(generic[field]) == "null" {
			t.Fatalf("%q is null rather than an empty list", field)
		}
	}
}
