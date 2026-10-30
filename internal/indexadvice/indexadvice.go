// Package indexadvice reads a directory server's log, which the operator
// supplies, and says which indexes the searches in it were missing (1.41).
//
// The input is a log and nothing else (2026-10-22): no accesslog read over
// LDAP, no cn=monitor, nothing fetched. Two formats are recognised by their
// lines, never by a flag naming a vendor: 389 Directory Server's access log,
// and OpenLDAP's with loglevel stats. Lines either does not write -- most of
// any real log -- are counted and skipped.
//
// A log is sensitive. It holds filter values, bind DNs and client addresses.
// Nothing but the attribute and the kind of match leaves this package: an
// example filter is rebuilt from its parsed tree with every value replaced,
// and bind DNs and addresses are never read at all.
//
// The package decides nothing about the server. It reads the index list it is
// given (from a configuration snapshot or the live server) and staging any
// suggestion goes through the ordinary 1.32 index path, planned and reviewed
// like any other change.
package indexadvice

import (
	"bufio"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
)

// The kinds of index a search can need, in both servers' own words: OpenLDAP's
// olcDbIndex and 389 DS's nsIndexType use the same four.
const (
	TypeEquality  = "eq"
	TypeSubstring = "sub"
	TypePresence  = "pres"
	TypeApprox    = "approx"
)

// Formats a log can be recognised as.
const (
	Format389      = "389-access"
	FormatOpenLDAP = "openldap-stats"
)

// What a suggestion amounts to.
const (
	// StatusCandidate: the backend has no index on the attribute, and one
	// can be staged through the index path.
	StatusCandidate = "candidate"
	// StatusTypeMissing: the attribute is indexed for other kinds of match
	// only. Alder does not change the types of an index that exists
	// (CONFIG-SNAPSHOTS.md), so adding this one is the operator's own change.
	StatusTypeMissing = "type_missing"
	// StatusBlocked: creating the index would be refused; Reason says why.
	StatusBlocked = "blocked"
)

// DefaultSlowerThan is the duration above which a search counts whatever the
// server said about it.
const DefaultSlowerThan = 100 * time.Millisecond

// maxExamples is how many example filters a suggestion carries. Without
// examples a suggestion is hard to trust; with values, the report would be as
// sensitive as the log.
const maxExamples = 3

// maxPending bounds the searches held while waiting for their result line, so
// a log that never writes results cannot grow memory without limit.
const maxPending = 200_000

// maxLine is the longest line read. A longer one is counted as unrecognised.
const maxLine = 1 << 20

// Backend is one database and what it indexes.
type Backend struct {
	// Suffix is the naming context it serves; a search is attributed to the
	// backend whose suffix is the longest one at or above its base.
	Suffix string
	// Indexes are by attribute, lower-cased, with the types each covers. An
	// index whose types are not recorded is taken to cover every kind, so it
	// is never suggested again.
	Indexes map[string][]string
	// Blocked, when set, says whether creating an index on an attribute would
	// be refused, and why in words. Nil refuses nothing.
	Blocked func(attribute string, types []string) string
}

// Options shape one reading.
type Options struct {
	Backends []Backend
	// SlowerThan: a search that took at least this long counts, whatever the
	// server said about it. Zero takes DefaultSlowerThan.
	SlowerThan time.Duration
}

// Report is what one log said.
type Report struct {
	// Format is what the log was recognised as; empty when no line was.
	Format string
	// Lines read, and how many were neither a search, its result, nor a
	// "not indexed" line.
	Lines, Unrecognised int
	// Searches is every search found; Counted the ones that met the rule
	// (marked unindexed, or slow); WithoutResult those whose result line was
	// not in the log.
	Searches, Counted, WithoutResult int
	// Unattributed searches had a base under no backend given, and
	// Unparsed a filter that is not RFC 4515.
	Unattributed, Unparsed int
	Suggestions            []Suggestion
}

// Suggestion is one index the searches needed.
type Suggestion struct {
	Backend   string
	Attribute string
	Type      string
	Status    string
	Reason    string
	// Searches counted for it; MarkedUnindexed of them the server itself
	// marked (389 DS's notes=U or notes=A); ServerSaidNotIndexed how many of
	// OpenLDAP's "not indexed" lines named this attribute and kind, which
	// carry no operation and so corroborate rather than decide.
	Searches, MarkedUnindexed, ServerSaidNotIndexed int
	Total, Worst                                    time.Duration
	// First and Last seen, where the log's lines carry a usable time.
	First, Last time.Time
	// Examples are filters with every value replaced by "…".
	Examples []string
}

type search struct {
	base   string
	filter string
	at     time.Time
}

type key struct{ backend, attribute, kind string }

// Read reads a log to its end and returns what it found. It holds no line
// once it has been read, so a log of any size can be read from a stream.
func Read(r io.Reader, opts Options) (*Report, error) {
	if opts.SlowerThan <= 0 {
		opts.SlowerThan = DefaultSlowerThan
	}
	backends := parseBackends(opts.Backends)
	report := &Report{}
	pending := map[string]search{}
	found := map[key]*Suggestion{}
	notIndexed := map[[2]string]int{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for {
		if !sc.Scan() {
			if err := sc.Err(); errors.Is(err, bufio.ErrTooLong) {
				// One line is too long to be one of ours. Count it and go
				// on: the rest of the log is still worth reading.
				report.Lines++
				report.Unrecognised++
				sc = bufio.NewScanner(r)
				sc.Buffer(make([]byte, 0, 64<<10), maxLine)
				continue
			} else if err != nil {
				return report, err
			}
			break
		}
		report.Lines++
		line := sc.Text()

		if attr, kind, ok := notIndexedLine(line); ok {
			notIndexed[[2]string{attr, kind}]++
			continue
		}
		id, rest, ok := connOp(line)
		if !ok {
			report.Unrecognised++
			continue
		}
		switch {
		case strings.HasPrefix(rest, "SRCH base="):
			base, _ := quoted(rest, "base=")
			f, ok := quoted(rest, "filter=")
			if !ok {
				report.Unrecognised++
				continue
			}
			report.Searches++
			if len(pending) >= maxPending {
				report.WithoutResult++
				continue
			}
			pending[id] = search{base: base, filter: f, at: lineTime(line)}
		case strings.HasPrefix(rest, "RESULT ") || strings.HasPrefix(rest, "SEARCH RESULT "):
			if strings.HasPrefix(rest, "SEARCH RESULT ") {
				report.Format = firstFormat(report.Format, FormatOpenLDAP)
			} else {
				report.Format = firstFormat(report.Format, Format389)
			}
			s, waiting := pending[id]
			if !waiting {
				// A result for an operation that is not a search we saw:
				// a bind, a modify, or a search from before the log began.
				continue
			}
			delete(pending, id)
			etime, timed := field(rest, "etime=")
			took := parseSeconds(etime)
			marked := unindexedNote(rest)
			if !marked && (!timed || took < opts.SlowerThan) {
				continue
			}
			report.Counted++
			count(report, found, backends, s, took, marked)
		default:
			// SRCH attr=..., ACCEPT, BIND, closed: lines of ours that say
			// nothing about indexes.
		}
	}
	report.WithoutResult += len(pending)

	for k, n := range notIndexed {
		for fk, s := range found {
			if fk.attribute == k[0] && fk.kind == k[1] {
				s.ServerSaidNotIndexed += n
			}
		}
	}
	for _, s := range found {
		report.Suggestions = append(report.Suggestions, *s)
	}
	sort.Slice(report.Suggestions, func(i, j int) bool {
		a, b := report.Suggestions[i], report.Suggestions[j]
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		if a.Searches != b.Searches {
			return a.Searches > b.Searches
		}
		if a.Backend != b.Backend {
			return a.Backend < b.Backend
		}
		if a.Attribute != b.Attribute {
			return a.Attribute < b.Attribute
		}
		return a.Type < b.Type
	})
	return report, nil
}

func firstFormat(have, saw string) string {
	if have != "" {
		return have
	}
	return saw
}

type parsedBackend struct {
	Backend
	suffix dn.DN
}

func parseBackends(in []Backend) []parsedBackend {
	var out []parsedBackend
	for _, b := range in {
		d, err := dn.Parse(b.Suffix)
		if err != nil {
			continue
		}
		out = append(out, parsedBackend{Backend: b, suffix: d})
	}
	// Longest suffix first, so a search under a nested database is
	// attributed to it and not to its parent.
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].suffix) > len(out[j].suffix) })
	return out
}

func backendFor(backends []parsedBackend, base string) (*parsedBackend, bool) {
	d, err := dn.Parse(base)
	if err != nil || d.IsEmpty() {
		return nil, false
	}
	for i := range backends {
		if d.HasSuffix(backends[i].suffix) {
			return &backends[i], true
		}
	}
	return nil, false
}

func count(report *Report, found map[key]*Suggestion, backends []parsedBackend, s search,
	took time.Duration, marked bool) {
	b, ok := backendFor(backends, s.base)
	if !ok {
		report.Unattributed++
		return
	}
	f, err := filter.Parse(s.filter)
	if err != nil {
		report.Unparsed++
		return
	}
	has := func(attribute, kind string) bool {
		types, indexed := b.Indexes[strings.ToLower(attribute)]
		if !indexed {
			return false
		}
		if len(types) == 0 {
			return true
		}
		for _, t := range types {
			if strings.EqualFold(t, kind) {
				return true
			}
		}
		return false
	}
	example := Mask(f)
	for _, need := range needed(f, has, !marked) {
		k := key{backend: b.Suffix, attribute: strings.ToLower(need[0]), kind: need[1]}
		sug, seen := found[k]
		if !seen {
			sug = &Suggestion{Backend: b.Suffix, Attribute: need[0], Type: need[1]}
			sug.Status, sug.Reason = statusOf(b, need[0], need[1])
			found[k] = sug
		}
		sug.Searches++
		if marked {
			sug.MarkedUnindexed++
		}
		sug.Total += took
		if took > sug.Worst {
			sug.Worst = took
		}
		if !s.at.IsZero() {
			if sug.First.IsZero() || s.at.Before(sug.First) {
				sug.First = s.at
			}
			if s.at.After(sug.Last) {
				sug.Last = s.at
			}
		}
		if len(sug.Examples) < maxExamples && !containsString(sug.Examples, example) {
			sug.Examples = append(sug.Examples, example)
		}
	}
}

func statusOf(b *parsedBackend, attribute, kind string) (string, string) {
	if types, indexed := b.Indexes[strings.ToLower(attribute)]; indexed {
		return StatusTypeMissing, "The backend indexes " + attribute + " for " + strings.Join(types, ", ") +
			" but not " + kind + ". Alder does not change the types of an index that already exists, " +
			"so adding " + kind + " is a change to make yourself."
	}
	if b.Blocked != nil {
		if reason := b.Blocked(attribute, []string{kind}); reason != "" {
			return StatusBlocked, reason
		}
	}
	return StatusCandidate, ""
}

// needed is what indexes a filter lacks, as (attribute, kind) pairs.
//
// A leaf needs its own index. An or needs every branch indexed, so it lacks
// whatever any branch lacks. A not cannot be answered from an index at all on
// either server, so nothing under it is suggested.
//
// An and can be served by any one branch that is indexed, so with excuse set
// an and holding one is not short of anything -- unless that branch is on
// objectClass, which narrows almost nothing. excuse is off for a search the
// server itself marked unindexed: 389 DS said the index it had was not
// enough, and every unindexed branch is then a suspect.
func needed(f filter.Filter, has func(attribute, kind string) bool, excuse bool) [][2]string {
	switch f.Kind {
	case filter.KindAnd:
		var all [][2]string
		for _, sub := range f.Subs {
			lack := needed(sub, has, excuse)
			if excuse && len(lack) == 0 && servable(sub) && !onObjectClass(sub) {
				return nil
			}
			all = appendPairs(all, lack)
		}
		return all
	case filter.KindOr:
		var all [][2]string
		for _, sub := range f.Subs {
			all = appendPairs(all, needed(sub, has, excuse))
		}
		return all
	case filter.KindNot, filter.KindExtensible, filter.KindInvalid:
		return nil
	}
	kind := kindOf(f.Kind)
	if kind == "" {
		return nil
	}
	// Every entry has an objectClass, and both servers treat its presence
	// as the whole database rather than an index lookup.
	if kind == TypePresence && strings.EqualFold(f.Attr, "objectClass") {
		return nil
	}
	if has(f.Attr, kind) {
		return nil
	}
	return [][2]string{{f.Attr, kind}}
}

func onObjectClass(f filter.Filter) bool {
	return f.Attr != "" && strings.EqualFold(f.Attr, "objectClass")
}

// servable reports whether a filter could be answered from indexes at all:
// one with a not or an extensible match in every path cannot, so an and
// branch made only of those does not excuse its siblings.
func servable(f filter.Filter) bool {
	switch f.Kind {
	case filter.KindAnd:
		for _, sub := range f.Subs {
			if servable(sub) {
				return true
			}
		}
		return false
	case filter.KindOr:
		for _, sub := range f.Subs {
			if !servable(sub) {
				return false
			}
		}
		return len(f.Subs) > 0
	case filter.KindNot, filter.KindExtensible, filter.KindInvalid:
		return false
	}
	return true
}

func kindOf(k filter.Kind) string {
	switch k {
	case filter.KindEquality, filter.KindGreaterOrEqual, filter.KindLessOrEqual:
		// Both servers answer an ordering match from the equality index.
		return TypeEquality
	case filter.KindSubstrings:
		return TypeSubstring
	case filter.KindPresent:
		return TypePresence
	case filter.KindApprox:
		return TypeApprox
	}
	return ""
}

func appendPairs(to, from [][2]string) [][2]string {
	for _, p := range from {
		dup := false
		for _, q := range to {
			if strings.EqualFold(p[0], q[0]) && p[1] == q[1] {
				dup = true
				break
			}
		}
		if !dup {
			to = append(to, p)
		}
	}
	return to
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Mask rebuilds a filter with every assertion value replaced by "…". The
// attribute names and the shape stay; nothing a user typed does.
func Mask(f filter.Filter) string {
	var b strings.Builder
	mask(&b, f, 0)
	out := b.String()
	if len(out) > 512 {
		out = out[:509] + "..."
	}
	return out
}

func mask(b *strings.Builder, f filter.Filter, depth int) {
	if depth > 50 {
		b.WriteString("(…)")
		return
	}
	b.WriteByte('(')
	switch f.Kind {
	case filter.KindAnd, filter.KindOr, filter.KindNot:
		b.WriteString(map[filter.Kind]string{filter.KindAnd: "&", filter.KindOr: "|", filter.KindNot: "!"}[f.Kind])
		for _, sub := range f.Subs {
			mask(b, sub, depth+1)
		}
	case filter.KindEquality:
		b.WriteString(f.Attr + "=…")
	case filter.KindApprox:
		b.WriteString(f.Attr + "~=…")
	case filter.KindGreaterOrEqual:
		b.WriteString(f.Attr + ">=…")
	case filter.KindLessOrEqual:
		b.WriteString(f.Attr + "<=…")
	case filter.KindPresent:
		b.WriteString(f.Attr + "=*")
	case filter.KindSubstrings:
		b.WriteString(f.Attr + "=")
		if f.Initial != "" {
			b.WriteString("…")
		}
		for range f.Any {
			b.WriteString("*…")
		}
		b.WriteString("*")
		if f.Final != "" {
			b.WriteString("…")
		}
	case filter.KindExtensible:
		b.WriteString(f.Attr)
		if f.DNAttributes {
			b.WriteString(":dn")
		}
		if f.MatchingRule != "" {
			b.WriteString(":" + f.MatchingRule)
		}
		b.WriteString(":=…")
	default:
		b.WriteString("?")
	}
	b.WriteByte(')')
}

// Caveat is what every report carries: the two facts about an index that an
// operator tends to forget, both already in CONFIG-SNAPSHOTS.md.
const Caveat = "Adding an index is not building it. Neither server indexes existing entries on its own: " +
	"run a reindex task on 389 Directory Server, or slapindex on OpenLDAP. Until then, searches that " +
	"use the new index can miss entries. Every index also makes every write to that attribute slower."
