// Package outcome decides what actually happened to a change whose outcome
// the directory never confirmed.
//
// A write interrupted in flight leaves one question: did it land? The driver
// refuses to guess and refuses to repeat the change (see
// directory.ErrWriteOutcomeUnknown). This is the other half -- reading the
// directory afterwards and saying, as precisely as the directory can support,
// which of four things is true.
//
// Four, not two. "Applied" and "not applied" are the easy pair; the two that
// matter are the ones a boolean would have to lie about:
//
//   - Conflicted: the entry is there, and it is not what the change would
//     have made. Somebody else has been here. Re-applying would overwrite
//     their work with a change that was reviewed against a directory that no
//     longer exists.
//   - Undeterminable: the question cannot be answered. A password is the
//     clearest case -- its value is never readable, so no amount of looking
//     will say whether it changed. An unreadable entry is another.
//
// Nothing here applies anything, and nothing here is a retry. It answers a
// question, and what the operator does next goes through the ordinary
// plan, review and confirm path with a baseline read fresh today.
package outcome

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// Verdict is what reading the directory established.
type Verdict string

const (
	// Applied: the directory holds what the change would have made.
	//
	// It does not prove *this* change did it. Another administrator making
	// the same change is indistinguishable, and the distinction does not
	// matter: the state is what was wanted, and nothing more should be sent.
	Applied Verdict = "applied"

	// NotApplied: the directory is as it was. The change can be made again,
	// as a new change, planned against the directory as it is now.
	NotApplied Verdict = "not_applied"

	// Conflicted: the entry exists and holds something else. The change was
	// reviewed against a state that is gone, so it is not re-offered.
	Conflicted Verdict = "conflicted"

	// Undeterminable: reading cannot answer the question. The outcome stays
	// unresolved, and says so, which is the honest end of this road.
	Undeterminable Verdict = "undeterminable"
)

// Result is the verdict and the reason for it, in words an operator can act
// on without reading this file.
type Result struct {
	Verdict Verdict
	// Reason is one sentence. It says what was looked at and what was found,
	// never what Alder supposes.
	Reason string
	// Attribute names the first attribute that did not match, where one did
	// not. Empty otherwise.
	Attribute string
}

// Reader is the one thing this package needs from a directory: the entry as
// it is now, or the fact that it is not there.
//
// An interface rather than a Session so the decision can be tested against
// every shape of answer, including the ones a live harness makes awkward to
// produce on demand -- an entry that vanished, a read refused by the access
// rules, a server that stopped answering mid-check.
type Reader interface {
	// Read returns the entry, or a nil entry with no error when the entry
	// does not exist. An error means the question could not be asked.
	Read(dn string, attributes []string) (*directory.Entry, error)
}

// Determine reads the directory and says what became of the change.
//
// sch may be nil, in which case values are compared byte for byte rather
// than by each attribute's equality rule. That is stricter than the
// directory itself and can report Conflicted where the server would have
// said the values match, so the reason says when it happened.
func Determine(r Reader, ch directory.ChangeRecord, sch *schema.Schema) Result {
	switch ch.Type {
	case directory.ChangeSetPassword:
		// Not a limitation to be worked around later: the value is never
		// readable, by design and by the charter's rule about secrets. A
		// password change is permanently unresolved, and saying so is the
		// whole of what Alder can honestly offer.
		return Result{
			Verdict: Undeterminable,
			Reason: "A password cannot be read back, so whether it changed cannot be established by " +
				"looking. If the new password works, it was applied; if it does not, set it again.",
		}
	case directory.ChangeAdd:
		return determineAdd(r, ch, sch)
	case directory.ChangeDelete:
		return determineDelete(r, ch)
	case directory.ChangeModify:
		return determineModify(r, ch, sch)
	case directory.ChangeModRDN:
		return determineModRDN(r, ch)
	default:
		return Result{
			Verdict: Undeterminable,
			Reason:  "Alder does not know how to check a change of this kind.",
		}
	}
}

func determineAdd(r Reader, ch directory.ChangeRecord, sch *schema.Schema) Result {
	live, err := r.Read(ch.DN.String(), attributesOf(ch))
	if err != nil {
		return unreadable(err)
	}
	if live == nil {
		return Result{
			Verdict: NotApplied,
			Reason:  "The entry does not exist, so the add did not reach the directory.",
		}
	}
	// The entry is there. Whether this change put it there is a different
	// question, and an unanswerable one -- but if it holds what the change
	// described, the state is what was wanted either way.
	if name, differs := firstDifference(ch.Attrs, live, sch); differs {
		return Result{
			Verdict:   Conflicted,
			Attribute: name,
			Reason: "The entry exists but " + name + " does not hold what the change would have " +
				"written, so something other than this change created it.",
		}
	}
	return Result{
		Verdict: Applied,
		Reason:  "The entry exists and holds what the change described.",
	}
}

func determineDelete(r Reader, ch directory.ChangeRecord) Result {
	live, err := r.Read(ch.DN.String(), []string{"objectClass"})
	if err != nil {
		return unreadable(err)
	}
	if live == nil {
		return Result{
			Verdict: Applied,
			Reason:  "The entry is gone.",
		}
	}
	return Result{
		Verdict: NotApplied,
		Reason:  "The entry is still there, so the delete did not reach the directory.",
	}
}

func determineModify(r Reader, ch directory.ChangeRecord, sch *schema.Schema) Result {
	live, err := r.Read(ch.DN.String(), attributesOf(ch))
	if err != nil {
		return unreadable(err)
	}
	if live == nil {
		return Result{
			Verdict: Conflicted,
			Reason: "The entry no longer exists, so the modification cannot have been applied and " +
				"cannot be applied now.",
		}
	}

	// A modification is a list of operations, and only some of them say
	// anything checkable about the result. A replace states the value the
	// attribute ends up with, which is exactly what can be compared. An add
	// of one value among many, or a delete of a value, says less: the
	// attribute holding the value proves nothing about who put it there.
	var checked int
	for _, m := range ch.Mods {
		if secret(m.Name) {
			return Result{
				Verdict:   Undeterminable,
				Attribute: m.Name,
				Reason: "The change writes " + m.Name + ", whose value Alder never holds and cannot " +
					"read back, so whether it was applied cannot be established by looking.",
			}
		}
		switch m.Op {
		case directory.ModReplace:
			checked++
			if !sameValues(sch, m.Name, live.Get(m.Name), m.Values) {
				return Result{
					Verdict:   NotApplied,
					Attribute: m.Name,
					Reason:    "The entry does not hold the value the change would have written to " + m.Name + ".",
				}
			}
		case directory.ModAdd:
			checked++
			if missingAny(sch, m.Name, live.Get(m.Name), m.Values) {
				return Result{
					Verdict:   NotApplied,
					Attribute: m.Name,
					Reason:    "The entry does not hold the value the change would have added to " + m.Name + ".",
				}
			}
		case directory.ModDelete:
			checked++
			if presentAny(sch, m.Name, live.Get(m.Name), m.Values) {
				return Result{
					Verdict:   NotApplied,
					Attribute: m.Name,
					Reason:    "The entry still holds the value the change would have removed from " + m.Name + ".",
				}
			}
		}
	}
	if checked == 0 {
		return Result{
			Verdict: Undeterminable,
			Reason:  "The change contains nothing that can be checked by reading the entry.",
		}
	}
	return Result{
		Verdict: Applied,
		Reason:  "The entry holds everything the change would have written.",
	}
}

func determineModRDN(r Reader, ch directory.ChangeRecord) Result {
	target, err := ch.Target()
	if err != nil {
		return Result{
			Verdict: Undeterminable,
			Reason:  "The new name of the entry could not be worked out, so nothing can be checked: " + err.Error(),
		}
	}
	moved, err := r.Read(target.String(), []string{"objectClass"})
	if err != nil {
		return unreadable(err)
	}
	original, err := r.Read(ch.DN.String(), []string{"objectClass"})
	if err != nil {
		return unreadable(err)
	}

	switch {
	case moved != nil && original == nil:
		return Result{Verdict: Applied, Reason: "The entry is at its new name and no longer at the old one."}
	case moved == nil && original != nil:
		return Result{Verdict: NotApplied, Reason: "The entry is still at its old name."}
	case moved != nil && original != nil:
		// Both. A rename cannot produce this, so something else did.
		return Result{
			Verdict: Conflicted,
			Reason: "Entries exist at both the old and the new name, which a rename does not produce; " +
				"something else has changed this part of the tree.",
		}
	default:
		return Result{
			Verdict: Conflicted,
			Reason:  "Neither the old nor the new name exists, so the entry has been removed by something else.",
		}
	}
}

func unreadable(err error) Result {
	return Result{
		Verdict: Undeterminable,
		Reason:  "The entry could not be read, so the outcome is still unknown: " + err.Error(),
	}
}

// attributesOf is what has to be read to judge this change, plus objectClass
// so the entry can be recognised as existing at all.
func attributesOf(ch directory.ChangeRecord) []string {
	out := []string{"objectClass"}
	for _, a := range ch.Attrs {
		out = append(out, a.Name)
	}
	for _, m := range ch.Mods {
		out = append(out, m.Name)
	}
	return out
}

// firstDifference reports the first attribute of an add that the live entry
// does not hold as described.
//
// Only the attributes the change named are compared. A directory adds its own
// -- operational attributes, and the superclasses of every objectClass it was
// given -- and an entry that matches in every respect the change spoke about
// is the entry the change described.
func firstDifference(attrs []directory.Attribute, live *directory.Entry, sch *schema.Schema) (string, bool) {
	for _, a := range attrs {
		if secret(a.Name) {
			// Never read back, so never compared. Its absence from the
			// comparison is not evidence either way, and the other
			// attributes decide.
			continue
		}
		if strings.EqualFold(a.Name, "objectClass") {
			// The server expands these. Every class the change named must be
			// there; extra ones are the server's doing.
			if missingAny(sch, a.Name, live.Get(a.Name), a.Values) {
				return a.Name, true
			}
			continue
		}
		if !sameValues(sch, a.Name, live.Get(a.Name), a.Values) {
			return a.Name, true
		}
	}
	return "", false
}

// secret is an attribute whose value Alder never holds, so it can never be
// compared against the directory.
func secret(name string) bool {
	return schema.IsSensitive(schema.BaseName(name))
}
