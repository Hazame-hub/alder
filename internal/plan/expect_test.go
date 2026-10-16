package plan

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

func planOne(t *testing.T, d *fakeDirectory, proposal Proposal) (*Planner, Item) {
	t.Helper()
	pl := newTestPlanner(t)
	p, err := pl.ComputeProposals(t.Context(), d, testSchema(t), []Proposal{proposal}, Options{})
	if err != nil {
		t.Fatalf("ComputeProposals: %v", err)
	}
	return pl, p.Items[0]
}

func expectation(exhaustive bool, pairs ...[]string) *Expectation {
	e := &Expectation{Exhaustive: exhaustive}
	for _, p := range pairs {
		a := directory.Attribute{Name: p[0]}
		for _, v := range p[1:] {
			a.Values = append(a.Values, []byte(v))
		}
		e.Attributes = append(e.Attributes, a)
	}
	return e
}

func TestAnExpectationThatHoldsPlansNormally(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})
	record := modifyRecord(t, alice, directory.ModReplace, "mail", "old@alder.test")

	_, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact,
		// An expected absence holds for an attribute the entry does not have.
		Expect: expectation(false, []string{"mail", "new@alder.test"}, []string{"telephoneNumber"})})
	if item.Action != ActionModify {
		t.Fatalf("action %s (%s: %s), want modify", item.Action, item.Problem, item.Reason)
	}
}

func TestAnExpectationThatNoLongerHoldsIsAConflict(t *testing.T) {
	cases := []struct {
		name   string
		expect *Expectation
		attr   string
	}{
		{"a value changed", expectation(false, []string{"mail", "other@alder.test"}), "mail"},
		{"an expected absence", expectation(false, []string{"description"}), "description"},
		{"an extra value", expectation(false, []string{"description", "Kept"}, []string{"mail"}), "mail"},
		{"an attribute nobody expected", expectation(true, []string{"mail", "new@alder.test"}), "description"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDirectory{}
			d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})
			record := modifyRecord(t, alice, directory.ModReplace, "mail", "old@alder.test")
			_, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact, Expect: tc.expect})
			if item.Action != ActionConflict || item.Problem == nil || item.Problem.Code != ProblemExpectedStateDiffers {
				t.Fatalf("action %s problem %s, want a conflict: expected_state_differs", item.Action, item.Problem)
			}
			if item.Baseline != "" {
				t.Error("a conflict carries a token that would let it be applied")
			}
		})
	}
}

func TestAnExhaustiveExpectationIgnoresWhatTheServerOwns(t *testing.T) {
	d := &fakeDirectory{}
	d.put(t, alice,
		[]string{"objectClass", "top", "person", "inetOrgPerson"},
		[]string{"sn", "Liddell"},
		[]string{"cn", "alice"},
		[]string{"entryUUID", "5f1d"},
		[]string{"modifyTimestamp", "20260101000000Z"},
		[]string{"userPassword", "{SSHA}hash"},
	)
	record := directory.ChangeRecord{DN: mustParse(t, alice), Type: directory.ChangeDelete}
	_, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact,
		Expect: expectation(true, []string{"sn", "Liddell"}, []string{"cn", "alice"})})
	if item.Action != ActionDelete {
		t.Fatalf("action %s (%s: %s), want delete", item.Action, item.Problem, item.Reason)
	}
}

func TestAnExpectationBindsTheBaseline(t *testing.T) {
	// The token covers what the expectation names, so a change to it between
	// plan and apply is stale even though the operation never touches it.
	d := &fakeDirectory{}
	d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})
	record := modifyRecord(t, alice, directory.ModReplace, "mail", "old@alder.test")
	pl, item := planOne(t, d, Proposal{Record: record, Intent: IntentExact,
		Expect: expectation(false, []string{"mail", "new@alder.test"}, []string{"description", "Kept"})})
	if item.Action != ActionModify {
		t.Fatalf("action %s", item.Action)
	}
	if err := pl.Verify(t.Context(), d, record, item.Baseline); err != nil {
		t.Fatalf("Verify of an unchanged entry: %v", err)
	}
	d.put(t, alice, []string{"mail", "new@alder.test"}, []string{"description", "Changed by somebody"})
	if err := pl.Verify(t.Context(), d, record, item.Baseline); !IsStale(err) {
		t.Errorf("Verify after the expected description changed: %v, want stale", err)
	}
}

func TestADefinitionIsExpectedByItsOIDAndNotItsPosition(t *testing.T) {
	const (
		probe = "( 1.3.6.1.4.1.99997.1.7 NAME 'alderProbe' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
		other = "( 1.3.6.1.4.1.99997.1.8 NAME 'alderOther' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"
	)
	live := directory.NewEntry(mustParse(t, alice))
	// Stored where the server keeps positions: the probe is third, and a
	// definition whose OID starts with the probe's is beside it.
	live.Set("olcAttributeTypes", [][]byte{[]byte("{0}" + other),
		[]byte("{1}( 1.3.6.1.4.1.99997.1.70 NAME 'x' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )"), []byte("{2}" + probe)})

	held := func(value []byte) *Expectation {
		return &Expectation{Definitions: []ExpectedDefinition{
			{Attribute: "olcAttributeTypes", OID: "1.3.6.1.4.1.99997.1.7", Value: value}}}
	}
	cases := []struct {
		name   string
		expect *Expectation
		holds  bool
	}{
		{"as stored", held([]byte("{2}" + probe)), true},
		{"at another position", held([]byte("{5}" + probe)), true},
		{"without a position", held([]byte(probe)), true},
		{"rendered differently", held([]byte(
			"{2}( 1.3.6.1.4.1.99997.1.7 NAME 'alderProbe' DESC 'x' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )")), false},
		{"expected absent", held(nil), false},
		{"another OID expected absent", &Expectation{Definitions: []ExpectedDefinition{
			{Attribute: "olcAttributeTypes", OID: "1.3.6.1.4.1.99997.1.9"}}}, true},
	}
	for _, tc := range cases {
		attr, differs := tc.expect.Differs(nil, live)
		if differs == tc.holds {
			t.Errorf("%s: differs %v (%s), want %v", tc.name, differs, attr, !tc.holds)
		}
	}
	if got := held(nil).ReadAttributes(); len(got) != 1 || got[0] != "olcAttributeTypes" {
		t.Errorf("ReadAttributes %v, want the attribute holding the definition", got)
	}
}

func TestAnAccessRuleIsExpectedByteForByte(t *testing.T) {
	// olcAccess is declared with caseIgnoreMatch, but a rule that differs in
	// case alone is a different rule to whoever changed it: a compensation
	// must not take it as the one it expects and write over it.
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrAttributeTypes: {
			"( 1.3.6.1.4.1.4203.1.12.2.3.0.1 NAME 'olcAccess' EQUALITY caseIgnoreMatch " +
				"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 X-ORDERED 'VALUES' )",
			"( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		},
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the schema does not parse: %v", sch.Errors)
	}
	live := directory.NewEntry(mustParse(t, "olcDatabase={1}mdb,cn=config"))
	live.Set("olcAccess", [][]byte{[]byte(`{0}to dn.exact="cn=A" by * none`)})
	live.Set("description", [][]byte{[]byte("Primary")})
	expect := &Expectation{Attributes: []directory.Attribute{
		{Name: "olcAccess", Values: [][]byte{[]byte(`{0}to dn.exact="cn=a" by * none`)}},
	}}
	if _, differs := expect.Differs(sch, live); !differs {
		t.Error("a rule that differs in case alone was taken as the one expected")
	}
	// Other attributes keep their equality rule.
	expect = &Expectation{Attributes: []directory.Attribute{
		{Name: "description", Values: [][]byte{[]byte("primary")}},
	}}
	if attr, differs := expect.Differs(sch, live); differs {
		t.Errorf("description differs (%s), and caseIgnoreMatch says it does not", attr)
	}
}
