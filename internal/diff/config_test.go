package diff

import (
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Comparing configuration. The rules being tested are the ones that make the
// answer trustworthy rather than merely produced:
//
//   - two providers are not compared at all;
//   - a setting is paired by what it is, not by where it sat;
//   - order matters only where the model said it does;
//   - a value nothing parses is compared as text and says so;
//   - a secret is compared as presence, never as a value;
//   - what could not be read is unknown, never removed;
//   - a difference is staged only where Alder already writes that setting.

func configSide(t *testing.T, provider string, partial bool, settings ...snapshot.ConfigSetting) ConfigSide {
	t.Helper()
	return ConfigSide{Snapshot: configSnapshot(t, provider, partial, settings...)}
}

func configSnapshot(t *testing.T, provider string, partial bool, settings ...snapshot.ConfigSetting) *snapshot.ConfigSnapshot {
	t.Helper()
	vendor := "OpenLDAP"
	if provider == snapshot.Provider389DS {
		vendor = "389 Project"
	}
	c := snapshot.ConfigCapture{Provider: provider, Vendor: vendor, Root: "cn=config",
		Sections:  []string{"limits", "backend", "access_control", "logging", "server", "plugins"},
		CreatedAt: time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)}
	if partial {
		c.Incomplete = []snapshot.ConfigIncomplete{{Reason: snapshot.IncompleteAccess, Scope: "cn=config"}}
	}
	var resources []snapshot.ConfigResource
	seen := map[string]bool{}
	for _, setting := range settings {
		if setting.Resource == "" || seen[setting.Resource] {
			continue
		}
		seen[setting.Resource] = true
		kind, name, _ := strings.Cut(setting.Resource, ":")
		resources = append(resources, snapshot.ConfigResource{Section: setting.Section, Kind: kind,
			Name: name, DN: setting.DN, Label: name})
	}
	s, err := snapshot.BuildConfig(c, resources, settings)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	return s
}

func idle(values ...string) snapshot.ConfigSetting {
	return snapshot.ConfigSetting{Section: "limits", Key: "olcIdleTimeout", Values: values, Type: "int",
		Mutability: snapshot.MutabilityWritable, Comparison: snapshot.ComparisonNormalised, DN: "cn=config"}
}

func argsFile(values ...string) snapshot.ConfigSetting {
	return snapshot.ConfigSetting{Section: "server", Key: "olcArgsFile", Values: values, Type: "path",
		Mutability: snapshot.MutabilityReadOnly, Comparison: snapshot.ComparisonNormalised,
		Operational: true, DN: "cn=config"}
}

func item(t *testing.T, r *ConfigResult, id string) ConfigItem {
	t.Helper()
	for _, item := range r.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("the comparison has no item %q; it has %v", id, itemIDs(r))
	return ConfigItem{}
}

func itemIDs(r *ConfigResult) []string {
	out := make([]string, 0, len(r.Items))
	for _, item := range r.Items {
		out = append(out, item.ID)
	}
	return out
}

func TestTwoProvidersConfigurationIsNotCompared(t *testing.T) {
	source := configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"), argsFile("/run/slapd/slapd.args"))
	target := configSide(t, snapshot.Provider389DS, false, snapshot.ConfigSetting{
		Section: "limits", Key: "nsslapd-idletimeout", Values: []string{"3600"}, Type: "int",
		Mutability: snapshot.MutabilityWritable, Comparison: snapshot.ComparisonNormalised, DN: "cn=config"})

	r := CompareConfig(source, target, ConfigOptions{})

	if !r.ProviderMismatch {
		t.Fatal("two providers' configuration was compared setting by setting")
	}
	if len(r.Items) != 0 {
		t.Fatalf("a cross-provider comparison produced %d items: %v", len(r.Items), itemIDs(r))
	}
	if r.Counts.Added != 0 || r.Counts.Removed != 0 {
		t.Fatalf("a cross-provider comparison invented %d added and %d removed", r.Counts.Added, r.Counts.Removed)
	}
	if r.Complete {
		t.Fatal("a comparison that compared nothing claimed to be complete")
	}
	if r.Source.Settings == 0 || r.Target.Settings == 0 {
		t.Fatal("each side should still be described in broad terms")
	}
	var found bool
	for _, reason := range r.Reasons {
		if reason.Code == ReasonProviderMismatch {
			found = true
		}
	}
	if !found {
		t.Fatalf("no provider mismatch reason: %+v", r.Reasons)
	}
}

func TestASettingIsPairedByWhatItIsNotByWhereItSat(t *testing.T) {
	// The same database under a different position in each snapshot. Its
	// identity is its suffix, so the comparison pairs the two.
	left := snapshot.ConfigSetting{Section: "backend", Resource: "database:dc=alder,dc=test",
		Key: "olcDbMaxSize", Values: []string{"1073741824"}, Type: "int",
		Mutability: snapshot.MutabilityWritable, Comparison: snapshot.ComparisonNormalised,
		DN: "olcDatabase={1}mdb,cn=config"}
	right := left
	right.Values = []string{"2147483648"}
	right.DN = "olcDatabase={3}mdb,cn=config"

	r := CompareConfig(configSide(t, snapshot.ProviderOpenLDAP, false, left),
		configSide(t, snapshot.ProviderOpenLDAP, false, right), ConfigOptions{})

	if r.Counts.Added != 0 || r.Counts.Removed != 0 {
		t.Fatalf("a renumbered database was reported as added and removed: %v", itemIDs(r))
	}
	got := item(t, r, "backend/database:dc=alder,dc=test/olcdbmaxsize")
	if got.Kind != Modified {
		t.Fatalf("kind is %q, want modified", got.Kind)
	}
}

func TestOrderIsADifferenceOnlyWhereTheModelSaidItMeansSomething(t *testing.T) {
	ordered := func(values ...string) snapshot.ConfigSetting {
		return snapshot.ConfigSetting{Section: "access_control", Key: "olcAccess", Values: values,
			Ordered: true, Type: "structured", Mutability: snapshot.MutabilityUnknown,
			Comparison: snapshot.ComparisonRaw, DN: "cn=config"}
	}
	unordered := func(values ...string) snapshot.ConfigSetting {
		return snapshot.ConfigSetting{Section: "server", Key: "olcObjectIdentifier", Values: values,
			Type: "string", Mutability: snapshot.MutabilityUnknown, Comparison: snapshot.ComparisonNormalised,
			DN: "cn=config"}
	}

	r := CompareConfig(
		configSide(t, snapshot.ProviderOpenLDAP, false, ordered("to * by users read", "to dn.base=\"\" by * read"),
			unordered("alpha 1.2.3", "zeta 1.2.4")),
		configSide(t, snapshot.ProviderOpenLDAP, false, ordered("to dn.base=\"\" by * read", "to * by users read"),
			unordered("zeta 1.2.4", "alpha 1.2.3")),
		ConfigOptions{IncludeUnchanged: true})

	if got := item(t, r, "access_control//olcaccess"); got.Kind != Modified {
		t.Fatalf("reordering access rules is a change and was reported as %q", got.Kind)
	}
	if got := item(t, r, "server//olcobjectidentifier"); got.Kind != Unchanged {
		t.Fatalf("reordering an unordered setting was reported as %q", got.Kind)
	}
}

func TestAValueNothingParsesIsComparedAsTextAndSaysSo(t *testing.T) {
	raw := func(value string) snapshot.ConfigSetting {
		return snapshot.ConfigSetting{Section: "access_control", Key: "olcAccess", Values: []string{value},
			Ordered: true, Type: "structured", Mutability: snapshot.MutabilityUnknown,
			Comparison: snapshot.ComparisonRaw, DN: "cn=config"}
	}
	r := CompareConfig(
		configSide(t, snapshot.ProviderOpenLDAP, false, raw("to * by users read")),
		configSide(t, snapshot.ProviderOpenLDAP, false, raw("to *  by  users  read")),
		ConfigOptions{})
	got := item(t, r, "access_control//olcaccess")
	if got.Kind != Modified {
		t.Fatal("two spellings of an unparsed value were called equal; nothing here knows they are")
	}
	if got.Comparison != snapshot.ComparisonRaw {
		t.Fatalf("comparison is %q, want raw", got.Comparison)
	}
	if !has(got.Problems, ProblemNotComparable) {
		t.Fatalf("the item does not say it was compared as text: %v", got.Problems)
	}
	if got.Actionable == ActionableWritable {
		t.Fatal("a difference nothing parsed was offered as something to change")
	}
}

func TestASecretIsComparedAsPresenceAndNeverAsAValue(t *testing.T) {
	secret := func(withheld int) snapshot.ConfigSetting {
		return snapshot.ConfigSetting{Section: "backend", Key: "olcRootPW", Sensitive: true, Withheld: withheld,
			Type: "string", Mutability: snapshot.MutabilityWritable, Comparison: snapshot.ComparisonNormalised,
			DN: "cn=config"}
	}

	same := CompareConfig(configSide(t, snapshot.ProviderOpenLDAP, false, secret(1)),
		configSide(t, snapshot.ProviderOpenLDAP, false, secret(1)), ConfigOptions{IncludeUnchanged: true})
	got := item(t, same, "backend//olcrootpw")
	if got.Kind != Unchanged {
		t.Fatalf("two withheld secrets were reported as %q; nothing was read to compare", got.Kind)
	}
	if len(got.Source) != 0 || len(got.Target) != 0 {
		t.Fatal("a withheld setting carried values into the comparison")
	}
	if !has(got.Problems, ProblemSensitive) {
		t.Fatalf("the item does not say the value was withheld: %v", got.Problems)
	}

	appeared := CompareConfig(configSide(t, snapshot.ProviderOpenLDAP, false, secret(0)),
		configSide(t, snapshot.ProviderOpenLDAP, false, secret(1)), ConfigOptions{})
	if got := item(t, appeared, "backend//olcrootpw"); got.Kind != Modified {
		t.Fatalf("a password appearing where there was none is a change and was reported as %q", got.Kind)
	}
	// And it is still never staged: the new value was never read.
	if got := item(t, appeared, "backend//olcrootpw"); got.Actionable == ActionableWritable {
		t.Fatal("a secret was offered as something to change")
	}
}

func TestAPartialCaptureCanNeverReportThatTwoSidesAreEqual(t *testing.T) {
	r := CompareConfig(configSide(t, snapshot.ProviderOpenLDAP, true, idle("0")),
		configSide(t, snapshot.ProviderOpenLDAP, false, idle("0")), ConfigOptions{})
	if r.Complete {
		t.Fatal("a comparison against a partial capture claimed to be complete")
	}
	var found bool
	for _, reason := range r.Reasons {
		if reason.Code == ReasonConfigPartial {
			found = true
		}
	}
	if !found {
		t.Fatalf("no reason says the capture was partial: %+v", r.Reasons)
	}
}

func TestASettingOnOneSideOnlyIsNeverTurnedIntoAChange(t *testing.T) {
	r := CompareConfig(
		configSide(t, snapshot.ProviderOpenLDAP, false, idle("0")),
		configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"), argsFile("/run/slapd/slapd.args")),
		ConfigOptions{})

	added := item(t, r, "server//olcargsfile")
	if added.Kind != Added {
		t.Fatalf("kind is %q, want added", added.Kind)
	}
	if added.Actionable == ActionableWritable {
		t.Fatal("a setting present on one side only was offered as something to change")
	}
	live := configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"))
	live.Live = true
	candidate := DeriveConfig(r, added, live)
	if candidate.Blocked != BlockedConfigAddRemove || len(candidate.Records) != 0 {
		t.Fatalf("an addition produced %+v; Alder does not add or remove configuration", candidate)
	}
}

func TestASettingMissingFromAPartialCaptureIsUnknownAndNotRemoved(t *testing.T) {
	// The target could not be read in full, and does not hold a setting the
	// source does. That is not a removal: nobody looked.
	source := configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"), argsFile("/run/slapd/slapd.args"))
	target := configSide(t, snapshot.ProviderOpenLDAP, true, idle("0"))

	r := CompareConfig(source, target, ConfigOptions{})

	got := item(t, r, "server//olcargsfile")
	if got.Kind != Unknown {
		t.Fatalf("kind is %q, want unknown: a partial capture cannot say something was removed", got.Kind)
	}
	if !has(got.Problems, ProblemUnread) {
		t.Fatalf("the item does not say why it is unknown: %v", got.Problems)
	}
	if r.Counts.Removed != 0 {
		t.Fatalf("removed is %d, want 0", r.Counts.Removed)
	}
	if got.Actionable == ActionableWritable {
		t.Fatal("something nobody read was offered as something to change")
	}
	liveSource := source
	liveSource.Live = true
	if blocked := DeriveConfig(r, got, liveSource); blocked.Blocked != BlockedConfigUnread || len(blocked.Records) != 0 {
		t.Fatalf("an unknown setting produced %+v", blocked)
	}
}

func TestOnlyASettingAlderAlreadyWritesBecomesAChange(t *testing.T) {
	live := configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"), argsFile("/run/slapd/slapd.args"))
	live.Live = true
	kept := configSide(t, snapshot.ProviderOpenLDAP, false, idle("1800"), argsFile("/var/run/slapd/slapd.args"))

	r := CompareConfig(live, kept, ConfigOptions{})

	writable := item(t, r, "limits//olcidletimeout")
	if writable.Actionable != ActionableWritable {
		t.Fatalf("a setting the model states Alder changes is %q", writable.Actionable)
	}
	candidate := DeriveConfig(r, writable, live)
	if candidate.Blocked != "" || len(candidate.Records) != 1 {
		t.Fatalf("no change was derived: %+v", candidate)
	}
	record := candidate.Records[0]
	if record.Type != directory.ChangeModify || record.DN.String() != "cn=config" {
		t.Fatalf("the change is %v on %q", record.Type, record.DN)
	}
	if len(record.Mods) != 1 || record.Mods[0].Op != directory.ModReplace ||
		!strings.EqualFold(record.Mods[0].Name, "olcIdleTimeout") || string(record.Mods[0].Values[0]) != "1800" {
		t.Fatalf("the change is not a replace of the setting: %+v", record.Mods)
	}

	readOnly := item(t, r, "server//olcargsfile")
	if readOnly.Actionable != ActionableReadOnly {
		t.Fatalf("a setting the server maintains is %q", readOnly.Actionable)
	}
	if !has(readOnly.Problems, ProblemNoWritePath) {
		t.Fatalf("the item does not say why it cannot be changed: %v", readOnly.Problems)
	}
	if blocked := DeriveConfig(r, readOnly, live); blocked.Blocked != BlockedConfigReadOnly || len(blocked.Records) != 0 {
		t.Fatalf("a read-only setting produced %+v", blocked)
	}
}

func TestNothingIsDerivedWhenTheSourceIsNotTheDirectory(t *testing.T) {
	source := configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"))
	target := configSide(t, snapshot.ProviderOpenLDAP, false, idle("1800"))
	r := CompareConfig(source, target, ConfigOptions{})
	got := DeriveConfig(r, item(t, r, "limits//olcidletimeout"), source)
	if got.Blocked != BlockedConfigSourceNotLive || len(got.Records) != 0 {
		t.Fatalf("a change was derived against a file: %+v", got)
	}
}

func TestTheChangeIsWrittenWhereTheLiveServerKeepsTheSetting(t *testing.T) {
	// The two sides keep the same setting in differently numbered entries. The
	// change must go to the live side's own entry, never the other side's.
	liveSetting := idle("0")
	liveSetting.DN = "olcDatabase={1}mdb,cn=config"
	liveSetting.Resource = "database:dc=alder,dc=test"
	fileSetting := idle("1800")
	fileSetting.DN = "olcDatabase={7}mdb,cn=config"
	fileSetting.Resource = "database:dc=alder,dc=test"

	live := configSide(t, snapshot.ProviderOpenLDAP, false, liveSetting)
	live.Live = true
	r := CompareConfig(live, configSide(t, snapshot.ProviderOpenLDAP, false, fileSetting), ConfigOptions{})
	candidate := DeriveConfig(r, item(t, r, "limits/database:dc=alder,dc=test/olcidletimeout"), live)
	if len(candidate.Records) != 1 {
		t.Fatalf("no change was derived: %+v", candidate)
	}
	if got := candidate.Records[0].DN.String(); got != "olcDatabase={1}mdb,cn=config" {
		t.Fatalf("the change goes to %q, which is the other server's entry", got)
	}
}

func TestSectionCountsAddUpToTheWhole(t *testing.T) {
	r := CompareConfig(
		configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"), argsFile("/a")),
		configSide(t, snapshot.ProviderOpenLDAP, false, idle("1800"), argsFile("/b")),
		ConfigOptions{})
	total := 0
	for _, section := range r.Sections {
		total += section.Counts.Compared
	}
	if total != len(r.Items) {
		t.Fatalf("the sections hold %d items and the comparison %d", total, len(r.Items))
	}
	if r.Counts.Modified != 2 {
		t.Fatalf("modified is %d, want 2", r.Counts.Modified)
	}
	if r.Counts.Actionable != 1 {
		t.Fatalf("actionable is %d, want 1: only one of the two is one Alder writes", r.Counts.Actionable)
	}
}

func TestComparingAConfigurationWithItselfIsNoDifferences(t *testing.T) {
	side := configSide(t, snapshot.ProviderOpenLDAP, false, idle("0"), argsFile("/run/slapd/slapd.args"))
	r := CompareConfig(side, side, ConfigOptions{})
	if len(r.Items) != 0 || r.Counts.Modified != 0 || r.Counts.Added != 0 || r.Counts.Removed != 0 {
		t.Fatalf("a configuration differs from itself: %v", itemIDs(r))
	}
	if !r.Complete {
		t.Fatalf("a complete comparison reported reasons: %+v", r.Reasons)
	}
}

func has(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestARawDifferenceIsNeverOfferedEvenOnAWritableSetting(t *testing.T) {
	index := func(values ...string) snapshot.ConfigSetting {
		return snapshot.ConfigSetting{Section: "backend", Key: "olcDbIndex", Values: values,
			Type: "structured", Mutability: snapshot.MutabilityWritable, Comparison: snapshot.ComparisonRaw,
			DN: "cn=config"}
	}
	live := configSide(t, snapshot.ProviderOpenLDAP, false, index("uid eq"))
	live.Live = true
	r := CompareConfig(live, configSide(t, snapshot.ProviderOpenLDAP, false, index("uid eq,sub")), ConfigOptions{})
	got := item(t, r, "backend//olcdbindex")
	if got.Actionable == ActionableWritable {
		t.Fatal("a difference nothing parsed was offered as a change")
	}
	if c := DeriveConfig(r, got, live); len(c.Records) != 0 {
		t.Fatalf("a raw difference produced %+v", c)
	}
}

func TestTwoSpellingsOfOneBooleanAreOneSetting(t *testing.T) {
	flag := func(v string) snapshot.ConfigSetting {
		return snapshot.ConfigSetting{Section: "backend", Key: "olcReadOnly", Values: []string{v}, Type: "bool",
			Mutability: snapshot.MutabilityWritable, Comparison: snapshot.ComparisonNormalised, DN: "cn=config"}
	}
	r := CompareConfig(configSide(t, snapshot.ProviderOpenLDAP, false, flag("FALSE")),
		configSide(t, snapshot.ProviderOpenLDAP, false, flag("false")), ConfigOptions{})
	if len(r.Items) != 0 {
		t.Fatalf("FALSE and false were reported as a difference: %v", itemIDs(r))
	}

	// And what is written back is the other side's spelling, not a lower-cased
	// one the server would refuse.
	live := configSide(t, snapshot.ProviderOpenLDAP, false, flag("FALSE"))
	live.Live = true
	d := CompareConfig(live, configSide(t, snapshot.ProviderOpenLDAP, false, flag("TRUE")), ConfigOptions{})
	c := DeriveConfig(d, item(t, d, "backend//olcreadonly"), live)
	if len(c.Records) != 1 || string(c.Records[0].Mods[0].Values[0]) != "TRUE" {
		t.Fatalf("the change writes %+v, want TRUE", c.Records)
	}
}
