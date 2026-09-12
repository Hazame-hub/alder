package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

func teamEntry(t *testing.T, uid string, teams ...string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, "uid="+uid+",ou=people,dc=alder,dc=test"))
	e.Set("objectClass", [][]byte{[]byte("top"), []byte("inetOrgPerson")})
	if len(teams) > 0 {
		vals := make([][]byte, 0, len(teams))
		for _, v := range teams {
			vals = append(vals, []byte(v))
		}
		e.Set("alderTeam", vals)
	}
	return e
}

func plainKind() schema.AttributeKind {
	return schema.AttributeKind{Name: "alderTeam", Kind: schema.KindString}
}

func rowFor2(res inventoryResult, want string) *InventoryRow {
	for i := range res.Values {
		if res.Values[i].Value.Text != nil && *res.Values[i].Value.Text == want {
			return &res.Values[i]
		}
	}
	return nil
}

func TestTallyCountsEntriesPerValue(t *testing.T) {
	entries := []*directory.Entry{
		teamEntry(t, "a", "platform"),
		teamEntry(t, "b", "platform"),
		teamEntry(t, "c", "network"),
	}
	got := tally(entries, "alderTeam", plainKind(), 200)

	if got.WithValue != 3 || got.WithoutValue != 0 {
		t.Errorf("withValue=%d withoutValue=%d", got.WithValue, got.WithoutValue)
	}
	if got.DistinctValues != 2 {
		t.Errorf("distinct is %d, want 2", got.DistinctValues)
	}
	if r := rowFor2(got, "platform"); r == nil || r.Entries != 2 {
		t.Errorf("platform row is %+v, want 2 entries", r)
	}
}

// The feature's whole purpose: a value one entry holds among many is usually a
// misspelling of one that many hold.
func TestTallyReportsSingletonsAsTheTypoSignal(t *testing.T) {
	entries := []*directory.Entry{
		teamEntry(t, "a", "platform"),
		teamEntry(t, "b", "platform"),
		teamEntry(t, "c", "platform"),
		teamEntry(t, "d", "platfrm"),
	}
	got := tally(entries, "alderTeam", plainKind(), 200)

	if got.SingletonValues != 1 {
		t.Errorf("singletons is %d, want 1 — the typo", got.SingletonValues)
	}
	// And it is findable: commonest first, so the rare one is last.
	last := got.Values[len(got.Values)-1]
	if last.Value.Text == nil || *last.Value.Text != "platfrm" {
		t.Errorf("the rare value is not last: %v", got.Values)
	}
}

// An entry that does not hold the attribute is part of the answer — "how many
// people have no team" is the same question from the other side.
func TestTallyCountsEntriesWithoutTheAttribute(t *testing.T) {
	entries := []*directory.Entry{
		teamEntry(t, "a", "platform"),
		teamEntry(t, "b"),
		teamEntry(t, "c"),
	}
	got := tally(entries, "alderTeam", plainKind(), 200)

	if got.WithValue != 1 || got.WithoutValue != 2 {
		t.Errorf("withValue=%d withoutValue=%d, want 1 and 2", got.WithValue, got.WithoutValue)
	}
}

// An entry holding one value twice is one entry, not two.
func TestAnEntryCountsOncePerDistinctValue(t *testing.T) {
	e := teamEntry(t, "a")
	e.Set("alderTeam", [][]byte{[]byte("platform"), []byte("platform")})
	got := tally([]*directory.Entry{e}, "alderTeam", plainKind(), 200)

	if r := rowFor2(got, "platform"); r == nil || r.Entries != 1 {
		t.Errorf("platform row is %+v, want 1 entry", r)
	}
	if got.WithValue != 1 {
		t.Errorf("withValue is %d, want 1", got.WithValue)
	}
}

// alderTeam and alderTeam;lang-fr are the same attribute.
func TestAttributeOptionsAreTalliedTogether(t *testing.T) {
	e := teamEntry(t, "a")
	e.Set("alderTeam;lang-fr", [][]byte{[]byte("plateforme")})
	got := tally([]*directory.Entry{e}, "alderTeam", plainKind(), 200)

	if got.WithValue != 1 || got.DistinctValues != 1 {
		t.Errorf("an option-carrying attribute was not tallied: %+v", got)
	}
}

// Values are bytes. Folding two spellings into one row would invent an equality
// the directory never agreed to.
func TestTallyDoesNotFoldCase(t *testing.T) {
	entries := []*directory.Entry{
		teamEntry(t, "a", "Platform"),
		teamEntry(t, "b", "platform"),
	}
	got := tally(entries, "alderTeam", plainKind(), 200)

	if got.DistinctValues != 2 {
		t.Errorf("distinct is %d; two spellings were merged into one row", got.DistinctValues)
	}
}

// The tail is reported, never dropped: a row count that silently omits values
// is the same lie as a truncated tally that does not say so.
func TestTheTailIsCountedRatherThanDropped(t *testing.T) {
	entries := []*directory.Entry{}
	for _, v := range []string{"a", "b", "c", "d", "e"} {
		entries = append(entries, teamEntry(t, v, v))
	}
	got := tally(entries, "alderTeam", plainKind(), 2)

	if len(got.Values) != 2 {
		t.Fatalf("returned %d rows past a cap of 2", len(got.Values))
	}
	if got.DistinctValues != 5 {
		t.Errorf("distinct is %d, want the exact 5 even though 2 rows were returned",
			got.DistinctValues)
	}
	if got.OtherValues != 3 || got.OtherEntries != 3 {
		t.Errorf("the tail is %d values / %d entries, want 3 and 3",
			got.OtherValues, got.OtherEntries)
	}
}

func TestTallyOfNothingIsEmptyNotNil(t *testing.T) {
	got := tally(nil, "alderTeam", plainKind(), 200)
	if got.Values == nil {
		t.Error("got nil; an empty list serialises as [] and nil as null")
	}
}

// Refused before the search runs, which is the difference between never reading
// a password and reading every password and choosing not to say.
func TestSensitiveAttributesAreRefusedNotFiltered(t *testing.T) {
	sch := testSchema(t)
	for _, name := range []string{"userPassword", "userpassword", "userPassword;binary"} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := inventoryTarget(sch, name); err == nil {
				t.Error("a sensitive attribute was accepted for inventory")
			}
		})
	}
}

// The same RFC 4512 check the filter builder makes, reused rather than written
// twice — so an attribute name that is really a filter fragment dies here.
func TestAnAttributeThatIsNotAnAttributeIsRefused(t *testing.T) {
	sch := testSchema(t)
	for _, bad := range []string{"alderTeam)(uid=*", "", "   ", "(objectClass=*)"} {
		if _, _, err := inventoryTarget(sch, bad); err == nil {
			t.Errorf("%q was accepted as an attribute to inventory", bad)
		}
	}
}

func TestAnOrdinaryAttributeIsAcceptedAndCanonicalised(t *testing.T) {
	sch := testSchema(t)
	name, _, err := inventoryTarget(sch, "CN")
	if err != nil {
		t.Fatalf("cn was refused: %v", err)
	}
	if !strings.EqualFold(name, "cn") {
		t.Errorf("canonical name is %q", name)
	}
}

// --- the streaming tally ----------------------------------------------------

// bulkTeamEntries makes n entries, each holding one of four teams, so a tally
// of them has a known shape whatever the paging does.
func bulkTeamEntries(t *testing.T, n int) []*directory.Entry {
	t.Helper()
	teams := []string{"platform", "network", "data", "release"}
	out := make([]*directory.Entry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, teamEntry(t, fmt.Sprintf("bulk%05d", i), teams[i%len(teams)]))
	}
	return out
}

func inventoryOf(t *testing.T, rig *testRig, limit int) InventoryResponse {
	t.Helper()
	body := fmt.Sprintf(
		`{"baseDn":"dc=alder,dc=test","scope":"sub","attribute":"alderTeam","limit":%d,"maxValues":200}`,
		limit)
	res := rig.do(t, http.MethodPost, "/api/v1/inventory", strings.NewReader(body))
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d: %s", res.Status, res.Body)
	}
	var out InventoryResponse
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return out
}

// The point of the change: a tally follows the server's pages to the end,
// rather than stopping at whatever one search returned.
//
// The old code issued a single Search and counted what came back, which looked
// correct against a fake that returned everything at once and silently examined
// one page against a real one.
func TestTheTallyFollowsEveryPage(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries:  bulkTeamEntries(t, 2500),
		pageSize: 100,
	})

	got := inventoryOf(t, rig, 250000)

	if got.Examined != 2500 {
		t.Errorf("examined %d of 2500; the tally stopped at a page boundary", got.Examined)
	}
	if got.WithValue != 2500 {
		t.Errorf("withValue is %d, want 2500", got.WithValue)
	}
	if got.DistinctValues != 4 {
		t.Errorf("distinct is %d, want 4", got.DistinctValues)
	}
	if got.Truncated {
		t.Error("truncated, though every page was read to the end")
	}
	if rig.fake.searchCount() < 2 {
		t.Errorf("%d searches for 25 pages: the loop is not paging", rig.fake.searchCount())
	}
}

// And it stops where it was told to, saying so, rather than reading a directory
// to the end because it was asked a question about part of it.
func TestTheTallyStopsAtTheLimitAndSaysSo(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries:  bulkTeamEntries(t, 2500),
		pageSize: 100,
	})

	got := inventoryOf(t, rig, 250)

	if got.Examined != 250 {
		t.Errorf("examined %d, want exactly the 250 asked for", got.Examined)
	}
	if !got.Truncated {
		t.Error("not truncated, though 2250 entries were never looked at")
	}
	if got.Limit != 250 {
		t.Errorf("limit reported as %d", got.Limit)
	}
}

// A tally of everything reports truncated false, which is the whole difference
// between an answer about a directory and an answer about a page of it.
func TestATallyThatReachedTheEndIsNotTruncated(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries:  bulkTeamEntries(t, 300),
		pageSize: 100,
	})

	got := inventoryOf(t, rig, 250000)

	if got.Truncated {
		t.Error("truncated after reading every entry there is")
	}
	if got.Examined != 300 {
		t.Errorf("examined %d, want 300", got.Examined)
	}
}
