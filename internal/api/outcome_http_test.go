package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
)

// Answering "did my change land?" over HTTP.
//
// The verdicts themselves are decided and tested in internal/outcome. What
// is checked here is the part that only exists at this layer: that the
// question can be asked at all on an instance that refuses writes, that the
// answer carries its reason, and that nothing in the response is an
// invitation to send the change again.

func outcomeRig(t *testing.T, cfg Config, byDN map[string]*directory.Entry) *testRig {
	t.Helper()
	return newRig(t, cfg, &fakeSession{
		caps: directory.Capabilities{NamingContexts: []string{"dc=alder,dc=test"}},
		byDN: byDN,
	})
}

func entryAt(t *testing.T, target string, attrs map[string][]string) *directory.Entry {
	t.Helper()
	parsed, err := dn.Parse(target)
	if err != nil {
		t.Fatalf("dn: %v", err)
	}
	e := directory.NewEntry(parsed)
	for name, values := range attrs {
		out := make([][]byte, 0, len(values))
		for _, v := range values {
			out = append(out, []byte(v))
		}
		e.Set(name, out)
	}
	return e
}

func askOutcome(t *testing.T, rig *testRig, body string) (int, ChangeOutcome) {
	t.Helper()
	res := rig.do(t, http.MethodPost, "/api/v1/changes/outcome", strings.NewReader(body))
	var out ChangeOutcome
	if res.Status == http.StatusOK {
		if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
			t.Fatalf("decoding the outcome: %v\n%s", err, res.Body)
		}
	}
	return res.Status, out
}

const outcomeTarget = "uid=alice,ou=people,dc=alder,dc=test"

func TestAnInterruptedDeleteIsJudgedByReading(t *testing.T) {
	// The entry is gone, so the delete landed.
	rig := outcomeRig(t, Config{}, map[string]*directory.Entry{})
	status, out := askOutcome(t, rig,
		`{"dn":"`+outcomeTarget+`","type":"delete"}`)

	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if out.Verdict != ChangeOutcomeVerdictApplied {
		t.Errorf("got %s: %s", out.Verdict, out.Reason)
	}
	if out.Reason == "" {
		t.Error("a verdict with no reason is a verdict nobody can check")
	}
	if out.Resolvable != nil && *out.Resolvable {
		t.Error("an applied change is not something to put right")
	}
}

func TestAChangeThatDidNotLandIsMarkedResolvable(t *testing.T) {
	// Still holding the old value, so the modification never arrived. This
	// is the only verdict that invites doing anything further -- and even
	// then through the ordinary plan and confirm path, which is why the
	// response carries a flag and not a change to resend.
	rig := outcomeRig(t, Config{}, map[string]*directory.Entry{
		strings.ToLower(outcomeTarget): entryAt(t, outcomeTarget, map[string][]string{
			"objectClass": {"person"}, "title": {"Engineer"},
		}),
	})
	status, out := askOutcome(t, rig,
		`{"dn":"`+outcomeTarget+`","type":"modify","mods":[`+
			`{"op":"replace","name":"title","values":[{"text":"Lead"}]}]}`)

	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if out.Verdict != ChangeOutcomeVerdictNotApplied {
		t.Fatalf("got %s: %s", out.Verdict, out.Reason)
	}
	if out.Resolvable == nil || !*out.Resolvable {
		t.Error("a change that did not land can be made again")
	}
	if out.Attribute == nil || *out.Attribute != "title" {
		t.Errorf("the verdict should name the attribute, got %v", out.Attribute)
	}
}

func TestAPasswordChangeIsReportedAsUnresolvedAndNotResolvable(t *testing.T) {
	rig := outcomeRig(t, Config{}, map[string]*directory.Entry{
		strings.ToLower(outcomeTarget): entryAt(t, outcomeTarget, map[string][]string{
			"objectClass": {"person"},
		}),
	})
	status, out := askOutcome(t, rig,
		`{"dn":"`+outcomeTarget+`","type":"setpassword","newPassword":"whatever"}`)

	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if out.Verdict != ChangeOutcomeVerdictUndeterminable {
		t.Fatalf("a password cannot be checked by reading, got %s: %s", out.Verdict, out.Reason)
	}
	if out.Resolvable != nil && *out.Resolvable {
		t.Error("an unresolved outcome must not be offered as something to put right")
	}
	// And the response must not carry the password back out.
	if strings.Contains(out.Reason, "whatever") {
		t.Errorf("the reason echoes the new password: %s", out.Reason)
	}
}

func TestTheQuestionCanBeAskedOnAReadOnlyInstance(t *testing.T) {
	// The moment an operator most needs to know whether a change landed is
	// the worst moment to tell them to reconnect as somebody who can write.
	// This reads, so it is allowed.
	rig := outcomeRig(t, Config{ReadOnly: true}, map[string]*directory.Entry{})
	status, out := askOutcome(t, rig, `{"dn":"`+outcomeTarget+`","type":"delete"}`)

	if status != http.StatusOK {
		t.Fatalf("a read-only instance refused to answer: status %d", status)
	}
	if out.Verdict != ChangeOutcomeVerdictApplied {
		t.Errorf("got %s: %s", out.Verdict, out.Reason)
	}
}

func TestAnUnreadableEntryLeavesTheOutcomeUnresolved(t *testing.T) {
	// A bind that may write but not read back. Reporting "not applied" here
	// would send somebody to apply a change that may already be in place.
	rig := newRig(t, Config{}, &fakeSession{
		caps:    directory.Capabilities{NamingContexts: []string{"dc=alder,dc=test"}},
		readErr: &ldapdriver.Error{Code: 50, Message: "Insufficient Access Rights"},
	})
	status, out := askOutcome(t, rig, `{"dn":"`+outcomeTarget+`","type":"delete"}`)

	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if out.Verdict != ChangeOutcomeVerdictUndeterminable {
		t.Fatalf("got %s: %s", out.Verdict, out.Reason)
	}
	if out.Resolvable != nil && *out.Resolvable {
		t.Error("an unresolved outcome is not resolvable")
	}
}

func TestAnUnreadableBodyIsRefusedWithoutGuessing(t *testing.T) {
	rig := outcomeRig(t, Config{}, map[string]*directory.Entry{})
	res := rig.do(t, http.MethodPost, "/api/v1/changes/outcome", strings.NewReader(`{"dn":`))
	if res.Status != http.StatusBadRequest {
		t.Errorf("status %d: %s", res.Status, res.Body)
	}
}

func TestTheOutcomeEndpointNeedsASession(t *testing.T) {
	rig := outcomeRig(t, Config{}, map[string]*directory.Entry{})
	res := rig.anonymous(t, http.MethodPost, "/api/v1/changes/outcome")
	if res.Status != http.StatusUnauthorized {
		t.Errorf("status %d: %s", res.Status, res.Body)
	}
}
