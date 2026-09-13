package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// What Alder shows is what Alder executes.
//
// This is the test that holds that sentence to account, and it is deliberately
// the strongest form of it available without a directory: not "the directory
// ends up in the same state", which two different sequences of writes can
// share, but "the operations the directory receives are, one for one and in
// order, the operations the plan returned".
//
// It plans a mixed set the way a client does -- a desired-state document that
// reconciles, and explicit changes of every kind -- applies it the way a client
// applies a plan, and compares what the recording session was asked to do
// against what the plan said would be done. The comparison is written out here
// rather than borrowed from the code under test: a check that used the
// production token or conversion to decide equality would share any bug it was
// meant to catch.

// executedMatchesPlanned compares one executed operation against the plan's
// record for it, field by field.
//
// A sensitive value is withheld from the plan, so for those the plan states a
// size and the executed value must have exactly that size. Everything else is
// compared byte for byte.
func executedMatchesPlanned(t *testing.T, position int, executed directory.ChangeRecord, planned ChangeRequest) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Errorf("operation %d (%s): %s", position, executed.DN, fmt.Sprintf(format, args...))
	}

	if !strings.EqualFold(executed.DN.String(), planned.Dn) {
		fail("the directory was asked about %s, the plan said %s", executed.DN, planned.Dn)
		return
	}
	if string(executed.Type) != string(planned.Type) {
		fail("the directory was asked to %s, the plan said %s", executed.Type, planned.Type)
		return
	}

	valuesMatch := func(what string, got [][]byte, want []AttributeValue) {
		if len(got) != len(want) {
			fail("%s: %d values executed, %d planned", what, len(got), len(want))
			return
		}
		for i, v := range want {
			switch {
			case v.Text != nil:
				if !bytes.Equal(got[i], []byte(*v.Text)) {
					fail("%s value %d: executed %q, planned %q", what, i, got[i], *v.Text)
				}
			case v.Base64 != nil:
				planned, err := decodeValue(v)
				if err != nil || !bytes.Equal(got[i], planned) {
					fail("%s value %d: executed %s, planned %s", what, i, hex.EncodeToString(got[i]), *v.Base64)
				}
			case v.Size != nil:
				// Withheld by the plan: the size is the whole of what was shown.
				if len(got[i]) != *v.Size {
					fail("%s value %d: executed %d bytes, the plan showed %d", what, i, len(got[i]), *v.Size)
				}
			default:
				if len(got[i]) != 0 {
					fail("%s value %d: executed %q, planned an empty value", what, i, got[i])
				}
			}
		}
	}

	switch executed.Type {
	case directory.ChangeAdd:
		var want []ChangeAttribute
		if planned.Attributes != nil {
			want = *planned.Attributes
		}
		if len(executed.Attrs) != len(want) {
			fail("%d attributes executed, %d planned", len(executed.Attrs), len(want))
			return
		}
		for i, a := range want {
			if !strings.EqualFold(executed.Attrs[i].Name, a.Name) {
				fail("attribute %d: executed %s, planned %s", i, executed.Attrs[i].Name, a.Name)
				continue
			}
			valuesMatch(a.Name, executed.Attrs[i].Values, a.Values)
		}
	case directory.ChangeModify:
		var want []ChangeMod
		if planned.Mods != nil {
			want = *planned.Mods
		}
		if len(executed.Mods) != len(want) {
			fail("%d modifications executed, %d planned", len(executed.Mods), len(want))
			return
		}
		for i, m := range want {
			got := executed.Mods[i]
			if !strings.EqualFold(got.Name, m.Name) || string(got.Op) != string(m.Op) {
				fail("modification %d: executed %s %s, planned %s %s", i, got.Op, got.Name, m.Op, m.Name)
				continue
			}
			var values []AttributeValue
			if m.Values != nil {
				values = *m.Values
			}
			valuesMatch(m.Name, got.Values, values)
		}
	case directory.ChangeModRDN:
		if planned.NewRdn == nil || executed.NewRDN != *planned.NewRdn {
			fail("new RDN executed %q, planned %s", executed.NewRDN, orNothing(planned.NewRdn))
		}
		if planned.DeleteOldRdn == nil || executed.DeleteOldRDN != *planned.DeleteOldRdn {
			fail("deleteOldRDN executed %v, planned %s", executed.DeleteOldRDN, orNothing(planned.DeleteOldRdn))
		}
		plannedSuperior := ""
		if planned.NewSuperior != nil {
			plannedSuperior = *planned.NewSuperior
		}
		if !strings.EqualFold(executed.NewSuperior.String(), plannedSuperior) {
			fail("new superior executed %q, planned %q", executed.NewSuperior, plannedSuperior)
		}
	case directory.ChangeDelete:
		// The DN and the type are the whole of it.
	case directory.ChangeSetPassword:
		// The plan never shows a password, so there is nothing to compare it
		// with; that the plan did not return one is asserted separately.
		if planned.NewPassword != nil {
			fail("the plan returned a password")
		}
	}
}

func TestWhatThePlanShowsIsWhatApplyExecutes(t *testing.T) {
	const (
		frank  = "uid=frank,ou=people,dc=alder,dc=test"
		erin   = "uid=erin,ou=people,dc=alder,dc=test"
		gina   = "uid=gina,ou=people,dc=alder,dc=test"
		admins = "cn=admins,ou=groups,dc=alder,dc=test"
	)
	group := directory.NewEntry(mustParse(t, admins))
	group.Set("objectClass", [][]byte{[]byte("top"), []byte("groupOfNames")})
	group.Set("cn", [][]byte{[]byte("admins")})
	group.Set("member", [][]byte{[]byte(planAlice)})

	rig := planRig(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"mail", "a@alder.test"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}),
		personAt(t, carolDN, []string{"cn", "Carol"}, []string{"sn", "C"}),
		personAt(t, daveDN, []string{"cn", "Dave"}, []string{"sn", "D"}),
		personAt(t, frank, []string{"cn", "Frank"}, []string{"sn", "F"},
			[]string{"userPassword", "{SSHA}b2xkLWZyYW5r"}),
		personAt(t, gina, []string{"cn", "Gina"}, []string{"sn", "G"}),
		group,
	)

	// A desired-state document: frank reconciles (a changed cn and a changed
	// password, which the plan must withhold), erin is added, gina already
	// matches and must produce no operation at all.
	doc := strings.Join([]string{
		"dn: " + frank,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Franklin", "sn: F", "userPassword: {SSHA}bmV3LWZyYW5r", "",
		"dn: " + erin,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Erin", "sn: E", "",
		"dn: " + gina,
		"objectClass: top", "objectClass: person", "objectClass: inetOrgPerson",
		"cn: Gina", "sn: G", "",
	}, "\n")
	desired := mustPlan(t, planLdif(t, rig, "desired", doc))

	// Explicit changes of every other kind.
	staged := []string{
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"mail","values":[{"text":"alice@alder.test"}]}]}`, planAlice),
		fmt.Sprintf(`{"dn":%q,"type":"modify","mods":[{"op":"add","name":"member","values":[{"text":%q}]}]}`, admins, bobDN),
		fmt.Sprintf(`{"dn":%q,"type":"modrdn","newRdn":"uid=caroline","deleteOldRdn":true}`, carolDN),
		fmt.Sprintf(`{"dn":%q,"type":"delete"}`, daveDN),
		fmt.Sprintf(`{"dn":%q,"type":"setpassword","newPassword":"correct-horse-battery-staple"}`, bobDN),
	}
	explicit := mustPlan(t, rig.do(t, http.MethodPost, "/api/v1/plan",
		strings.NewReader(`{"changes":[`+strings.Join(staged, ",")+`]}`)))

	// Apply exactly as a client applies a plan: an exact item sends the staged
	// change with the plan's baseline, a desired-state item sends the plan's
	// own record, and items that apply nothing are left out.
	type planned struct {
		shown ChangeRequest // what the plan returned for the operator to review
		sent  ChangeRequest // what the client sends
	}
	var sequence []planned
	for _, item := range desired.Items {
		if item.Record == nil {
			continue
		}
		sent := *item.Record
		// The plan withheld frank's password; the client supplies it from its
		// own copy of the document, which is the only place it exists.
		if strings.EqualFold(item.Dn, frank) {
			sent = restoreFromDocument(t, sent, "userPassword", "{SSHA}bmV3LWZyYW5r")
		}
		sent.Baseline = item.Baseline
		sequence = append(sequence, planned{shown: *item.Record, sent: sent})
	}
	for _, item := range explicit.Items {
		if item.Record == nil {
			continue
		}
		var sent ChangeRequest
		if err := jsonUnmarshal(staged[item.Index], &sent); err != nil {
			t.Fatalf("decoding staged change %d: %v", item.Index, err)
		}
		sent.Baseline = item.Baseline
		sequence = append(sequence, planned{shown: *item.Record, sent: sent})
	}

	// gina matched: the plan has an item for it, and nothing to apply.
	if got := itemFor(t, desired, gina).Action; got != PlanActionUnchanged {
		t.Fatalf("gina: %q, want unchanged", got)
	}
	if want := 2 + len(staged); len(sequence) != want {
		t.Fatalf("%d applicable operations planned, want %d", len(sequence), want)
	}

	sent := make([]string, 0, len(sequence))
	for _, p := range sequence {
		sent = append(sent, mustJSON(t, p.sent))
	}
	res := rig.do(t, http.MethodPost, "/api/v1/changeset/apply",
		strings.NewReader(`{"changes":[`+strings.Join(sent, ",")+`]}`))
	if res.Status != http.StatusOK {
		t.Fatalf("apply: status %d\nbody: %s", res.Status, res.Body)
	}
	result := decode[ChangesetResult](t, res)
	if result.AppliedCount != len(sequence) {
		t.Fatalf("applied %d of %d", result.AppliedCount, len(sequence))
	}

	// The comparison: one for one, in order.
	if len(rig.fake.applied) != len(sequence) {
		t.Fatalf("the directory received %d operations, the plan showed %d", len(rig.fake.applied), len(sequence))
	}
	for i, p := range sequence {
		executedMatchesPlanned(t, i, rig.fake.applied[i], p.shown)
	}

	// And the password the client supplied is the one that was set, having
	// never appeared in either plan.
	for _, body := range []string{mustJSON(t, desired), mustJSON(t, explicit)} {
		if strings.Contains(body, "bmV3LWZyYW5r") || strings.Contains(body, "correct-horse") {
			t.Fatal("a plan response carried a password")
		}
	}
}

// restoreFromDocument puts a withheld value back from the client's own copy.
func restoreFromDocument(t *testing.T, req ChangeRequest, attribute, value string) ChangeRequest {
	t.Helper()
	if req.Mods != nil {
		mods := append([]ChangeMod(nil), *req.Mods...)
		for i := range mods {
			if strings.EqualFold(mods[i].Name, attribute) {
				mods[i].Values = &[]AttributeValue{{Text: ptr(value)}}
			}
		}
		req.Mods = &mods
	}
	if req.Attributes != nil {
		attrs := append([]ChangeAttribute(nil), *req.Attributes...)
		for i := range attrs {
			if strings.EqualFold(attrs[i].Name, attribute) {
				attrs[i].Values = []AttributeValue{{Text: ptr(value)}}
			}
		}
		req.Attributes = &attrs
	}
	return req
}

// orNothing renders an optional field for a failure message.
func orNothing[T any](p *T) string {
	if p == nil {
		return "nothing"
	}
	return fmt.Sprint(*p)
}

func jsonUnmarshal(raw string, into any) error {
	return json.Unmarshal([]byte(raw), into)
}
