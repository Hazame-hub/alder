// Package diff compares two directory states and says what differs.
//
// A diff is a statement of fact with a direction: source is where you are,
// target is what you are comparing it with, and "added" means present in the
// target and not in the source. It is not executable. What can be done about a
// difference is a separate question, answered by Derive, and even Derive only
// proposes change records for the existing plan pipeline to judge against the
// directory as it is at that moment.
//
// Three rules keep a diff honest:
//
//   - Equality follows the attribute's equality rule where both sides recorded
//     the same known rule, and bytes otherwise.
//   - A rename is only recognised from a stable identity both sides share,
//     never from resemblance. Without one, a moved entry is removed at one DN
//     and added at another, which is what the data can prove.
//   - What could not be seen is unknown, not absent. An entry beyond a search
//     limit, outside the other side's scope, or an attribute the live
//     directory refuses to show, is classified unknown, and makes the whole
//     comparison partial.
package diff

import (
	"context"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Kind classifies an entry or an attribute difference. The values are stable
// identifiers for clients to switch on.
type Kind string

const (
	Added     Kind = "added"
	Removed   Kind = "removed"
	Modified  Kind = "modified"
	Renamed   Kind = "renamed"
	Unchanged Kind = "unchanged"
	Unknown   Kind = "unknown"
)

// Reason codes for a partial comparison or an unknown item.
const (
	ReasonSearchLimit        = "search_limit_reached"
	ReasonScopeMismatch      = "scope_mismatch"
	ReasonInsufficientAccess = "insufficient_access"
	ReasonAccessNotVerified  = "access_not_verified"
	ReasonSchemaUnavailable  = "schema_unavailable"
)

// Reason is why a comparison is partial.
type Reason struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Side is one state being compared.
type Side struct {
	Snapshot *snapshot.Snapshot
	// Live reports that this side was read from the directory the session is
	// connected to just now, rather than loaded from a file.
	Live bool
	// Truncated reports that the live read stopped at a bound, so an entry
	// missing from this side may simply not have been reached.
	Truncated bool
}

// AttributeChange is one attribute's difference within an entry.
type AttributeChange struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Added and Removed are the values present on only one side, in the form
	// that side holds them, capped at MaxValues each.
	Added            []snapshot.Value `json:"added,omitempty"`
	Removed          []snapshot.Value `json:"removed,omitempty"`
	AddedOmitted     int              `json:"addedOmitted,omitempty"`
	RemovedOmitted   int              `json:"removedOmitted,omitempty"`
	Operational      bool             `json:"operational,omitempty"`
	Sensitive        bool             `json:"sensitive,omitempty"`
	ComparedByBytes  bool             `json:"comparedByBytes,omitempty"`
	WithheldSource   int              `json:"withheldSource,omitempty"`
	WithheldTarget   int              `json:"withheldTarget,omitempty"`
	UnknownReason    string           `json:"unknownReason,omitempty"`
	sourceHasNothing bool
	targetHasNothing bool
}

// Item is one entry's difference.
type Item struct {
	Kind       Kind              `json:"kind"`
	SourceDN   string            `json:"sourceDn,omitempty"`
	TargetDN   string            `json:"targetDn,omitempty"`
	Attributes []AttributeChange `json:"attributes,omitempty"`
	Reason     string            `json:"reason,omitempty"`

	source, target *snapshot.Entry
	sourceDN       dn.DN
	targetDN       dn.DN
}

// Counts tallies the items by kind.
type Counts struct {
	Compared  int `json:"compared"`
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Modified  int `json:"modified"`
	Renamed   int `json:"renamed"`
	Unchanged int `json:"unchanged"`
	Unknown   int `json:"unknown"`
}

// Result is a whole comparison.
type Result struct {
	Complete bool     `json:"complete"`
	Reasons  []Reason `json:"reasons,omitempty"`
	Counts   Counts   `json:"counts"`
	// CrossVendor reports that the two sides came from different server
	// products, or that one side's server did not name itself. The comparison
	// runs; its reader should know.
	CrossVendor bool `json:"crossVendor"`
	// ComparedByBytes lists attributes compared byte for byte because no
	// shared, known equality rule was recorded for them.
	ComparedByBytes []string `json:"comparedByBytes,omitempty"`
	// RuleDifferences lists attributes whose equality rule differs between the
	// sides. They are compared byte for byte.
	RuleDifferences []string `json:"ruleDifferences,omitempty"`
	// OperationalIgnored reports that operational attributes were left out of
	// the comparison because one side did not capture them.
	OperationalIgnored bool   `json:"operationalIgnored"`
	Items              []Item `json:"items"`

	source, target Side
}

// Probe answers whether an attribute absent from a live entry is absent or
// merely hidden from the bound identity.
type Probe func(ctx context.Context, target dn.DN, attribute string) (directory.AttributeVisibility, error)

// Options tunes a comparison.
type Options struct {
	// IncludeUnchanged lists unchanged entries as items; otherwise they are
	// only counted.
	IncludeUnchanged bool
	// MaxValues caps the values listed per attribute side. Zero means 1000.
	MaxValues int
	// Probe, when set, is asked about attributes a live side lacks and the
	// other side holds, at most ProbeBudget times.
	Probe       Probe
	ProbeBudget int
}

// Compare compares source with target.
func Compare(ctx context.Context, source, target Side, opts Options) (*Result, error) {
	if opts.MaxValues <= 0 {
		opts.MaxValues = 1000
	}
	r := &Result{Complete: true, Items: []Item{}, source: source, target: target}
	s, t := source.Snapshot, target.Snapshot

	// Different products, or one side from a server that does not identify
	// itself: either way the two cannot be assumed to share semantics.
	if !strings.EqualFold(strings.TrimSpace(s.Source.Vendor), strings.TrimSpace(t.Source.Vendor)) {
		r.CrossVendor = true
	}
	includeOperational := s.OperationalAttributes && t.OperationalAttributes
	r.OperationalIgnored = s.OperationalAttributes != t.OperationalAttributes

	sameScope := sameCapture(s, t)
	filtersDiffer := strings.TrimSpace(s.Source.Filter) != strings.TrimSpace(t.Source.Filter)
	if !sameScope {
		r.addReason(snapshot.Source{}, ReasonScopeMismatch,
			"the two sides were captured with a different base, scope or filter, so an entry present on only one side may lie outside the other")
	}
	if source.Truncated || target.Truncated {
		r.addReason(snapshot.Source{}, ReasonSearchLimit,
			"the live read stopped at its bound, so entries it did not reach are unknown rather than absent")
	}
	if !s.SchemaAvailable || !t.SchemaAvailable {
		r.addReason(snapshot.Source{}, ReasonSchemaUnavailable,
			"one side has no schema, so its values are compared byte for byte")
	}

	rules := newRuleBook(s, t, r)
	probes := 0

	// presenceUnknown reports whether "not on that side" cannot be trusted.
	presenceUnknown := func(other Side, otherSnap *snapshot.Snapshot, d dn.DN) string {
		if other.Truncated {
			return ReasonSearchLimit
		}
		if filtersDiffer || !otherSnap.InScope(d) {
			return ReasonScopeMismatch
		}
		return ""
	}

	matchedTarget := make([]bool, len(t.Entries))
	for i := range s.Entries {
		se := &s.Entries[i]
		sd := s.DNAt(i)
		key := snapshot.DNKey(sd)
		if te, td, ok := t.EntryByKey(key); ok {
			matchedTarget[indexOf(t, key)] = true
			item := Item{source: se, target: te, sourceDN: sd, targetDN: td, SourceDN: se.DN, TargetDN: te.DN}
			item.Attributes = compareAttributes(se, te, rules, includeOperational, opts.MaxValues)
			if err := r.probeMissing(ctx, &item, source, target, opts, &probes); err != nil {
				return nil, err
			}
			r.classify(item, opts.IncludeUnchanged)
			continue
		}
		if te, td, ok := t.EntryByID(se.ID); ok {
			if _, _, clash := s.EntryByKey(snapshot.DNKey(td)); !clash {
				matchedTarget[indexOf(t, snapshot.DNKey(td))] = true
				item := Item{Kind: Renamed, source: se, target: te, sourceDN: sd, targetDN: td, SourceDN: se.DN, TargetDN: te.DN}
				item.Attributes = compareAttributes(se, te, rules, includeOperational, opts.MaxValues)
				if err := r.probeMissing(ctx, &item, source, target, opts, &probes); err != nil {
					return nil, err
				}
				r.add(item)
				continue
			}
		}
		item := Item{Kind: Removed, source: se, sourceDN: sd, SourceDN: se.DN}
		if why := presenceUnknown(target, t, sd); why != "" {
			item.Kind, item.Reason = Unknown, why
		}
		r.add(item)
	}
	for i := range t.Entries {
		if matchedTarget[i] {
			continue
		}
		te := &t.Entries[i]
		td := t.DNAt(i)
		item := Item{Kind: Added, target: te, targetDN: td, TargetDN: te.DN}
		if why := presenceUnknown(source, s, td); why != "" {
			item.Kind, item.Reason = Unknown, why
		}
		r.add(item)
	}

	sort.SliceStable(r.Items, func(a, b int) bool { return itemKey(r.Items[a]) < itemKey(r.Items[b]) })
	r.ComparedByBytes = rules.bytesList()
	r.RuleDifferences = rules.differenceList()
	return r, nil
}

func indexOf(s *snapshot.Snapshot, key string) int {
	// EntryByKey returned a pointer into s.Entries; recover its index.
	e, _, _ := s.EntryByKey(key)
	for i := range s.Entries {
		if &s.Entries[i] == e {
			return i
		}
	}
	return -1
}

func itemKey(it Item) string {
	if it.TargetDN != "" {
		return strings.ToLower(it.TargetDN)
	}
	return strings.ToLower(it.SourceDN)
}

func sameCapture(a, b *snapshot.Snapshot) bool {
	ab, errA := dn.Parse(a.Source.Base)
	bb, errB := dn.Parse(b.Source.Base)
	return errA == nil && errB == nil && ab.Equal(bb) && a.Source.Scope == b.Source.Scope &&
		strings.TrimSpace(a.Source.Filter) == strings.TrimSpace(b.Source.Filter)
}

func (r *Result) addReason(_ snapshot.Source, code, detail string) {
	for _, existing := range r.Reasons {
		if existing.Code == code {
			return
		}
	}
	r.Complete = false
	r.Reasons = append(r.Reasons, Reason{Code: code, Detail: detail})
}

func (r *Result) classify(item Item, includeUnchanged bool) {
	for _, a := range item.Attributes {
		if a.Kind == Unknown {
			item.Kind = Modified
			break
		}
	}
	if item.Kind == "" {
		if len(item.Attributes) == 0 {
			item.Kind = Unchanged
		} else {
			item.Kind = Modified
		}
	}
	if item.Kind == Unchanged && !includeUnchanged {
		r.Counts.Compared++
		r.Counts.Unchanged++
		return
	}
	r.add(item)
}

func (r *Result) add(item Item) {
	r.Counts.Compared++
	switch item.Kind {
	case Added:
		r.Counts.Added++
	case Removed:
		r.Counts.Removed++
	case Modified:
		r.Counts.Modified++
	case Renamed:
		r.Counts.Renamed++
	case Unchanged:
		r.Counts.Unchanged++
	case Unknown:
		r.Counts.Unknown++
	}
	r.Items = append(r.Items, item)
}

// probeMissing asks the live directory about attributes it lacks and the other
// side holds, and turns a denied or unverified answer into unknown.
func (r *Result) probeMissing(ctx context.Context, item *Item, source, target Side, opts Options, probes *int) error {
	for i := range item.Attributes {
		a := &item.Attributes[i]
		var liveDN dn.DN
		switch {
		case source.Live && a.sourceHasNothing && !a.targetHasNothing:
			liveDN = item.sourceDN
		case target.Live && a.targetHasNothing && !a.sourceHasNothing:
			liveDN = item.targetDN
		default:
			continue
		}
		if opts.Probe == nil {
			continue
		}
		if *probes >= opts.ProbeBudget {
			a.Kind, a.UnknownReason = Unknown, ReasonAccessNotVerified
			r.addReason(snapshot.Source{}, ReasonAccessNotVerified,
				"more attributes were missing from the live directory than could be checked for access, and those not checked are unknown")
			continue
		}
		*probes++
		visibility, err := opts.Probe(ctx, liveDN, a.Name)
		if err != nil {
			return err
		}
		switch visibility {
		case directory.VisibilityAbsent:
		case directory.VisibilityDenied:
			a.Kind, a.UnknownReason = Unknown, ReasonInsufficientAccess
			r.addReason(snapshot.Source{}, ReasonInsufficientAccess,
				"the bound identity may not read some attributes, so their absence from the live directory is not evidence of anything")
		default:
			a.Kind, a.UnknownReason = Unknown, ReasonAccessNotVerified
			r.addReason(snapshot.Source{}, ReasonAccessNotVerified,
				"the directory did not say whether some missing attributes are absent or hidden")
		}
	}
	return nil
}

// ruleBook decides, once per attribute, how its values compare.
type ruleBook struct {
	s, t        *snapshot.Snapshot
	cache       map[string]snapshot.Keyer
	byBytes     map[string]string
	differences map[string]string
}

func newRuleBook(s, t *snapshot.Snapshot, _ *Result) *ruleBook {
	return &ruleBook{s: s, t: t, cache: map[string]snapshot.Keyer{},
		byBytes: map[string]string{}, differences: map[string]string{}}
}

func (rb *ruleBook) keyer(name string) (snapshot.Keyer, bool) {
	key := strings.ToLower(schema.BaseName(name))
	if k, ok := rb.cache[key]; ok {
		_, exact := rb.byBytes[key]
		_, differs := rb.differences[key]
		return k, exact || differs
	}
	si, sok := rb.s.Info(name)
	ti, tok := rb.t.Info(name)
	var chosen snapshot.Keyer = snapshot.ExactKey
	switch {
	case sok && tok && si.Equality != "" && ti.Equality != "" && !strings.EqualFold(si.Equality, ti.Equality):
		rb.differences[key] = name
	case sok && tok && strings.EqualFold(si.Equality, ti.Equality):
		if k, known := snapshot.KeyerForRule(si.Equality); known {
			chosen = k
		} else {
			rb.byBytes[key] = name
		}
	case sok != tok:
		// Only one side holds the attribute at all, so no value of it is ever
		// compared with another: its own rule, where known, lists the values,
		// and saying it was compared by bytes would claim an imprecision there
		// was no occasion for.
		info := si
		if tok {
			info = ti
		}
		if k, known := snapshot.KeyerForRule(info.Equality); known {
			chosen = k
		}
		rb.cache[key] = chosen
		return chosen, false
	default:
		rb.byBytes[key] = name
	}
	rb.cache[key] = chosen
	_, exact := rb.byBytes[key]
	_, differs := rb.differences[key]
	return chosen, exact || differs
}

func (rb *ruleBook) bytesList() []string      { return sortedValues(rb.byBytes) }
func (rb *ruleBook) differenceList() []string { return sortedValues(rb.differences) }

func sortedValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

type keyedValues struct {
	values []snapshot.Value
	keys   []string
	set    map[string]bool
}

func keyValues(values []snapshot.Value, keyer snapshot.Keyer) keyedValues {
	kv := keyedValues{values: values, keys: make([]string, len(values)), set: make(map[string]bool, len(values))}
	for i, v := range values {
		raw, _ := v.Bytes()
		kv.keys[i] = keyer(raw)
		kv.set[kv.keys[i]] = true
	}
	return kv
}

func attributeMap(e *snapshot.Entry) map[string]*snapshot.Attribute {
	m := make(map[string]*snapshot.Attribute, len(e.Attributes))
	for i := range e.Attributes {
		m[strings.ToLower(e.Attributes[i].Name)] = &e.Attributes[i]
	}
	return m
}

func compareAttributes(se, te *snapshot.Entry, rules *ruleBook, includeOperational bool, maxValues int) []AttributeChange {
	sm, tm := attributeMap(se), attributeMap(te)
	names := make([]string, 0, len(sm)+len(tm))
	display := map[string]string{}
	for _, e := range []*snapshot.Entry{se, te} {
		for _, a := range e.Attributes {
			key := strings.ToLower(a.Name)
			if _, seen := display[key]; !seen {
				display[key] = a.Name
				names = append(names, key)
			}
		}
	}
	sort.Strings(names)

	var out []AttributeChange
	for _, key := range names {
		name := display[key]
		sa, ta := sm[key], tm[key]
		operational := isOperational(rules.s, name) || isOperational(rules.t, name)
		if operational && !includeOperational {
			continue
		}
		sensitive := schema.IsSensitive(name)
		change := AttributeChange{Name: name, Operational: operational, Sensitive: sensitive,
			sourceHasNothing: sa == nil, targetHasNothing: ta == nil}

		if sensitive {
			sw, tw := withheld(sa), withheld(ta)
			if sw == tw {
				continue
			}
			change.WithheldSource, change.WithheldTarget = sw, tw
			switch {
			case sa == nil:
				change.Kind = Added
			case ta == nil:
				change.Kind = Removed
			default:
				change.Kind = Modified
			}
			out = append(out, change)
			continue
		}

		keyer, byBytes := rules.keyer(name)
		change.ComparedByBytes = byBytes
		var sv, tv keyedValues
		if sa != nil {
			sv = keyValues(sa.Values, keyer)
		}
		if ta != nil {
			tv = keyValues(ta.Values, keyer)
		}
		for i, k := range tv.keys {
			if !sv.set[k] {
				if len(change.Added) < maxValues {
					change.Added = append(change.Added, tv.values[i])
				} else {
					change.AddedOmitted++
				}
			}
		}
		for i, k := range sv.keys {
			if !tv.set[k] {
				if len(change.Removed) < maxValues {
					change.Removed = append(change.Removed, sv.values[i])
				} else {
					change.RemovedOmitted++
				}
			}
		}
		if len(change.Added)+change.AddedOmitted+len(change.Removed)+change.RemovedOmitted == 0 {
			continue
		}
		switch {
		case sa == nil:
			change.Kind = Added
		case ta == nil:
			change.Kind = Removed
		default:
			change.Kind = Modified
		}
		out = append(out, change)
	}
	return out
}

func withheld(a *snapshot.Attribute) int {
	if a == nil {
		return 0
	}
	return a.Withheld
}

func isOperational(s *snapshot.Snapshot, name string) bool {
	info, ok := s.Info(name)
	return ok && info.Operational
}
