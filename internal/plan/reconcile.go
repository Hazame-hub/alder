package plan

import (
	"bytes"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// Importing a document whose entries already exist.
//
// An LDIF content record says "this entry looks like this". Applied as an add,
// that is a claim the entry does not exist yet, and a directory refuses it with
// entryAlreadyExists — which is correct, and useless as the second half of a
// loop whose first half is an export. Export a subtree, correct a dozen values
// in the file, import it back, and every record fails.
//
// Reconciling turns such a record into a modify that brings the existing entry
// to what the document describes.
//
// What it deliberately does *not* do is make the entry equal to the document.
// A record lists the attributes somebody chose to write down; an export omits
// operational attributes by default and omits userPassword always. Treating the
// document as the whole truth would delete a password because a file does not
// mention it. So each attribute the document names is replaced, and every
// attribute it does not name is left exactly as it is.

// ReconcileOutcome is what reconciling one record decided, and why.
type ReconcileOutcome struct {
	// Change is the modification to apply. Meaningful only when Changed.
	Change directory.ChangeRecord
	// Changed is false when the entry already matches the document, which is
	// the expected answer for a round trip with no edits. A no-op modify would
	// be a worse answer: it reads as work that needs confirming.
	Changed bool
	// Skipped names the attributes left out because the directory owns them.
	// They are reported rather than silently dropped: a document carrying
	// entryUUID was exported with operational attributes, and the reader should
	// know those are not being enforced.
	Skipped []string
}

// Reconcile turns an add record into the modify that makes the live entry match
// the attributes the document names.
func Reconcile(add directory.ChangeRecord, live *directory.Entry, sch *schema.Schema) ReconcileOutcome {
	out := ReconcileOutcome{
		Change: directory.ChangeRecord{DN: add.DN, Type: directory.ChangeModify},
	}

	for _, attr := range add.Attrs {
		base := schema.BaseName(attr.Name)

		// The directory owns these and will refuse to be told otherwise, which
		// would fail the whole record rather than the one attribute.
		if directoryOwns(sch, base) {
			out.Skipped = append(out.Skipped, attr.Name)
			continue
		}
		// A document that carries a password hash is reconciled on it like any
		// other attribute; one that does not must not be read as "remove it".
		// Both fall out of only ever touching what the document names.

		if SameValues(liveValues(live, attr.Name), attr.Values) {
			continue
		}
		out.Change.Mods = append(out.Change.Mods, directory.Mod{
			Op:     directory.ModReplace,
			Name:   attr.Name,
			Values: attr.Values,
		})
	}

	out.Changed = len(out.Change.Mods) > 0
	return out
}

// AppendNew adds the names not already present, so one attribute skipped in
// forty records is reported once rather than forty times.
func AppendNew(into, names []string) []string {
	for _, name := range names {
		seen := false
		for _, have := range into {
			if strings.EqualFold(have, name) {
				seen = true
				break
			}
		}
		if !seen {
			into = append(into, name)
		}
	}
	return into
}

// directoryOwns reports whether the server, not the operator, decides this
// attribute's values.
func directoryOwns(sch *schema.Schema, name string) bool {
	if sch == nil {
		return false
	}
	at := sch.AttributeType(name)
	if at == nil {
		// An attribute this server does not define is not one it owns. The
		// directory will refuse it on its own terms, and saying so there is
		// better than guessing here.
		return false
	}
	return sch.EffectiveNoUserModification(at) || sch.EffectiveUsage(at).Operational()
}

// liveValues reads an attribute off the live entry, matching the name the way
// LDAP does.
func liveValues(live *directory.Entry, name string) [][]byte {
	if live == nil {
		return nil
	}
	return live.Get(name)
}

// Byte-exact rather than by the attribute's matching rule. The rule would call
// "Bob" and "bob" equal for a caseIgnoreMatch attribute, and Alder would then
// decline to write a correction somebody deliberately made in the file. The
// case that has to be silent is the round trip — export, no edits, import —
// where the values come back from the same server byte for byte, and this is
// silent for exactly that.
// SameValues compares two attribute values as the sets they are.
func SameValues(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	left := sortedCopy(a)
	right := sortedCopy(b)
	for i := range left {
		if !bytes.Equal(left[i], right[i]) {
			return false
		}
	}
	return true
}

func sortedCopy(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}
