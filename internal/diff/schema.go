package diff

import (
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hazame-hub/alder/internal/snapshot"
)

// Comparing schema.
//
// A schema comparison follows the rules a data comparison does, applied to
// definitions instead of entries:
//
//   - Identity is the OID. Two definitions with the same OID are the same
//     element whatever they are called; a renamed attribute type is modified,
//     not removed and added. Two definitions with different OIDs are different
//     elements however alike their names. Nothing is paired by resemblance.
//   - Definitions are compared as what they mean, not as text. Whitespace,
//     quoting, the order of NAME aliases and of SUP, MUST and MAY, and whether a
//     reference is written as a name or an OID make no difference. Every field
//     that changes what a definition does does.
//   - X- extensions are compared separately from the definition. A difference
//     only in them is metadata_only: reported, and never proposed as a change.
//   - What could not be understood is unknown, not absent: a definition that did
//     not parse, one with a keyword Alder does not know, one whose name another
//     definition also claims. A side with anything unparsed makes the whole
//     comparison incomplete, and an incomplete comparison proposes no removal.
//
// Only attribute types and object classes are compared. The syntaxes and
// matching rules a snapshot holds are used to resolve references, and never
// reported on.

// Schema element kinds.
const (
	ElementAttributeType = "attributeType"
	ElementObjectClass   = "objectClass"
)

// MetadataOnly is a definition whose meaning is the same on both sides and
// whose X- extensions are not.
const MetadataOnly Kind = "metadata_only"

// Field categories.
const (
	// FieldCore changes what the definition does.
	FieldCore = "core"
	// FieldDescription is DESC: a person's text, reported as a modification.
	FieldDescription = "description"
	// FieldExtension is an X- extension: metadata, never proposed as a change.
	FieldExtension = "extension"
)

// Reason a schema comparison is incomplete.
const ReasonSchemaPartial = "schema_partial"

// Problems a schema item can carry.
const (
	ProblemUnparsed            = "unparsed_definition"
	ProblemAmbiguousName       = "ambiguous_name"
	ProblemUnrecognizedKeyword = "unrecognized_keyword"
	ProblemNonNumericOID       = "non_numeric_oid"
	ProblemUnresolvedReference = "unresolved_reference"
	ProblemReferencedBySchema  = "referenced_by_schema"
	ProblemUsedByEntries       = "used_by_entries"
	ProblemUsageUnknown        = "usage_unknown"
	ProblemServerDefined       = "server_defined"
	ProblemDependencyCycle     = "dependency_cycle"
)

// SchemaSide is one schema state being compared.
type SchemaSide struct {
	Snapshot *snapshot.SchemaSnapshot
	// Live reports that this side was captured from the session's directory
	// just now.
	Live bool
}

// FieldChange is one field's difference. Values are as each side wrote them.
type FieldChange struct {
	Field    string   `json:"field"`
	Category string   `json:"category"`
	Source   []string `json:"source,omitempty"`
	Target   []string `json:"target,omitempty"`
}

// SchemaRef is a reference from one definition to another.
type SchemaRef struct {
	Element  string `json:"element"`
	OID      string `json:"oid"`
	Name     string `json:"name,omitempty"`
	Relation string `json:"relation"`
}

// SchemaItem is one definition's difference.
type SchemaItem struct {
	Element          string        `json:"element"`
	OID              string        `json:"oid"`
	Names            []string      `json:"names,omitempty"`
	Kind             Kind          `json:"kind"`
	Fields           []FieldChange `json:"fields,omitempty"`
	SourceDefinition string        `json:"sourceDefinition,omitempty"`
	TargetDefinition string        `json:"targetDefinition,omitempty"`
	SourceCollection string        `json:"sourceCollection,omitempty"`
	TargetCollection string        `json:"targetCollection,omitempty"`
	// Requires are the definitions the target's definition refers to.
	Requires []SchemaRef `json:"requires,omitempty"`
	// RequiredBy are the source's definitions that refer to this one.
	RequiredBy []SchemaRef `json:"requiredBy,omitempty"`
	Problems   []string    `json:"problems,omitempty"`

	sourceAT, targetAT *snapshot.SchemaAttributeType
	sourceOC, targetOC *snapshot.SchemaObjectClass
}

// Key identifies an item within a comparison.
func (it SchemaItem) Key() string { return it.Element + ":" + strings.ToLower(it.OID) }

// SchemaCounts tallies one element kind.
type SchemaCounts struct {
	Compared     int `json:"compared"`
	Added        int `json:"added"`
	Removed      int `json:"removed"`
	Modified     int `json:"modified"`
	MetadataOnly int `json:"metadataOnly"`
	Unchanged    int `json:"unchanged"`
	Unknown      int `json:"unknown"`
}

// SchemaResult is a whole schema comparison.
type SchemaResult struct {
	Complete       bool         `json:"complete"`
	Reasons        []Reason     `json:"reasons,omitempty"`
	CrossVendor    bool         `json:"crossVendor"`
	AttributeTypes SchemaCounts `json:"attributeTypes"`
	ObjectClasses  SchemaCounts `json:"objectClasses"`
	Items          []SchemaItem `json:"items"`
	// Order lists the keys of the items a change could be derived for, in an
	// order that applies them safely: what a definition depends on before the
	// definition, for additions and modifications, and what depends on a
	// definition before it, for removals. It is a dependency order, never a
	// lexical one.
	Order []string `json:"order"`

	source, target SchemaSide
	index          map[string]int
}

// Source and Target are the sides compared.
func (r *SchemaResult) Source() SchemaSide { return r.source }
func (r *SchemaResult) Target() SchemaSide { return r.target }

// Item returns the item with a key.
func (r *SchemaResult) Item(key string) (SchemaItem, bool) {
	i, ok := r.index[key]
	if !ok {
		return SchemaItem{}, false
	}
	return r.Items[i], true
}

// SchemaOptions tunes a schema comparison.
type SchemaOptions struct {
	// IncludeUnchanged lists unchanged definitions as items.
	IncludeUnchanged bool
}

// schemaIndex resolves names and OIDs within one side.
type schemaIndex struct {
	ats map[string]*snapshot.SchemaAttributeType
	ocs map[string]*snapshot.SchemaObjectClass
	// atNames and ocNames map a folded name to every OID that claims it.
	atNames, ocNames map[string][]string
	// rules maps a matching rule's folded name or OID to its OID.
	rules map[string]string
	// unparsed holds the OIDs of definitions that did not parse, by element.
	unparsed map[string]map[string]bool
	// unparsedUnknown counts unparsed compared definitions whose OID could not
	// even be read.
	unparsedUnknown int
	ambiguous       map[string]bool
}

var leadingOID = regexp.MustCompile(`^\(\s*([^\s()']+)`)

func newSchemaIndex(s *snapshot.SchemaSnapshot) *schemaIndex {
	ix := &schemaIndex{
		ats: map[string]*snapshot.SchemaAttributeType{}, ocs: map[string]*snapshot.SchemaObjectClass{},
		atNames: map[string][]string{}, ocNames: map[string][]string{}, rules: map[string]string{},
		unparsed: map[string]map[string]bool{ElementAttributeType: {}, ElementObjectClass: {}}, ambiguous: map[string]bool{},
	}
	for i := range s.AttributeTypes {
		at := &s.AttributeTypes[i]
		ix.ats[strings.ToLower(at.OID)] = at
		for _, n := range at.Names {
			ix.atNames[strings.ToLower(n)] = append(ix.atNames[strings.ToLower(n)], strings.ToLower(at.OID))
		}
	}
	for i := range s.ObjectClasses {
		oc := &s.ObjectClasses[i]
		ix.ocs[strings.ToLower(oc.OID)] = oc
		for _, n := range oc.Names {
			ix.ocNames[strings.ToLower(n)] = append(ix.ocNames[strings.ToLower(n)], strings.ToLower(oc.OID))
		}
	}
	for element, names := range map[string]map[string][]string{ElementAttributeType: ix.atNames, ElementObjectClass: ix.ocNames} {
		for _, oids := range names {
			if len(oids) > 1 {
				for _, oid := range oids {
					ix.ambiguous[element+":"+oid] = true
				}
			}
		}
	}
	for _, mr := range s.Context.MatchingRules {
		ix.rules[strings.ToLower(mr.OID)] = strings.ToLower(mr.OID)
		for _, n := range mr.Names {
			ix.rules[strings.ToLower(n)] = strings.ToLower(mr.OID)
		}
	}
	for _, u := range s.Unparsed {
		element := ""
		switch u.Attribute {
		case "attributeTypes":
			element = ElementAttributeType
		case "objectClasses":
			element = ElementObjectClass
		default:
			continue
		}
		if m := leadingOID.FindStringSubmatch(strings.TrimSpace(u.Definition)); m != nil {
			ix.unparsed[element][strings.ToLower(m[1])] = true
		} else {
			ix.unparsedUnknown++
		}
	}
	return ix
}

// resolveAT returns the folded OID a reference to an attribute type names, or
// "name:" and the folded name when this side defines nothing unambiguous by it.
func (ix *schemaIndex) resolveAT(ref string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(ref))
	if _, ok := ix.ats[key]; ok {
		return key, true
	}
	if oids := ix.atNames[key]; len(oids) == 1 {
		return oids[0], true
	}
	return "name:" + key, false
}

func (ix *schemaIndex) resolveOC(ref string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(ref))
	if _, ok := ix.ocs[key]; ok {
		return key, true
	}
	if oids := ix.ocNames[key]; len(oids) == 1 {
		return oids[0], true
	}
	return "name:" + key, false
}

func (ix *schemaIndex) resolveRule(ref string) string {
	if ref == "" {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(ref))
	if oid, ok := ix.rules[key]; ok {
		return oid
	}
	return key
}

// CompareSchema compares two schema states.
func CompareSchema(source, target SchemaSide, opts SchemaOptions) *SchemaResult {
	r := &SchemaResult{Complete: true, source: source, target: target, Items: []SchemaItem{}, Order: []string{}, index: map[string]int{}}
	ss, ts := source.Snapshot, target.Snapshot
	r.CrossVendor = ss.Source.Vendor == "" || ts.Source.Vendor == "" || !strings.EqualFold(ss.Source.Vendor, ts.Source.Vendor)
	si, ti := newSchemaIndex(ss), newSchemaIndex(ts)
	for name, side := range map[string]*snapshot.SchemaSnapshot{"source": ss, "target": ts} {
		if side.Completeness != snapshot.SchemaComplete {
			r.Complete = false
			r.Reasons = append(r.Reasons, Reason{Code: ReasonSchemaPartial,
				Detail: "the " + name + " schema has definitions Alder could not parse, so what they define is unknown"})
		}
	}
	sort.Slice(r.Reasons, func(i, j int) bool { return r.Reasons[i].Detail < r.Reasons[j].Detail })

	r.compareAttributeTypes(si, ti, opts)
	r.compareObjectClasses(si, ti, opts)
	sort.SliceStable(r.Items, func(i, j int) bool {
		if r.Items[i].Element != r.Items[j].Element {
			return r.Items[i].Element == ElementAttributeType
		}
		return snapshot.OIDLess(r.Items[i].OID, r.Items[j].OID)
	})
	for i, it := range r.Items {
		r.index[it.Key()] = i
	}
	r.dependencies(si, ti)
	r.order()
	return r
}

func unionOIDs[T any](a, b map[string]T, extra ...map[string]bool) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	for _, m := range extra {
		for k := range m {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return snapshot.OIDLess(out[i], out[j]) })
	return out
}

func (r *SchemaResult) record(counts *SchemaCounts, item SchemaItem, include bool) {
	counts.Compared++
	switch item.Kind {
	case Added:
		counts.Added++
	case Removed:
		counts.Removed++
	case Modified:
		counts.Modified++
	case MetadataOnly:
		counts.MetadataOnly++
	case Unknown:
		counts.Unknown++
	case Unchanged:
		counts.Unchanged++
		if !include {
			return
		}
	}
	r.Items = append(r.Items, item)
}

// unknownBecause decides whether a definition present on either side cannot
// be compared, and why.
func unknownBecause(element, key string, si, ti *schemaIndex, sourceUnrecognized, targetUnrecognized bool, sameText bool) []string {
	var problems []string
	if si.unparsed[element][key] || ti.unparsed[element][key] {
		problems = append(problems, ProblemUnparsed)
	}
	if si.ambiguous[element+":"+key] || ti.ambiguous[element+":"+key] {
		problems = append(problems, ProblemAmbiguousName)
	}
	if (sourceUnrecognized || targetUnrecognized) && !sameText {
		problems = append(problems, ProblemUnrecognizedKeyword)
	}
	return problems
}

func (r *SchemaResult) compareAttributeTypes(si, ti *schemaIndex, opts SchemaOptions) {
	for _, key := range unionOIDs(si.ats, ti.ats, si.unparsed[ElementAttributeType], ti.unparsed[ElementAttributeType]) {
		s, t := si.ats[key], ti.ats[key]
		item := SchemaItem{Element: ElementAttributeType, sourceAT: s, targetAT: t}
		switch {
		case t != nil:
			item.OID, item.Names, item.TargetDefinition, item.TargetCollection = t.OID, t.Names, t.Definition, t.Collection
		case s != nil:
			item.OID, item.Names = s.OID, s.Names
		default:
			item.OID = key
		}
		if s != nil {
			item.SourceDefinition, item.SourceCollection = s.Definition, s.Collection
		}
		sameText := s != nil && t != nil && s.Definition == t.Definition
		item.Problems = unknownBecause(ElementAttributeType, key, si, ti, s != nil && len(s.Unrecognized) > 0, t != nil && len(t.Unrecognized) > 0, sameText)
		if !snapshot.IsNumericOID(item.OID) {
			item.Problems = append(item.Problems, ProblemNonNumericOID)
		}
		switch {
		case hasUnknownProblem(item.Problems):
			item.Kind = Unknown
		case s == nil:
			item.Kind = Added
		case t == nil:
			item.Kind = Removed
		default:
			item.Fields = attributeTypeFields(s, t, si, ti)
			item.Kind = kindOfFields(item.Fields)
		}
		r.record(&r.AttributeTypes, item, opts.IncludeUnchanged)
	}
}

func (r *SchemaResult) compareObjectClasses(si, ti *schemaIndex, opts SchemaOptions) {
	for _, key := range unionOIDs(si.ocs, ti.ocs, si.unparsed[ElementObjectClass], ti.unparsed[ElementObjectClass]) {
		s, t := si.ocs[key], ti.ocs[key]
		item := SchemaItem{Element: ElementObjectClass, sourceOC: s, targetOC: t}
		switch {
		case t != nil:
			item.OID, item.Names, item.TargetDefinition, item.TargetCollection = t.OID, t.Names, t.Definition, t.Collection
		case s != nil:
			item.OID, item.Names = s.OID, s.Names
		default:
			item.OID = key
		}
		if s != nil {
			item.SourceDefinition, item.SourceCollection = s.Definition, s.Collection
		}
		sameText := s != nil && t != nil && s.Definition == t.Definition
		item.Problems = unknownBecause(ElementObjectClass, key, si, ti, s != nil && len(s.Unrecognized) > 0, t != nil && len(t.Unrecognized) > 0, sameText)
		if !snapshot.IsNumericOID(item.OID) {
			item.Problems = append(item.Problems, ProblemNonNumericOID)
		}
		switch {
		case hasUnknownProblem(item.Problems):
			item.Kind = Unknown
		case s == nil:
			item.Kind = Added
		case t == nil:
			item.Kind = Removed
		default:
			item.Fields = objectClassFields(s, t, si, ti)
			item.Kind = kindOfFields(item.Fields)
		}
		r.record(&r.ObjectClasses, item, opts.IncludeUnchanged)
	}
}

func hasUnknownProblem(problems []string) bool {
	for _, p := range problems {
		switch p {
		case ProblemUnparsed, ProblemAmbiguousName, ProblemUnrecognizedKeyword:
			return true
		}
	}
	return false
}

func kindOfFields(fields []FieldChange) Kind {
	if len(fields) == 0 {
		return Unchanged
	}
	for _, f := range fields {
		if f.Category != FieldExtension {
			return Modified
		}
	}
	return MetadataOnly
}

// fieldDiff appends a change when the comparison keys differ.
func fieldDiff(out *[]FieldChange, field, category string, sourceKey, targetKey any, sourceShown, targetShown []string) {
	if reflect.DeepEqual(sourceKey, targetKey) {
		return
	}
	*out = append(*out, FieldChange{Field: field, Category: category, Source: sourceShown, Target: targetShown})
}

func one(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func flag(b bool) []string {
	if b {
		return []string{"true"}
	}
	return []string{"false"}
}

// nameSet is NAME compared as a set, case-insensitively.
func nameSet(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.ToLower(n))
	}
	sort.Strings(out)
	return out
}

func resolvedSet(refs []string, resolve func(string) (string, bool)) []string {
	out := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		key, _ := resolve(ref)
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func attributeTypeFields(s, t *snapshot.SchemaAttributeType, si, ti *schemaIndex) []FieldChange {
	var out []FieldChange
	fieldDiff(&out, "names", FieldCore, nameSet(s.Names), nameSet(t.Names), s.Names, t.Names)
	fieldDiff(&out, "desc", FieldDescription, s.Desc, t.Desc, one(s.Desc), one(t.Desc))
	fieldDiff(&out, "obsolete", FieldCore, s.Obsolete, t.Obsolete, flag(s.Obsolete), flag(t.Obsolete))
	sourceSup, targetSup := "", ""
	if s.Sup != "" {
		sourceSup, _ = si.resolveAT(s.Sup)
	}
	if t.Sup != "" {
		targetSup, _ = ti.resolveAT(t.Sup)
	}
	fieldDiff(&out, "sup", FieldCore, sourceSup, targetSup, one(s.Sup), one(t.Sup))
	fieldDiff(&out, "equality", FieldCore, si.resolveRule(s.Equality), ti.resolveRule(t.Equality), one(s.Equality), one(t.Equality))
	fieldDiff(&out, "ordering", FieldCore, si.resolveRule(s.Ordering), ti.resolveRule(t.Ordering), one(s.Ordering), one(t.Ordering))
	fieldDiff(&out, "substr", FieldCore, si.resolveRule(s.Substr), ti.resolveRule(t.Substr), one(s.Substr), one(t.Substr))
	fieldDiff(&out, "syntax", FieldCore, strings.ToLower(s.Syntax), strings.ToLower(t.Syntax), one(s.Syntax), one(t.Syntax))
	fieldDiff(&out, "syntaxLength", FieldCore, s.SyntaxLength, t.SyntaxLength, intText(s.SyntaxLength), intText(t.SyntaxLength))
	fieldDiff(&out, "singleValue", FieldCore, s.SingleValue, t.SingleValue, flag(s.SingleValue), flag(t.SingleValue))
	fieldDiff(&out, "collective", FieldCore, s.Collective, t.Collective, flag(s.Collective), flag(t.Collective))
	fieldDiff(&out, "noUserModification", FieldCore, s.NoUserModification, t.NoUserModification,
		flag(s.NoUserModification), flag(t.NoUserModification))
	fieldDiff(&out, "usage", FieldCore, strings.ToLower(s.Usage), strings.ToLower(t.Usage), one(s.Usage), one(t.Usage))
	extensionFields(&out, s.Extensions, t.Extensions)
	return out
}

func objectClassFields(s, t *snapshot.SchemaObjectClass, si, ti *schemaIndex) []FieldChange {
	var out []FieldChange
	fieldDiff(&out, "names", FieldCore, nameSet(s.Names), nameSet(t.Names), s.Names, t.Names)
	fieldDiff(&out, "desc", FieldDescription, s.Desc, t.Desc, one(s.Desc), one(t.Desc))
	fieldDiff(&out, "obsolete", FieldCore, s.Obsolete, t.Obsolete, flag(s.Obsolete), flag(t.Obsolete))
	fieldDiff(&out, "sup", FieldCore, resolvedSet(s.Sup, si.resolveOC), resolvedSet(t.Sup, ti.resolveOC), s.Sup, t.Sup)
	fieldDiff(&out, "kind", FieldCore, strings.ToUpper(s.Kind), strings.ToUpper(t.Kind), one(s.Kind), one(t.Kind))
	fieldDiff(&out, "must", FieldCore, resolvedSet(s.Must, si.resolveAT), resolvedSet(t.Must, ti.resolveAT), s.Must, t.Must)
	fieldDiff(&out, "may", FieldCore, resolvedSet(s.May, si.resolveAT), resolvedSet(t.May, ti.resolveAT), s.May, t.May)
	extensionFields(&out, s.Extensions, t.Extensions)
	return out
}

func intText(n int) []string {
	if n == 0 {
		return nil
	}
	return []string{strconv.Itoa(n)}
}

func extensionFields(out *[]FieldChange, s, t map[string][]string) {
	keys := map[string]bool{}
	for k := range s {
		keys[k] = true
	}
	for k := range t {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fieldDiff(out, k, FieldExtension, s[k], t[k], s[k], t[k])
	}
}

// dependencies fills Requires from the target's definitions and RequiredBy
// from the source's.
func (r *SchemaResult) dependencies(si, ti *schemaIndex) {
	refsOfAT := func(ix *schemaIndex, at *snapshot.SchemaAttributeType) []SchemaRef {
		if at == nil || at.Sup == "" {
			return nil
		}
		oid, ok := ix.resolveAT(at.Sup)
		if !ok {
			return []SchemaRef{{Element: ElementAttributeType, OID: "", Name: at.Sup, Relation: "sup"}}
		}
		return []SchemaRef{{Element: ElementAttributeType, OID: ix.ats[oid].OID, Name: at.Sup, Relation: "sup"}}
	}
	refsOfOC := func(ix *schemaIndex, oc *snapshot.SchemaObjectClass) []SchemaRef {
		if oc == nil {
			return nil
		}
		var out []SchemaRef
		for _, sup := range oc.Sup {
			if oid, ok := ix.resolveOC(sup); ok {
				out = append(out, SchemaRef{Element: ElementObjectClass, OID: ix.ocs[oid].OID, Name: sup, Relation: "sup"})
			} else {
				out = append(out, SchemaRef{Element: ElementObjectClass, Name: sup, Relation: "sup"})
			}
		}
		for rel, list := range map[string][]string{"must": oc.Must, "may": oc.May} {
			for _, name := range list {
				if oid, ok := ix.resolveAT(name); ok {
					out = append(out, SchemaRef{Element: ElementAttributeType, OID: ix.ats[oid].OID, Name: name, Relation: rel})
				} else {
					out = append(out, SchemaRef{Element: ElementAttributeType, Name: name, Relation: rel})
				}
			}
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Relation != out[j].Relation {
				return out[i].Relation > out[j].Relation
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		return out
	}

	// Who refers to what, on the source side.
	referrers := map[string][]SchemaRef{}
	for i := range r.source.Snapshot.AttributeTypes {
		at := &r.source.Snapshot.AttributeTypes[i]
		for _, ref := range refsOfAT(si, at) {
			if ref.OID != "" {
				key := ref.Element + ":" + strings.ToLower(ref.OID)
				referrers[key] = append(referrers[key], SchemaRef{Element: ElementAttributeType, OID: at.OID, Name: firstName(at.Names, at.OID), Relation: ref.Relation})
			}
		}
	}
	for i := range r.source.Snapshot.ObjectClasses {
		oc := &r.source.Snapshot.ObjectClasses[i]
		for _, ref := range refsOfOC(si, oc) {
			if ref.OID != "" {
				key := ref.Element + ":" + strings.ToLower(ref.OID)
				referrers[key] = append(referrers[key], SchemaRef{Element: ElementObjectClass, OID: oc.OID, Name: firstName(oc.Names, oc.OID), Relation: ref.Relation})
			}
		}
	}

	for i := range r.Items {
		it := &r.Items[i]
		switch it.Element {
		case ElementAttributeType:
			if it.targetAT != nil {
				it.Requires = refsOfAT(ti, it.targetAT)
			}
		case ElementObjectClass:
			if it.targetOC != nil {
				it.Requires = refsOfOC(ti, it.targetOC)
			}
		}
		for _, ref := range it.Requires {
			if ref.OID == "" && !hasProblem(it.Problems, ProblemUnresolvedReference) && it.Kind != Unknown {
				it.Problems = append(it.Problems, ProblemUnresolvedReference)
			}
		}
		if it.Kind == Removed || it.Kind == Modified {
			refs := referrers[it.Key()]
			sort.SliceStable(refs, func(a, b int) bool { return snapshot.OIDLess(refs[a].OID, refs[b].OID) })
			it.RequiredBy = refs
		}
	}
}

func firstName(names []string, oid string) string {
	if len(names) > 0 {
		return names[0]
	}
	return oid
}

func hasProblem(problems []string, p string) bool {
	for _, x := range problems {
		if x == p {
			return true
		}
	}
	return false
}

// order computes Order: a topological order over the items a change could be
// derived for.
func (r *SchemaResult) order() {
	actionable := func(k Kind) bool { return k == Added || k == Modified || k == Removed }
	keys := map[string]bool{}
	for _, it := range r.Items {
		if actionable(it.Kind) {
			keys[it.Key()] = true
		}
	}
	// edges[a] lists the keys that must come after a.
	edges := map[string][]string{}
	indegree := map[string]int{}
	for k := range keys {
		indegree[k] = 0
	}
	addEdge := func(before, after string) {
		if before == after || !keys[before] || !keys[after] {
			return
		}
		edges[before] = append(edges[before], after)
		indegree[after]++
	}
	for _, it := range r.Items {
		if !actionable(it.Kind) {
			continue
		}
		switch it.Kind {
		case Added, Modified:
			// What the new definition needs comes first, if it is being added
			// or changed too.
			for _, ref := range it.Requires {
				if ref.OID == "" {
					continue
				}
				dep := ref.Element + ":" + strings.ToLower(ref.OID)
				if d, ok := r.Item(dep); ok && (d.Kind == Added || d.Kind == Modified) {
					addEdge(dep, it.Key())
				}
			}
		case Removed:
			// What refers to a removed definition goes first, if it is being
			// removed or changed too.
			for _, ref := range it.RequiredBy {
				dep := ref.Element + ":" + strings.ToLower(ref.OID)
				if d, ok := r.Item(dep); ok && (d.Kind == Removed || d.Kind == Modified) {
					addEdge(dep, it.Key())
				}
			}
		}
	}
	// Removals after everything else they are not tied to: a definition being
	// changed may stop referring to one being removed.
	for k := range keys {
		it, _ := r.Item(k)
		if it.Kind != Removed {
			continue
		}
		for other := range keys {
			o, _ := r.Item(other)
			if o.Kind != Removed {
				addEdge(other, k)
			}
		}
	}

	var ready []string
	for k, d := range indegree {
		if d == 0 {
			ready = append(ready, k)
		}
	}
	less := func(a, b string) bool {
		ia, _ := r.Item(a)
		ib, _ := r.Item(b)
		if ia.Element != ib.Element {
			if ia.Kind == Removed && ib.Kind == Removed {
				return ia.Element == ElementObjectClass
			}
			return ia.Element == ElementAttributeType
		}
		return snapshot.OIDLess(ia.OID, ib.OID)
	}
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
		next := ready[0]
		ready = ready[1:]
		r.Order = append(r.Order, next)
		for _, after := range edges[next] {
			indegree[after]--
			if indegree[after] == 0 {
				ready = append(ready, after)
			}
		}
	}
	if len(r.Order) < len(keys) {
		ordered := map[string]bool{}
		for _, k := range r.Order {
			ordered[k] = true
		}
		for k := range keys {
			if !ordered[k] {
				i := r.index[k]
				r.Items[i].Problems = append(r.Items[i].Problems, ProblemDependencyCycle)
			}
		}
	}
}
