package changepkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// 1.11: the change package format.
//
// A package is intent, written down. These are the rules that make it portable:
// it carries no secret and no plan token, its dependencies are explicit and
// well formed, and the same intent is the same bytes.

var created = time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

func dataItem(id, target, label string) Item {
	return Item{ID: id, Kind: KindData, Label: label, Data: &DataChange{
		DN: target, Type: OpModify,
		Mods: []Mod{{Op: "replace", Name: "title", Values: []Value{{Text: "Senior Engineer"}}}},
	}}
}

func addItem(id, target string) Item {
	return Item{ID: id, Kind: KindData, Data: &DataChange{
		DN: target, Type: OpAdd,
		Attributes: []Attribute{
			{Name: "objectClass", Values: []Value{{Text: "top"}, {Text: "organizationalUnit"}}},
			{Name: "ou", Values: []Value{{Text: "teams"}}},
		},
	}}
}

const (
	proofAT = "( 1.3.6.1.4.1.99999.7.1 NAME 'alderPackAttr' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	proofOC = "( 1.3.6.1.4.1.99999.7.2 NAME 'alderPackClass' SUP top AUXILIARY MUST alderPackAttr )"
)

func schemaItem(t *testing.T, id, element, op, oid, definition string) Item {
	t.Helper()
	item, err := SchemaItem(id, element, op, oid, definition, "")
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func build(t *testing.T, p *Package) *Package {
	t.Helper()
	out, err := Build(p, created)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func encoded(t *testing.T, p *Package) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, p); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestAPackageIsTheSameBytesForTheSameIntent(t *testing.T) {
	first := build(t, &Package{ID: "p1", Title: "add a title", Changes: []Item{
		dataItem("c1", "uid=alice,ou=people,dc=alder,dc=test", "edit alice"),
		schemaItem(t, "c2", ElementAttributeType, SchemaAdd, "", proofAT),
	}})
	// The same intent, given in another order and with the dependency written
	// the other way round.
	second := build(t, &Package{ID: "p1", Title: "add a title", Changes: []Item{
		schemaItem(t, "c2", ElementAttributeType, SchemaAdd, "1.3.6.1.4.1.99999.7.1",
			"(  1.3.6.1.4.1.99999.7.1   NAME 'alderPackAttr'  SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 EQUALITY caseIgnoreMatch )"),
		dataItem("c1", "uid=alice,ou=people,dc=alder,dc=test", "edit alice"),
	}})
	if encoded(t, first) != encoded(t, second) {
		t.Errorf("the same intent serialised differently:\n%s\n%s", encoded(t, first), encoded(t, second))
	}
	if first.Checksum != second.Checksum || first.Checksum == "" {
		t.Errorf("checksums %q and %q", first.Checksum, second.Checksum)
	}

	// createdAt is outside the checksum, as in every other Alder artifact.
	later, err := Build(&Package{ID: "p1", Title: "add a title", Changes: first.Changes}, created.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if later.Checksum != first.Checksum {
		t.Errorf("the checksum moved with createdAt: %s vs %s", later.Checksum, first.Checksum)
	}
}

func TestAPackageRoundTripsAndVerifies(t *testing.T) {
	p := build(t, &Package{
		Title:       "team schema and its first user",
		Description: "adds an attribute type, the class that needs it, and one entry",
		Source:      Provenance{Method: MethodChangeset, Vendor: "389 Project", AlderVersion: "1.11.0"},
		Assumptions: Assumptions{NamingContexts: []string{"dc=alder,dc=test"}},
		Changes: []Item{
			schemaItem(t, "c1", ElementAttributeType, SchemaAdd, "", proofAT),
			func() Item {
				item := schemaItem(t, "c2", ElementObjectClass, SchemaAdd, "", proofOC)
				item.DependsOn = []string{"c1"}
				return item
			}(),
			addItem("c3", "ou=teams,dc=alder,dc=test"),
		},
	})
	if p.ID == "" || len(p.ID) != 36 {
		t.Errorf("identifier %q is not a UUID", p.ID)
	}
	if p.Counts != (Counts{Changes: 3, Data: 1, Schema: 2}) {
		t.Errorf("counts %+v", p.Counts)
	}

	back, integrity, err := Decode([]byte(encoded(t, p)))
	if err != nil {
		t.Fatal(err)
	}
	if integrity != IntegrityVerified {
		t.Errorf("integrity %q", integrity)
	}
	if encoded(t, back) != encoded(t, p) {
		t.Error("a package changed by being read and written again")
	}
	if !strings.Contains(encoded(t, p), "\"dependsOn\": [\n        \"c1\"\n      ]") {
		t.Errorf("the dependency is not in the document:\n%s", encoded(t, p))
	}
}

func TestOrderComesFromTheGraphAndNotTheArray(t *testing.T) {
	// The class before the attribute type it needs, and the entry last, all
	// given in the wrong order on purpose.
	entry := addItem("entry", "ou=teams,dc=alder,dc=test")
	entry.DependsOn = []string{"class"}
	class := schemaItem(t, "class", ElementObjectClass, SchemaAdd, "", proofOC)
	class.DependsOn = []string{"attr"}
	attr := schemaItem(t, "attr", ElementAttributeType, SchemaAdd, "", proofAT)

	p := build(t, &Package{ID: "p", Changes: []Item{entry, class, attr}})
	var ids []string
	for _, item := range p.Changes {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, " ") != "attr class entry" {
		t.Fatalf("order %v", ids)
	}
	order, err := p.Order()
	if err != nil || strings.Join(order, " ") != "attr class entry" {
		t.Fatalf("order %v (%v)", order, err)
	}

	// Independent items sort by identifier, so the order is the same every
	// time rather than the order they happened to arrive in.
	flat := build(t, &Package{ID: "p", Changes: []Item{
		dataItem("c3", "uid=c,dc=alder,dc=test", ""),
		dataItem("c1", "uid=a,dc=alder,dc=test", ""),
		dataItem("c2", "uid=b,dc=alder,dc=test", ""),
	}})
	ids = nil
	for _, item := range flat.Changes {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, " ") != "c1 c2 c3" {
		t.Errorf("independent items: %v", ids)
	}
}

func TestDecodeRefuses(t *testing.T) {
	good := build(t, &Package{ID: "p", Changes: []Item{
		dataItem("c1", "uid=alice,ou=people,dc=alder,dc=test", ""),
		schemaItem(t, "c2", ElementAttributeType, SchemaAdd, "", proofAT),
	}})
	doc := encoded(t, good)

	cases := map[string]struct {
		body string
		code string
	}{
		"not a package":     {`{"format":"alder-snapshot","version":1}`, CodeNotPackage},
		"not JSON":          {`nonsense`, CodeNotPackage},
		"a later version":   {strings.Replace(doc, `"version": 1`, `"version": 2`, 1), CodeUnsupportedVersion},
		"version zero":      {strings.Replace(doc, `"version": 1`, `"version": 0`, 1), CodeUnsupportedVersion},
		"an unknown field":  {strings.Replace(doc, `"id": "p"`, `"id": "p",  "baseline": "smuggled"`, 1), CodeInvalid},
		"trailing content":  {doc + "{}", CodeInvalid},
		"an edited change":  {strings.Replace(doc, "Senior Engineer", "Staff Engineer", 1), CodeChecksumMismatch},
		"a duplicate id":    {strings.Replace(doc, `"id": "c2"`, `"id": "c1"`, 1), CodeInvalid},
		"a missing id":      {strings.Replace(doc, `"id": "c2"`, `"id": ""`, 1), CodeInvalid},
		"a missing subject": {strings.Replace(doc, `"dn": "uid=alice,ou=people,dc=alder,dc=test"`, `"dn": "not a dn"`, 1), CodeInvalid},
		"a bad definition":  {strings.Replace(doc, "NAME 'alderPackAttr'", "NAME 'alder PackAttr'", 1), CodeInvalid},
		"a wrong count":     {strings.Replace(doc, `"changes": 2`, `"changes": 3`, 1), CodeInvalid},
		"a wrong destructive": {strings.Replace(doc, `"destructive": false,
      "data"`, `"destructive": true,
      "data"`, 1), CodeInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if tc.body == doc {
				t.Fatal("the case does not change the document")
			}
			_, _, err := Decode([]byte(tc.body))
			var e *Error
			if err == nil {
				t.Fatalf("accepted")
			}
			if !errorsAs(err, &e) || e.Code != tc.code {
				t.Fatalf("error %v, want code %s", err, tc.code)
			}
		})
	}
}

func errorsAs(err error, target **Error) bool { return errors.As(err, target) }

func TestADependencyGraphMustBeUsable(t *testing.T) {
	cases := map[string][]Item{
		"a dependency that is not in the package": {
			func() Item {
				i := dataItem("c1", "uid=a,dc=alder,dc=test", "")
				i.DependsOn = []string{"nowhere"}
				return i
			}(),
		},
		"a change that depends on itself": {
			func() Item { i := dataItem("c1", "uid=a,dc=alder,dc=test", ""); i.DependsOn = []string{"c1"}; return i }(),
		},
		"a cycle": {
			func() Item { i := dataItem("c1", "uid=a,dc=alder,dc=test", ""); i.DependsOn = []string{"c2"}; return i }(),
			func() Item { i := dataItem("c2", "uid=b,dc=alder,dc=test", ""); i.DependsOn = []string{"c1"}; return i }(),
		},
	}
	for name, changes := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Build(&Package{ID: "p", Changes: changes}, created); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestSecretsAreNeverPackaged(t *testing.T) {
	// A password change cannot become an item at all.
	if _, err := FromRecord("c1", passwordRecord(t), ""); err == nil {
		t.Fatal("a password change was packaged")
	} else {
		var notPortable *NotPortableError
		if !asNotPortable(err, &notPortable) || notPortable.Reason != OmittedSecret {
			t.Fatalf("error %v", err)
		}
		if strings.Contains(notPortable.Error(), "hunter2") {
			t.Error("the refusal repeats the password")
		}
	}

	// Nor can a modification of a sensitive attribute, whichever way it
	// arrives: as a record, or written into a document by hand.
	secret := Item{ID: "c1", Kind: KindData, Data: &DataChange{DN: "uid=alice,dc=alder,dc=test", Type: OpModify,
		Mods: []Mod{{Op: "replace", Name: "userPassword", Values: []Value{{Text: "hunter2"}}}}}}
	if _, err := Build(&Package{ID: "p", Changes: []Item{secret}}, created); err == nil {
		t.Fatal("a userPassword modification was packaged")
	}

	p := build(t, &Package{ID: "p", Changes: []Item{dataItem("c1", "uid=alice,dc=alder,dc=test", "")},
		Omitted: []Omitted{{Subject: "uid=alice,dc=alder,dc=test", Kind: KindData, Reason: OmittedSecret,
			Detail: "a password change is not portable"}}})
	if p.Counts.Omitted != 1 {
		t.Errorf("an omission is not counted: %+v", p.Counts)
	}
	if strings.Contains(encoded(t, p), "hunter2") {
		t.Error("the package holds a password")
	}
}

func asNotPortable(err error, target **NotPortableError) bool { return errors.As(err, target) }

func TestHostileTextIsCarriedAsGivenAndBounded(t *testing.T) {
	// Directory content is directory content: a package keeps it exactly, and
	// every place that shows it escapes it. What it refuses is size.
	hostile := "Engineer\u202e\u0007 gnp.exe"
	item := Item{ID: "c1", Kind: KindData, Data: &DataChange{DN: "uid=alice,dc=alder,dc=test", Type: OpModify,
		Mods: []Mod{{Op: "replace", Name: "title", Values: []Value{{Text: hostile}}}}}}
	p := build(t, &Package{ID: "p", Changes: []Item{item}})
	back, _, err := Decode([]byte(encoded(t, p)))
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Changes[0].Data.Mods[0].Values[0].Text; got != hostile {
		t.Errorf("the value changed: %q", got)
	}

	huge := Item{ID: "c1", Kind: KindData, Data: &DataChange{DN: "uid=alice,dc=alder,dc=test", Type: OpModify,
		Mods: []Mod{{Op: "replace", Name: "title", Values: []Value{{Text: strings.Repeat("x", MaxTextBytes+1)}}}}}}
	if _, err := Build(&Package{ID: "p", Changes: []Item{huge}}, created); err == nil {
		t.Error("a value past the bound was accepted")
	}

	many := make([]Item, 0, MaxChanges+1)
	for i := 0; i <= MaxChanges; i++ {
		many = append(many, dataItem("c"+itoa(i), "uid=a,dc=alder,dc=test", ""))
	}
	_, err = Build(&Package{ID: "p", Changes: many}, created)
	var e *Error
	if err == nil || !errorsAs(err, &e) || e.Code != CodeTooLarge {
		t.Errorf("a package past the bound: %v", err)
	}
}

func itoa(n int) string {
	out, err := json.Marshal(n)
	if err != nil {
		return "0"
	}
	return string(out)
}

func TestOnlyAnAddMayAskForDesiredState(t *testing.T) {
	add := addItem("c1", "ou=teams,dc=alder,dc=test")
	add.Intent = IntentDesired
	if _, err := Build(&Package{ID: "p", Changes: []Item{add}}, created); err != nil {
		t.Fatalf("a reconciling add: %v", err)
	}
	modify := dataItem("c1", "uid=alice,dc=alder,dc=test", "")
	modify.Intent = IntentDesired
	if _, err := Build(&Package{ID: "p", Changes: []Item{modify}}, created); err == nil {
		t.Error("a modification asked for desired state and was accepted")
	}
	// Absence is never deletion: there is no way to say "make it look like
	// this" except by naming the operation.
	del := Item{ID: "c1", Kind: KindData, Destructive: true,
		Data: &DataChange{DN: "uid=gone,dc=alder,dc=test", Type: OpDelete}}
	p := build(t, &Package{ID: "p", Changes: []Item{del}})
	if p.Counts.Destructive != 1 || !p.Changes[0].Destructive {
		t.Errorf("a delete is not marked destructive: %+v", p.Counts)
	}
}

func TestDependenciesAreDerivedFromTheChangesThemselves(t *testing.T) {
	// Nobody said what depends on what: an object class needs its attribute
	// type, an entry needs the class it uses and the parent it goes under.
	attr := schemaItem(t, "attr", ElementAttributeType, SchemaAdd, "", proofAT)
	class := schemaItem(t, "class", ElementObjectClass, SchemaAdd, "", proofOC)
	parent := addItem("parent", "ou=teams,dc=alder,dc=test")
	child := Item{ID: "child", Kind: KindData, Data: &DataChange{
		DN: "uid=pat,ou=teams,dc=alder,dc=test", Type: OpAdd,
		Attributes: []Attribute{
			{Name: "objectClass", Values: []Value{{Text: "top"}, {Text: "person"}, {Text: "alderPackClass"}}},
			{Name: "cn", Values: []Value{{Text: "Pat"}}},
			{Name: "sn", Values: []Value{{Text: "Doe"}}},
			{Name: "alderPackAttr", Values: []Value{{Text: "platform"}}},
		}}}

	p := build(t, &Package{ID: "p", Changes: Derive([]Item{child, parent, class, attr})})
	deps := map[string][]string{}
	for _, item := range p.Changes {
		deps[item.ID] = item.DependsOn
	}
	if !slices.Contains(deps["class"], "attr") {
		t.Errorf("the class does not depend on its attribute type: %v", deps["class"])
	}
	for _, needs := range []string{"class", "parent", "attr"} {
		if !slices.Contains(deps["child"], needs) {
			t.Errorf("the entry does not depend on %s: %v", needs, deps["child"])
		}
	}
	var ids []string
	for _, item := range p.Changes {
		ids = append(ids, item.ID)
	}
	if strings.Join(ids, " ") != "attr class parent child" {
		t.Errorf("order %v", ids)
	}

	// Deletions go the other way: the child before the parent.
	deletions := Derive([]Item{
		{ID: "parent", Kind: KindData, Destructive: true, Data: &DataChange{DN: "ou=teams,dc=alder,dc=test", Type: OpDelete}},
		{ID: "child", Kind: KindData, Destructive: true, Data: &DataChange{DN: "uid=pat,ou=teams,dc=alder,dc=test", Type: OpDelete}},
	})
	removal := build(t, &Package{ID: "p", Changes: deletions})
	if removal.Changes[0].ID != "child" {
		t.Errorf("removal order %s then %s", removal.Changes[0].ID, removal.Changes[1].ID)
	}
}
