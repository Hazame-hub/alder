//go:build conformance

package conformance

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// 1.23: indexes, against the live servers.
//
// "Index title for equality" is one intention and two entirely different
// writes: a value added to the database entry on OpenLDAP, an entry of its own
// under the backend on 389 DS. This is the test that says the two are the same
// object as far as an operator is concerned -- the same identity, the same
// difference, the same button -- and that each server gets the write it
// actually wants.
//
// title is indexed on neither harness server, so the round trip starts and
// ends with a configuration identical to the one it found.

const (
	indexAttribute = "title"
	indexID        = "index:" + suffix + "/" + indexAttribute
)

func TestAnIndexIsCreatedAndRemovedOnBothServers(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		client, base := alderSession(t, s, true)
		before := captureConfig(t, client, base)
		snap, _, err := snapshot.DecodeConfig([]byte(before))
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := snap.ResourceByID(indexID); exists {
			t.Skipf("%s already indexes %s", s.name, indexAttribute)
		}
		// Whatever the servers hold, they hold it under one identity: an
		// index is named by the backend and the attribute on both.
		if len(indexesOf(snap)) == 0 {
			t.Fatalf("%s reports no indexes at all; resources are %v", s.name, resourceIDsOf(snap))
		}

		wanted := rebuiltConfig(t, before, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			return append(resources, snapshot.ConfigResource{Section: "performance", Kind: "index",
				Name: suffix + "/" + indexAttribute, Label: indexAttribute}), settings
		})

		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+wanted+`}}`)
		object := configObject(t, d, indexID)
		if object.Kind != api.DiffKindAdded || object.Actionable != api.ConfigActionableWritable {
			t.Fatalf("%s: object: %s", s.name, mustEncode(t, object))
		}
		// An index with no types recorded is created for equality, and the
		// comparison says so rather than leaving the reader to guess.
		if object.Types == nil || strings.Join(*object.Types, ",") != "eq" {
			t.Errorf("%s: the comparison does not say what the index covers: %s", s.name, mustEncode(t, object))
		}
		if object.Candidate == nil || len(object.Candidate.Changes) != 1 {
			t.Fatalf("%s: no candidate: %s", s.name, mustEncode(t, object))
		}
		// The write each server wants, which is not the same write.
		change := object.Candidate.Changes[0]
		if s.name == "openldap" {
			if change.Type != api.ChangeRequestTypeModify {
				t.Fatalf("OpenLDAP index is created by %s, want a value on the database entry", change.Type)
			}
		} else if change.Type != api.ChangeRequestTypeAdd {
			t.Fatalf("389 DS index is created by %s, want an entry of its own", change.Type)
		}

		created := false
		t.Cleanup(func() {
			if !created {
				return
			}
			// The harness goes back as it was whatever happened below.
			restoreIndex(t, sess, s)
		})

		planAndApplyChanges(t, client, base, object.Candidate.Changes)
		created = true

		// The server really holds it, read from the server rather than from
		// Alder's own answer.
		if where := indexOnServer(t, sess); where == "" {
			t.Fatalf("%s: the index was applied and the server does not have it", s.name)
		}
		after := captureConfig(t, client, base)
		afterSnap, _, err := snapshot.DecodeConfig([]byte(after))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := afterSnap.ResourceByID(indexID); !ok {
			t.Fatalf("%s: the capture does not hold the index: %v", s.name, indexesOf(afterSnap))
		}

		// And taking it away is the same journey backwards, asked for by name.
		back := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+before+`}}`)
		removal := configObject(t, back, indexID)
		if removal.Kind != api.DiffKindRemoved || removal.Destructive == nil || !*removal.Destructive {
			t.Fatalf("%s: removal: %s", s.name, mustEncode(t, removal))
		}
		if removal.Candidate == nil || len(removal.Candidate.Changes) != 1 {
			t.Fatalf("%s: no deletion candidate: %s", s.name, mustEncode(t, removal))
		}
		planAndApplyChanges(t, client, base, removal.Candidate.Changes)
		created = false

		if where := indexOnServer(t, sess); where != "" {
			t.Fatalf("%s: the index is still there: %s", s.name, where)
		}
		if got := configChecksum(t, client, base); got != snap.Checksum {
			t.Fatalf("%s: the configuration did not come back: %s want %s", s.name, got, snap.Checksum)
		}
	})
}

// TestAnIndexTheServerKeepsForItselfIsNotOffered holds the one refusal that
// exists only on 389 DS: an index marked nsSystemIndex is the server's own,
// and removing it is not something Alder offers.
func TestAnIndexTheServerKeepsForItselfIsNotOffered(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.name == "openldap" {
			t.Skip("a system index is 389 DS's own idea")
		}
		client, base := alderSession(t, s, true)
		before := captureConfig(t, client, base)
		snap, _, err := snapshot.DecodeConfig([]byte(before))
		if err != nil {
			t.Fatal(err)
		}
		system := ""
		for _, setting := range snap.Settings {
			if !strings.EqualFold(setting.Key, "nsSystemIndex") ||
				!strings.HasPrefix(setting.Resource, "index:") {
				continue
			}
			if len(setting.Values) == 1 && strings.EqualFold(setting.Values[0], "true") {
				system = setting.Resource
				break
			}
		}
		if system == "" {
			t.Skip("this server reports no system index")
		}

		// A document without it: the removal that a comparison would propose.
		without := rebuiltConfig(t, before, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			kept := resources[:0:0]
			for _, r := range resources {
				if !strings.EqualFold(r.ID(), system) {
					kept = append(kept, r)
				}
			}
			// The settings that belong to it go with it: a document that
			// keeps them names a resource it does not hold.
			left := settings[:0:0]
			for _, setting := range settings {
				if !strings.EqualFold(setting.Resource, system) {
					left = append(left, setting)
				}
			}
			return kept, left
		})
		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+without+`}}`)
		object := configObject(t, d, system)
		if object.Actionable == api.ConfigActionableWritable {
			t.Fatalf("removing a system index was offered: %s", mustEncode(t, object))
		}
		if object.Refusal == nil || *object.Refusal != "system_index" {
			t.Fatalf("refusal: %s", mustEncode(t, object))
		}
	})
}

// TestAnIndexSharingItsValueIsNotRemoved holds the refusal that exists only on
// OpenLDAP: one olcDbIndex value may name several attributes, and taking one
// of them out is a rewrite of the others' index rather than a deletion. The
// harness writes such a value on purpose, because slaptest never produces one.
func TestAnIndexSharingItsValueIsNotRemoved(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.name != "openldap" {
			t.Skip("one value naming several attributes is OpenLDAP's")
		}
		client, base := alderSession(t, s, true)
		before := captureConfig(t, client, base)
		snap, _, err := snapshot.DecodeConfig([]byte(before))
		if err != nil {
			t.Fatal(err)
		}
		shared := ""
		for _, setting := range snap.Settings {
			if !strings.EqualFold(setting.Key, "olcDbIndex") || len(setting.Values) != 1 {
				continue
			}
			names, _, _ := strings.Cut(setting.Values[0], " ")
			if strings.Contains(names, ",") {
				shared = setting.Resource
				break
			}
		}
		if shared == "" {
			t.Skip("no index on this server shares its value with another")
		}

		without := rebuiltConfig(t, before, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			kept := resources[:0:0]
			for _, r := range resources {
				if !strings.EqualFold(r.ID(), shared) {
					kept = append(kept, r)
				}
			}
			left := settings[:0:0]
			for _, setting := range settings {
				if !strings.EqualFold(setting.Resource, shared) {
					left = append(left, setting)
				}
			}
			return kept, left
		})
		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+without+`}}`)
		object := configObject(t, d, shared)
		if object.Actionable == api.ConfigActionableWritable {
			t.Fatalf("rewriting somebody else's index was offered: %s", mustEncode(t, object))
		}
		if object.Refusal == nil || *object.Refusal != "index_value_shared" {
			t.Fatalf("refusal: %s", mustEncode(t, object))
		}
	})
}

// indexOnServer finds the index on whichever server this is, in the shape that
// server writes it: a value on the database entry, or an entry of its own.
// Empty when the server does not have it.
func indexOnServer(t *testing.T, sess directory.Session) string {
	t.Helper()
	res, err := sess.Search(ctx(t), directory.SearchRequest{
		BaseDN: mustDN(t, "cn=config"), Scope: directory.ScopeSubtree,
		Filter: filter.Or(
			filter.Equal("olcDbIndex", indexAttribute+" eq"),
			filter.And(filter.Equal("objectClass", "nsIndex"), filter.Equal("cn", indexAttribute)),
		),
		Attributes: []string{"1.1"}, Limit: 10, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("looking for the index: %v", err)
	}
	if len(res.Entries) == 0 {
		return ""
	}
	return res.Entries[0].DN.String()
}

// restoreIndex takes the index back off a server the test left it on.
func restoreIndex(t *testing.T, sess directory.Session, s server) {
	t.Helper()
	where := indexOnServer(t, sess)
	if where == "" {
		return
	}
	record := directory.ChangeRecord{DN: mustDN(t, where), Type: directory.ChangeDelete}
	if s.name == "openldap" {
		record = directory.ChangeRecord{DN: mustDN(t, where), Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModDelete, Name: "olcDbIndex",
				Values: [][]byte{[]byte(indexAttribute + " eq")}}}}
	}
	_ = sess.Apply(ctx(t), record)
}

func indexesOf(s *snapshot.ConfigSnapshot) []string {
	var out []string
	for _, r := range s.Resources {
		if r.Kind == "index" {
			out = append(out, r.ID())
		}
	}
	return out
}
