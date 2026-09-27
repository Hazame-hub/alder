package config

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Indexes (1.23).
//
// The point of the feature is that one intention -- "index mail for equality"
// -- is two entirely different writes, and that a comparison can still say the
// two servers differ. These tests hold both halves: the same identity on both
// sides, and each server's own change.

func indexIDs(s *snapshot.ConfigSnapshot) []string {
	var out []string
	for _, r := range s.Resources {
		if r.Kind == KindIndex {
			out = append(out, r.ID())
		}
	}
	return out
}

func TestBothServersReportAnIndexAsTheSameKindOfThing(t *testing.T) {
	openldap := capture(t, openldapReader(t))
	ds389 := capture(t, ds389Reader(t))

	// OpenLDAP writes them as values of one attribute; one value naming two
	// attributes is two indexes.
	for _, want := range []string{
		"index:dc=alder,dc=test/objectclass",
		"index:dc=alder,dc=test/uid",
		"index:dc=alder,dc=test/cn",
	} {
		if _, ok := openldap.ResourceByID(want); !ok {
			t.Errorf("OpenLDAP index %q is missing; indexes are %v", want, indexIDs(openldap))
		}
	}
	// And the same attribute on the same suffix is the same identity on the
	// other server, where it is an entry rather than a value. Without that a
	// comparison could never say the two servers index differently.
	if _, ok := ds389.ResourceByID("index:dc=alder,dc=test/uid"); !ok {
		t.Fatalf("389 DS index is missing; indexes are %v", indexIDs(ds389))
	}
}

func TestTheAttributeAnIndexCameFromIsNotReportedTwice(t *testing.T) {
	s := capture(t, openldapReader(t))
	for _, setting := range s.Settings {
		if !strings.EqualFold(setting.Key, "olcDbIndex") {
			continue
		}
		if !strings.HasPrefix(setting.Resource, KindIndex+":") {
			t.Errorf("olcDbIndex is still a setting of %q as well as an index", setting.Resource)
		}
	}
	// The value that names two attributes carries its types to both of them.
	types := IndexTypesOf(snapshot.ConfigResource{Section: SectionPerformance, Kind: KindIndex,
		Name: "dc=alder,dc=test/cn"}, s.Settings)
	if strings.Join(types, ",") != "eq,sub" {
		t.Errorf("cn is indexed for %v, want eq and sub", types)
	}
}

func TestCreatingAnIndexIsEachServersOwnWrite(t *testing.T) {
	want := snapshot.ConfigResource{Section: SectionPerformance, Kind: KindIndex,
		Name: "dc=alder,dc=test/mail", Label: "mail"}

	record, refusal := CreateRecord(capture(t, openldapReader(t)), want, []string{"eq", "sub"})
	if refusal != "" {
		t.Fatalf("OpenLDAP refused to create an index: %s", refusal)
	}
	if record.Type != directory.ChangeModify || record.DN.String() != "olcDatabase={1}mdb,cn=config" {
		t.Fatalf("OpenLDAP index is created by %v on %s", record.Type, record.DN)
	}
	if len(record.Mods) != 1 || record.Mods[0].Op != directory.ModAdd ||
		string(record.Mods[0].Values[0]) != "mail eq,sub" {
		t.Fatalf("OpenLDAP index change is %+v", record.Mods)
	}

	record, refusal = CreateRecord(capture(t, ds389Reader(t)), want, []string{"eq", "sub"})
	if refusal != "" {
		t.Fatalf("389 DS refused to create an index: %s", refusal)
	}
	if record.Type != directory.ChangeAdd {
		t.Fatalf("389 DS index is created by %v", record.Type)
	}
	if got := record.DN.String(); got != "cn=mail,cn=index,cn=userRoot,cn=ldbm database,cn=plugins,cn=config" {
		t.Fatalf("389 DS index entry is %s", got)
	}
}

func TestAnIndexIsNotRemovedByRewritingSomebodyElses(t *testing.T) {
	live := capture(t, openldapReader(t))
	// "uid,cn eq,sub" is one value and two indexes. Taking cn away means
	// rewriting the value uid is written in, which is not a deletion.
	shared := snapshot.ConfigResource{Section: SectionPerformance, Kind: KindIndex,
		Name: "dc=alder,dc=test/cn", Label: "cn"}
	if _, refusal := RemoveRecord(live, shared); refusal != RefusalIndexShared {
		t.Errorf("removing a shared index was refused with %q, want %q", refusal, RefusalIndexShared)
	}
	// One that has a value to itself comes out.
	alone := snapshot.ConfigResource{Section: SectionPerformance, Kind: KindIndex,
		Name: "dc=alder,dc=test/objectclass", Label: "objectClass"}
	record, refusal := RemoveRecord(live, alone)
	if refusal != "" {
		t.Fatalf("removing objectClass's index was refused: %s", refusal)
	}
	if len(record.Mods) != 1 || record.Mods[0].Op != directory.ModDelete ||
		string(record.Mods[0].Values[0]) != "objectClass eq" {
		t.Fatalf("the removal is %+v, want the value it is written as", record.Mods)
	}
}

func TestAnIndexTheServerKeepsForItselfIsNotRemoved(t *testing.T) {
	live := capture(t, ds389Reader(t))
	system := snapshot.ConfigResource{Section: SectionPerformance, Kind: KindIndex,
		Name: "dc=alder,dc=test/objectclass", Label: "objectclass"}
	if _, refusal := RemoveRecord(live, system); refusal != RefusalSystemIndex {
		t.Errorf("removing a system index was refused with %q, want %q", refusal, RefusalSystemIndex)
	}
	ordinary := snapshot.ConfigResource{Section: SectionPerformance, Kind: KindIndex,
		Name: "dc=alder,dc=test/uid", Label: "uid"}
	record, refusal := RemoveRecord(live, ordinary)
	if refusal != "" {
		t.Fatalf("removing an ordinary index was refused: %s", refusal)
	}
	if record.Type != directory.ChangeDelete ||
		record.DN.String() != "cn=uid,cn=index,cn=userRoot,cn=ldbm database,cn=plugins,cn=config" {
		t.Fatalf("the removal is %v on %s", record.Type, record.DN)
	}
}

func TestAnIndexValueIsReadTheWaySlapdWritesIt(t *testing.T) {
	for _, tc := range []struct {
		value      string
		attributes string
		types      string
	}{
		{"mail eq", "mail", "eq"},
		{"uid,cn eq,sub", "uid|cn", "eq|sub"},
		{"objectClass", "objectClass", ""},
		{"  sn   sub,eq  ", "sn", "eq|sub"},
		{"", "", ""},
	} {
		attributes, types := splitIndexValue(tc.value)
		if strings.Join(attributes, "|") != tc.attributes || strings.Join(types, "|") != tc.types {
			t.Errorf("%q read as %v %v, want %q %q", tc.value, attributes, types, tc.attributes, tc.types)
		}
	}
}
