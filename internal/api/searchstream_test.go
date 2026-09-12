package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
)

// The search response is streamed, which is a change to how the document is
// written and to nothing else. These are the guards on "and to nothing else":
// the same fields, the same values, every page, and a failure that cannot be
// mistaken for a short answer.

func searchBody(limit, pageSize int) string {
	return fmt.Sprintf(
		`{"baseDn":"dc=alder,dc=test","scope":"sub","filter":"(objectClass=*)","limit":%d,"pageSize":%d}`,
		limit, pageSize)
}

func postSearch(t *testing.T, rig *testRig, body string) response {
	t.Helper()
	res := rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(body))
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d: %s", res.Status, res.Body)
	}
	return res
}

// Every page is streamed, not just the first. A handler that hands its whole
// limit to the driver in one call gets the same entries back and looks correct;
// what it does not do is keep them out of memory, which is the entire point.
func TestTheSearchStreamsEveryPage(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 250), pageSize: 25,
		// driverPaging, so that a handler which hands its whole limit to the
		// driver is given all 250 in one call and fails the count below. Without
		// it the fake refuses to return more than a page whatever it is asked
		// for, and the regression this test exists for passes.
		driverPaging: true,
	})

	res := postSearch(t, rig, searchBody(1000, 25))
	out := decode[SearchResponse](t, res)

	if len(out.Entries) != 250 {
		t.Errorf("%d entries came back, want 250", len(out.Entries))
	}
	if rig.fake.searches < 10 {
		t.Errorf("%d searches for 250 entries at 25 a page: the handler is not paging",
			rig.fake.searches)
	}
	// A deferred cancel on the request context cuts the stream off at its first
	// page, because the stream writer runs after the handler has returned. The
	// fake honours the context precisely so that mistake fails here.
	if len(out.Entries) == 25 {
		t.Error("exactly one page came back: the context died with the handler")
	}
}

// The order of a JSON object's members carries no meaning, so writing the
// entries first and the rest last is not a change to the contract. This is the
// assertion that it stayed that way: same keys, same values.
func TestTheStreamedSearchIsTheSameDocument(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 40), pageSize: 10,
	})

	res := postSearch(t, rig, searchBody(1000, 10))

	if !strings.HasPrefix(res.Body, `{"entries":[`) {
		t.Errorf("the document does not lead with its entries:\n%.80s", res.Body)
	}

	// The same answer built the way the handler used to build it: one struct,
	// marshalled whole. Compared against that rather than against the streamed
	// body decoded and re-encoded, which would only prove the body agrees with
	// itself -- a dropped rdn or a duplicated key survives that and not this.
	out := decode[SearchResponse](t, res)
	sch := rig.fake.sch
	want := SearchResponse{Entries: make([]SearchResultEntry, 0, len(rig.fake.entries))}
	for _, e := range rig.fake.entries {
		want.Entries = append(want.Entries, SearchResultEntry{
			Dn:         e.DN.String(),
			Rdn:        ptr(rdnLabel(e.DN)),
			Attributes: ptr(entryAttributes(e, sch, sch.Requirements(e.ObjectClasses()))),
		})
	}
	// How long it took is the one thing that cannot be predicted, and streaming
	// does not change what it means.
	want.Took, want.Command = out.Took, out.Command

	whole, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshalling the whole response: %v", err)
	}
	var streamed, materialised map[string]any
	if err := json.Unmarshal([]byte(res.Body), &streamed); err != nil {
		t.Fatalf("the streamed body is not JSON: %v", err)
	}
	if err := json.Unmarshal(whole, &materialised); err != nil {
		t.Fatalf("the materialised body is not JSON: %v", err)
	}
	if !reflect.DeepEqual(streamed, materialised) {
		t.Errorf("the streamed document differs from the same answer marshalled whole:\n%s\n\n%s",
			res.Body, whole)
	}
}

// searchTail is a second declaration of the response's fields, and a second
// declaration is a thing to forget. This drives a search where every one of
// them has a value -- truncated, with a cookie, with referrals -- and asks that
// all of them arrived, so a field added to SearchResponse and not to the tail
// fails here rather than going quietly missing from the UI.
func TestTheStreamedSearchSendsEveryFieldItDeclares(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 60), pageSize: 10,
		referrals: []string{"ldap://elsewhere.alder.test/dc=alder,dc=test"},
	})

	res := postSearch(t, rig, searchBody(30, 10))

	var streamed map[string]any
	if err := json.Unmarshal([]byte(res.Body), &streamed); err != nil {
		t.Fatalf("the streamed body is not JSON: %v", err)
	}
	rt := reflect.TypeOf(SearchResponse{})
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if _, ok := streamed[name]; !ok {
			t.Errorf("the streamed response has no %q, which SearchResponse declares", name)
		}
	}
}

// Referrals arrive one page at a time and have to be collected across all of
// them. A handler that reported only the last page's would drop most of them.
func TestTheStreamedSearchCollectsReferralsFromEveryPage(t *testing.T) {
	fake := &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 30), pageSize: 10,
		referrals: []string{"ldap://elsewhere.alder.test/dc=alder,dc=test"},
	}
	rig := newRig(t, Config{}, fake)

	out := decode[SearchResponse](t, postSearch(t, rig, searchBody(1000, 10)))

	if out.Referrals == nil || len(*out.Referrals) != 3 {
		t.Fatalf("referrals are %v, want one from each of three pages", out.Referrals)
	}
}

// A search that asks for seven entries must ask the directory for seven. The
// page loop makes it easy to ask for a page instead, and a server without the
// paging control answers a size limit rather than a cookie: it would send
// ninety-three entries nobody wanted and the handler would throw them away.
func TestTheStreamedSearchNeverAsksForMoreThanTheLimit(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 500), pageSize: 100,
	})

	out := decode[SearchResponse](t, postSearch(t, rig, searchBody(7, 100)))

	if got := rig.fake.lastSearch; got == nil || got.Limit != 7 {
		t.Errorf("the directory was asked for %v, want 7", got)
	}
	if len(out.Entries) != 7 {
		t.Errorf("%d entries came back, want 7", len(out.Entries))
	}
}

// Truncation and the cookie are decided after the last entry is written, which
// is the one thing streaming genuinely changes about them.
func TestTheStreamedSearchStillReportsTruncation(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 500), pageSize: 50,
	})

	out := decode[SearchResponse](t, postSearch(t, rig, searchBody(100, 50)))

	if len(out.Entries) != 100 {
		t.Fatalf("%d entries, want the 100 asked for", len(out.Entries))
	}
	if !out.Truncated {
		t.Error("a search cut short at its limit does not say so")
	}
	if out.Cookie == nil || *out.Cookie == "" {
		t.Error("no cookie came back, so the next page cannot be asked for")
	}

	whole := decode[SearchResponse](t, postSearch(t, rig, searchBody(1000, 50)))
	if whole.Truncated {
		t.Error("a search that returned everything claims to be truncated")
	}
	if whole.Cookie != nil {
		t.Errorf("a finished search handed back a cookie: %q", *whole.Cookie)
	}
}

// Rule 6, on a rewritten surface: the value of a sensitive attribute does not
// travel because the response is now written a piece at a time.
func TestTheStreamedSearchStillWithholdsSecrets(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: []*directory.Entry{entryFixture(t)},
	})

	res := postSearch(t, rig, searchBody(100, 100))

	if strings.Contains(res.Body, "averyrealsecret") {
		t.Error("the search response carries a password")
	}
	if !strings.Contains(res.Body, "userPassword") {
		t.Error("userPassword vanished; it should be reported as set and withheld")
	}
}

// Nothing matching is a 200 with an empty array, not an error and not a
// document that omits the field.
func TestTheStreamedSearchAnswersNothingWithAnEmptyArray(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})

	res := postSearch(t, rig, searchBody(100, 100))

	if !strings.HasPrefix(res.Body, `{"entries":[],`) {
		t.Errorf("an empty search is not an empty array:\n%.80s", res.Body)
	}
	out := decode[SearchResponse](t, res)
	if out.Entries == nil || len(out.Entries) != 0 {
		t.Errorf("entries is %v, want an empty array", out.Entries)
	}
}

// A directory that refuses the search is still a proper status code, because
// the first page is fetched before a byte is sent.
func TestASearchThatFailsBeforeTheFirstByteIsStillAStatus(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		searchErr: errors.New("the directory is not having it"),
	})

	res := rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(searchBody(100, 100)))

	if res.Status == fiber.StatusOK {
		t.Fatalf("a refused search answered 200:\n%s", res.Body)
	}
	if strings.Contains(res.Body, `"entries"`) {
		t.Errorf("the error came back as a search response:\n%s", res.Body)
	}
}

// A failure partway through cannot be a status code, and the response has no
// field for it. So the document is abandoned unterminated: a client's parse
// fails loudly, where a closed object holding half the entries would be
// indistinguishable from a complete answer.
func TestASearchThatFailsMidStreamDoesNotLookComplete(t *testing.T) {
	rig := newRig(t, Config{}, &fakeSession{
		caps: defaultCaps(), sch: testSchema(t),
		entries: bulkExportEntries(t, 100), pageSize: 10,
		failAfterSearches: 3,
	})

	res := rig.do(t, http.MethodPost, "/api/v1/search", strings.NewReader(searchBody(1000, 10)))

	// The status was settled before the failure, so it is 200 and cannot say so.
	if res.Status != fiber.StatusOK {
		t.Fatalf("got %d, and the point is that the status was already sent", res.Status)
	}
	var out SearchResponse
	if err := json.Unmarshal([]byte(res.Body), &out); err == nil {
		t.Errorf("a search that died after 30 of 100 entries parsed as a whole response:\n%s",
			lastLines(res.Body, 3))
	}
	if !strings.Contains(res.Body, "this search failed after 30 entries") {
		t.Errorf("the body does not say what happened:\n%s", lastLines(res.Body, 6))
	}
	if strings.Contains(res.Body, `"truncated"`) {
		t.Error("the abandoned document still claims a truncation, which reads as a short answer")
	}
}
