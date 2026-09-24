package diff

import (
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.16: configuration objects, and the one kind Alder creates.
//
// A comparison lists every object each side holds. It offers to act on exactly
// one: an OpenLDAP overlay whose module the server has already loaded. The
// precondition is checked before anything is sent, a removal is never derived
// unless it is named, and nothing at all is offered when neither side is the
// live directory.

func withResources(t *testing.T, provider string, resources []snapshot.ConfigResource, settings ...snapshot.ConfigSetting) ConfigSide {
	t.Helper()
	c := snapshot.ConfigCapture{Provider: provider, Vendor: "OpenLDAP", Root: "cn=config",
		Sections:  []string{"limits", "backend", "plugins", "server", "logging", "access_control"},
		CreatedAt: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
	s, err := snapshot.BuildConfig(c, resources, settings)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	return ConfigSide{Snapshot: s}
}

var mdb = snapshot.ConfigResource{Section: "backend", Kind: "database", Name: "dc=alder,dc=test",
	DN: "olcDatabase={1}mdb,cn=config", Label: "{1}mdb"}

var moduleList = snapshot.ConfigResource{Section: "plugins", Kind: "module", Name: "/usr/lib/ldap",
	DN: "cn=module{0},cn=config", Label: "modules"}

func moduleLoad(values ...string) snapshot.ConfigSetting {
	return snapshot.ConfigSetting{Section: "plugins", Resource: "module:/usr/lib/ldap", Key: "olcModuleLoad",
		Values: values, Ordered: true, Type: "string", Mutability: snapshot.MutabilityUnknown,
		Comparison: snapshot.ComparisonNormalised, DN: "cn=module{0},cn=config"}
}

func overlayResource(name string) snapshot.ConfigResource {
	return snapshot.ConfigResource{Section: "plugins", Kind: "overlay", Name: "dc=alder,dc=test/" + name,
		DN: "olcOverlay={0}" + name + ",olcDatabase={1}mdb,cn=config", Label: name}
}

func TestAnOverlayTheTargetHasIsOfferedWhenItsModuleIsLoaded(t *testing.T) {
	live := withResources(t, snapshot.ProviderOpenLDAP, []snapshot.ConfigResource{mdb, moduleList},
		moduleLoad("back_mdb", "memberof"))
	live.Live = true
	wanted := withResources(t, snapshot.ProviderOpenLDAP,
		[]snapshot.ConfigResource{mdb, moduleList, overlayResource("memberof")},
		moduleLoad("back_mdb", "memberof"),
		snapshot.ConfigSetting{Section: "plugins", Resource: "overlay:dc=alder,dc=test/memberof",
			Key: "olcMemberOfRefint", Values: []string{"TRUE"}, Type: "bool",
			Mutability: snapshot.MutabilityUnknown, Comparison: snapshot.ComparisonNormalised,
			DN: "olcOverlay={0}memberof,olcDatabase={1}mdb,cn=config"})

	r := CompareConfig(live, wanted, ConfigOptions{})
	object, ok := r.ObjectByID("overlay:dc=alder,dc=test/memberof")
	if !ok {
		t.Fatalf("the overlay is not in the comparison: %+v", r.Objects)
	}
	if object.Kind != Added || object.Actionable != ActionableWritable || object.Refusal != "" {
		t.Fatalf("object: %+v", object)
	}
	c := DeriveConfigObject(r, object, live, false)
	if len(c.Records) != 1 {
		t.Fatalf("no change was derived: %+v", c)
	}
	record := c.Records[0]
	if record.Type != directory.ChangeAdd || record.DN.String() != "olcOverlay=memberof,olcDatabase={1}mdb,cn=config" {
		t.Fatalf("the change is %v on %q", record.Type, record.DN)
	}
	// The overlay's own name carries no position: the server assigns one. The
	// database it sits on keeps the position it already has.
	if rdn, _, _ := strings.Cut(record.DN.String(), ","); strings.Contains(rdn, "{") {
		t.Fatalf("the new overlay was given a position: %s", rdn)
	}
	var classes, overlays []string
	for _, attr := range record.Attrs {
		for _, v := range attr.Values {
			if strings.EqualFold(attr.Name, "objectClass") {
				classes = append(classes, string(v))
			}
			if strings.EqualFold(attr.Name, "olcOverlay") {
				overlays = append(overlays, string(v))
			}
		}
	}
	if len(classes) != 1 || classes[0] != "olcOverlayConfig" || len(overlays) != 1 || overlays[0] != "memberof" {
		t.Fatalf("the entry is %+v", record.Attrs)
	}
}

func TestAnOverlayWhoseModuleIsNotLoadedIsRefusedBeforeAnythingIsSent(t *testing.T) {
	live := withResources(t, snapshot.ProviderOpenLDAP, []snapshot.ConfigResource{mdb, moduleList},
		moduleLoad("back_mdb"))
	live.Live = true
	wanted := withResources(t, snapshot.ProviderOpenLDAP,
		[]snapshot.ConfigResource{mdb, moduleList, overlayResource("memberof")}, moduleLoad("back_mdb"))

	r := CompareConfig(live, wanted, ConfigOptions{})
	object, _ := r.ObjectByID("overlay:dc=alder,dc=test/memberof")
	if object.Actionable == ActionableWritable {
		t.Fatal("an overlay whose module is not loaded was offered")
	}
	if object.Refusal != config.RefusalModuleNotLoaded {
		t.Fatalf("refusal %q", object.Refusal)
	}
	if c := DeriveConfigObject(r, object, live, false); len(c.Records) != 0 || c.Blocked != config.RefusalModuleNotLoaded {
		t.Fatalf("candidate: %+v", c)
	}
}

func TestARemovalIsDerivedOnlyWhenItIsAskedForByName(t *testing.T) {
	live := withResources(t, snapshot.ProviderOpenLDAP,
		[]snapshot.ConfigResource{mdb, moduleList, overlayResource("memberof")}, moduleLoad("back_mdb", "memberof"))
	live.Live = true
	without := withResources(t, snapshot.ProviderOpenLDAP, []snapshot.ConfigResource{mdb, moduleList},
		moduleLoad("back_mdb", "memberof"))

	r := CompareConfig(live, without, ConfigOptions{})
	object, ok := r.ObjectByID("overlay:dc=alder,dc=test/memberof")
	if !ok || object.Kind != Removed || !object.Destructive {
		t.Fatalf("object: %+v", object)
	}
	if c := DeriveConfigObject(r, object, live, false); len(c.Records) != 0 || c.Blocked != BlockedConfigRemovalNotSelected {
		t.Fatalf("a removal was derived without being asked for: %+v", c)
	}
	c := DeriveConfigObject(r, object, live, true)
	if len(c.Records) != 1 || c.Records[0].Type != directory.ChangeDelete {
		t.Fatalf("candidate: %+v", c)
	}
	// The entry removed is the live server's own, with the position it has.
	if c.Records[0].DN.String() != "olcOverlay={0}memberof,olcDatabase={1}mdb,cn=config" {
		t.Fatalf("the change deletes %q", c.Records[0].DN)
	}
}

func TestOnlyAnOverlayIsCreated(t *testing.T) {
	live := withResources(t, snapshot.ProviderOpenLDAP, []snapshot.ConfigResource{mdb, moduleList},
		moduleLoad("back_mdb", "memberof"))
	live.Live = true
	second := snapshot.ConfigResource{Section: "backend", Kind: "database", Name: "dc=second,dc=test",
		DN: "olcDatabase={2}mdb,cn=config", Label: "{2}mdb"}
	wanted := withResources(t, snapshot.ProviderOpenLDAP, []snapshot.ConfigResource{mdb, moduleList, second},
		moduleLoad("back_mdb", "memberof"))

	r := CompareConfig(live, wanted, ConfigOptions{})
	object, _ := r.ObjectByID("database:dc=second,dc=test")
	if object.Actionable == ActionableWritable || object.Refusal != config.RefusalNotCreatable {
		t.Fatalf("a database was offered for creation: %+v", object)
	}

	// And nothing at all on 389 DS, whose plugins are a fixed set the server
	// ships, switched with a setting rather than created.
	plugin := func(name string) snapshot.ConfigResource {
		return snapshot.ConfigResource{Section: "plugins", Kind: "plugin", Name: name,
			DN: "cn=" + name + ",cn=plugins,cn=config", Label: name}
	}
	ds := withResources(t, snapshot.Provider389DS, []snapshot.ConfigResource{plugin("memberof")})
	ds.Live = true
	dsWanted := withResources(t, snapshot.Provider389DS, []snapshot.ConfigResource{plugin("memberof"), plugin("referint")})
	dsResult := CompareConfig(ds, dsWanted, ConfigOptions{})
	got, _ := dsResult.ObjectByID("plugin:referint")
	if got.Actionable == ActionableWritable || got.Refusal != config.RefusalNotCreatable {
		t.Fatalf("a 389 DS plugin was offered for creation: %+v", got)
	}
}

func TestObjectsAreListedEvenWhenNothingCanBeDoneAboutThem(t *testing.T) {
	// Two files, neither of them live: the objects are still listed, because
	// "the other server has no memberof overlay" is one fact worth one line.
	left := withResources(t, snapshot.ProviderOpenLDAP, []snapshot.ConfigResource{mdb, overlayResource("memberof")})
	right := withResources(t, snapshot.ProviderOpenLDAP, []snapshot.ConfigResource{mdb})
	r := CompareConfig(left, right, ConfigOptions{})
	object, ok := r.ObjectByID("overlay:dc=alder,dc=test/memberof")
	if !ok || object.Kind != Removed {
		t.Fatalf("object: %+v", object)
	}
	if object.Actionable == ActionableWritable || object.Refusal != BlockedConfigSourceNotLive {
		t.Fatalf("two files offered a change: %+v", object)
	}
}
