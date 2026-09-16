// Package plan answers "what would this do" without doing it.
//
// Alder could already preview one change exactly: the LDIF in the confirmation
// dialog is rendered from the same ChangeRecord that Session.Apply receives, so
// what you confirm is what runs. What it could not do was answer the question
// one level up -- given forty proposed changes and a directory, which of them
// are additions, which are modifications, which would do nothing at all, and
// which cannot be applied as written.
//
// # The rule this package exists to keep
//
// The records a plan carries are the records Apply receives -- the same values,
// not an equivalent reconstruction. Nothing downstream of a plan decides again
// what to write. Since 1.5 that is enforced rather than conventional: each
// planned record carries a token binding the exact operation, and the apply
// path refuses a request that is not the operation that was planned (see
// baseline.go).
//
// # Two kinds of proposal
//
// A proposal is either an exact operation or a statement of desired state, and
// the two are never confused.
//
// An exact operation -- a changetype record, or a change built in the editor --
// is planned as written. It is checked against the directory (does the entry it
// modifies exist, does the one it adds not) and classified, but it is never
// rewritten: an add of an entry that exists is a conflict, not a modification
// somebody did not ask for.
//
// A desired-state proposal -- a content record, what an export produces -- says
// what an entry should look like. It is reconciled: created if absent, turned
// into the modification of the attributes it names if present, and reported
// unchanged if it already matches. Attributes it does not name are left alone,
// and an entry it does not mention is not a deletion.
package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	// ActionInvalid is a change the schema would refuse whatever state the
	// directory were in: an attribute no class on the entry permits, a second
	// value for a single-valued attribute. Separate from conflict because the
	// remedy is different -- a conflict may resolve itself when the directory
	// changes, an invalid change will not.
	ActionInvalid Action = "invalid"
)

// Intent is how a proposal is to be read.
type Intent int

const (
	// IntentExact plans the record as written.
	IntentExact Intent = iota
	// IntentDesired reconciles an add against the entry that is there.
	IntentDesired
)

func (i Intent) String() string {
	if i == IntentDesired {
		return "desired"
	}
	return "exact"
}

// Proposal is one change offered for planning, with how to read it.
type Proposal struct {
	Record directory.ChangeRecord
	Intent Intent
	// Expect, when set, is state the entry must hold for the change to apply.
	Expect *Expectation
}

// Exact wraps records as exact operations.
func Exact(records ...directory.ChangeRecord) []Proposal {
	out := make([]Proposal, 0, len(records))
	for _, r := range records {
		out = append(out, Proposal{Record: r, Intent: IntentExact})
	}
	return out
}

// ProblemCode says precisely why a change would not apply. Stable, like Action.
type ProblemCode string

const (
	// ProblemEntryMissing: the change needs an entry that is not there.
	ProblemEntryMissing ProblemCode = "entry_missing"
	// ProblemEntryExists: an exact add of an entry that is already there.
	ProblemEntryExists ProblemCode = "entry_exists"
	// ProblemHasChildren: a delete of an entry that is not a leaf.
	ProblemHasChildren ProblemCode = "has_children"
	// ProblemRenameTargetExists: a rename onto a DN that is already taken.
	ProblemRenameTargetExists ProblemCode = "rename_target_exists"
	// ProblemObjectClassUndefined: an object class the schema does not define.
	ProblemObjectClassUndefined ProblemCode = "object_class_undefined"
	// ProblemAttributeUndefined: an attribute type the schema does not define.
	ProblemAttributeUndefined ProblemCode = "attribute_undefined"
	// ProblemAttributeNotPermitted: an attribute no class on the entry allows.
	ProblemAttributeNotPermitted ProblemCode = "attribute_not_permitted"
	// ProblemSingleValue: more than one value for a single-valued attribute.
	ProblemSingleValue ProblemCode = "single_value_violation"
	// ProblemMissingRequired: an add that omits an attribute a class requires.
	ProblemMissingRequired ProblemCode = "missing_required_attribute"
)

// Problem is the typed reason a change would not apply.
type Problem struct {
	Code ProblemCode
	// Attribute names the attribute or object class concerned, where there is
	// one.
	Attribute string
}

// MembershipChange is one membership attribute's net effect on a group.
type MembershipChange struct {
	Attribute string
	// Gained and Removed are the values in the spelling the change or the
	// directory used. Net, not per modification: an add followed by a delete
	// of the same member is no change.
	Gained  []string
	Removed []string
}

// Item is one change and what the plan makes of it.
type Item struct {
	// Index is the change's position in the set it arrived in, so a caller can
	// line the plan up against what it sent.
	Index int
	// DN is the entry the change is about.
	DN dn.DN
	// Intent is how the proposal was read.
	Intent Intent
	// Action is the classification.
	Action Action
	// Record is exactly what Apply will be given. Empty for unchanged, conflict
	// and invalid, which are the outcomes that apply nothing.
	Record directory.ChangeRecord
	// Problem is set for conflict and invalid.
	Problem *Problem
	// Reason explains an unchanged, a conflict, or a record the planner
	// rewrote. Prose, for a person; Action and Problem are what a client reads.
	Reason string
	// Baseline binds the operation and the state it was planned against.
	// Handed back on apply, where the server checks both.
	Baseline Baseline
	// Exists is whether the entry was there when the plan was made.
	Exists bool
	// SkippedAttributes are attributes left out of a reconciled record because
	// the directory owns them.
	SkippedAttributes []string
	// Membership is what this change does to group membership, where it
	// touches a membership attribute.
	Membership []MembershipChange
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
	Invalid     int
}

// Subtree is a set of planned deletions that together remove a branch.
type Subtree struct {
	// Root is the topmost entry deleted.
	Root dn.DN
	// Entries is how many planned deletions fall under Root, Root included.
	Entries int
}

// Plan is the whole answer.
type Plan struct {
	Items  []Item
	Counts Counts
	// Subtrees groups deletions that remove a branch rather than a leaf, so a
	// deletion of four hundred entries does not read as four hundred unrelated
	// one-line changes -- or, worse, as the one entry at the top.
	Subtrees []Subtree
}

// Applicable is the records that would actually run, in order.
//
// This is the handoff between planning and applying, and it exists so that
// nothing between them re-derives anything: the caller takes these and gives
// them to Apply.
func (p Plan) Applicable() []directory.ChangeRecord {
	out := make([]directory.ChangeRecord, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Action.AppliesNothing() {
			continue
		}
		out = append(out, item.Record)
	}
	return out
}

// AppliesNothing reports whether an item of this action is left out of what is
// applied: unchanged, conflict and invalid.
func (a Action) AppliesNothing() bool {
	return a == ActionUnchanged || a == ActionConflict || a == ActionInvalid
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
	// Reconcile is the 1.4 switch: read every add as desired state. Only
	// consulted by Compute; ComputeProposals takes the intent per proposal.
	Reconcile bool
	// MembershipAttributes names the attributes that hold group members, so
	// the plan can report memberships gained and lost. Empty reports none.
	MembershipAttributes []string
	// SchemaLocator says which attributes of which entries hold schema
	// definitions, so changes to them are checked against each other and the
	// live schema. Nil checks none.
	SchemaLocator SchemaLocator
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

// Compute plans records with the 1.4 reading: exact, except that an add is
// desired state when Reconcile is set.
// ForSession returns a planner whose tokens bind secret values under a key
// scoped to one session. The API plans and verifies through it with the session
// ID, so a token carrying a secret is only ever reproducible where that secret
// was typed. The copy shares everything else, the fingerprint key included.
func (pl *Planner) ForSession(scope []byte) *Planner {
	scoped := *pl
	scoped.fp = pl.fp.Scoped(scope)
	return &scoped
}

func (pl *Planner) Compute(
	ctx context.Context,
	r Reader,
	sch *schema.Schema,
	records []directory.ChangeRecord,
	opts Options,
) (Plan, error) {
	proposals := make([]Proposal, 0, len(records))
	for _, rec := range records {
		intent := IntentExact
		if opts.Reconcile && rec.Type == directory.ChangeAdd {
			intent = IntentDesired
		}
		proposals = append(proposals, Proposal{Record: rec, Intent: intent})
	}
	return pl.ComputeProposals(ctx, r, sch, proposals, opts)
}

// ComputeProposals classifies each proposal against the directory as it is now.
//
// The records must already be valid in form: this reports what they would do,
// not whether they parse. The API layer validates before it gets here, exactly
// as it does before Apply.
func (pl *Planner) ComputeProposals(
	ctx context.Context,
	r Reader,
	sch *schema.Schema,
	proposals []Proposal,
	opts Options,
) (Plan, error) {
	p := Plan{Items: make([]Item, 0, len(proposals))}
	members := foldSet(opts.MembershipAttributes)

	// The schema as this set would leave it: a change that uses a definition an
	// earlier change in the same set adds is second, not invalid.
	view := newSchemaView(sch, opts.SchemaLocator)
	deps := newSchemaDeps(sch, opts.SchemaLocator)

	for i, proposal := range proposals {
		item, err := pl.classify(ctx, r, view.schema(), i, proposal, members)
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
		case ActionInvalid:
			p.Counts.Invalid++
		}
		// The dependency check runs here, not over the finished plan, so that
		// only a change that would actually run changes what the next one is
		// judged against: a schema change refused for its dependencies removes
		// nothing and adds nothing.
		deps.check(&p, i)
		if !p.Items[i].Action.AppliesNothing() {
			view.apply(proposal.Record)
		}
	}
	p.Subtrees = deletedSubtrees(p.Items)
	return p, nil
}

func (pl *Planner) classify(
	ctx context.Context,
	r Reader,
	sch *schema.Schema,
	index int,
	proposal Proposal,
	members map[string]bool,
) (Item, error) {
	record := proposal.Record
	item := Item{Index: index, DN: record.DN, Record: record, Intent: proposal.Intent}

	// One read per proposal: what the fingerprint needs, plus -- for a delete --
	// the membership the entry takes with it, which is impact rather than
	// state and so is read here but never folded into the baseline.
	attrs := Attributes(record)
	if record.Type == directory.ChangeDelete {
		attrs = append(attrs, sortedKeys(members)...)
	}
	if proposal.Expect != nil {
		attrs = append(attrs, proposal.Expect.ReadAttributes()...)
	}
	live, err := pl.read(ctx, r, record.DN, attrs)
	if err != nil {
		return Item{}, err
	}
	item.Exists = live != nil
	// The state this decision rests on is what the *proposal* named, even when
	// the operation that runs is narrower.
	deps := dependsOn(record)

	// A change derived from an earlier state is refused when the entry is no
	// longer in it, before anything else is decided about it. The attributes it
	// expects become part of what the plan depends on, so a change to them
	// between planning and applying is caught as a stale plan.
	if proposal.Expect != nil && live != nil {
		if attr, differs := proposal.Expect.Differs(sch, live); differs {
			return refuse(item, ActionConflict, ProblemExpectedStateDiffers, attr,
				"The entry no longer holds what this change expects: "+attr+
					" has changed since the state it was derived from. Nothing was decided for it."), nil
		}
		deps = mergeDependencies(deps, proposal.Expect.dependencies(sch, live))
	}

	switch record.Type {
	case directory.ChangeAdd:
		item = planAdd(item, live, sch, proposal.Intent)
	case directory.ChangeModify:
		item = planModify(item, live)
	case directory.ChangeDelete:
		item = planDelete(ctx, r, item)
	case directory.ChangeModRDN:
		item, err = pl.planRename(ctx, r, item)
		if err != nil {
			return Item{}, err
		}
	case directory.ChangeSetPassword:
		if !item.Exists {
			item = refuse(item, ActionConflict, ProblemEntryMissing, "",
				"There is no such entry, so there is no password to set.")
		} else {
			// Never unchanged: there is no way to ask a directory whether a
			// password is already the one being set, and guessing would be the
			// only place in Alder that reported a write as unnecessary without
			// having compared anything.
			item.Action = ActionSetPassword
		}
	default:
		return Item{}, fmt.Errorf("plan: Alder does not know how to plan a %q change", record.Type)
	}

	// The schema is checked against the operation that would actually run --
	// the reconciled modification, not the add it came from -- because that
	// is what the directory will be asked to accept.
	if item.Action == ActionAdd || item.Action == ActionModify {
		if problem := validate(sch, item.Record, live); problem != nil {
			item = refuse(item, ActionInvalid, problem.Code, problem.Attribute,
				problemReason(*problem))
		}
	}

	if len(members) > 0 {
		item.Membership = membershipChanges(item, live, members)
	}
	if !item.Action.AppliesNothing() {
		item.Baseline = pl.fp.Token(deps, item.Record, live)
	}
	return item, nil
}

// read fetches an entry, turning "there is no such entry" into nil.
//
// Anything other than "it is not there" is a real failure. Planning half a set
// and reporting the rest as absent would be worse than saying the plan could not
// be made.
func (pl *Planner) read(ctx context.Context, r Reader, target dn.DN, attrs []string) (*directory.Entry, error) {
	live, err := r.Read(ctx, target, attrs)
	switch {
	case err == nil:
		return live, nil
	case pl.notFound(err):
		return nil, nil
	default:
		return nil, fmt.Errorf("plan: reading %s: %w", target, err)
	}
}

// refuse turns an item into one that applies nothing.
func refuse(item Item, action Action, code ProblemCode, attribute, reason string) Item {
	item.Action = action
	item.Problem = &Problem{Code: code, Attribute: attribute}
	item.Reason = reason
	item.Record = directory.ChangeRecord{}
	return item
}

// planAdd decides between creating an entry, refusing to, and reconciling one
// that is already there -- and which of those depends on the intent, never on
// a guess.
func planAdd(item Item, live *directory.Entry, sch *schema.Schema, intent Intent) Item {
	if !item.Exists {
		item.Action = ActionAdd
		return item
	}
	if intent != IntentDesired {
		return refuse(item, ActionConflict, ProblemEntryExists, "",
			"This entry already exists, and the change would create it. An explicit "+
				"add is planned as written; offer the record as desired state to "+
				"reconcile it instead.")
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
		return refuse(item, ActionConflict, ProblemEntryMissing, "", "There is no such entry to modify.")
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
			return refuse(item, ActionConflict, ProblemHasChildren, "",
				"This entry has children. LDAP deletes one leaf at a time, so "+
					"everything below it has to go first, deepest first.")
		}
	}
	item.Action = ActionDelete
	return item
}

func (pl *Planner) planRename(ctx context.Context, r Reader, item Item) (Item, error) {
	if !item.Exists {
		return refuse(item, ActionConflict, ProblemEntryMissing, "", "There is no such entry to rename."), nil
	}
	target, err := item.Record.Target()
	if err != nil {
		return Item{}, fmt.Errorf("plan: working out where %s would move: %w", item.DN, err)
	}
	// Renaming onto a name that is taken is refused by every directory. A
	// rename to the same name -- a change of case, say -- is not a collision.
	if !target.Equal(item.DN) {
		occupant, readErr := pl.read(ctx, r, target, []string{"objectClass"})
		if readErr != nil {
			return Item{}, readErr
		}
		if occupant != nil {
			return refuse(item, ActionConflict, ProblemRenameTargetExists, "",
				"There is already an entry at "+target.String()+"."), nil
		}
	}
	item.Action = ActionRename
	return item, nil
}

// satisfied reports whether every modification in a set is already true of the
// live entry.
//
// Conservative on purpose: anything it cannot decide is reported as a change.
// A plan that says "modify" and turns out to have nothing to do costs a line in
// a summary; one that says "unchanged" about a change that would have done
// something is the plan lying.
//
// Each attribute's current values are indexed once, so adding one member to a
// group of a hundred thousand is a map lookup rather than a scan per value --
// the compare endpoint taught that lesson in 1.4 and this path would otherwise
// have had to learn it again.
func satisfied(mods []directory.Mod, live *directory.Entry) bool {
	if len(mods) == 0 {
		return false
	}
	indexes := map[string]map[string]bool{}
	valuesOf := func(name string) map[string]bool {
		key := strings.ToLower(name)
		if set, ok := indexes[key]; ok {
			return set
		}
		current := live.Get(name)
		set := make(map[string]bool, len(current))
		for _, v := range current {
			set[string(v)] = true
		}
		indexes[key] = set
		return set
	}

	for _, m := range mods {
		switch m.Op {
		case directory.ModReplace:
			if !SameValues(live.Get(m.Name), m.Values) {
				return false
			}
		case directory.ModAdd:
			have := valuesOf(m.Name)
			for _, v := range m.Values {
				if !have[string(v)] {
					return false
				}
			}
		case directory.ModDelete:
			have := valuesOf(m.Name)
			if len(m.Values) == 0 {
				// Removing the whole attribute. Already gone means nothing to do.
				if len(have) > 0 {
					return false
				}
				continue
			}
			for _, v := range m.Values {
				if have[string(v)] {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// Verify reports whether an apply request is still the plan it claims to be.
//
// The server does this, from a fresh read, at apply time. A token the client
// hands back is only ever compared against one Alder recomputes; it is never
// trusted as a description of anything.
//
// The read covers the attributes the token names as well as the ones the
// operation touches. They differ for a reconciled change -- planned from an add
// naming several attributes, applied as a modify of fewer -- and a directory
// returns only what it is asked for. Reading only the operation's attributes
// made every reconciled plan look stale against a real server, which the 1.4
// fake could not show because it returned whole entries.
func (pl *Planner) Verify(
	ctx context.Context,
	r Reader,
	record directory.ChangeRecord,
	claimed Baseline,
) error {
	_, _, err := pl.VerifyRead(ctx, r, nil, record, claimed, nil, nil)
	return err
}

// VerifyRead is Verify for a change that may carry an expectation, returning
// the entry it read -- nil when there is none -- and the attributes it asked
// for.
//
// The expectation is checked again here, not only at plan time. The token
// binds the attributes the entry held when it was planned, so an attribute
// added since is not one it can see; an exhaustive expectation can. extra
// widens the one read for a caller that needs more of the entry, which is how
// a recovery bundle is derived without reading the entry a second time.
func (pl *Planner) VerifyRead(
	ctx context.Context,
	r Reader,
	sch *schema.Schema,
	record directory.ChangeRecord,
	claimed Baseline,
	expect *Expectation,
	extra []string,
) (*directory.Entry, []string, error) {
	attrs := append(Attributes(record), claimed.Attributes()...)
	if expect != nil {
		attrs = append(attrs, expect.ReadAttributes()...)
	}
	attrs = append(attrs, extra...)
	live, err := pl.read(ctx, r, record.DN, attrs)
	if err != nil {
		return nil, nil, fmt.Errorf("plan: re-reading %s: %w", record.DN, err)
	}
	switch pl.fp.Check(claimed, record, live) {
	case VerdictMismatch:
		return nil, nil, &MismatchError{DN: record.DN}
	case VerdictStale:
		return nil, nil, &StaleError{DN: record.DN}
	}
	if expect != nil {
		if live == nil {
			return nil, nil, &StaleError{DN: record.DN}
		}
		if _, differs := expect.Differs(sch, live); differs {
			return nil, nil, &StaleError{DN: record.DN}
		}
	}
	return live, attrs, nil
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

// MismatchError is returned when a request carries a baseline for a different
// operation than the one it asks for.
type MismatchError struct{ DN dn.DN }

func (e *MismatchError) Error() string {
	return fmt.Sprintf("plan: the change to %s is not the change that was planned", e.DN)
}

// IsMismatch reports whether an error is a request that is not its plan.
func IsMismatch(err error) bool {
	var mismatch *MismatchError
	return errors.As(err, &mismatch)
}
