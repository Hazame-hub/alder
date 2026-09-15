package plan

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Schema changes that depend on one another.
//
// A schema definition refers to others: an object class to its superclasses
// and to the attribute types it requires or allows, an attribute type to its
// supertype. A plan reads each change against the directory as it is, so it
// can see what the live schema defines -- and, going through the set in order,
// what the changes before this one would add or remove. That is enough to catch
// the two mistakes a set of schema changes makes most: adding a definition
// before what it names exists, and removing a definition something else still
// names. Both are conflicts with a stable code; neither is fixed by adding or
// reordering changes on anyone's behalf.
//
// It does not model everything a server checks, and does not try: a definition
// the planner cannot parse is left for the directory to judge.

// Problem codes for schema dependencies.
const (
	// ProblemDependencyRequired: a definition names another that neither the
	// live schema nor an earlier change in the set provides.
	ProblemDependencyRequired ProblemCode = "dependency_required"
	// ProblemReferencedBySchema: a definition is removed while the live schema,
	// less what the set removes, still names it.
	ProblemReferencedBySchema ProblemCode = "referenced_by_schema"
)

// SchemaLocator reports whether an attribute of an entry holds schema
// definitions, and of which kind.
type SchemaLocator func(target dn.DN, attribute string) (directory.SchemaDefKind, bool)

type schemaDef struct {
	kind   directory.SchemaDefKind
	oid    string
	names  []string
	refsAT []string
	refsOC []string
}

func parseSchemaValue(kind directory.SchemaDefKind, raw []byte) (schemaDef, bool) {
	text := strings.TrimSpace(string(raw))
	// A configuration collection stores each definition behind its load order.
	if strings.HasPrefix(text, "{") {
		if end := strings.IndexByte(text, '}'); end > 0 {
			text = strings.TrimSpace(text[end+1:])
		}
	}
	switch kind {
	case directory.SchemaDefAttributeType:
		at, err := schema.ParseAttributeType(text)
		if err != nil {
			return schemaDef{}, false
		}
		d := schemaDef{kind: kind, oid: at.OID, names: at.Names}
		if at.SuperName != "" {
			d.refsAT = []string{at.SuperName}
		}
		return d, true
	case directory.SchemaDefObjectClass:
		oc, err := schema.ParseObjectClass(text)
		if err != nil {
			return schemaDef{}, false
		}
		return schemaDef{kind: kind, oid: oc.OID, names: oc.Names, refsOC: oc.SuperNames,
			refsAT: append(append([]string{}, oc.Must...), oc.May...)}, true
	}
	return schemaDef{}, false
}

// schemaState is the schema as the changes so far would leave it.
type schemaState struct {
	sch     *schema.Schema
	added   map[directory.SchemaDefKind]map[string]string // folded OID or name -> folded OID
	removed map[directory.SchemaDefKind]map[string]bool   // folded OID
}

func (s *schemaState) oidOf(kind directory.SchemaDefKind, ref string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(ref))
	if oid, ok := s.added[kind][key]; ok {
		return oid, true
	}
	if s.sch == nil {
		return "", false
	}
	switch kind {
	case directory.SchemaDefAttributeType:
		if at := s.sch.AttributeType(ref); at != nil {
			return strings.ToLower(at.OID), true
		}
	case directory.SchemaDefObjectClass:
		if oc := s.sch.ObjectClass(ref); oc != nil {
			return strings.ToLower(oc.OID), true
		}
	}
	return "", false
}

// provides reports whether a reference names a definition that exists once
// the changes so far are applied.
func (s *schemaState) provides(kind directory.SchemaDefKind, ref string) bool {
	oid, ok := s.oidOf(kind, ref)
	if !ok {
		return false
	}
	if _, readded := s.added[kind][oid]; readded {
		return true
	}
	return !s.removed[kind][oid]
}

// CheckSchemaDependencies turns applicable schema changes whose dependencies do
// not hold into conflicts, in the order the set would apply.
func CheckSchemaDependencies(p *Plan, sch *schema.Schema, locate SchemaLocator) {
	if locate == nil {
		return
	}
	state := &schemaState{sch: sch,
		added:   map[directory.SchemaDefKind]map[string]string{directory.SchemaDefAttributeType: {}, directory.SchemaDefObjectClass: {}},
		removed: map[directory.SchemaDefKind]map[string]bool{directory.SchemaDefAttributeType: {}, directory.SchemaDefObjectClass: {}},
	}
	for i := range p.Items {
		item := &p.Items[i]
		if item.Action.AppliesNothing() || item.Record.Type != directory.ChangeModify {
			continue
		}
		var adds, deletes []schemaDef
		for _, m := range item.Record.Mods {
			kind, ok := locate(item.Record.DN, m.Name)
			if !ok {
				continue
			}
			for _, v := range m.Values {
				d, parsed := parseSchemaValue(kind, v)
				if !parsed {
					continue
				}
				switch m.Op {
				case directory.ModAdd:
					adds = append(adds, d)
				case directory.ModDelete:
					deletes = append(deletes, d)
				}
			}
		}
		if len(adds) == 0 && len(deletes) == 0 {
			continue
		}
		readded := map[string]bool{}
		for _, d := range adds {
			readded[string(d.kind)+":"+strings.ToLower(d.oid)] = true
		}

		// What this change adds is available to itself: an object class and the
		// attribute type it needs may arrive together.
		local := &schemaState{sch: sch, added: map[directory.SchemaDefKind]map[string]string{}, removed: state.removed}
		for k, m := range state.added {
			local.added[k] = map[string]string{}
			for name, oid := range m {
				local.added[k][name] = oid
			}
		}
		for _, d := range adds {
			register(local.added[d.kind], d)
		}
		code, attribute := ProblemCode(""), ""
		for _, d := range adds {
			for _, ref := range d.refsAT {
				if !local.provides(directory.SchemaDefAttributeType, ref) {
					code, attribute = ProblemDependencyRequired, ref
				}
			}
			for _, ref := range d.refsOC {
				if !local.provides(directory.SchemaDefObjectClass, ref) {
					code, attribute = ProblemDependencyRequired, ref
				}
			}
			if code != "" {
				break
			}
		}
		if code == "" {
			for _, d := range deletes {
				if readded[string(d.kind)+":"+strings.ToLower(d.oid)] {
					continue
				}
				if name := referrer(sch, state, d, deletes); name != "" {
					code, attribute = ProblemReferencedBySchema, name
					break
				}
			}
		}
		if code != "" {
			reason := "This schema change names " + attribute + ", which the schema would not define when it runs."
			if code == ProblemReferencedBySchema {
				reason = "This schema change removes a definition that " + attribute + " still names."
			}
			action := item.Action
			*item = refuse(*item, ActionConflict, code, attribute, reason)
			item.Baseline = ""
			switch action {
			case ActionModify:
				p.Counts.Modify--
			case ActionAdd:
				p.Counts.Add--
			}
			p.Counts.Conflict++
			continue
		}
		for _, d := range deletes {
			if !readded[string(d.kind)+":"+strings.ToLower(d.oid)] {
				state.removed[d.kind][strings.ToLower(d.oid)] = true
			}
		}
		for _, d := range adds {
			register(state.added[d.kind], d)
			delete(state.removed[d.kind], strings.ToLower(d.oid))
		}
	}
}

func register(m map[string]string, d schemaDef) {
	oid := strings.ToLower(d.oid)
	m[oid] = oid
	for _, n := range d.names {
		m[strings.ToLower(n)] = oid
	}
}

// referrer names a live definition, not itself removed, that still refers to
// d, or "".
func referrer(sch *schema.Schema, state *schemaState, d schemaDef, deletes []schemaDef) string {
	if sch == nil {
		return ""
	}
	goingAway := func(kind directory.SchemaDefKind, oid string) bool {
		key := strings.ToLower(oid)
		if state.removed[kind][key] {
			return true
		}
		for _, other := range deletes {
			if other.kind == kind && strings.EqualFold(other.oid, oid) {
				return true
			}
		}
		return false
	}
	switch d.kind {
	case directory.SchemaDefAttributeType:
		must, may := sch.UsedBy(d.oid)
		for _, oc := range append(must, may...) {
			if !goingAway(directory.SchemaDefObjectClass, oc.OID) {
				return oc.Name()
			}
		}
		for _, at := range sch.AttributeTypes {
			if at.SuperName == "" || goingAway(directory.SchemaDefAttributeType, at.OID) {
				continue
			}
			if sup := sch.AttributeType(at.SuperName); sup != nil && strings.EqualFold(sup.OID, d.oid) {
				return at.Name()
			}
		}
	case directory.SchemaDefObjectClass:
		if oc := sch.ObjectClass(d.oid); oc != nil {
			for _, sub := range sch.SubclassesOf(oc) {
				if !goingAway(directory.SchemaDefObjectClass, sub.OID) {
					return sub.Name()
				}
			}
		}
	}
	return ""
}
