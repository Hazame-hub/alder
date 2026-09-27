//go:build conformance

package conformance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.25: the replication view, against four live servers.
//
// Two of them supply and two consume, and the two pairs have nothing in
// common at the wire level: OpenLDAP keeps an incoming link as a value on a
// database entry and records no status for it anywhere; 389 DS keeps its role
// on a replica entry and each outgoing link as an agreement carrying the
// outcome of the last exchange in the server's own words. The assertions
// below never learn which server they are talking to, which is the whole
// point of the four containers.

func replicationOf(t *testing.T, s server, withConfig bool) api.ReplicationReport {
	t.Helper()
	client, base := alderSession(t, s, withConfig)
	res := get(t, client, base+"/replication")
	if res.status != http.StatusOK {
		t.Fatalf("%s: GET /replication: %d\n%s", s.name, res.status, res.body)
	}
	var report api.ReplicationReport
	if err := json.Unmarshal([]byte(res.body), &report); err != nil {
		t.Fatalf("%s: decoding the report: %v\n%s", s.name, err, res.body)
	}
	return report
}

func suffixOf(t *testing.T, report api.ReplicationReport, name string) api.ReplicatedSuffix {
	t.Helper()
	for _, s := range report.Suffixes {
		if strings.EqualFold(s.Dn, name) {
			return s
		}
	}
	t.Fatalf("the report holds nothing about %s: %s", name, mustEncode(t, report))
	return api.ReplicatedSuffix{}
}

func TestASupplierSaysItSupplies(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		report := replicationOf(t, s, true)

		if report.Role != api.ReplicationRoleSupplier {
			t.Fatalf("%s is a supplier and reports %q: %s", s.name, report.Role, report.Why)
		}
		if report.Why == "" || report.Disclaimer == "" {
			t.Errorf("%s: a role with no words behind it: %s", s.name, mustEncode(t, report))
		}
		suffix := suffixOf(t, report, suffix)

		// How far along it is. This is the one measure the two servers can be
		// compared on, and each writes it somewhere completely different.
		if len(suffix.Cursors) == 0 {
			t.Fatalf("%s: the report does not say how far along this server is: %s",
				s.name, mustEncode(t, suffix))
		}
		for _, cursor := range suffix.Cursors {
			if cursor.Origin == "" || cursor.Raw == "" {
				t.Errorf("%s: a change sequence with no origin or no value: %+v", s.name, cursor)
			}
			// It has to be a real moment, not the zero time a failed parse
			// would leave behind. The harness was seeded minutes ago.
			if cursor.At.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("%s: %q was read as %s", s.name, cursor.Raw, cursor.At)
			}
			if cursor.At.After(time.Now().Add(time.Hour)) {
				t.Errorf("%s: %q was read as %s, which is in the future", s.name, cursor.Raw, cursor.At)
			}
		}
		if suffix.ServerId == nil || *suffix.ServerId == "" {
			t.Errorf("%s: the server does not say which identity it writes changes under: %s",
				s.name, mustEncode(t, suffix))
		}
		t.Logf("%s: %s, id %s, %d cursors, %d links", s.name, report.Role,
			derefOr(suffix.ServerId, "?"), len(suffix.Cursors), len(suffix.Links))
	})
}

func TestAConsumerSaysWhereItsChangesComeFrom(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable || s.replicaPort == 0 {
			t.Skip("this server has no consumer in the harness")
		}
		report := replicationOf(t, replicaOf(s), true)

		// A consumer receives. It may also be set up to serve what it
		// received -- OpenLDAP's consumer carries syncprov so that it keeps a
		// contextCSN -- which is "both", and is as true as "consumer".
		if report.Role != api.ReplicationRoleConsumer && report.Role != api.ReplicationRoleBoth {
			t.Fatalf("%s's consumer reports %q: %s", s.name, report.Role, report.Why)
		}
		suffix := suffixOf(t, report, suffix)
		if len(suffix.Cursors) == 0 {
			t.Fatalf("%s's consumer does not say how far along it is: %s", s.name, mustEncode(t, suffix))
		}

		// Whatever the server calls it, the report has to be able to say
		// where the changes come from. On OpenLDAP that is the syncrepl
		// link's provider; on 389 DS the consumer has no link of its own, and
		// the report says so in a note rather than leaving a blank.
		incoming := 0
		for _, link := range suffix.Links {
			// A link is named by what it is, never by where it sits. slapd
			// writes an ordered value with its position in front -- "{0}rid=001
			// provider=..." -- and a report that passed that through would be
			// naming a link by a number that moves when a different one is
			// removed.
			if strings.ContainsAny(link.Name, "{}") {
				t.Errorf("%s: a link named by its position: %q", s.name, link.Name)
			}
			if link.Direction == api.ReplicationIncoming {
				incoming++
				if link.Peer == nil || *link.Peer == "" {
					t.Errorf("%s: an incoming link with no provider: %s", s.name, mustEncode(t, link))
				}
				if link.BindDn == nil || *link.BindDn == "" {
					t.Errorf("%s: an incoming link with no identity: %s", s.name, mustEncode(t, link))
				}
			}
		}
		notes := 0
		if suffix.Notes != nil {
			notes = len(*suffix.Notes)
		}
		if incoming == 0 && notes == 0 {
			t.Fatalf("%s's consumer says neither who it receives from nor why not: %s",
				s.name, mustEncode(t, suffix))
		}
		t.Logf("%s's consumer: %s, %d incoming link(s), %d note(s)", s.name, report.Role, incoming, notes)
	})
}

// TestASupplierReportsWhatItLastSaidAboutEachLink holds the half of the
// feature only 389 DS can answer: the outcome of the last exchange, in the
// server's own words. OpenLDAP records none, and the report says so rather
// than leaving a column that reads as "nothing wrong".
func TestASupplierReportsWhatItLastSaidAboutEachLink(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		report := replicationOf(t, s, true)
		suffix := suffixOf(t, report, suffix)

		outgoing := []api.ReplicationLink{}
		for _, link := range suffix.Links {
			if link.Direction == api.ReplicationOutgoing {
				outgoing = append(outgoing, link)
			}
		}
		if len(outgoing) == 0 {
			// Which is the honest answer for OpenLDAP: it serves whoever
			// asks and records nobody. The suffix must say so.
			if suffix.Notes == nil || len(*suffix.Notes) == 0 {
				t.Fatalf("%s lists no outgoing link and does not say why: %s", s.name, mustEncode(t, suffix))
			}
			t.Logf("%s: no outgoing link listed, and the report explains it", s.name)
			return
		}

		for _, link := range outgoing {
			if link.Name == "" {
				t.Errorf("%s: an unnamed link: %s", s.name, mustEncode(t, link))
			}
			if link.Peer == nil || *link.Peer == "" {
				t.Errorf("%s: a link to nowhere: %s", s.name, mustEncode(t, link))
			}
			// The harness is in step, so the server's last word is a success
			// and the view has to read it as one.
			if link.State != api.ReplicationStateOK {
				t.Errorf("%s: %s is %q and the harness is in step; the server said %q",
					s.name, link.Name, link.State, derefOr(link.Status, ""))
			}
			if link.Status == nil || *link.Status == "" {
				t.Errorf("%s: a verdict with none of the server's own words behind it: %s",
					s.name, mustEncode(t, link))
			}
			if link.LastUpdate == nil {
				t.Errorf("%s: %s has exchanged nothing, on a harness that just replicated",
					s.name, link.Name)
			}
			t.Logf("%s: %s -> %s, %s", s.name, link.Name, derefOr(link.Peer, "?"), link.State)
		}
	})
}

// TestTheReplicationViewNeverCarriesACredential is the one assertion that
// would matter most if it failed. An olcSyncrepl value carries the password
// the consumer binds with, in the clear, inside an ordinary configuration
// attribute; a 389 DS agreement carries an encrypted one. Neither may reach
// the response, whatever else does.
func TestTheReplicationViewNeverCarriesACredential(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skip("the configuration tree is not readable")
		}
		for _, target := range []server{s, replicaOf(s)} {
			if target.port == 0 {
				continue
			}
			client, base := alderSession(t, target, true)
			res := get(t, client, base+"/replication")
			if res.status != http.StatusOK {
				t.Fatalf("%s: GET /replication: %d\n%s", target.name, res.status, res.body)
			}
			for _, secret := range []string{
				s.bindPW, "alder-replication", "credentials=", "nsDS5ReplicaCredentials",
			} {
				if strings.Contains(res.body, secret) {
					t.Fatalf("%s: the replication view carries %q", target.name, secret)
				}
			}
		}
	})
}

func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}
