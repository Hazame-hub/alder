package plan

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// The schema as a set of changes would leave it.
//
// A plan reads every change against the directory as it is. That is right for
// entries -- nothing else can be known -- but it is wrong for the schema when
// the set changes the schema itself: an entry that uses an object class the
// same set adds two changes earlier is not invalid, it is second. Judging it
// against the schema as it is now would refuse a change the directory accepts,
// which is the one failure mode schema validation must not have.
//
// So the planner follows the schema through the set: each schema change updates
// the view, and every later change is judged against the view. What is added
// counts as defined, what is removed stops counting, and the order is the
// order the operator gave -- the same reading the dependency check uses.
//
// The view is rebuilt by parsing, because that is what makes a definition real:
// a definition string nobody parsed would let a change through on the strength
// of text the server may well reject.
type schemaView struct {
	current *schema.Schema
	locate  SchemaLocator
	// definitions holds the raw definitions of the current schema by kind, in
	// load order, so the view can be rebuilt after a change.
	definitions map[directory.SchemaDefKind][]string
	dirty       bool
}

func newSchemaView(sch *schema.Schema, locate SchemaLocator) *schemaView {
	return &schemaView{current: sch, locate: locate}
}

// schema is the view as it stands.
func (v *schemaView) schema() *schema.Schema {
	if v == nil {
		return nil
	}
	if v.dirty {
		v.rebuild()
	}
	return v.current
}

// apply notes what a change does to the schema. Anything that is not a schema
// change leaves the view alone.
func (v *schemaView) apply(record directory.ChangeRecord) {
	if v == nil || v.locate == nil || v.current == nil || record.Type != directory.ChangeModify {
		return
	}
	for _, mod := range record.Mods {
		kind, ok := v.locate(record.DN, mod.Name)
		if !ok {
			continue
		}
		v.ensure()
		for _, value := range mod.Values {
			definition := storedDefinition(string(value))
			oid := definitionOID(definition)
			if oid == "" {
				continue
			}
			switch mod.Op {
			case directory.ModAdd, directory.ModReplace:
				v.remove(kind, oid)
				v.definitions[kind] = append(v.definitions[kind], definition)
				v.dirty = true
			case directory.ModDelete:
				v.remove(kind, oid)
				v.dirty = true
			}
		}
	}
}

// ensure captures the definitions the live schema holds, once, so the view can
// be rebuilt from them.
func (v *schemaView) ensure() {
	if v.definitions != nil {
		return
	}
	v.definitions = map[directory.SchemaDefKind][]string{
		directory.SchemaDefAttributeType: make([]string, 0, len(v.current.AttributeTypes)),
		directory.SchemaDefObjectClass:   make([]string, 0, len(v.current.ObjectClasses)),
	}
	for _, at := range v.current.AttributeTypes {
		v.definitions[directory.SchemaDefAttributeType] =
			append(v.definitions[directory.SchemaDefAttributeType], at.Raw)
	}
	for _, oc := range v.current.ObjectClasses {
		v.definitions[directory.SchemaDefObjectClass] =
			append(v.definitions[directory.SchemaDefObjectClass], oc.Raw)
	}
}

func (v *schemaView) remove(kind directory.SchemaDefKind, oid string) {
	held := v.definitions[kind]
	for i, definition := range held {
		if strings.EqualFold(definitionOID(definition), oid) {
			v.definitions[kind] = append(held[:i:i], held[i+1:]...)
			return
		}
	}
}

// rebuild parses the definitions again. The rest of the schema -- syntaxes,
// matching rules and the others -- comes from the live schema unchanged, since
// a change to those is not something Alder writes.
func (v *schemaView) rebuild() {
	v.dirty = false
	attrs := map[string][]string{
		schema.AttrAttributeTypes: v.definitions[directory.SchemaDefAttributeType],
		schema.AttrObjectClasses:  v.definitions[directory.SchemaDefObjectClass],
	}
	for _, s := range v.current.Syntaxes {
		attrs[schema.AttrLDAPSyntaxes] = append(attrs[schema.AttrLDAPSyntaxes], s.Raw)
	}
	for _, r := range v.current.MatchingRules {
		attrs[schema.AttrMatchingRules] = append(attrs[schema.AttrMatchingRules], r.Raw)
	}
	for _, u := range v.current.MatchingRuleUses {
		attrs[schema.AttrMatchingRuleUse] = append(attrs[schema.AttrMatchingRuleUse], u.Raw)
	}
	for _, c := range v.current.DITContentRules {
		attrs[schema.AttrDITContentRules] = append(attrs[schema.AttrDITContentRules], c.Raw)
	}
	for _, f := range v.current.NameForms {
		attrs[schema.AttrNameForms] = append(attrs[schema.AttrNameForms], f.Raw)
	}
	v.current = schema.Load(v.current.DN, attrs)
}

// storedDefinition is a definition without the load order prefix a
// configuration keeps in front of it.
func storedDefinition(value string) string {
	text := strings.TrimSpace(value)
	if strings.HasPrefix(text, "{") {
		if end := strings.IndexByte(text, '}'); end > 0 {
			text = strings.TrimSpace(text[end+1:])
		}
	}
	return text
}

// definitionOID is the OID a definition begins with.
func definitionOID(definition string) string {
	text := strings.TrimPrefix(strings.TrimSpace(definition), "(")
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, " \t"); i > 0 {
		return text[:i]
	}
	return ""
}
