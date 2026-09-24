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

// 1.16: creating and removing a configuration object, against the live
// servers.
//
// One kind, one precondition. The harness loads the memberof module and
// deliberately leaves the overlay unconfigured, so this creates it through the
// ordinary path -- a comparison proposes it, a plan judges it, an apply makes
// it -- reads the server to see that it is really there, and removes it again
// the same way.

const overlayID = "overlay:" + suffix + "/memberof"

func TestAnOverlayIsCreatedAndRemovedThroughAlder(t *testing.T) {
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
		if _, exists := snap.ResourceByID(overlayID); exists {
			t.Skipf("%s already has the overlay", s.name)
		}

		// A document that wants the overlay: the capture, with the object and
		// nothing else added.
		wanted := rebuiltConfig(t, before, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			return append(resources, snapshot.ConfigResource{Section: "plugins", Kind: "overlay",
				Name: suffix + "/memberof", Label: "memberof"}), settings
		})

		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+wanted+`}}`)
		object := configObject(t, d, overlayID)
		if s.name != "openldap" {
			// 389 DS has no overlays: the object is reported and nothing is
			// offered, which is the whole point of naming one kind.
			if object.Actionable == api.ConfigActionableWritable {
				t.Fatalf("%s offered to create an overlay: %+v", s.name, object)
			}
			return
		}
		if object.Kind != api.DiffKindAdded || object.Actionable != api.ConfigActionableWritable {
			t.Fatalf("object: %s", mustEncode(t, object))
		}
		if object.Candidate == nil || len(object.Candidate.Changes) != 1 {
			t.Fatalf("no candidate: %s", mustEncode(t, object))
		}
		if object.Candidate.Changes[0].Type != api.ChangeRequestTypeAdd {
			t.Fatalf("the change is %s, not an add", object.Candidate.Changes[0].Type)
		}

		created := false
		t.Cleanup(func() {
			if !created {
				return
			}
			// Whatever happened below, the harness goes back as it was.
			entry := overlayEntry(t, sess)
			if entry != "" {
				_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, entry), Type: directory.ChangeDelete})
			}
		})

		planAndApplyChanges(t, client, base, object.Candidate.Changes)
		created = true

		// The server has it, under the name the server chose.
		entry := overlayEntry(t, sess)
		if entry == "" {
			t.Fatal("the overlay was applied and the server does not have it")
		}
		if !strings.Contains(strings.ToLower(entry), "olcoverlay={") {
			t.Errorf("the server did not assign a position: %s", entry)
		}

		// And a capture now holds it, as an object of its own.
		after := captureConfig(t, client, base)
		afterSnap, _, err := snapshot.DecodeConfig([]byte(after))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := afterSnap.ResourceByID(overlayID); !ok {
			t.Fatalf("the capture does not hold the overlay: %v", resourceIDsOf(afterSnap))
		}

		// Removing it: proposed only against a document without it, and only
		// when the removal is asked for.
		back := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+before+`}}`)
		removal := configObject(t, back, overlayID)
		if removal.Kind != api.DiffKindRemoved || removal.Destructive == nil || !*removal.Destructive {
			t.Fatalf("removal: %s", mustEncode(t, removal))
		}
		if removal.Candidate == nil || len(removal.Candidate.Changes) != 1 ||
			removal.Candidate.Changes[0].Type != api.ChangeRequestTypeDelete {
			t.Fatalf("no deletion candidate: %s", mustEncode(t, removal))
		}
		planAndApplyChanges(t, client, base, removal.Candidate.Changes)
		created = false

		if entry := overlayEntry(t, sess); entry != "" {
			t.Fatalf("the overlay is still there: %s", entry)
		}
		// The configuration is what it was before any of this.
		if got := configChecksum(t, client, base); got != snap.Checksum {
			t.Fatalf("the configuration did not come back: %s want %s", got, snap.Checksum)
		}
	})
}

func TestAnOverlayWhoseModuleIsNotLoadedIsNotOffered(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.name != "openldap" {
			t.Skip("overlays are OpenLDAP's")
		}
		client, base := alderSession(t, s, true)
		before := captureConfig(t, client, base)
		// An overlay whose module this server has not loaded. The refusal has
		// to come from Alder, before anything is sent: the server's own answer
		// arrives only after a write, and says "handler exited with 1".
		wanted := rebuiltConfig(t, before, func(_ *snapshot.ConfigCapture,
			resources []snapshot.ConfigResource, settings []snapshot.ConfigSetting) ([]snapshot.ConfigResource, []snapshot.ConfigSetting) {
			return append(resources, snapshot.ConfigResource{Section: "plugins", Kind: "overlay",
				Name: suffix + "/dynlist", Label: "dynlist"}), settings
		})
		d := configDiff(t, client, base, `{"source":{"live":{"kind":"config"}},"target":{"snapshot":`+wanted+`}}`)
		object := configObject(t, d, "overlay:"+suffix+"/dynlist")
		if object.Actionable == api.ConfigActionableWritable {
			t.Fatalf("an overlay with no module was offered: %s", mustEncode(t, object))
		}
		if object.Refusal == nil || *object.Refusal != "module_not_loaded" {
			t.Fatalf("refusal: %s", mustEncode(t, object))
		}
	})
}

// overlayEntry is the DN of the memberof overlay on the live server, or empty.
func overlayEntry(t *testing.T, sess directory.Session) string {
	t.Helper()
	res, err := sess.Search(ctx(t), directory.SearchRequest{
		BaseDN: mustDN(t, "cn=config"), Scope: directory.ScopeSubtree,
		Filter: filter.Equal("olcOverlay", "memberof"), Attributes: []string{"1.1"}, Limit: 10, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("looking for the overlay: %v", err)
	}
	for _, entry := range res.Entries {
		if strings.Contains(strings.ToLower(entry.DN.String()), "memberof") {
			return entry.DN.String()
		}
	}
	return ""
}

func configObject(t *testing.T, d api.Diff, id string) api.ConfigDiffObject {
	t.Helper()
	for _, object := range d.Config.Objects {
		if strings.EqualFold(object.Id, id) {
			return object
		}
	}
	t.Fatalf("the comparison has no object %s: %s", id, mustEncode(t, d.Config.Objects))
	return api.ConfigDiffObject{}
}

func resourceIDsOf(s *snapshot.ConfigSnapshot) []string {
	out := make([]string, 0, len(s.Resources))
	for _, r := range s.Resources {
		out = append(out, r.ID())
	}
	return out
}
