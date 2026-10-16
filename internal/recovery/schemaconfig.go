package recovery

import (
	"regexp"
	"strings"

	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Compensating changes to the schema and the server's configuration.
//
// Both are ordinary modifications of ordinary entries, and both are planned,
// reviewed and applied like any other compensation. What sets them apart is
// that the server, not the change, decides what ends up stored:
//
//   - A configuration entry keeps a "{n}" position in front of each value of an
//     ordered attribute and renumbers them when one goes; it re-renders an
//     access rule or an index in its own words; it spells a boolean its own way.
//   - A schema entry stores a definition as the server renders it -- with an
//     ordering prefix where the schema lives in the configuration, with an
//     X-ORIGIN of its own where it does not -- and a definition is removed by
//     naming the value the server holds, not the one that was sent.
//
// So working out what the change left from the change itself, as a data
// modification does, would be guessing at the server's rendering. The entry is
// read again once the change has applied, and the compensation and its
// expectation are built from that read and the one before. Nothing in either
// branch asks which server it is talking to: the values say what they are.

// deriveConfig: each setting the change touched is put back exactly as the
// server stored it before, by a replace of the whole attribute -- ordered
// values with their positions, a single value in the spelling it had -- or
// removed if it had no value. The expectation is the setting as the server
// stored it after the change.
//
// A replace rather than value by value, which is how data is compensated. A
// configuration attribute is short; a single-valued setting cannot hold the
// old value and the new one between a delete and an add; and an ordered one
// is only put back in its order by writing the whole set, which the server
// takes as a true round trip.
func deriveConfig(step Step, record directory.ChangeRecord, pre, post *directory.Entry, sch *schema.Schema) Step {
	if record.Type != directory.ChangeModify {
		return step.unavailable(ReasonConfigEntryNotCompensated, "")
	}
	if pre == nil {
		return step.unavailable(ReasonPreStateUnavailable, "")
	}
	if post == nil {
		return step.unavailable(ReasonPostStateUnavailable, "")
	}
	order := touchedAttributes(record)
	change := Change{DN: record.DN.String(), Type: string(directory.ChangeModify), Expect: &Expect{}}
	skipped := 0
	for _, t := range order {
		if code, owned := notCompensated(sch, t, pre, post); owned {
			step.Reasons = append(step.Reasons, Reason{Code: code, Attribute: t.name})
			skipped++
			continue
		}
		before, after := pre.Get(t.name), post.Get(t.name)
		if accessRule(t.name) && (len(before) == 0 || (len(after) == 0 && leavesValues(t.mods))) {
			// An empty read of an access rule is not proof there is none: a
			// bind may be allowed to write rules it cannot read, and one rule
			// can hide another. Nothing read before would make the
			// compensation a delete of the whole list, unread rules
			// included; nothing read after a change that left rules leaves
			// no expectation to hold the compensation to. Neither is
			// guessed at.
			step.Reasons = append(step.Reasons, Reason{Code: ReasonHiddenAttributesUnknown, Attribute: t.name})
			skipped++
			continue
		}
		// Byte for byte: a value the server re-spelled is put back as it was,
		// and an ordered value's position is part of the value.
		if sameSet(before, after, snapshot.ExactKey) {
			continue
		}
		if len(before) == 0 {
			change.Mods = append(change.Mods, Mod{Op: string(directory.ModDelete), Name: t.name})
		} else {
			change.Mods = append(change.Mods, Mod{Op: string(directory.ModReplace), Name: t.name, Values: encode(before)})
		}
		change.Expect.Attributes = append(change.Expect.Attributes, Attribute{Name: t.name, Values: encode(after)})
	}
	step.Recoverability = level(skipped, len(order))
	if len(change.Mods) > 0 {
		step.Compensation = []Change{change}
	}
	return step
}

// notCompensated says why a configuration attribute is left alone, if it is.
//
// An access rule is not left alone. A reviewed change replaced it and Alder
// applied it, so putting back what the server held before is the same kind of
// write, planned, warned about and checked against its expectation like the
// change was (option B of docs/proposals/access-rules-in-recovery.md). Until
// 1.39 it was held back here with access_rule_not_written, which stays in the
// enum for the bundles written then.
func notCompensated(sch *schema.Schema, t *touchedAttribute, pre, post *directory.Entry) (ReasonCode, bool) {
	name := t.name
	switch {
	case config.NameLooksSensitive(name) || carriesCredentials(t, pre, post):
		// olcRootPW, nsslapd-rootpw, olcSyncrepl with its credentials: the
		// name is recorded, the value never read into the bundle.
		return ReasonSensitiveNotCaptured, true
	case definitionAttribute(name):
		// A definition written to a configuration entry that is not a
		// listed schema target -- cn=schema,cn=config itself, or a
		// collection created after the session connected. It is not a
		// setting: putting the attribute back whole would remove or rewrite
		// every definition it holds, and the definitions entries may now use.
		return ReasonSchemaOrConfig, true
	case accessRule(name):
		// Before the server-owned test: a server that keeps aci declares it
		// USAGE directoryOperation, which reads as server-owned, but it has
		// no NO-USER-MODIFICATION and the change that is being undone wrote
		// it.
		return "", false
	case serverOwned(sch, name):
		return ReasonServerOwnedAttribute, true
	}
	return "", false
}

// credentialsField is how OpenLDAP writes a bind password inside a longer
// configuration value: olcSyncrepl, olcDbIDAssertBind, olcDbACLBind, and
// whatever the next back end adds.
var credentialsField = regexp.MustCompile(`(?i)\bcredentials=`)

// carriesCredentials reports whether any value of the attribute -- as the
// change wrote it, or as the server held it before or after -- carries a bind
// password. The sensitive table names the attributes known to; this catches
// the one it does not, by what the value says it is.
func carriesCredentials(t *touchedAttribute, pre, post *directory.Entry) bool {
	var values [][]byte
	for _, m := range t.mods {
		values = append(values, m.Values...)
	}
	for _, e := range []*directory.Entry{pre, post} {
		if e != nil {
			values = append(values, e.Get(t.name)...)
		}
	}
	for _, v := range values {
		if credentialsField.Match(v) {
			return true
		}
	}
	return false
}

// definitionAttribute reports whether an attribute carries schema
// definitions, under either arrangement a server keeps its schema in.
//
// By name, as accessRule is: this is what Alder chooses not to write as a
// setting, not a guess at what a server can do. The schema branch, which
// asks the server which attributes it writes definitions to, is where these
// are compensated.
func definitionAttribute(name string) bool {
	switch strings.ToLower(schema.BaseName(name)) {
	case "attributetypes", "objectclasses", "ditcontentrules", "ditstructurerules", "nameforms",
		"ldapsyntaxes", "matchingrules", "matchingruleuse",
		"olcattributetypes", "olcobjectclasses", "olcditcontentrules", "olcldapsyntaxes",
		"olcobjectidentifier":
		return true
	}
	return false
}

// accessRule reports whether an attribute carries access rules.
//
// By name, which section 3 of the charter otherwise forbids. That rule is
// about detecting what a server can do; this is a decision about what an
// access rule is, and the charter names both attributes. The entry editor
// holds them back the same way, and the lockout warning names them too.
func accessRule(name string) bool {
	switch strings.ToLower(schema.BaseName(name)) {
	case "aci", "olcaccess":
		return true
	}
	return false
}

// leavesValues reports whether modifications certainly leave the attribute
// holding values: the last one adds or replaces with values. A delete of some
// values may or may not empty it, so it is not counted.
func leavesValues(mods []directory.Mod) bool {
	if len(mods) == 0 {
		return false
	}
	last := mods[len(mods)-1]
	return (last.Op == directory.ModAdd || last.Op == directory.ModReplace) && len(last.Values) > 0
}

// definitionChange is what a change did to one schema definition.
type definitionChange struct {
	attribute string
	oid       string
	// before and after are the definition as stored, nil where there was
	// none.
	before, after []byte
	// replacedInPlace is a definition changed by an add alone, which a server
	// whose subschema is directly writable takes as a replacement of the one
	// with the same OID and names. The compensation is written the same way:
	// removing a definition another one names is refused there, even when the
	// same modification puts it straight back.
	replacedInPlace bool
}

// deriveSchema: definition by definition, identified by OID, one added is
// removed, one removed is added back, and one replaced is put back -- each as
// the server stored it, read before the change for what goes back and after
// it for what goes. The expectation names the definitions the change touched,
// as it left them, and no others.
//
// A definition added back keeps the ordering prefix it was stored with, which
// puts it back in its place on a server that keeps one; the server takes a
// value with a position as an insertion there.
//
// Removing a definition, or putting back the one it replaced, is never exact:
// entries may have come to use it since, and Alder neither searches for them
// nor could see all of them. A definition put back after being removed is.
func deriveSchema(step Step, record directory.ChangeRecord, pre, post *directory.Entry, definitions func(string) bool) Step {
	if record.Type != directory.ChangeModify {
		return step.unavailable(ReasonSchemaOrConfig, "")
	}
	if pre == nil {
		return step.unavailable(ReasonPreStateUnavailable, "")
	}
	if post == nil {
		return step.unavailable(ReasonPostStateUnavailable, "")
	}
	order := touchedAttributes(record)
	var changed []definitionChange
	skipped := 0
	for _, t := range order {
		found, ok := definitionChanges(t, pre, post, definitions)
		if !ok {
			step.Reasons = append(step.Reasons, Reason{Code: ReasonSchemaOrConfig, Attribute: t.name})
			skipped++
			continue
		}
		changed = append(changed, found...)
	}

	change := Change{DN: record.DN.String(), Type: string(directory.ChangeModify),
		Expect: &Expect{Attributes: []Attribute{}}}
	inUse := map[string]bool{}
	// In reverse: a class added after the attribute type it names goes before
	// the type does, and a type removed after the class that named it comes
	// back before the class.
	for i := len(changed) - 1; i >= 0; i-- {
		d := changed[i]
		expect := Definition{Attribute: d.attribute, OID: d.oid}
		switch {
		case d.before == nil && d.after == nil:
			// Added and removed by the same change: nothing is left of it.
			continue
		case d.before == nil:
			change.Mods = append(change.Mods, Mod{Op: string(directory.ModDelete), Name: d.attribute, Values: encode([][]byte{d.after})})
			expect.Value = &encode([][]byte{d.after})[0]
			inUse[strings.ToLower(d.attribute)] = true
		case d.after == nil:
			change.Mods = append(change.Mods, Mod{Op: string(directory.ModAdd), Name: d.attribute, Values: encode([][]byte{d.before})})
		case string(d.before) == string(d.after):
			continue
		default:
			if !d.replacedInPlace {
				change.Mods = append(change.Mods, Mod{Op: string(directory.ModDelete), Name: d.attribute, Values: encode([][]byte{d.after})})
			}
			change.Mods = append(change.Mods, Mod{Op: string(directory.ModAdd), Name: d.attribute, Values: encode([][]byte{d.before})})
			expect.Value = &encode([][]byte{d.after})[0]
			inUse[strings.ToLower(d.attribute)] = true
		}
		change.Expect.Definitions = append(change.Expect.Definitions, expect)
	}
	for _, t := range order {
		if inUse[strings.ToLower(t.name)] {
			step.Reasons = append(step.Reasons, Reason{Code: ReasonDefinitionMayBeInUse, Attribute: t.name})
			delete(inUse, strings.ToLower(t.name))
		}
	}

	step.Recoverability = level(skipped, len(order))
	if step.Recoverability == Exact && len(step.Reasons) > 0 {
		step.Recoverability = Partial
	}
	if len(change.Mods) > 0 {
		step.Compensation = []Change{change}
	}
	return step
}

// definitionChanges is what a change did to the definitions one attribute
// carries, or false when it is not something compensated definition by
// definition: an attribute that carries none, a whole collection replaced or
// removed -- which Alder's schema editor never writes -- a value that is not a
// definition, or an OID the entry holds more than once.
func definitionChanges(t *touchedAttribute, pre, post *directory.Entry, definitions func(string) bool) ([]definitionChange, bool) {
	if definitions == nil || !definitions(t.name) {
		return nil, false
	}
	var oids []string
	seen := map[string]bool{}
	removed := map[string]bool{}
	for _, m := range t.mods {
		if m.Op == directory.ModReplace || (m.Op == directory.ModDelete && len(m.Values) == 0) {
			return nil, false
		}
		for _, v := range m.Values {
			oid := strings.ToLower(directory.DefinitionOID(string(v)))
			if oid == "" {
				return nil, false
			}
			if !seen[oid] {
				seen[oid] = true
				oids = append(oids, oid)
			}
			if m.Op == directory.ModDelete {
				removed[oid] = true
			}
		}
	}
	out := make([]definitionChange, 0, len(oids))
	for _, oid := range oids {
		before, ok := storedDefinition(pre.Get(t.name), oid)
		if !ok {
			return nil, false
		}
		after, ok := storedDefinition(post.Get(t.name), oid)
		if !ok {
			return nil, false
		}
		out = append(out, definitionChange{attribute: t.name, oid: oid, before: before, after: after,
			replacedInPlace: before != nil && !removed[oid]})
	}
	return out, true
}

// storedDefinition is the one value with this OID, nil when there is none, and
// false when there is more than one -- which no server should store, and which
// no compensation could tell apart.
func storedDefinition(values [][]byte, oid string) ([]byte, bool) {
	var found []byte
	for _, v := range values {
		if strings.EqualFold(directory.DefinitionOID(string(v)), oid) {
			if found != nil {
				return nil, false
			}
			found = v
		}
	}
	return found, true
}

// assessState stands in for the reads a plan has not made yet, so Assess can
// say how recoverable a schema or configuration change will be from the change
// alone. Its values are the change's own: what a schema change removes is
// taken as held before it, what it adds as held after. For a configuration
// change both are empty but for access rules: which settings are compensated
// depends on their names alone, and whether one had a value before is
// something the bundle made at apply says.
func assessState(record directory.ChangeRecord, kind string) (pre, post *directory.Entry) {
	pre, post = directory.NewEntry(record.DN), directory.NewEntry(record.DN)
	if kind != KindSchema {
		// An access rule is assessed as one that was read before and after:
		// an empty read of one is reported as unread rather than as no rules,
		// and whether the bind could read them is what the bundle made at
		// apply says. These values never leave Assess.
		for _, t := range touchedAttributes(record) {
			if accessRule(t.name) {
				pre.Set(t.name, [][]byte{[]byte("before")})
				post.Set(t.name, [][]byte{[]byte("after")})
			}
		}
		return pre, post
	}
	for _, t := range touchedAttributes(record) {
		var removed, added [][]byte
		for _, m := range t.mods {
			switch m.Op {
			case directory.ModDelete:
				removed = append(removed, m.Values...)
			case directory.ModAdd:
				added = append(added, m.Values...)
			}
		}
		pre.Set(t.name, removed)
		post.Set(t.name, added)
	}
	return pre, post
}
