package policy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
)

// Finding the policy and the state.
//
// Both servers are asked the same three questions, in their own attributes:
// what does this account record about itself, does it name a policy of its
// own, and what is the server's default. Nothing branches on a vendor name --
// the attributes that answer are looked for, and whichever answer arrives is
// what the report holds.

// Reader is what a policy report needs. Reads for the entry and the policy it
// names; a search for the server's default, which lives in the configuration
// tree on one server and is pointed at from it on the other.
type Reader interface {
	Capabilities() directory.Capabilities
	Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error)
	Search(ctx context.Context, req directory.SearchRequest) (*directory.SearchResult, error)
}

// ErrNoEntry is returned when the entry itself cannot be read.
var ErrNoEntry = errors.New("policy: that entry could not be read")

// stateAttributes are what each server records on the account.
var stateAttributes = []struct {
	key   string
	label string
	kind  string // time, times, bool, count, lock
}{
	// OpenLDAP ppolicy.
	{"pwdaccountlockedtime", "locked since", "lock"},
	{"pwdchangedtime", "password last changed", "time"},
	{"pwdreset", "must be changed at the next bind", "bool"},
	{"pwdfailuretime", "failed binds recorded", "times"},
	{"pwdgraceusetime", "grace logins used", "times"},
	{"pwdlastsuccess", "last successful bind", "time"},
	{"pwdstarttime", "usable from", "time"},
	{"pwdendtime", "usable until", "time"},
	{"pwdpolicysubentry", "policy named by this entry", ""},

	// 389 Directory Server.
	{"nsaccountlock", "administratively locked", "lock"},
	{"passwordexpirationtime", "password expires", "time"},
	{"passwordretrycount", "failed binds recorded", "count"},
	{"retrycountresettime", "failure count resets at", "time"},
	{"accountunlocktime", "unlocks at", "time"},
	{"passwordallowchangetime", "may be changed from", "time"},
	{"passwordgraceusertime", "grace logins used", "count"},
	{"passwordexpwarned", "expiry warning sent", "count"},
	{"pwdpolicysubentry", "policy named by this entry", ""},
}

func stateAttributeNames() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range stateAttributes {
		if seen[a.key] {
			continue
		}
		seen[a.key] = true
		out = append(out, a.key)
	}
	return out
}

// For reports the password policy in force on an entry, and what the server
// records about that account.
func For(ctx context.Context, r Reader, target dn.DN) (*Report, error) {
	attrs := append(stateAttributeNames(), "objectClass")
	entry, err := r.Read(ctx, target, attrs)
	if err != nil {
		return nil, errors.Join(ErrNoEntry, err)
	}
	report := &Report{DN: target.String(), State: stateOf(entry)}
	report.Unlock = unlockFor(target, entry, report.State)

	named := firstOf(entry, "pwdpolicysubentry")
	if named != "" {
		p, unread := readPolicyEntry(ctx, r, named, SourceEntry,
			"this entry names the policy it is held to")
		if p != nil {
			report.Policy = p
			return report, nil
		}
		// The account names a policy Alder could not read. The server's
		// default is *not* the policy in force here, and reporting it as
		// though it were would be a confident wrong answer about the one
		// thing this report exists to get right.
		report.Unread = append(report.Unread, unread...)
		report.Policy = &Policy{Source: SourceEntry, DN: named,
			Why: "this entry names the policy it is held to, and Alder could not read it. " +
				"Whatever that policy says is what applies here -- not the server's default."}
		return report, nil
	}

	// No pointer on the entry. Before taking that as "the default applies",
	// find out whether this bind can see the pointer at all: an attribute
	// hidden from the reader looks exactly like one that is not there, and
	// the difference decides which policy is in force.
	hidden, why := pointerHidden(ctx, r, target)
	if hidden {
		report.Unread = append(report.Unread, Unread{Where: target.String(), Reason: why})
	}

	p, unread := defaultPolicy(ctx, r)
	report.Policy = p
	report.Unread = append(report.Unread, unread...)
	if report.Policy == nil {
		report.Policy = &Policy{Source: SourceNone,
			Why: "Alder found no password policy on this server that it could read. That is not the same as there being none in force."}
		return report, nil
	}
	if hidden {
		report.Policy.Why = "the server's own default, which applies unless this entry names its own -- " +
			"and whether it does is hidden from this session"
	}
	return report, nil
}

// visibilityReader is a session that can tell an absent attribute from a
// hidden one. Optional, because that is a question not every driver can ask.
type visibilityReader interface {
	VisibilityOf(ctx context.Context, target dn.DN, attribute string) (directory.AttributeVisibility, error)
}

// pointerHidden reports whether the account may name a policy this session
// cannot see.
//
// Only asked when it would change the answer -- the pointer is absent and a
// default is about to be reported as the policy in force -- because it costs
// the server an extra operation.
func pointerHidden(ctx context.Context, r Reader, target dn.DN) (bool, string) {
	asker, ok := r.(visibilityReader)
	if !ok {
		return false, ""
	}
	seen, err := asker.VisibilityOf(ctx, target, "pwdpolicysubentry")
	if err != nil || seen != directory.VisibilityDenied {
		return false, ""
	}
	return true, "this session may not read pwdPolicySubentry on this entry, so whether the account " +
		"names a policy of its own is unknown; the default below may not be the policy in force"
}

// stateOf reads what the account itself records.
func stateOf(entry *directory.Entry) *State {
	state := &State{}
	// Both servers' tables name pwdpolicysubentry, and an attribute both keep
	// must be reported once. Reported twice it reads as two different facts.
	seen := map[string]bool{}
	for _, a := range stateAttributes {
		values := entry.GetStrings(a.key)
		if len(values) == 0 || seen[a.key] {
			continue
		}
		seen[a.key] = true
		setting := Setting{Key: a.key, Values: values, Label: a.label}
		switch a.kind {
		case "time", "lock":
			setting.Detail = readableTime(values[0])
		case "times":
			setting.Detail = fmt.Sprintf("%d recorded, most recent %s", len(values), readableTime(values[len(values)-1]))
		}
		state.Attributes = append(state.Attributes, setting)

		switch strings.ToLower(a.key) {
		case "pwdaccountlockedtime":
			state.Locked = true
			// 000001010000Z is ppolicy's "locked until an administrator says
			// otherwise", and reads as the year zero unless it is named.
			if strings.HasPrefix(values[0], "00000101") {
				state.LockedDetail = "locked with no automatic release; an administrator has to clear it"
			} else {
				state.LockedDetail = "locked since " + readableTime(values[0])
			}
		case "nsaccountlock":
			if strings.EqualFold(values[0], "true") {
				state.Locked = true
				state.LockedDetail = "the account is administratively locked (nsAccountLock)"
			}
		case "accountunlocktime":
			// 389 DS's automatic lockout. The server sets the moment it will
			// release itself; until then a bind is refused, which is what
			// anybody means by locked. Reporting it as unlocked -- which is
			// what this did until 1.29 -- made the commonest lock on this
			// server the one the screen had nothing to say about.
			//
			// Only while it is still in the future: a time that has passed
			// is a record of a lockout that has already released, and
			// calling that locked would be the opposite error.
			if until, ok := parseGeneralized(values[0]); ok && until.After(time.Now()) {
				state.Locked = true
				state.LockedDetail = "locked by failed binds until " + readableTime(values[0])
			}
		case "pwdreset":
			state.MustChange = state.MustChange || strings.EqualFold(values[0], "true")
		case "passwordexpirationtime":
			state.Expiry = readableTime(values[0])
		case "pwdchangedtime":
			state.Changed = readableTime(values[0])
		case "passwordretrycount":
			state.Failures = values[0] + " failed binds recorded"
		case "pwdfailuretime":
			state.Failures = fmt.Sprintf("%d failed binds recorded", len(values))
		}
	}
	return state
}

// readPolicyEntry reads a policy written as an entry, which is how OpenLDAP
// keeps every policy and how 389 DS keeps a per-entry one.
func readPolicyEntry(ctx context.Context, r Reader, target, source, why string) (*Policy, []Unread) {
	parsed, err := dn.Parse(target)
	if err != nil {
		return nil, []Unread{{Where: target, Reason: "the entry names a policy whose DN does not parse"}}
	}
	entry, err := r.Read(ctx, parsed, append(PolicyAttributeNames(), "objectClass", "cn", "description"))
	if err != nil {
		return nil, []Unread{{Where: target, Reason: "the policy this entry names could not be read"}}
	}
	settings := settingsFrom(entry)
	if len(settings) == 0 {
		return nil, []Unread{{Where: target, Reason: "the policy entry holds no setting Alder recognises"}}
	}
	return &Policy{Source: source, DN: entry.DN.String(), Why: why, Settings: settings}, nil
}

// defaultPolicy finds the policy that applies to an account naming none.
//
// The two servers put it in different places, and both are looked for: the
// attributes on the server's own configuration entry, and the entry an
// overlay's configuration points at.
func defaultPolicy(ctx context.Context, r Reader) (*Policy, []Unread) {
	caps := r.Capabilities()
	root := caps.Config.DN
	if root == "" {
		root = caps.ConfigContext
	}
	if root == "" {
		return nil, nil
	}
	if !caps.Config.Readable {
		reason := caps.Config.Reason
		if reason == "" {
			reason = "this session cannot read the configuration tree, where the server's default policy is kept or named"
		}
		return nil, []Unread{{Where: root, Reason: reason}}
	}
	base, err := dn.Parse(root)
	if err != nil {
		return nil, nil
	}

	// The configuration entry itself, which is where 389 DS keeps the global
	// policy outright.
	if entry, err := r.Read(ctx, base, append(PolicyAttributeNames(), "objectClass")); err == nil {
		if settings := settingsFrom(entry); len(settings) > 0 {
			return &Policy{Source: SourceDefault, DN: entry.DN.String(),
				Why:      "the server's own default, from its configuration",
				Settings: settings}, nil
		}
	}

	// An overlay that names a default policy entry, which is how OpenLDAP
	// does it: the policy is an ordinary entry in the data tree.
	res, err := r.Search(ctx, directory.SearchRequest{
		BaseDN: base, Scope: directory.ScopeSubtree,
		Filter:     filter.Present("olcPPolicyDefault"),
		Attributes: []string{"olcPPolicyDefault"}, Limit: 20,
	})
	if err != nil {
		return nil, []Unread{{Where: root, Reason: "the configuration tree could not be searched for a default policy"}}
	}
	for _, entry := range res.Entries {
		named := firstOf(entry, "olcppolicydefault")
		if named == "" {
			continue
		}
		policy, unread := readPolicyEntry(ctx, r, named, SourceDefault,
			"the server's default policy, named by its password policy overlay")
		if policy != nil {
			return policy, nil
		}
		return nil, unread
	}
	return nil, nil
}

// settingsFrom picks the policy attributes off an entry, in report order.
func settingsFrom(entry *directory.Entry) []Setting {
	var out []Setting
	for _, a := range policyAttributes {
		values := entry.GetStrings(a.key)
		if len(values) == 0 {
			continue
		}
		setting := Setting{Key: a.key, Values: values, Label: a.label}
		switch a.unit {
		case "seconds":
			setting.Detail = readableDuration(values[0])
		case "bool":
			setting.Detail = readableBool(values[0])
		}
		out = append(out, setting)
	}
	return out
}

func firstOf(entry *directory.Entry, attribute string) string {
	values := entry.GetStrings(attribute)
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

// readableDuration turns a count of seconds into something a person reads,
// without replacing the number itself.
func readableDuration(value string) string {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds <= 0 {
		return ""
	}
	d := time.Duration(seconds) * time.Second
	switch {
	case d >= 24*time.Hour:
		days := seconds / 86400
		if days*86400 == seconds {
			return plural(days, "day")
		}
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= time.Hour:
		return plural(seconds/3600, "hour")
	case d >= time.Minute:
		return plural(seconds/60, "minute")
	}
	return plural(seconds, "second")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// readableBool reads the spellings both servers use, and passes anything else
// through untouched rather than guessing at it.
func readableBool(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "on", "yes", "1":
		return "yes"
	case "false", "off", "no", "0":
		return "no"
	}
	return ""
}

// readableTime renders a GeneralizedTime, and leaves anything else alone.
// parseGeneralized reads the generalized time both servers write, and says
// whether it could.
func parseGeneralized(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"20060102150405Z", "20060102150405Z0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func readableTime(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := time.Parse("20060102150405Z", value)
	if err != nil {
		if p2, err2 := time.Parse("20060102150405Z0700", value); err2 == nil {
			parsed = p2
		} else {
			return ""
		}
	}
	return parsed.UTC().Format("2 Jan 2006, 15:04 UTC")
}
