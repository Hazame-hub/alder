package changepkg

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// 1.11: a package validated against a target.
//
// The same package is carried to each environment unchanged. What differs is
// the answer, and these tests are mostly about that: ready here, already
// satisfied there, and a missing dependency somewhere else.

var errNoSuchEntry = errors.New("no such entry")

type fakeTarget struct {
	caps    directory.Capabilities
	sch     *schema.Schema
	entries map[string]*directory.Entry
	stored  map[string][]string
	reads   int
}

func (f *fakeTarget) Capabilities() directory.Capabilities { return f.caps }

func (f *fakeTarget) RefreshSchema(context.Context) (*schema.Schema, error) { return f.sch, nil }

func (f *fakeTarget) Read(_ context.Context, target dn.DN, _ []string) (*directory.Entry, error) {
	f.reads++
	if entry, ok := f.entries[strings.ToLower(target.String())]; ok {
		return entry, nil
	}
	return nil, errNoSuchEntry
}

func (f *fakeTarget) SchemaDefinitions(_ context.Context, target string, kind directory.SchemaDefKind) ([]string, error) {
	return f.stored[strings.ToLower(target)+"|"+string(kind)], nil
}

func targetSchema(t testing.TB, extraAT, extraOC []string) *schema.Schema {
	t.Helper()
	return schema.Load("cn=schema", map[string][]string{
		schema.AttrAttributeTypes: append([]string{
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.11 NAME 'ou' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.12 NAME 'title' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 0.9.2342.19200300.100.1.1 NAME 'uid' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			"( 2.5.4.4 NAME 'sn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		}, extraAT...),
		schema.AttrObjectClasses: append([]string{
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou )",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( cn $ sn ) MAY title )",
		}, extraOC...),
	})
}

func newTarget(t testing.TB, sch *schema.Schema, entries ...*directory.Entry) *fakeTarget {
	t.Helper()
	target := &fakeTarget{
		caps: directory.Capabilities{
			VendorName: "389 Project", NamingContexts: []string{"dc=alder,dc=test"},
			SubschemaSubentry: "cn=schema",
			SchemaWrite: directory.SchemaWrite{
				Style:             directory.SchemaStyleSubschema,
				Targets:           []directory.SchemaTarget{{DN: "cn=schema", Name: "cn=schema"}},
				AttributeTypeAttr: "attributeTypes", ObjectClassAttr: "objectClasses",
			},
		},
		sch: sch, entries: map[string]*directory.Entry{}, stored: map[string][]string{},
	}
	for _, e := range entries {
		target.entries[strings.ToLower(e.DN.String())] = e
	}
	return target
}

func entryAt(t testing.TB, target string, pairs ...[2]string) *directory.Entry {
	t.Helper()
	parsed, err := dn.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	e := directory.NewEntry(parsed)
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("person")})
	for _, p := range pairs {
		e.Set(p[0], [][]byte{[]byte(p[1])})
	}
	return e
}

func passwordRecord(t *testing.T) directory.ChangeRecord {
	t.Helper()
	target, err := dn.Parse("uid=alice,ou=people,dc=alder,dc=test")
	if err != nil {
		t.Fatal(err)
	}
	return directory.ChangeRecord{DN: target, Type: directory.ChangeSetPassword, NewPassword: "hunter2"}
}

func validate(t *testing.T, p *Package, target *fakeTarget) *Result {
	t.Helper()
	res, err := Validate(context.Background(), p, target, Options{
		NotFound: func(err error) bool { return errors.Is(err, errNoSuchEntry) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func statuses(res *Result) map[string]string {
	out := map[string]string{}
	for _, item := range res.Items {
		out[item.ID] = item.Status
	}
	return out
}

// The package used throughout: a schema attribute type, the class that needs
// it, and an entry that uses the class.
func mixedPackage(t *testing.T) *Package {
	t.Helper()
	attr := schemaItem(t, "attr", ElementAttributeType, SchemaAdd, "", proofAT)
	class := schemaItem(t, "class", ElementObjectClass, SchemaAdd, "", proofOC)
	class.DependsOn = []string{"attr"}
	entry := Item{ID: "entry", Kind: KindData, DependsOn: []string{"class"}, Data: &DataChange{
		DN: "uid=pat,ou=people,dc=alder,dc=test", Type: OpAdd,
		Attributes: []Attribute{
			{Name: "objectClass", Values: []Value{{Text: "top"}, {Text: "person"}, {Text: "alderPackClass"}}},
			{Name: "cn", Values: []Value{{Text: "Pat"}}},
			{Name: "sn", Values: []Value{{Text: "Doe"}}},
			{Name: "alderPackAttr", Values: []Value{{Text: "platform"}}},
		},
	}}
	return build(t, &Package{ID: "p-mixed", Title: "team schema and a user",
		Assumptions: Assumptions{NamingContexts: []string{"dc=alder,dc=test"}},
		Changes:     []Item{entry, class, attr}})
}

func TestAMixedPackageIsReadyInDependencyOrder(t *testing.T) {
	p := mixedPackage(t)
	target := newTarget(t, targetSchema(t, nil, nil),
		entryAt(t, "ou=people,dc=alder,dc=test"))
	res := validate(t, p, target)

	if got := strings.Join(res.Order, " "); got != "attr class entry" {
		t.Fatalf("order %q", got)
	}
	if res.Counts.Ready != 3 {
		t.Fatalf("counts %+v\nitems %+v", res.Counts, res.Items)
	}
	records, _ := res.Records()
	if len(records) != 3 {
		t.Fatalf("records %+v", records)
	}
	// The schema items became modifications of this target's schema entry --
	// the package never said where the schema lives.
	if records[0].Type != directory.ChangeModify || records[0].DN.String() != "cn=schema" {
		t.Errorf("first record %+v", records[0])
	}
	if records[2].Type != directory.ChangeAdd || records[2].DN.String() != "uid=pat,ou=people,dc=alder,dc=test" {
		t.Errorf("last record %+v", records[2])
	}
	for _, a := range res.Assumptions {
		if !a.Satisfied {
			t.Errorf("assumption %+v", a)
		}
	}
}

// The heart of promotion: one package, three environments, three answers.
func TestTheSamePackageIsRevalidatedInEachEnvironment(t *testing.T) {
	p := mixedPackage(t)
	before := encoded(t, p)

	t.Run("an environment that has none of it", func(t *testing.T) {
		target := newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"))
		res := validate(t, p, target)
		if res.Counts.Ready != 3 {
			t.Errorf("counts %+v", res.Counts)
		}
	})

	t.Run("an environment that already has it", func(t *testing.T) {
		pat := entryAt(t, "uid=pat,ou=people,dc=alder,dc=test", [2]string{"cn", "Pat"}, [2]string{"sn", "Doe"})
		target := newTarget(t, targetSchema(t, []string{proofAT}, []string{proofOC}),
			entryAt(t, "ou=people,dc=alder,dc=test"), pat)
		res := validate(t, p, target)
		if res.Counts.AlreadySatisfied != 2 || res.Counts.Conflict != 1 {
			t.Fatalf("counts %+v\nitems %+v", res.Counts, res.Items)
		}
		// The entry is there but is not what the package describes, so it is a
		// conflict rather than a silent overwrite.
		if s := statuses(res); s["attr"] != StatusAlreadySatisfied || s["class"] != StatusAlreadySatisfied ||
			s["entry"] != StatusConflict {
			t.Errorf("statuses %+v", s)
		}
	})

	t.Run("an environment whose schema is not writable", func(t *testing.T) {
		target := newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"))
		target.caps.SchemaWrite = directory.SchemaWrite{Style: directory.SchemaStyleNone,
			Unavailable: "this connection has no writable schema location"}
		res := validate(t, p, target)
		s := statuses(res)
		// The attribute type cannot be written here at all. The class and the
		// entry are not unsupported in themselves -- what they need is simply
		// not going to be there, which is a different answer and a different
		// fix.
		if s["attr"] != StatusUnsupported {
			t.Fatalf("statuses %+v", s)
		}
		if s["class"] != StatusDependencyMissing || s["entry"] != StatusDependencyMissing {
			t.Errorf("statuses %+v", s)
		}
		if res.Counts.Ready != 0 {
			t.Errorf("counts %+v", res.Counts)
		}
	})

	t.Run("an environment that is missing the parent", func(t *testing.T) {
		target := newTarget(t, targetSchema(t, nil, nil))
		res := validate(t, p, target)
		if statuses(res)["entry"] != StatusDependencyMissing {
			t.Errorf("statuses %+v", statuses(res))
		}
	})

	if encoded(t, p) != before {
		t.Error("validating changed the package")
	}
}

func TestADifferentBaselineIsNotOverwrittenSilently(t *testing.T) {
	// The intent: title becomes Senior Engineer.
	p := build(t, &Package{ID: "p-title", Changes: []Item{
		dataItem("c1", "uid=alice,ou=people,dc=alder,dc=test", "promote alice")}})

	cases := map[string]struct {
		title  string
		status string
	}{
		"the title the package expects to change": {"Engineer", StatusReady},
		"the title the package intends":           {"Senior Engineer", StatusAlreadySatisfied},
		"a title nobody mentioned":                {"Staff Engineer", StatusReady},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			alice := entryAt(t, "uid=alice,ou=people,dc=alder,dc=test",
				[2]string{"cn", "Alice"}, [2]string{"sn", "A"}, [2]string{"title", tc.title})
			target := newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"), alice)
			res := validate(t, p, target)
			if got := statuses(res)["c1"]; got != tc.status {
				t.Fatalf("status %s, want %s", got, tc.status)
			}
			// A ready item prepares the modification, and the modification says
			// what it sets -- never what the other environment held.
			if tc.status == StatusReady {
				records, _ := res.Records()
				if len(records) != 1 || string(records[0].Mods[0].Values[0]) != "Senior Engineer" {
					t.Errorf("records %+v", records)
				}
			}
		})
	}

	// An entry that is gone entirely is a conflict, not an add.
	target := newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"))
	res := validate(t, p, target)
	if statuses(res)["c1"] != StatusConflict || res.Items[0].Problems[0].Code != ProblemEntryMissing {
		t.Errorf("a missing entry: %+v", res.Items)
	}
}

func TestDeletionIsExplicitAndChecked(t *testing.T) {
	p := build(t, &Package{ID: "p-del", Changes: []Item{
		{ID: "c1", Kind: KindData, Destructive: true,
			Data: &DataChange{DN: "uid=dave,ou=people,dc=alder,dc=test", Type: OpDelete}},
	}})
	if !p.Changes[0].Destructive || p.Counts.Destructive != 1 {
		t.Fatalf("the package does not mark the deletion: %+v", p.Counts)
	}

	present := newTarget(t, targetSchema(t, nil, nil),
		entryAt(t, "ou=people,dc=alder,dc=test"), entryAt(t, "uid=dave,ou=people,dc=alder,dc=test"))
	res := validate(t, p, present)
	if statuses(res)["c1"] != StatusReady || !res.Items[0].Destructive {
		t.Errorf("a deletion of an entry that is there: %+v", res.Items)
	}

	// Already gone is not a failure, and it is not an instruction to delete
	// something else either.
	absent := newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"))
	res = validate(t, p, absent)
	if statuses(res)["c1"] != StatusAlreadySatisfied {
		t.Errorf("a deletion of an entry that is gone: %+v", res.Items)
	}
}

func TestAChangeOutsideTheDirectoryIsIncompatible(t *testing.T) {
	p := build(t, &Package{ID: "p-elsewhere",
		Assumptions: Assumptions{NamingContexts: []string{"dc=other,dc=test"}},
		Changes:     []Item{dataItem("c1", "uid=alice,ou=people,dc=other,dc=test", "")}})
	target := newTarget(t, targetSchema(t, nil, nil))
	res := validate(t, p, target)
	if statuses(res)["c1"] != StatusTargetIncompatible {
		t.Fatalf("statuses %+v", statuses(res))
	}
	if len(res.Assumptions) != 1 || res.Assumptions[0].Satisfied {
		t.Errorf("assumptions %+v", res.Assumptions)
	}
}

func TestASchemaDefinitionThatDiffersIsAConflict(t *testing.T) {
	// The target defines the same OID as something else.
	other := "( 1.3.6.1.4.1.99999.7.1 NAME 'alderPackAttr' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"
	target := newTarget(t, targetSchema(t, []string{other}, nil), entryAt(t, "ou=people,dc=alder,dc=test"))
	p := build(t, &Package{ID: "p-schema", Changes: []Item{
		schemaItem(t, "attr", ElementAttributeType, SchemaAdd, "", proofAT)}})
	res := validate(t, p, target)
	if statuses(res)["attr"] != StatusConflict || res.Items[0].Problems[0].Code != ProblemDefinitionDiffers {
		t.Fatalf("items %+v", res.Items)
	}

	// Asking to replace something that is not there is a conflict too: there
	// is nothing to replace, and an add is a different intent.
	p = build(t, &Package{ID: "p-replace", Changes: []Item{
		schemaItem(t, "attr", ElementAttributeType, SchemaReplace, "",
			"( 1.3.6.1.4.1.99999.7.9 NAME 'alderMissing' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")}})
	res = validate(t, p, target)
	if statuses(res)["attr"] != StatusConflict || res.Items[0].Problems[0].Code != ProblemDefinitionMissing {
		t.Errorf("items %+v", res.Items)
	}
}

func TestValidationReadsTheDirectoryWithoutWritingIt(t *testing.T) {
	p := mixedPackage(t)
	target := newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"))
	res := validate(t, p, target)
	if res.Counts.Ready == 0 {
		t.Fatalf("counts %+v", res.Counts)
	}
	// The interface validation is given has no write on it at all, so this is
	// structural rather than observed -- but the reads should still be few:
	// one per entry it asks about, not one per attribute.
	if target.reads > 4 {
		t.Errorf("%d reads for %d changes", target.reads, len(p.Changes))
	}
}

func TestAnEntryThatAlreadyMatchesIsSatisfiedNotAConflict(t *testing.T) {
	// The package adds an entry. This directory already has it, exactly as
	// described -- somebody applied this change here last week, or the package
	// came back to where it was made. That is the intent, satisfied.
	add := Item{ID: "entry", Kind: KindData, Data: &DataChange{
		DN: "uid=pat,ou=people,dc=alder,dc=test", Type: OpAdd,
		Attributes: []Attribute{
			{Name: "objectClass", Values: []Value{{Text: "top"}, {Text: "person"}}},
			{Name: "cn", Values: []Value{{Text: "Pat"}}},
			{Name: "sn", Values: []Value{{Text: "Doe"}}},
		}}}
	p := build(t, &Package{ID: "p-add", Changes: []Item{add}})

	pat := entryAt(t, "uid=pat,ou=people,dc=alder,dc=test", [2]string{"cn", "Pat"}, [2]string{"sn", "Doe"})
	target := newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"), pat)
	res := validate(t, p, target)
	if statuses(res)["entry"] != StatusAlreadySatisfied || res.Counts.AlreadySatisfied != 1 {
		t.Fatalf("an entry that already matches: %+v", res.Items)
	}
	if records, _ := res.Records(); len(records) != 0 {
		t.Errorf("something was prepared for a change with nothing to do: %+v", records)
	}

	// The same entry with one value different is a conflict, not an overwrite.
	pat.Set("sn", [][]byte{[]byte("Other")})
	res = validate(t, p, newTarget(t, targetSchema(t, nil, nil), entryAt(t, "ou=people,dc=alder,dc=test"), pat))
	if statuses(res)["entry"] != StatusConflict {
		t.Errorf("an entry that differs: %+v", res.Items)
	}
}
