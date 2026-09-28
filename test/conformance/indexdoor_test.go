//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/filter"
)

// Indexes, reached without a comparison.
//
// Creating one was possible from 1.23 and only from inside a configuration
// comparison: a row appeared where this server differed from a snapshot of
// one that already had the index you wanted. That makes the feature "copy an
// index another server has", and leaves the ordinary reason to add one -- a
// slow search just diagnosed -- with no path at all. A UI audit went looking
// on the database entry, in the editor and on the directory screen, found
// nothing, and wrote down that you were back in ldapmodify.
//
// The endpoints here are that door. The derivation behind them is the one
// the comparison already used, which is the point: two ways to ask, one way
// to write.

const doorAttribute = "roomNumber"

func indexReport(t *testing.T, client *http.Client, base string) api.IndexReport {
	t.Helper()
	res := get(t, client, base+"/config/indexes")
	if res.status != http.StatusOK {
		t.Fatalf("GET /config/indexes: %d\n%s", res.status, res.body)
	}
	var report api.IndexReport
	if err := json.Unmarshal([]byte(res.body), &report); err != nil {
		t.Fatalf("decoding the index report: %v\n%s", err, res.body)
	}
	return report
}

func dataBackend(t *testing.T, report api.IndexReport) api.IndexBackend {
	t.Helper()
	for _, b := range report.Backends {
		if strings.EqualFold(b.Name, suffix) {
			return b
		}
	}
	t.Fatalf("no backend serving %s; got %v", suffix, backendNames(report))
	return api.IndexBackend{}
}

func backendNames(report api.IndexReport) []string {
	out := make([]string, 0, len(report.Backends))
	for _, b := range report.Backends {
		out = append(out, b.Name)
	}
	return out
}

func indexNamed(backend api.IndexBackend, attribute string) (api.IndexEntry, bool) {
	for _, i := range backend.Indexes {
		if strings.EqualFold(i.Attribute, attribute) {
			return i, true
		}
	}
	return api.IndexEntry{}, false
}

// TestTheIndexDoorListsOnlyBackendsThatHoldData.
//
// 389 Directory Server calls a great many things a backend: on the harness
// the configuration model reports thirty-nine, of which one holds directory
// data and the rest are the ldbm machinery and one entry per default-index
// template. A panel listing all of them is a panel nobody reads, so the
// report keeps the backends serving a naming context the server advertises
// -- its own answer, not a list of names Alder carries.
func TestTheIndexDoorListsOnlyBackendsThatHoldData(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		report := indexReport(t, client, base)

		if report.Provider == "" {
			t.Error("the report does not say which configuration model answered")
		}
		backend := dataBackend(t, report)
		if len(backend.Indexes) == 0 {
			t.Fatalf("%s: the backend serving %s reports no indexes at all", s.name, suffix)
		}
		// Everything listed is a backend somebody would index, rather than
		// the server's own furniture.
		for _, b := range report.Backends {
			if len(b.Indexes) == 0 {
				t.Errorf("%s: %q is listed with no indexes and is not a naming context", s.name, b.Name)
			}
			if b.Dn == "" {
				t.Errorf("%s: backend %q carries no DN, so no screen can tell whether it is the entry in front of it",
					s.name, b.Name)
			}
		}
		t.Logf("%s: %d backend(s) hold data, %s has %d indexes",
			s.name, len(report.Backends), backend.Name, len(backend.Indexes))
	})
}

// TestTheIndexDoorSaysWhyARemovalIsRefused.
//
// Each server has one refusal and it is not the other's. The audit read
// `index_value_shared` off a screen as a bare code, which is a code-golf
// answer to "why can I not remove this".
func TestTheIndexDoorSaysWhyARemovalIsRefused(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		backend := dataBackend(t, indexReport(t, client, base))

		want := "index_value_shared"
		if s.name != "openldap" {
			want = "system_index"
		}
		found := false
		for _, index := range backend.Indexes {
			if index.Blocked == nil {
				if index.Remove == nil {
					t.Errorf("%s: %s offers neither a removal nor a reason", s.name, index.Attribute)
				}
				continue
			}
			found = true
			if index.Remove != nil {
				t.Errorf("%s: %s is refused and carries a change anyway", s.name, index.Attribute)
			}
			if index.BlockedDetail == nil || len(*index.BlockedDetail) < 20 {
				t.Errorf("%s: %s is refused with a code and no sentence: %s",
					s.name, index.Attribute, mustEncode(t, index))
			}
		}
		if !found {
			t.Errorf("%s: no index is refused, so %s went unexercised", s.name, want)
		}
	})
}

// TestAnIndexIsCreatedAndRemovedThroughTheDoor is the round trip an operator
// makes: ask for an attribute to be indexed, read what the server would be
// sent, apply it, then take it away again from the same list.
func TestAnIndexIsCreatedAndRemovedThroughTheDoor(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		backend := dataBackend(t, indexReport(t, client, base))
		if _, exists := indexNamed(backend, doorAttribute); exists {
			t.Skipf("%s already indexes %s", s.name, doorAttribute)
		}

		// An attribute already indexed is not a refusal, and says so.
		already := indexCandidate(t, client, base, backend.Name, backend.Indexes[0].Attribute, nil)
		if already.Exists == nil || !*already.Exists {
			t.Errorf("%s: asking for an index that exists did not say so: %s", s.name, mustEncode(t, already))
		}
		if already.Change != nil {
			t.Errorf("%s: an index that exists was offered a change", s.name)
		}

		candidate := indexCandidate(t, client, base, backend.Name, doorAttribute, []string{"eq", "sub"})
		if candidate.Change == nil {
			t.Fatalf("%s: no change offered for %s: %s", s.name, doorAttribute, mustEncode(t, candidate))
		}
		// The write each server wants, which is not the same write.
		if s.name == "openldap" {
			if candidate.Change.Type != api.ChangeRequestTypeModify {
				t.Errorf("OpenLDAP wants a value on the database entry, got %s", candidate.Change.Type)
			}
		} else if candidate.Change.Type != api.ChangeRequestTypeAdd {
			t.Errorf("389 DS wants an entry of its own, got %s", candidate.Change.Type)
		}

		applied := false
		t.Cleanup(func() {
			if applied {
				// Not restoreIndex: that one is hard-wired to the attribute
				// and the exact value the 1.23 test creates, so it would
				// match nothing here and quietly leave this index behind.
				removeDoorIndex(t, sess, s)
			}
		})
		planAndApplyChanges(t, client, base, []api.ChangeRequest{*candidate.Change})
		applied = true

		// It is on the server, and the door now lists it with a way back.
		after := dataBackend(t, indexReport(t, client, base))
		index, ok := indexNamed(after, doorAttribute)
		if !ok {
			t.Fatalf("%s: applied the change and the door does not list %s: %v",
				s.name, doorAttribute, after.Indexes)
		}
		if got := strings.Join(strs(index.Types), ","); !strings.Contains(got, "eq") {
			t.Errorf("%s: %s was asked for as eq,sub and reads as %q", s.name, doorAttribute, got)
		}
		if index.Remove == nil {
			t.Fatalf("%s: the index just created cannot be removed: %s", s.name, mustEncode(t, index))
		}

		planAndApplyChanges(t, client, base, []api.ChangeRequest{*index.Remove})
		applied = false

		back := dataBackend(t, indexReport(t, client, base))
		if _, still := indexNamed(back, doorAttribute); still {
			t.Fatalf("%s: removed the index and it is still listed", s.name)
		}
		t.Logf("%s: %s indexed and unindexed through the panel, %d indexes before and after",
			s.name, doorAttribute, len(back.Indexes))
	})
}

func indexCandidate(t *testing.T, client *http.Client, base, backend, attribute string,
	types []string) api.IndexCandidate {
	t.Helper()
	body := map[string]any{"backend": backend, "attribute": attribute}
	if types != nil {
		body["types"] = types
	}
	res := post(t, client, base+"/config/indexes/candidate", mustEncode(t, body))
	if res.status != http.StatusOK {
		t.Fatalf("POST /config/indexes/candidate: %d\n%s", res.status, res.body)
	}
	var out api.IndexCandidate
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatalf("decoding the candidate: %v\n%s", err, res.body)
	}
	return out
}

// removeDoorIndex takes the index this test made back off the server,
// whatever value it ended up written as.
//
// The harness has to end as it started: the conformance inventory compares
// configurations, and one index left behind by a test fails a later one with
// a difference nobody wrote.
func removeDoorIndex(t *testing.T, sess directory.Session, s server) {
	t.Helper()
	if s.name != "openldap" {
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN: mustDN(t, "cn=config"), Scope: directory.ScopeSubtree,
			Filter:     filter.And(filter.Equal("objectClass", "nsIndex"), filter.Equal("cn", doorAttribute)),
			Attributes: []string{"1.1"}, Limit: 10, PageSize: 10,
		})
		if err != nil || len(res.Entries) == 0 {
			return
		}
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: res.Entries[0].DN, Type: directory.ChangeDelete})
		return
	}

	// OpenLDAP: the value is deleted as the server holds it, character for
	// character, so it is read back rather than rebuilt.
	res, err := sess.Search(ctx(t), directory.SearchRequest{
		BaseDN: mustDN(t, "cn=config"), Scope: directory.ScopeSubtree,
		Filter:     filter.Present("olcDbIndex"),
		Attributes: []string{"olcDbIndex"}, Limit: 50, PageSize: 50,
	})
	if err != nil {
		return
	}
	for _, entry := range res.Entries {
		for _, value := range entry.GetStrings("olcDbIndex") {
			names, _, _ := strings.Cut(value, " ")
			matched := false
			for _, name := range strings.Split(names, ",") {
				if strings.EqualFold(strings.TrimSpace(name), doorAttribute) {
					matched = true
				}
			}
			if !matched {
				continue
			}
			_ = sess.Apply(ctx(t), directory.ChangeRecord{
				DN: entry.DN, Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModDelete, Name: "olcDbIndex",
					Values: [][]byte{[]byte(value)}}},
			})
		}
	}
}
