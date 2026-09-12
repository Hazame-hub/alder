// Package plan answers "what would this do" without doing it.
//
// Alder could already preview one change exactly: the LDIF in the confirmation
// dialog is rendered from the same ChangeRecord that Session.Apply receives, so
// what you confirm is what runs. What it could not do was answer the question
// one level up -- given forty proposed changes and a directory, which of them
// are additions, which are modifications, which would do nothing at all, and
// which cannot be applied as written.
//
// The LDIF import came closest: it reconciles content records against live
// entries, reports how many it rewrote and lists the ones that already matched.
// This is that, generalised to any set of changes and made machine-readable,
// with the classification a client can switch on instead of parsing prose.
//
// # What this is not
//
// It is not a second way to work out what to write. The records a plan carries
// are the records Apply receives -- the same values, not an equivalent
// reconstruction -- which is the one property that stops a plan from describing
// a change the apply does not make. Everything here classifies and annotates
// records; nothing here invents one, except by calling the same reconcile the
// import path has always used.
package plan

import (
	"context"
	"errors"
	"fmt"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Action is what a change would do. It is a stable identifier: a client
// switches on it rather than reading the summary.
type Action string

const (
	// ActionAdd creates an entry that is not there.
	ActionAdd Action = "add"
	// ActionModify changes an entry that is.
	ActionModify Action = "modify"
	// ActionDelete removes one.
	ActionDelete Action = "delete"
	// ActionRename moves or renames one.
	ActionRename Action = "rename"
	// ActionUnchanged is a change that would do nothing: the entry already
	// holds what it describes. Reported rather than applied, because a no-op
	// modify reads as work that needs confirming.
	ActionUnchanged Action = "unchanged"
	// ActionSetPassword is an RFC 3062 password change. Separate from modify
	// because it is an extended operation rather than a modification, has no
	// LDIF form, and cannot be compared against current state at all -- so it
	// is never "unchanged", however many times it is applied.
	ActionSetPassword Action = "set_password"
	// ActionConflict is a change the directory would refuse as things stand:
	// modifying an entry that is not there, deleting one that has children.
	// Reported rather than attempted, and never applied by a plan.
	ActionConflict Action = "conflict"
)

// Item is one change and what the plan makes of it.
type Item struct {
	// Index is the change's position in the set it arrived in, so a caller can
	// line the plan up against what it sent.
	Index int
	// DN is the entry the change is about.
	DN dn.DN
	// Action is the classification.
	Action Action
	// Record is exactly what Apply will be given. Empty for unchanged and for
	// conflict, which are the two outcomes that apply nothing.
	Record directory.ChangeRecord
	// Reason explains an unchanged, a conflict, or a record the planner
	// rewrote. Prose, for a person; Action is what a client reads.
	Reason string
	// Baseline is the state this decision was made against. Handed back on
	// apply, where the server recomputes it.
	Baseline Baseline
	// Exists is whether the entry was there when the plan was made.
	Exists bool
	// SkippedAttributes are attributes left out of a reconciled record because
	// the directory owns them. Reported rather than dropped silently: a
	// document carrying entryUUID was exported with operational attributes and
	// the reader should know they are not being enforced.
	SkippedAttributes []string
}

// Counts is the summary, which is what an operator reads first.
type Counts struct {
	Examined    int
	Add         int
	Modify      int
	Delete      int
	Rename      int
	SetPassword int
	Unchanged   int
	Conflict    int
}

// Plan is the whole answer.
type Plan struct {
	Items  []Item
	Counts Counts
}

// Applicable is the records that would actually run, in order.
//
// This is the handoff between planning and applying, and it exists so that
// nothing between them re-derives anything: the caller takes these and gives
// them to Apply.
func (p Plan) Applicable() []directory.ChangeRecord {
	out := make([]directory.ChangeRecord, 0, len(p.Items))
	for _, item := range p.Items {
		switch item.Action {
		case ActionUnchanged, ActionConflict:
			continue
		}
		out = append(out, item.Record)
	}
	return out
}

// Reader is what a plan needs from a directory: the ability to look at what is
// there. Narrower than directory.Session on purpose -- a plan reads and never
// writes, and a type that cannot write cannot be made to by accident.
type Reader interface {
	Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error)
}

// ChildCounter is the optional extra a driver may provide, used to tell a
// delete that will work from one the directory will refuse.
type ChildCounter interface {
	HasChildren(ctx context.Context, target dn.DN) (bool, error)
}

// Options tune what the planner does.
type Options struct {
	// Reconcile turns an add of an existing entry into the modification that
	// makes it match, which is what closes the export-edit-import loop. Without
	// it such a record is a conflict, because a directory refuses an add for an
	// entry that exists.
	Reconcile bool
}

// Planner computes plans and checks them.
//
// It holds the fingerprint key and the driver's own test for "there is no such
// entry". Both are per-process rather than per-call, and neither is package
// state: a global would make the key a thing two Alders in one test binary
// share, and the not-found test a thing a driver could change under another.
type Planner struct {
	fp *Fingerprinter
	// notFound recognises the read failure that is not one. The concrete error
	// type belongs to the LDAP driver, which this package does not import --
	// planning is about records and entries, not about one protocol's codes.
	notFound func(error) bool
}

// NewPlanner returns a planner with a fresh fingerprint key.
func NewPlanner(notFound func(error) bool) (*Planner, error) {
	fp, err := NewFingerprinter()
	if err != nil {
		return nil, err
	}
	if notFound == nil {
		notFound = func(error) bool { return false }
	}
	return &Planner{fp: fp, notFound: notFound}, nil
}

// Compute classifies each record against the directory as it is now.
//
// The records must already be valid: this reports what they would do, not
// whether they are well formed, and a caller that has not validated is one
// whose plan describes a change Apply will refuse. The API layer validates
// before it gets here, exactly as it does before Apply.
func (pl *Planner) Compute(
	ctx context.Context,
	r Reader,
	sch *schema.Schema,
	records []directory.ChangeRecord,
	opts Options,
) (Plan, error) {
	p := Plan{Items: make([]Item, 0, len(records))}

	for i, record := range records {
		item, err := pl.classify(ctx, r, sch, i, record, opts)
		if err != nil {
			return Plan{}, err
		}
		p.Items = append(p.Items, item)
		p.Counts.Examined++
		switch item.Action {
		case ActionAdd:
			p.Counts.Add++
		case ActionModify:
			p.Counts.Modify++
		case ActionDelete:
			p.Counts.Delete++
		case ActionRename:
			p.Counts.Rename++
		case ActionSetPassword:
			p.Counts.SetPassword++
		case ActionUnchanged:
			p.Counts.Unchanged++
		case ActionConflict:
			p.Counts.Conflict++
		}
	}
	return p, nil
}

func (pl *Planner) classify(
	ctx context.Context,
	r Reader,
	sch *schema.Schema,
	index int,
	record directory.ChangeRecord,
	opts Options,
) (Item, error) {
	item := Item{Index: index, DN: record.DN, Record: record}

	live, err := r.Read(ctx, record.DN, Attributes(record))
	switch {
	case err == nil:
		item.Exists = live != nil
	case pl.notFound(err):
		item.Exists = false
		live = nil
	default:
		// Anything other than "it is not there" is a real failure. Planning
		// half a set and reporting the rest as absent would be worse than
		// saying the plan could not be made.
		return Item{}, fmt.Errorf("plan: reading %s: %w", record.DN, err)
	}
	item.Baseline = pl.fp.Of(record, live)

	switch record.Type {
	case directory.ChangeAdd:
		return planAdd(item, live, sch, opts), nil
	case directory.ChangeModify:
		return planModify(item, live), nil
	case directory.ChangeDelete:
		return planDelete(ctx, r, item), nil
	case directory.ChangeModRDN:
		return planRename(item), nil
	case directory.ChangeSetPassword:
		if !item.Exists {
			item.Action = ActionConflict
			item.Reason = "There is no such entry, so there is no password to set."
			item.Record = directory.ChangeRecord{}
			return item, nil
		}
		// Never unchanged: there is no way to ask a directory whether a
		// password is already the one being set, and guessing would be the
		// only place in Alder that reported a write as unnecessary without
		// having compared anything.
		item.Action = ActionSetPassword
		return item, nil
	}
	item.Action = ActionConflict
	item.Reason = fmt.Sprintf("Alder does not know how to plan a %q change.", record.Type)
	item.Record = directory.ChangeRecord{}
	return item, nil
}

// planAdd decides between creating an entry and reconciling one that is
// already there.
func planAdd(item Item, live *directory.Entry, sch *schema.Schema, opts Options) Item {
	if !item.Exists {
		item.Action = ActionAdd
		return item
	}
	if !opts.Reconcile {
		item.Action = ActionConflict
		item.Reason = "This entry already exists, and the change would create it. " +
			"Plan with reconcile to turn it into the modification that makes it match."
		item.Record = directory.ChangeRecord{}
		return item
	}

	// The same reconcile the import path has used since it was written. Not a
	// second implementation of "what would have to change": one of those is
	// how a plan and an apply come to disagree.
	outcome := Reconcile(item.Record, live, sch)
	item.SkippedAttributes = outcome.Skipped
	if !outcome.Changed {
		item.Action = ActionUnchanged
		item.Reason = "The entry already holds every value this record names."
		item.Record = directory.ChangeRecord{}
		return item
	}
	item.Action = ActionModify
	item.Reason = "The entry exists, so this record becomes the modification that makes " +
		"the attributes it names match. Attributes it does not name are left alone."
	item.Record = outcome.Change
	return item
}

func planModify(item Item, live *directory.Entry) Item {
	if !item.Exists {
		item.Action = ActionConflict
		item.Reason = "There is no such entry to modify."
		item.Record = directory.ChangeRecord{}
		return item
	}
	if satisfied(item.Record.Mods, live) {
		item.Action = ActionUnchanged
		item.Reason = "The entry already holds what this change would set."
		item.Record = directory.ChangeRecord{}
		return item
	}
	item.Action = ActionModify
	return item
}

func planDelete(ctx context.Context, r Reader, item Item) Item {
	if !item.Exists {
		item.Action = ActionUnchanged
		item.Reason = "There is no such entry, so there is nothing to delete."
		item.Record = directory.ChangeRecord{}
		return item
	}
	// A directory deletes leaves. An entry with children is refused, and
	// finding that out at apply time means finding it out halfway through a
	// changeset.
	if counter, ok := r.(ChildCounter); ok {
		if kids, err := counter.HasChildren(ctx, item.DN); err == nil && kids {
			item.Action = ActionConflict
			item.Reason = "This entry has children. LDAP deletes one leaf at a time, so " +
				"everything below it has to go first, deepest first."
			item.Record = directory.ChangeRecord{}
			return item
		}
	}
	item.Action = ActionDelete
	return item
}

func planRename(item Item) Item {
	if !item.Exists {
		item.Action = ActionConflict
		item.Reason = "There is no such entry to rename."
		item.Record = directory.ChangeRecord{}
		return item
	}
	item.Action = ActionRename
	return item
}

// satisfied reports whether every modification in a set is already true of the
// live entry.
//
// Conservative on purpose: anything it cannot decide is reported as a change.
// A plan that says "modify" and turns out to have nothing to do costs a line in
// a summary; one that says "unchanged" about a change that would have done
// something is the plan lying.
func satisfied(mods []directory.Mod, live *directory.Entry) bool {
	if len(mods) == 0 {
		return false
	}
	for _, m := range mods {
		current := live.Get(m.Name)
		switch m.Op {
		case directory.ModReplace:
			if !SameValues(current, m.Values) {
				return false
			}
		case directory.ModAdd:
			for _, v := range m.Values {
				if !containsValue(current, v) {
					return false
				}
			}
		case directory.ModDelete:
			if len(m.Values) == 0 {
				// Removing the whole attribute. Already gone means nothing to do.
				if len(current) > 0 {
					return false
				}
				continue
			}
			for _, v := range m.Values {
				if containsValue(current, v) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func containsValue(have [][]byte, want []byte) bool {
	for _, v := range have {
		if string(v) == string(want) {
			return true
		}
	}
	return false
}

// Verify reports whether the directory still looks the way the plan assumed.
//
// The server does this, from a fresh read, at apply time. A baseline the client
// hands back is only ever compared against one Alder recomputes; it is never
// trusted as a description of anything.
func (pl *Planner) Verify(
	ctx context.Context,
	r Reader,
	record directory.ChangeRecord,
	claimed Baseline,
) error {
	live, err := r.Read(ctx, record.DN, Attributes(record))
	switch {
	case err == nil:
	case pl.notFound(err):
		live = nil
	default:
		return fmt.Errorf("plan: re-reading %s: %w", record.DN, err)
	}
	if !pl.fp.Verify(record.DN.String(), claimed, live) {
		return &StaleError{DN: record.DN}
	}
	return nil
}

// StaleError is returned when the directory has moved since the plan was made.
type StaleError struct{ DN dn.DN }

func (e *StaleError) Error() string {
	return fmt.Sprintf("plan: %s has changed since this was planned", e.DN)
}

// IsStale reports whether an error is a stale baseline.
func IsStale(err error) bool {
	var stale *StaleError
	return errors.As(err, &stale)
}
