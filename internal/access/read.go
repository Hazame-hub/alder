package access

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
)

// Finding the rules.
//
// Nothing here branches on a vendor name. Both places rules can live are
// looked in -- aci attributes on the entry and its ancestors, olcAccess in the
// configuration tree -- and whichever answers is what the report holds. A
// server that keeps them somewhere else reports nothing and says the tree it
// could not read, rather than reporting a confident nothing.

// Reader is what reading access rules needs: reads, a search for the
// configuration tree, and what the server said about itself.
type Reader interface {
	Capabilities() directory.Capabilities
	Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error)
	Search(ctx context.Context, req directory.SearchRequest) (*directory.SearchResult, error)
}

// RightsReader is a session whose server answers what an identity may do.
//
// An optional interface rather than a method on Reader: a driver that cannot
// ask should not have to pretend it can, and the report says which it was.
type RightsReader interface {
	EffectiveRights(ctx context.Context, target dn.DN, subject string) (*directory.EffectiveRights, error)
}

// Options steer a report.
type Options struct {
	// Subject is the identity to ask the server about, where the server
	// answers at all. Empty means the one this session is bound as, which is
	// the common case: "why can't I write this?" is asked about oneself.
	Subject string
}

// ErrNoEntry is returned when the entry asked about cannot be read. Without it
// there is nothing to report rules about: an answer for an entry that is not
// there would be a list of rules and no subject.
var ErrNoEntry = errors.New("access: that entry could not be read")

// For reports the access control rules that bear on one entry, and the
// server's own verdict where it gives one.
func For(ctx context.Context, r Reader, target dn.DN, opts Options) (*Report, error) {
	if _, err := r.Read(ctx, target, []string{"1.1"}); err != nil {
		return nil, errors.Join(ErrNoEntry, err)
	}
	report := &Report{DN: target.String()}

	acis, unread := readACIs(ctx, r, target)
	if len(acis) > 0 {
		report.Styles = append(report.Styles, StyleACI)
		report.Rules = append(report.Rules, acis...)
	}
	report.Unread = append(report.Unread, unread...)

	olc, olcUnread := readOlcAccess(ctx, r, target)
	if len(olc) > 0 {
		report.Styles = append(report.Styles, StyleOpenLDAP)
		report.Rules = append(report.Rules, olc...)
	}
	report.Unread = append(report.Unread, olcUnread...)

	report.Effective, report.RightsNote = effective(ctx, r, target, opts.Subject)
	return report, nil
}

// effective asks the server what the subject may do, where it will answer.
//
// Every failure here is a note rather than an error: the rules are the body of
// the report, and losing them because the server declined one question would
// be a poor trade. What is never done is turning silence into a verdict.
func effective(ctx context.Context, r Reader, target dn.DN, subject string) (*Effective, string) {
	asker, ok := r.(RightsReader)
	if !ok || !r.Capabilities().EffectiveRights {
		return nil, "This server does not answer what an identity may do: only the rules above are available. " +
			"389 Directory Server publishes the control that answers it; OpenLDAP has no equivalent."
	}
	rights, err := asker.EffectiveRights(ctx, target, subject)
	switch {
	case errors.Is(err, directory.ErrRightsUnsupported):
		return nil, "This server does not answer what an identity may do."
	case errors.Is(err, directory.ErrRightsUnanswered):
		return nil, "The server declined to say what that identity may do here, which is not the same as saying it may do nothing."
	case err != nil:
		return nil, "The server was asked what that identity may do and the question failed: " + err.Error()
	}
	out := &Effective{Subject: rights.Subject, Entry: rights.Entry, EntryWords: rights.EntryWords()}
	for _, a := range rights.Attributes {
		out.Attributes = append(out.Attributes, AttributeRight{
			Name: a.Name, Rights: a.Rights, Words: directory.AttributeWords(a.Rights),
		})
	}
	return out, ""
}

// readACIs reads aci attributes from the entry and every ancestor down to the
// naming context it sits in, because an aci on an ancestor is in force here.
//
// An ancestor this bind cannot read is not an error: it is a fact about the
// bind, and it is reported as a place that could not be read. A report that
// failed outright because one entry above was invisible would be useless to
// exactly the operator who most needs it.
func readACIs(ctx context.Context, r Reader, target dn.DN) ([]Rule, []Unread) {
	var rules []Rule
	var unread []Unread
	for _, at := range ancestry(r.Capabilities(), target) {
		parsed, err := dn.Parse(at)
		if err != nil {
			continue
		}
		entry, err := r.Read(ctx, parsed, []string{"aci"})
		if err != nil {
			if !dnEqual(at, target.String()) {
				unread = append(unread, Unread{Where: at, Reason: "this session cannot read that entry"})
			}
			continue
		}
		for _, value := range entry.GetStrings("aci") {
			rules = append(rules, ParseACI(value, at, target.String()))
		}
	}
	return rules, unread
}

// ancestry is the entry and its ancestors, nearest first, stopping at the
// naming context that contains it (or at the root when none matches).
func ancestry(caps directory.Capabilities, target dn.DN) []string {
	var out []string
	current := target.String()
	stop := ""
	for _, ctx := range caps.NamingContexts {
		if dnUnder(current, ctx) && len(ctx) > len(stop) {
			stop = ctx
		}
	}
	for current != "" {
		out = append(out, current)
		if stop != "" && dnEqual(current, stop) {
			break
		}
		current = parentOf(current)
	}
	return out
}

// readOlcAccess reads the rules OpenLDAP keeps in its configuration tree: the
// ones on the database whose suffix contains this entry, and the frontend's,
// which the server consults after them.
func readOlcAccess(ctx context.Context, r Reader, target dn.DN) ([]Rule, []Unread) {
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
			reason = "this session cannot read the configuration tree, where OpenLDAP keeps its access rules"
		}
		return nil, []Unread{{Where: root, Reason: reason}}
	}
	base, err := dn.Parse(root)
	if err != nil {
		return nil, nil
	}

	req := directory.SearchRequest{
		BaseDN: base,
		// A subtree search, not one level: rules live on the database entry,
		// and they can also live on an entry beneath it -- an overlay's own.
		// A one-level search found the first and dropped the second without
		// saying so, which is the answer this whole feature exists not to
		// give.
		Scope:  directory.ScopeSubtree,
		Filter: filter.Present("olcAccess"),
		// The rule's suffix decides which database's rules bear on the entry,
		// and the naming attribute says which database it is.
		Attributes: []string{"olcAccess", "olcSuffix", "olcDatabase"},
		Limit:      500,
	}
	res, err := r.Search(ctx, req)
	if err != nil {
		return nil, []Unread{{Where: root, Reason: "the configuration tree could not be searched for access rules"}}
	}
	var unread []Unread
	if res.Truncated {
		unread = append(unread, Unread{Where: root,
			Reason: "there are more entries carrying access rules than one search returns, so this is not the whole list"})
	}

	// The databases whose suffix holds this entry. Their rules are the entry's,
	// and so are the rules on anything beneath them.
	covering := map[string]bool{}
	for _, entry := range res.Entries {
		for _, suffix := range entry.GetStrings("olcSuffix") {
			if dnUnder(target.String(), suffix) {
				covering[strings.ToLower(entry.DN.String())] = true
				break
			}
		}
	}

	type source struct {
		dn    string
		order int
		rules []string
	}
	var sources []source
	for _, entry := range res.Entries {
		values := entry.GetStrings("olcAccess")
		if len(values) == 0 {
			continue
		}
		at := strings.ToLower(entry.DN.String())
		name := strings.ToLower(entry.GetOne("olcDatabase"))
		under := false
		for db := range covering {
			if at != db && dnUnder(at, db) {
				under = true
				break
			}
		}
		switch {
		case covering[at]:
			// A database's rules bear on the entries it holds, and on nothing
			// else. A database whose suffix is elsewhere is not this entry's.
			sources = append(sources, source{dn: entry.DN.String(), order: 0, rules: values})
		case under:
			// An overlay on that database, which has rules of its own.
			sources = append(sources, source{dn: entry.DN.String(), order: 1, rules: values})
		case strings.Contains(name, "frontend"):
			// The frontend's rules are consulted after the database's, so
			// they are reported after them.
			sources = append(sources, source{dn: entry.DN.String(), order: 2, rules: values})
		default:
			// The configuration database's own rules, or another database's:
			// they do not bear on this entry.
			continue
		}
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].order < sources[j].order })

	var rules []Rule
	for _, s := range sources {
		parsed := make([]Rule, 0, len(s.rules))
		for _, value := range s.rules {
			parsed = append(parsed, ParseOpenLDAP(value, s.dn, target.String()))
		}
		// In the server's own order, which is the mechanism: first match wins.
		sort.SliceStable(parsed, func(i, j int) bool {
			a, b := parsed[i].Index, parsed[j].Index
			if a == nil || b == nil {
				return false
			}
			return *a < *b
		})
		rules = append(rules, parsed...)
	}
	return rules, unread
}
