package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.7: snapshots and comparisons over HTTP.
//
// The fake here is a small directory that can change: writes the apply
// endpoint records are replayed onto it, so a comparison after an apply sees
// what the apply did. That is what lets the invariant at the bottom be stated
// end to end -- capture, drift, compare, plan, apply, compare again, and find
// nothing left to do.

type liveFake struct {
	t     *testing.T
	rig   *testRig
	order []string
	byKey map[string]*directory.Entry
}

func newLiveFake(t *testing.T, entries ...*directory.Entry) *liveFake {
	t.Helper()
	caps := defaultCaps()
	caps.VendorName = "389 Project"
	l := &liveFake{t: t, byKey: map[string]*directory.Entry{}}
	l.rig = newRig(t, Config{}, &fakeSession{caps: caps, sch: testSchema(t)})
	for _, e := range entries {
		l.put(e)
	}
	return l
}

func (l *liveFake) put(e *directory.Entry) {
	key := strings.ToLower(e.DN.String())
	listed := false
	for _, k := range l.order {
		if k == key {
			listed = true
		}
	}
	if !listed {
		l.order = append(l.order, key)
	}
	l.byKey[key] = e
	l.sync()
}

func (l *liveFake) remove(dnText string) {
	delete(l.byKey, strings.ToLower(dnText))
	l.sync()
}

func (l *liveFake) entry(dnText string) *directory.Entry {
	e, ok := l.byKey[strings.ToLower(dnText)]
	if !ok {
		l.t.Fatalf("no entry %s", dnText)
	}
	return e
}

func (l *liveFake) set(dnText, attr string, values ...string) {
	raw := make([][]byte, 0, len(values))
	for _, v := range values {
		raw = append(raw, []byte(v))
	}
	l.entry(dnText).Set(attr, raw)
	l.sync()
}

func (l *liveFake) sync() {
	f := l.rig.fake
	f.entries = nil
	f.byDN = map[string]*directory.Entry{}
	for _, key := range l.order {
		if e, ok := l.byKey[key]; ok {
			f.entries = append(f.entries, e)
			f.byDN[key] = e
		}
	}
}

// replay applies recorded writes to the fake, as a directory would.
func (l *liveFake) replay(records []directory.ChangeRecord) {
	for _, rec := range records {
		switch rec.Type {
		case directory.ChangeAdd:
			e := directory.NewEntry(rec.DN)
			for _, a := range rec.Attrs {
				e.Set(a.Name, a.Values)
			}
			l.put(e)
		case directory.ChangeDelete:
			l.remove(rec.DN.String())
		case directory.ChangeModify:
			e := l.entry(rec.DN.String())
			for _, m := range rec.Mods {
				current := e.Get(m.Name)
				switch m.Op {
				case directory.ModAdd:
					e.Set(m.Name, append(append([][]byte(nil), current...), m.Values...))
				case directory.ModReplace:
					e.Set(m.Name, m.Values)
				case directory.ModDelete:
					if len(m.Values) == 0 {
						dropAttribute(e, m.Name)
						continue
					}
					var kept [][]byte
					for _, v := range current {
						gone := false
						for _, d := range m.Values {
							if string(d) == string(v) {
								gone = true
							}
						}
						if !gone {
							kept = append(kept, v)
						}
					}
					if len(kept) == 0 {
						dropAttribute(e, m.Name)
					} else {
						e.Set(m.Name, kept)
					}
				}
			}
			l.sync()
		default:
			l.t.Fatalf("the fake does not replay %s", rec.Type)
		}
	}
}

func dropAttribute(e *directory.Entry, name string) {
	for key := range e.Attributes {
		if strings.EqualFold(key, name) {
			delete(e.Attributes, key)
		}
	}
	kept := e.Order[:0]
	for _, key := range e.Order {
		if !strings.EqualFold(key, name) {
			kept = append(kept, key)
		}
	}
	e.Order = kept
}

const (
	snapAdmins = "cn=admins,ou=groups,dc=alder,dc=test"
	snapDave   = "uid=dave,ou=people,dc=alder,dc=test"
)

func snapGroup(t *testing.T, members ...string) *directory.Entry {
	t.Helper()
	g := directory.NewEntry(mustParse(t, snapAdmins))
	g.Set("objectClass", [][]byte{[]byte("top"), []byte("groupOfNames")})
	g.Set("cn", [][]byte{[]byte("admins")})
	values := make([][]byte, 0, len(members))
	for _, m := range members {
		values = append(values, []byte(m))
	}
	g.Set("member", values)
	return g
}

func (l *liveFake) capture(body string) string {
	l.t.Helper()
	res := l.rig.do(l.t, http.MethodPost, "/api/v1/snapshots/capture", strings.NewReader(body))
	if res.Status != http.StatusOK {
		l.t.Fatalf("capture: status %d\n%s", res.Status, res.Body)
	}
	return res.Body
}

func (l *liveFake) diff(body string) Diff {
	l.t.Helper()
	res := l.rig.do(l.t, http.MethodPost, "/api/v1/diff", strings.NewReader(body))
	if res.Status != http.StatusOK {
		l.t.Fatalf("diff: status %d\n%s", res.Status, res.Body)
	}
	return decode[Diff](l.t, res)
}

func errorCode(t *testing.T, res response) ErrorError {
	t.Helper()
	return decode[Error](t, res).Error
}

func diffItem(t *testing.T, d Diff, dnText string) DiffItem {
	t.Helper()
	for _, it := range d.Items {
		if (it.TargetDn != nil && strings.EqualFold(*it.TargetDn, dnText)) ||
			(it.SourceDn != nil && strings.EqualFold(*it.SourceDn, dnText)) {
			return it
		}
	}
	t.Fatalf("no item for %s in %+v", dnText, d.Items)
	return DiffItem{}
}

func TestACaptureIsCanonicalAndCarriesNoSecret(t *testing.T) {
	const secret = "{SSHA}c25hcHNob3Qtc2VjcmV0"
	l := newLiveFake(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"title", "Engineer"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}, []string{"userPassword", secret}))
	first := l.capture(`{"base":"dc=alder,dc=test"}`)
	if strings.Contains(first, secret) || strings.Contains(first, "c25hcHNob3Qtc2VjcmV0") {
		t.Fatal("the snapshot carries the password")
	}
	a, integrity, err := snapshot.Decode([]byte(first))
	if err != nil || integrity != snapshot.IntegrityVerified {
		t.Fatalf("decoding the capture: %v (%s)", err, integrity)
	}

	// Reverse the fake's order: the directory is the same, the document must be.
	for i, j := 0, len(l.order)-1; i < j; i, j = i+1, j-1 {
		l.order[i], l.order[j] = l.order[j], l.order[i]
	}
	l.sync()
	b, _, err := snapshot.Decode([]byte(l.capture(`{"base":"dc=alder,dc=test"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if a.Checksum != b.Checksum {
		t.Error("the same directory captured twice has two checksums")
	}
}

func TestACaptureReadsEveryPage(t *testing.T) {
	l := newLiveFake(t)
	for i := 0; i < 7; i++ {
		l.put(personAt(t, fmt.Sprintf("uid=user%d,ou=people,dc=alder,dc=test", i), []string{"cn", "U"}, []string{"sn", "U"}))
	}
	l.rig.fake.pageSize = 2
	s, _, err := snapshot.Decode([]byte(l.capture(`{"base":"dc=alder,dc=test"}`)))
	if err != nil {
		t.Fatal(err)
	}
	if s.EntryCount != 7 {
		t.Errorf("captured %d of 7 entries across pages", s.EntryCount)
	}
}

func TestACaptureRefusesWhatItCannotCaptureWhole(t *testing.T) {
	l := newLiveFake(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
	post := func(body string) response {
		return l.rig.do(t, http.MethodPost, "/api/v1/snapshots/capture", strings.NewReader(body))
	}
	if res := post(`{"base":"cn=subschema"}`); res.Status != http.StatusBadRequest || errorCode(t, res) != ErrorErrorSnapshotScopeUnsupported {
		t.Errorf("schema base: %d %s", res.Status, res.Body)
	}
	if res := post(`{"base":"dc=alder,dc=test","filter":"(cn=unbalanced"}`); res.Status != http.StatusBadRequest {
		t.Errorf("bad filter: %d %s", res.Status, res.Body)
	}
	l.rig.fake.referrals = []string{"ldap://elsewhere.example/ou=remote,dc=alder,dc=test"}
	if res := post(`{"base":"dc=alder,dc=test"}`); res.Status != http.StatusBadRequest || errorCode(t, res) != ErrorErrorSnapshotTooLarge {
		t.Errorf("a read with referrals is not whole: %d %s", res.Status, res.Body)
	}
}

func TestInspectRefusesWhatIsNotAUsableSnapshot(t *testing.T) {
	l := newLiveFake(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
	doc := l.capture(`{"base":"dc=alder,dc=test"}`)
	inspect := func(body string) response {
		return l.rig.do(t, http.MethodPost, "/api/v1/snapshots/inspect", strings.NewReader(body))
	}
	res := inspect(doc)
	if res.Status != http.StatusOK || decode[SnapshotInspection](t, res).Integrity != SnapshotIntegrityVerified {
		t.Fatalf("inspecting a capture: %d %s", res.Status, res.Body)
	}
	for name, tc := range map[string]struct {
		body string
		code ErrorError
	}{
		"edited":         {strings.Replace(doc, `"text": "Alice"`, `"text": "Mallory"`, 1), ErrorErrorSnapshotChecksumMismatch},
		"future version": {strings.Replace(doc, `"version": 1`, `"version": 9`, 1), ErrorErrorSnapshotUnsupportedVersion},
		"not a snapshot": {`{"hello":"world"}`, ErrorErrorSnapshotInvalid},
	} {
		if res := inspect(tc.body); res.Status != http.StatusBadRequest || errorCode(t, res) != tc.code {
			t.Errorf("%s: %d %s", name, res.Status, res.Body)
		}
	}
}

func TestADiffNeedsExactlyOneStatePerSide(t *testing.T) {
	l := newLiveFake(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}))
	for name, body := range map[string]string{
		"both live":    `{"source":{"live":{"base":"dc=alder,dc=test"}},"target":{"live":{"base":"dc=alder,dc=test"}}}`,
		"empty source": `{"source":{},"target":{"live":{"base":"dc=alder,dc=test"}}}`,
	} {
		if res := l.rig.do(t, http.MethodPost, "/api/v1/diff", strings.NewReader(body)); res.Status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, res.Status, res.Body)
		}
	}
}

func TestTwoSnapshotsProposeNothing(t *testing.T) {
	l := newLiveFake(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"title", "Engineer"}))
	before := l.capture(`{"base":"dc=alder,dc=test"}`)
	l.set(planAlice, "title", "Staff")
	after := l.capture(`{"base":"dc=alder,dc=test"}`)
	d := l.diff(`{"source":{"snapshot":` + before + `},"target":{"snapshot":` + after + `}}`)
	it := diffItem(t, d, planAlice)
	if it.Kind != DiffKindModified || it.Candidate != nil {
		t.Errorf("item = %+v; a comparison of two files carries no candidate", it)
	}
	if d.Source.Kind != DiffSideKindSnapshot || d.Target.Integrity == nil || *d.Target.Integrity != SnapshotIntegrityVerified {
		t.Errorf("sides = %+v / %+v", d.Source, d.Target)
	}
}

func TestAPartialLiveReadNeverProposesADelete(t *testing.T) {
	l := newLiveFake(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}))
	s1 := l.capture(`{"base":"dc=alder,dc=test"}`)
	l.put(personAt(t, snapDave, []string{"cn", "Dave"}, []string{"sn", "D"}))
	l.remove(bobDN)
	l.rig.fake.referrals = []string{"ldap://elsewhere.example/ou=remote,dc=alder,dc=test"}

	d := l.diff(`{"source":{"live":{}},"target":{"snapshot":` + s1 + `}}`)
	if d.Complete {
		t.Fatal("a read with referrals produced a complete comparison")
	}
	if it := diffItem(t, d, bobDN); it.Kind != DiffKindUnknown {
		t.Errorf("bob, missing from a partial live read, is %s, want unknown", it.Kind)
	}
	dave := diffItem(t, d, snapDave)
	if dave.Candidate == nil || len(dave.Candidate.Changes) != 0 || dave.Candidate.Blocked == nil ||
		*dave.Candidate.Blocked != DiffBlockedIncompleteComparison {
		t.Errorf("dave's candidate = %+v, want a blocked delete", dave.Candidate)
	}
}

func TestAnAttributeTheLiveDirectoryHidesIsUnknown(t *testing.T) {
	l := newLiveFake(t, personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"title", "Engineer"}))
	s1 := l.capture(`{"base":"dc=alder,dc=test"}`)
	dropAttribute(l.entry(planAlice), "title")
	l.sync()
	l.rig.fake.visibility = map[string]directory.AttributeVisibility{
		strings.ToLower(planAlice + "|title"): directory.VisibilityDenied,
	}
	d := l.diff(`{"source":{"live":{}},"target":{"snapshot":` + s1 + `}}`)
	it := diffItem(t, d, planAlice)
	if d.Complete || it.Attributes == nil || (*it.Attributes)[0].Kind != DiffKindUnknown {
		t.Fatalf("complete = %v, item = %+v", d.Complete, it)
	}
	if it.Candidate != nil && len(it.Candidate.Changes) > 0 {
		t.Errorf("a hidden attribute was proposed for writing: %+v", it.Candidate.Changes)
	}
}

// The invariant: a difference describes state, and acting on it still goes
// through the plan -- after which the same comparison finds nothing.
func TestSnapshotDiffPlanApplyConverges(t *testing.T) {
	l := newLiveFake(t,
		personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"}, []string{"title", "Engineer"}),
		personAt(t, bobDN, []string{"cn", "Bob"}, []string{"sn", "B"}),
		snapGroup(t, planAlice, bobDN))
	s1 := l.capture(`{"base":"dc=alder,dc=test"}`)

	// The directory drifts from S1.
	l.set(planAlice, "title", "Senior Engineer")
	l.set(planAlice, "description", "added after the snapshot")
	l.set(snapAdmins, "member", planAlice, carolDN)
	l.remove(bobDN)
	l.put(personAt(t, snapDave, []string{"cn", "Dave"}, []string{"sn", "D"}))

	body := `{"source":{"live":{}},"target":{"snapshot":` + s1 + `}}`
	d := l.diff(body)
	if !d.Complete || d.Counts.Modified != 2 || d.Counts.Added != 1 || d.Counts.Removed != 1 {
		t.Fatalf("complete = %v, counts = %+v, reasons = %+v", d.Complete, d.Counts, d.Reasons)
	}

	// Select every non-destructive candidate, and the delete explicitly.
	var selected []ChangeRequest
	for _, it := range d.Items {
		if it.Candidate == nil || it.Candidate.Blocked != nil {
			t.Fatalf("item without a usable candidate: %+v", it)
		}
		if it.Candidate.Destructive {
			if it.SourceDn == nil || !strings.EqualFold(*it.SourceDn, snapDave) {
				t.Fatalf("an unexpected destructive candidate: %+v", it)
			}
		}
		selected = append(selected, it.Candidate.Changes...)
	}
	encoded, _ := json.Marshal(selected)

	p := mustPlan(t, l.rig.do(t, http.MethodPost, "/api/v1/plan", strings.NewReader(`{"changes":`+string(encoded)+`}`)))
	if len(p.Items) != len(selected) {
		t.Fatalf("planned %d items for %d changes", len(p.Items), len(selected))
	}
	for i, item := range p.Items {
		if item.Baseline == nil || item.Record == nil {
			t.Fatalf("change %d did not plan as applicable: %+v", i, item)
		}
		selected[i].Baseline = item.Baseline
	}
	encoded, _ = json.Marshal(selected)
	res := l.rig.do(t, http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(`{"changes":`+string(encoded)+`}`))
	if res.Status != http.StatusOK || decode[ChangesetResult](t, res).AppliedCount != len(selected) {
		t.Fatalf("apply: %d %s", res.Status, res.Body)
	}
	if len(l.rig.fake.applied) != len(p.Items) {
		t.Fatalf("the directory received %d operations for %d planned", len(l.rig.fake.applied), len(p.Items))
	}
	for i, item := range p.Items {
		executedMatchesPlanned(t, i, l.rig.fake.applied[i], *item.Record)
	}
	l.replay(l.rig.fake.applied)

	again := l.diff(body)
	if again.Counts.Added+again.Counts.Removed+again.Counts.Modified+again.Counts.Renamed+again.Counts.Unknown != 0 {
		t.Errorf("after applying the plan the comparison still differs: %+v\nitems: %+v", again.Counts, again.Items)
	}
}
