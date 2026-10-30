package replication

import (
	"sort"
	"strings"
	"time"
)

// Compare (1.41) sets two servers' own records side by side and says, per
// suffix and per origin, whether they hold the same latest change.
//
// The verdict is in time only (2026-10-22). A change sequence says when, and
// by which server, a change was made; it does not say how many changes lie
// between two of them, so "behind by N changes" is never said. What is said
// is a span between two stamps made by the SAME origin's clock -- the one
// server whose clock both values came from -- so the answer does not depend
// on whether A's and B's clocks agree, which is the trouble with comparing
// each side's "how long ago" (Suffix.Behind).
//
// Nothing here reads a directory. The two reports were each read from a
// server the operator connected to; no peer named in an agreement is asked.

// Verdicts, for an origin, a suffix, and the comparison as a whole.
const (
	// VerdictInStep: both hold the same latest change.
	VerdictInStep = "in_step"
	// VerdictABehind and VerdictBBehind: that side lacks changes the other
	// holds. For an origin, it also covers a side holding nothing from it.
	VerdictABehind = "a_behind"
	VerdictBBehind = "b_behind"
	// VerdictDiverged: each side holds changes the other lacks, from
	// different origins. Ordinary in flight on a multi-supplier pair, and
	// said in those words rather than as two contradictory "behinds".
	VerdictDiverged = "diverged"
	// VerdictNotComparable: the two records are not positions on one
	// history, so no verdict is given. Why says which reason.
	VerdictNotComparable = "not_comparable"
	// VerdictIncomplete: something needed for the verdict could not be read.
	VerdictIncomplete = "incomplete"
)

// InStepCaveat is what "in step" never appears without.
const InStepCaveat = "In step means both servers hold the same latest change from each origin. " +
	"If an origin has made no change since, a link that stopped working looks the same. " +
	"The age of the latest change is shown so you can tell."

// CompareOptions adjust a comparison.
type CompareOptions struct {
	// Tolerate counts an origin behind by no more than this as in step, for
	// a monitoring job that should not page on a change in flight. Zero is
	// exact.
	Tolerate time.Duration
}

// Comparison is two servers' records, compared.
type Comparison struct {
	Verdict  string
	Why      string
	Suffixes []SuffixComparison
	Caveat   string
}

// SuffixComparison is one part of the tree, compared.
type SuffixComparison struct {
	DN      string
	Verdict string
	// Why is set when there is no verdict to give, or when one side alone
	// replicates this suffix.
	Why     string
	Origins []OriginComparison
}

// OriginComparison is how far each side has got with one origin's changes.
type OriginComparison struct {
	Origin  string
	Verdict string
	// A and B are each side's cursor; nil where that side holds nothing
	// from this origin.
	A, B *Cursor
	// Span is how much later the ahead side's latest change from this origin
	// was made, by that origin's clock. Zero when in step, and also when the
	// two changes were made within the same second on a server that stamps
	// whole seconds; Verdict tells those apart.
	Span time.Duration
	// Tolerated reports a side behind by no more than CompareOptions.Tolerate,
	// counted as in step.
	Tolerated bool
}

// Compare compares two reports. A and B are only names: the API calls the
// session's server A and the reference B, the command line its two files in
// the order given.
func Compare(a, b Report, opts CompareOptions) Comparison {
	out := Comparison{Caveat: InStepCaveat}

	if a.Provider != "" && b.Provider != "" && !strings.EqualFold(a.Provider, b.Provider) {
		out.Verdict = VerdictNotComparable
		out.Why = "The two servers follow different replication models (" + a.Provider + " and " +
			b.Provider + "), so they cannot replicate to each other, and origin numbers on one " +
			"do not name the same servers on the other."
		return out
	}
	if len(a.Suffixes) == 0 && len(b.Suffixes) == 0 {
		out.Verdict = VerdictNotComparable
		out.Why = "Neither server replicates anything."
		return out
	}

	byDN := map[string][2]*Suffix{}
	var keys []string
	for side, report := range []Report{a, b} {
		for i := range report.Suffixes {
			s := &report.Suffixes[i]
			key := strings.ToLower(s.DN)
			pair, seen := byDN[key]
			if !seen {
				keys = append(keys, key)
			}
			pair[side] = s
			byDN[key] = pair
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		pair := byDN[key]
		out.Suffixes = append(out.Suffixes, compareSuffix(pair[0], pair[1], a, b, opts))
	}
	out.Verdict, out.Why = overall(out.Suffixes)
	// Something either server could not read that belongs to no suffix
	// compared here may be a suffix that is missing from its report
	// altogether. The verdict cannot then speak for the whole server.
	if out.Verdict != VerdictNotComparable && out.Verdict != VerdictIncomplete &&
		(strayUnread(a, byDN) || strayUnread(b, byDN)) {
		out.Verdict = VerdictIncomplete
		out.Why = "Part of one server's replication record could not be read, so a suffix may be missing from it."
	}
	return out
}

func strayUnread(r Report, compared map[string][2]*Suffix) bool {
	for _, u := range r.Unread {
		if _, ok := compared[strings.ToLower(u.Where)]; !ok {
			return true
		}
	}
	return false
}

func compareSuffix(a, b *Suffix, ra, rb Report, opts CompareOptions) SuffixComparison {
	switch {
	case a == nil:
		return SuffixComparison{DN: b.DN, Verdict: VerdictNotComparable,
			Why: "Only the second server replicates this suffix."}
	case b == nil:
		return SuffixComparison{DN: a.DN, Verdict: VerdictNotComparable,
			Why: "Only the first server replicates this suffix."}
	}
	out := SuffixComparison{DN: a.DN}
	if unreadAt(ra, a.DN) || unreadAt(rb, b.DN) {
		out.Verdict = VerdictIncomplete
		out.Why = "How far along one of the servers is could not be read for this suffix."
		return out
	}
	if a.Generation != "" && b.Generation != "" && !strings.EqualFold(a.Generation, b.Generation) {
		out.Verdict = VerdictNotComparable
		out.Why = "The two replicas belong to different generations (" + a.Generation + " and " +
			b.Generation + "): one was initialised from a different history, so their vectors " +
			"are not positions on the same one."
		return out
	}
	if len(a.Cursors) == 0 && len(b.Cursors) == 0 {
		out.Verdict = VerdictIncomplete
		out.Why = "Neither server records how far along it is for this suffix."
		return out
	}

	origins := map[string][2]*Cursor{}
	var ids []string
	for side, cursors := range [][]Cursor{a.Cursors, b.Cursors} {
		for i := range cursors {
			c := &cursors[i]
			pair, seen := origins[c.Origin]
			if !seen {
				ids = append(ids, c.Origin)
			}
			pair[side] = c
			origins[c.Origin] = pair
		}
	}
	sortOrigins(ids)

	aBehind, bBehind := false, false
	for _, id := range ids {
		pair := origins[id]
		o := compareOrigin(id, pair[0], pair[1], opts)
		switch o.Verdict {
		case VerdictABehind:
			aBehind = true
		case VerdictBBehind:
			bBehind = true
		}
		out.Origins = append(out.Origins, o)
	}
	switch {
	case aBehind && bBehind:
		out.Verdict = VerdictDiverged
	case aBehind:
		out.Verdict = VerdictABehind
	case bBehind:
		out.Verdict = VerdictBBehind
	default:
		out.Verdict = VerdictInStep
	}
	return out
}

func compareOrigin(id string, a, b *Cursor, opts CompareOptions) OriginComparison {
	out := OriginComparison{Origin: id, A: a, B: b}
	switch {
	case a == nil:
		out.Verdict = VerdictABehind
		return out
	case b == nil:
		out.Verdict = VerdictBBehind
		return out
	}
	order := strings.Compare(csnOf(*a), csnOf(*b))
	if order == 0 {
		out.Verdict = VerdictInStep
		return out
	}
	if order > 0 {
		out.Verdict, out.Span = VerdictBBehind, a.At.Sub(b.At)
	} else {
		out.Verdict, out.Span = VerdictABehind, b.At.Sub(a.At)
	}
	if out.Span < 0 {
		// Both stamps are one origin's, so this would be that origin's clock
		// stepping backwards between two of its own changes. The sequence
		// still orders them; the span is not worth reporting.
		out.Span = 0
	}
	if opts.Tolerate > 0 && out.Span <= opts.Tolerate {
		out.Verdict, out.Tolerated = VerdictInStep, true
	}
	return out
}

// csnOf is the change sequence a cursor is compared on. Within one origin,
// both servers' forms order as text: fixed-width fields, the time first.
func csnOf(c Cursor) string {
	if c.CSN != "" {
		return c.CSN
	}
	return strings.ToLower(c.Raw)
}

func unreadAt(r Report, suffix string) bool {
	for _, u := range r.Unread {
		if strings.EqualFold(u.Where, suffix) {
			return true
		}
	}
	return false
}

// sortOrigins orders server ids as numbers where they are numbers, so server
// 10 follows server 9.
func sortOrigins(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		if len(ids[i]) != len(ids[j]) {
			return len(ids[i]) < len(ids[j])
		}
		return ids[i] < ids[j]
	})
}

// overall folds the suffixes into one verdict. Not comparable and incomplete
// win over any difference, as an incomplete diff wins over one with
// differences: a partial answer has not said what the rest holds.
func overall(suffixes []SuffixComparison) (string, string) {
	counts := map[string]int{}
	for _, s := range suffixes {
		counts[s.Verdict]++
	}
	switch {
	case counts[VerdictNotComparable] > 0:
		return VerdictNotComparable, "At least one suffix cannot be compared."
	case counts[VerdictIncomplete] > 0:
		return VerdictIncomplete, "At least one suffix could not be read on both servers."
	case counts[VerdictDiverged] > 0 || (counts[VerdictABehind] > 0 && counts[VerdictBBehind] > 0):
		return VerdictDiverged, "Each server holds changes the other does not have yet."
	case counts[VerdictABehind] > 0:
		return VerdictABehind, "The first server lacks changes the second holds."
	case counts[VerdictBBehind] > 0:
		return VerdictBBehind, "The second server lacks changes the first holds."
	}
	return VerdictInStep, "Both servers hold the same latest change from every origin."
}
