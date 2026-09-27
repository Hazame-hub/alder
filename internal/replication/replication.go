// Package replication reports what one directory server says about its own
// replication.
//
// Three questions, and an operator asks them in this order: is this server a
// supplier, a consumer or both; what links does it have and to whom; and how
// far behind is it. Every one of them is answered from a different place on
// the two target servers, and none of it is translated into the other's
// vocabulary.
//
// The rule this package is built on is the same one the access and policy
// views are built on: it reports what this server holds and nothing else. It
// does not connect to a peer named in an agreement -- a report that dials out
// to whatever host a configuration value happens to name is a report that can
// be pointed at anything -- and it does not decide that replication is
// "healthy". It says what the server records, and where it recorded it.
package replication

import (
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Disclaimer is the one sentence this view is never shown without. Shared by
// the API, the UI and the documentation so that the three cannot drift.
const Disclaimer = "This is what this server records about its own replication. " +
	"Alder does not contact the other servers, so a peer's own view may differ, " +
	"and nothing here is a promise that a change has arrived everywhere."

// Roles a server can play for one suffix.
const (
	RoleSupplier = "supplier"
	RoleConsumer = "consumer"
	RoleBoth     = "both"
	RoleNone     = "none"
)

// Directions a link runs in, from this server's point of view.
const (
	// DirectionOutgoing: this server sends changes over this link.
	DirectionOutgoing = "outgoing"
	// DirectionIncoming: this server receives changes over this link.
	DirectionIncoming = "incoming"
)

// What the server's own last word on a link amounts to. Derived, and coarse
// on purpose: the server's own sentence is always carried alongside.
const (
	// StateOK: the server's last attempt on this link succeeded.
	StateOK = "ok"
	// StateWorking: an update is in progress right now.
	StateWorking = "working"
	// StateFailing: the server's last attempt did not succeed.
	StateFailing = "failing"
	// StateUnknown: the server records no outcome for this link. OpenLDAP
	// records none for any of them, which is a fact about OpenLDAP rather
	// than about this link.
	StateUnknown = "unknown"
)

// Report is one server's account of its own replication.
type Report struct {
	// Provider is the configuration model this was read through. Display
	// only, as everywhere else.
	Provider string
	// Role across every suffix: what this server is, in one word.
	Role string
	// Why is the sentence behind Role, in the reader's language.
	Why string
	// Suffixes are the parts of the tree replication was found for. A server
	// with none replicates nothing, which is an answer rather than an error.
	Suffixes []Suffix
	// Unread is what could not be read, so a partial answer says so rather
	// than reading as a complete one.
	Unread []Unread
	// Disclaimer is the constant above, carried with the data.
	Disclaimer string
}

// Suffix is replication as it applies to one part of the tree.
type Suffix struct {
	DN   string
	Role string
	// ServerID is this server's identity in the topology: OpenLDAP's
	// serverID, 389 DS's replica id. It is what appears inside a change's
	// sequence number, which is how a change's origin is legible at all.
	ServerID string
	// Cursors are the latest change this server holds from each origin. This
	// is the measure of how far along it is, and it is the one thing the two
	// servers can be compared on: OpenLDAP writes it as contextCSN on the
	// suffix, 389 DS as the replica update vector, and both mean "I have
	// everything from server N up to this moment".
	Cursors []Cursor
	// Links are the agreements this suffix has.
	Links []Link
	// Notes are things worth saying about this suffix that are not a link:
	// that writes here are referred elsewhere, for instance.
	Notes []string
}

// Cursor is how far this server has got with one origin's changes.
type Cursor struct {
	// Origin is the server id the changes came from.
	Origin string
	// At is when the latest change this server holds from that origin was
	// made -- by that server's clock, not this one's.
	At time.Time
	// Raw is the value as the server wrote it, kept because a change sequence
	// is the thing operators paste to each other.
	Raw string
}

// Link is one replication agreement, from this server's side of it.
type Link struct {
	// Name is what this server calls it: an agreement's name, a syncrepl
	// rid. Never a position.
	Name      string
	Direction string
	// Peer is the other end as this server has it written down. It is a
	// value out of the configuration and it is never dialled.
	Peer string
	// BindDN is the identity this link authenticates as. The credential that
	// goes with it is a secret and is never read.
	BindDN string
	// Transport is how it connects, where the server says: LDAP, LDAPS,
	// StartTLS.
	Transport string
	// State is the coarse derived verdict; Status is the server's own words,
	// which are always shown.
	State  string
	Status string
	// LastUpdate and LastInit are when the server last exchanged anything
	// over this link, and when it last filled the other end from scratch.
	// Zero where the server records nothing.
	LastUpdate time.Time
	LastInit   time.Time
	// InProgress reports an exchange happening right now.
	InProgress bool
	// DN is the entry this was read from.
	DN    string
	Notes []string
}

// Unread is something this session could not read.
type Unread struct {
	Where  string
	Reason string
}

// roleOf folds one suffix's directions into a word.
func roleOf(supplies, consumes bool) string {
	switch {
	case supplies && consumes:
		return RoleBoth
	case supplies:
		return RoleSupplier
	case consumes:
		return RoleConsumer
	}
	return RoleNone
}

// overallRole folds every suffix's role into one. A server that supplies one
// suffix and consumes another is both, which is ordinary in a real topology.
func overallRole(suffixes []Suffix) string {
	supplies, consumes := false, false
	for _, s := range suffixes {
		switch s.Role {
		case RoleSupplier:
			supplies = true
		case RoleConsumer:
			consumes = true
		case RoleBoth:
			supplies, consumes = true, true
		}
	}
	return roleOf(supplies, consumes)
}

// whyRole is the sentence behind the word.
func whyRole(role string, suffixes []Suffix) string {
	// Careful with the tense. On OpenLDAP the only evidence that a server
	// supplies anything is that it is set up to answer a consumer that asks;
	// the server does not record who, or whether anybody ever has. Claiming
	// it "sends changes to other servers" would be claiming to know something
	// no single OpenLDAP can be asked.
	switch role {
	case RoleBoth:
		return "This server receives changes from another server, and is set up to serve them on."
	case RoleSupplier:
		// No "and receives none itself". Neither server records enough to
		// support that: a 389 DS read-write replica is type 3 whether it is
		// the only supplier or one of several, and an OpenLDAP provider knows
		// nothing about who writes to it. Saying it flatly contradicted the
		// origins listed on the same card.
		return "This server is set up to serve changes to other servers."
	case RoleConsumer:
		return "This server receives changes from another server. Writes sent here are " +
			"refused or referred elsewhere, not applied."
	}
	if len(suffixes) > 0 {
		return "Replication is configured here but this server neither sends nor receives."
	}
	return "This server replicates nothing: nothing in its configuration sets up a link to another server."
}

// --- change sequence numbers -------------------------------------------------
//
// Both servers stamp every change with a sequence number carrying a timestamp
// and the id of the server that made it, and neither writes it the same way.
// Reading both into the same pair is what lets one screen answer "how far
// along is this server" whichever server it is.

// parseOpenLDAPCSN reads OpenLDAP's form:
//
//	20260927075958.266363Z#000000#001#000000
//	 time                  count  sid  mod
//
// The sid is three HEXADECIMAL digits -- slapd writes it with %03x -- while
// olcServerID is decimal. Reading it as text made the same server appear
// under two different numbers on the two cards an operator is being told to
// compare: id 16 stamps "#010#", which read as text is 10. Anything above 9
// was wrong, and "00a" rendered as a bare letter.
func parseOpenLDAPCSN(value string) (Cursor, bool) {
	parts := strings.Split(strings.TrimSpace(value), "#")
	if len(parts) < 3 {
		return Cursor{}, false
	}
	stamp, frac, _ := strings.Cut(parts[0], ".")
	stamp = strings.TrimSuffix(stamp, "Z")
	frac = strings.TrimSuffix(frac, "Z")
	at, err := time.Parse("20060102150405", stamp)
	if err != nil {
		return Cursor{}, false
	}
	if micros, err := strconv.Atoi(frac); err == nil && micros > 0 {
		at = at.Add(time.Duration(micros) * time.Microsecond)
	}
	sid, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 16, 64)
	if err != nil {
		return Cursor{}, false
	}
	return Cursor{
		Origin: strconv.FormatInt(sid, 10),
		At:     at.UTC(),
		Raw:    strings.TrimSpace(value),
	}, true
}

// parse389CSN reads 389 DS's form: twenty hex digits, as
// seconds-sequence-replicaid-subsequence.
//
//	6ab8d2b6 000b 0001 0000
func parse389CSN(value string) (Cursor, bool) {
	v := strings.TrimSpace(value)
	if len(v) < 16 {
		return Cursor{}, false
	}
	if _, err := hex.DecodeString(v[:16]); err != nil {
		return Cursor{}, false
	}
	seconds, err := strconv.ParseInt(v[0:8], 16, 64)
	if err != nil {
		return Cursor{}, false
	}
	origin, err := strconv.ParseInt(v[12:16], 16, 64)
	if err != nil {
		return Cursor{}, false
	}
	return Cursor{
		Origin: strconv.FormatInt(origin, 10),
		At:     time.Unix(seconds, 0).UTC(),
		Raw:    v,
	}, true
}

// parseRUV reads one value of 389 DS's replica update vector:
//
//	{replica 1 ldap://ds389:3389} <first csn> <last csn>
//
// The last CSN is the one that matters: it is the most recent change this
// server holds from that replica. The generation line names no replica and is
// not a position anybody can act on, so it is skipped.
func parseRUV(value string) (Cursor, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") {
		return Cursor{}, false
	}
	head, rest, ok := strings.Cut(value[1:], "}")
	if !ok {
		return Cursor{}, false
	}
	fields := strings.Fields(head)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "replica") {
		return Cursor{}, false
	}
	csns := strings.Fields(rest)
	if len(csns) == 0 {
		return Cursor{}, false
	}
	cursor, ok := parse389CSN(csns[len(csns)-1])
	if !ok {
		return Cursor{}, false
	}
	// The vector names the replica in the head, which is more reliable than
	// digging it out of the CSN and is what the server itself displays.
	cursor.Origin = fields[1]
	cursor.Raw = value
	return cursor, true
}

// Behind is how long ago the most recent change this server holds was made,
// and the origin it came from. It is the closest thing to "how far behind"
// that one server can answer on its own: the peer is not asked.
func (s Suffix) Behind(now time.Time) (time.Duration, string, bool) {
	latest := time.Time{}
	origin := ""
	for _, c := range s.Cursors {
		if c.At.After(latest) {
			latest, origin = c.At, c.Origin
		}
	}
	if latest.IsZero() {
		return 0, "", false
	}
	age := now.Sub(latest)
	if age < 0 {
		// The other server's clock is ahead of this one's. Reporting a
		// negative age would read as nonsense; that the two disagree is worth
		// saying, and the caller says it.
		age = 0
	}
	return age, origin, true
}

// stateFrom turns 389 DS's own last word into the coarse verdict.
//
// The server writes both a sentence and, since 1.4, a JSON object carrying a
// colour. The colour is used where it is there because it is the server's own
// judgement rather than ours; the sentence is shown either way.
func stateFrom(status, statusJSON string, inProgress bool) string {
	if inProgress {
		return StateWorking
	}
	switch strings.ToLower(jsonField(statusJSON, "state")) {
	case "green":
		return StateOK
	case "amber", "yellow":
		return StateWorking
	case "red":
		return StateFailing
	}
	if status == "" {
		return StateUnknown
	}
	// The sentence always begins "Error (N) ...", and N is 0 for success,
	// which is a sentence only a directory server could write.
	if code, ok := errorCodeIn(status); ok {
		if code == 0 {
			return StateOK
		}
		return StateFailing
	}
	return StateUnknown
}

// errorCodeIn reads the N out of "Error (N) ...".
func errorCodeIn(status string) (int, bool) {
	_, rest, ok := strings.Cut(status, "(")
	if !ok {
		return 0, false
	}
	digits, _, ok := strings.Cut(rest, ")")
	if !ok {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(digits))
	if err != nil {
		return 0, false
	}
	return code, true
}

// jsonField pulls one string field out of the small, flat JSON object 389 DS
// writes. Decoding it properly would mean a struct per server version; what
// is wanted is one field, and a value that is not there simply is not there.
func jsonField(doc, name string) string {
	key := `"` + name + `"`
	i := strings.Index(doc, key)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(key):]
	_, rest, ok := strings.Cut(rest, ":")
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	value, _, ok := strings.Cut(rest[1:], `"`)
	if !ok {
		return ""
	}
	return value
}

// parseGeneralizedTime reads the 20260927080004Z form both servers use for
// their own timestamps. A zero value means the server recorded none, which is
// different from recording one Alder cannot read.
func parseGeneralizedTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" || strings.HasPrefix(value, "1970") {
		return time.Time{}
	}
	for _, layout := range []string{"20060102150405Z", "20060102150405Z0700", "20060102150405"} {
		if at, err := time.Parse(layout, value); err == nil {
			return at.UTC()
		}
	}
	return time.Time{}
}
