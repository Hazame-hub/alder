package diff

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Candidate is what a difference would become as plan input.
//
// It is only ever input. The records go to the plan like any other change,
// which re-reads the directory, classifies them against what is there now, and
// issues the tokens an apply is held to. Nothing here writes, and nothing here
// is trusted as a description of the live directory.
type Candidate struct {
	Records []directory.ChangeRecord
	// Destructive marks a candidate that deletes an entry. It is never
	// selected on anyone's behalf.
	Destructive bool
	// Blocked explains why there are no records, as a stable identifier.
	Blocked string
}

// Reasons a difference cannot be turned into plan input.
const (
	BlockedSourceNotLive      = "source_not_live"
	BlockedIncomplete         = "incomplete_comparison"
	BlockedNothingToChange    = "nothing_to_change"
	BlockedUnknown            = "unknown_difference"
	BlockedOnlyUnchangeable   = "only_unchangeable_attributes"
	BlockedUnresolvableRename = "unresolvable_rename"
)

// Derive turns one difference into the change records that would move the
// source -- which must be the live directory -- toward the target.
//
// The rules are the safety of the whole feature:
//
//   - The source must be live. A diff between two files proposes nothing,
//     because there is no directory it describes.
//   - An entry present only in the target becomes an add of what the target
//     holds, without operational attributes and without sensitive values,
//     which a snapshot never has.
//   - An entry present only in the live directory becomes a delete, marked
//     destructive, and only when the comparison is complete. A partial
//     comparison cannot prove an entry should not exist.
//   - A modification deletes the values the target lacks and adds the values
//     the source lacks, attribute by attribute, never touching an operational,
//     sensitive or unknown attribute.
//   - A rename becomes a modrdn to the target's name, then the modification of
//     whatever else differs.
func Derive(r *Result, item Item) Candidate {
	if !r.source.Live {
		return Candidate{Blocked: BlockedSourceNotLive}
	}
	switch item.Kind {
	case Unchanged:
		return Candidate{Blocked: BlockedNothingToChange}
	case Unknown:
		return Candidate{Blocked: BlockedUnknown}
	case Added:
		return deriveAdd(r, item)
	case Removed:
		if !r.Complete {
			return Candidate{Destructive: true, Blocked: BlockedIncomplete}
		}
		return Candidate{Destructive: true, Records: []directory.ChangeRecord{{DN: item.sourceDN, Type: directory.ChangeDelete}}}
	case Modified:
		mods := modsFor(r, item, nil)
		if len(mods) == 0 {
			return Candidate{Blocked: BlockedOnlyUnchangeable}
		}
		return Candidate{Records: []directory.ChangeRecord{{DN: item.sourceDN, Type: directory.ChangeModify, Mods: mods}}}
	case Renamed:
		return deriveRename(r, item)
	}
	return Candidate{Blocked: BlockedNothingToChange}
}

func deriveAdd(r *Result, item Item) Candidate {
	rec := directory.ChangeRecord{DN: item.targetDN, Type: directory.ChangeAdd}
	for _, a := range item.target.Attributes {
		if a.Withheld > 0 || isOperational(r.target.Snapshot, a.Name) || isIdentity(a.Name) {
			continue
		}
		values := rawValues(a.Values)
		if len(values) == 0 {
			continue
		}
		rec.Attrs = append(rec.Attrs, directory.Attribute{Name: a.Name, Values: values})
	}
	if len(rec.Attrs) == 0 {
		return Candidate{Blocked: BlockedOnlyUnchangeable}
	}
	return Candidate{Records: []directory.ChangeRecord{rec}}
}

func deriveRename(r *Result, item Item) Candidate {
	oldRDN, newRDN := item.sourceDN.RDN(), item.targetDN.RDN()
	if len(newRDN) == 0 {
		return Candidate{Blocked: BlockedUnresolvableRename}
	}
	rename := directory.ChangeRecord{DN: item.sourceDN, Type: directory.ChangeModRDN, NewRDN: newRDN.String()}
	if !item.sourceDN.Parent().Equal(item.targetDN.Parent()) {
		rename.NewSuperior = item.targetDN.Parent()
	}
	// Keep the old naming value only where the target still holds it.
	rename.DeleteOldRDN = !targetHoldsRDN(item.target, oldRDN)

	// The naming attributes are the rename's to change; the modification
	// covers the rest, addressed to the new name.
	skip := map[string]bool{}
	for _, ava := range oldRDN {
		skip[strings.ToLower(ava.Type)] = true
	}
	for _, ava := range newRDN {
		skip[strings.ToLower(ava.Type)] = true
	}
	records := []directory.ChangeRecord{rename}
	if mods := modsFor(r, item, skip); len(mods) > 0 {
		records = append(records, directory.ChangeRecord{DN: item.targetDN, Type: directory.ChangeModify, Mods: mods})
	}
	return Candidate{Records: records}
}

func targetHoldsRDN(target *snapshot.Entry, rdn dn.RDN) bool {
	for _, ava := range rdn {
		found := false
		for _, a := range target.Attributes {
			if !strings.EqualFold(a.Name, ava.Type) {
				continue
			}
			for _, v := range a.Values {
				raw, _ := v.Bytes()
				if strings.EqualFold(string(raw), ava.Value) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func modsFor(r *Result, item Item, skip map[string]bool) []directory.Mod {
	sm, tm := attributeMap(item.source), attributeMap(item.target)
	rules := newRuleBook(r.source.Snapshot, r.target.Snapshot, r)
	var mods []directory.Mod
	for _, change := range item.Attributes {
		key := strings.ToLower(change.Name)
		if skip[strings.ToLower(strings.SplitN(change.Name, ";", 2)[0])] {
			continue
		}
		if change.Kind == Unknown || change.Sensitive || change.Operational || isIdentity(change.Name) {
			continue
		}
		sa, ta := sm[key], tm[key]
		switch {
		case ta == nil:
			mods = append(mods, directory.Mod{Op: directory.ModDelete, Name: change.Name})
		case sa == nil:
			mods = append(mods, directory.Mod{Op: directory.ModAdd, Name: change.Name, Values: rawValues(ta.Values)})
		default:
			keyer, _ := rules.keyer(change.Name)
			sv, tv := keyValues(sa.Values, keyer), keyValues(ta.Values, keyer)
			var remove, add [][]byte
			for i, k := range sv.keys {
				if !tv.set[k] {
					raw, _ := sv.values[i].Bytes()
					remove = append(remove, raw)
				}
			}
			for i, k := range tv.keys {
				if !sv.set[k] {
					raw, _ := tv.values[i].Bytes()
					add = append(add, raw)
				}
			}
			if len(remove) > 0 {
				mods = append(mods, directory.Mod{Op: directory.ModDelete, Name: change.Name, Values: remove})
			}
			if len(add) > 0 {
				mods = append(mods, directory.Mod{Op: directory.ModAdd, Name: change.Name, Values: add})
			}
		}
	}
	return mods
}

func rawValues(values []snapshot.Value) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		if raw, err := v.Bytes(); err == nil {
			out = append(out, raw)
		}
	}
	return out
}

// isIdentity reports an attribute the server assigns to name an entry for good.
// It is never proposed for writing, whatever the recorded schema said about it:
// a snapshot taken without a schema would otherwise offer to set entryUUID.
func isIdentity(name string) bool {
	base := strings.SplitN(name, ";", 2)[0]
	for _, id := range snapshot.IdentityAttributes {
		if strings.EqualFold(id, base) {
			return true
		}
	}
	return false
}
