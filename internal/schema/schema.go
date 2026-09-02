// Package schema implements RFC 4512 directory schema: parsing the subschema
// subentry a server publishes, indexing it, and answering the questions an
// editor needs to ask about an entry.
//
// The parser is deliberately tolerant. RFC 4512 is precise, real servers are
// not, and a schema browser that refuses to load because one vendor emitted a
// non-numeric OID or a stray space is useless. Definitions that cannot be
// parsed at all are collected in Schema.Errors rather than failing the load,
// so the UI can show what it understood and be honest about the rest.
package schema

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Kind is the object class kind: abstract, structural or auxiliary.
type Kind int

// The object class kinds of RFC 4512 section 2.4.
const (
	KindStructural Kind = iota // the default when a definition omits the kind
	KindAbstract
	KindAuxiliary
)

func (k Kind) String() string {
	switch k {
	case KindAbstract:
		return "ABSTRACT"
	case KindAuxiliary:
		return "AUXILIARY"
	default:
		return "STRUCTURAL"
	}
}

// Usage is the attribute type usage of RFC 4512 section 2.5.1.
type Usage int

// The attribute usages. UsageUserApplications is the default.
const (
	UsageUserApplications Usage = iota
	UsageDirectoryOperation
	UsageDistributedOperation
	UsageDSAOperation
)

func (u Usage) String() string {
	switch u {
	case UsageDirectoryOperation:
		return "directoryOperation"
	case UsageDistributedOperation:
		return "distributedOperation"
	case UsageDSAOperation:
		return "dSAOperation"
	default:
		return "userApplications"
	}
}

// Operational reports whether the usage marks an attribute the server owns.
// Operational attributes are shown read-only and are never offered for editing.
func (u Usage) Operational() bool { return u != UsageUserApplications }

// Extensions holds the X-ORIGIN style extension values of a definition, keyed
// by extension name.
type Extensions map[string][]string

// ObjectClass is an RFC 4512 object class definition.
type ObjectClass struct {
	OID        string
	Names      []string
	Desc       string
	Obsolete   bool
	SuperNames []string
	Kind       Kind
	Must       []string
	May        []string
	Extensions Extensions
	// Raw is the definition exactly as the server published it. The schema
	// browser shows it and the LDIF export writes it, so neither depends on
	// this package rendering a byte-identical definition back.
	Raw string
}

// AttributeType is an RFC 4512 attribute type definition.
type AttributeType struct {
	OID       string
	Names     []string
	Desc      string
	Obsolete  bool
	SuperName string
	Equality  string
	Ordering  string
	Substr    string
	// Syntax is the syntax OID with any {len} suffix removed; SyntaxLen holds
	// that suffix, which servers use as an advisory maximum length.
	Syntax             string
	SyntaxLen          int
	SingleValue        bool
	Collective         bool
	NoUserModification bool
	Usage              Usage
	Extensions         Extensions
	Raw                string
}

// Syntax is an RFC 4512 LDAP syntax definition.
type Syntax struct {
	OID        string
	Desc       string
	Extensions Extensions
	Raw        string
}

// MatchingRule is an RFC 4512 matching rule definition.
type MatchingRule struct {
	OID        string
	Names      []string
	Desc       string
	Obsolete   bool
	Syntax     string
	Extensions Extensions
	Raw        string
}

// MatchingRuleUse is an RFC 4512 matching rule use definition: the attribute
// types to which a matching rule may be applied in an extensible match.
type MatchingRuleUse struct {
	OID        string
	Names      []string
	Desc       string
	Obsolete   bool
	Applies    []string
	Extensions Extensions
	Raw        string
}

// DITContentRule is an RFC 4512 DIT content rule definition.
type DITContentRule struct {
	OID        string
	Names      []string
	Desc       string
	Obsolete   bool
	Aux        []string
	Must       []string
	May        []string
	Not        []string
	Extensions Extensions
	Raw        string
}

// NameForm is an RFC 4512 name form definition.
type NameForm struct {
	OID         string
	Names       []string
	Desc        string
	Obsolete    bool
	ObjectClass string
	Must        []string
	May         []string
	Extensions  Extensions
	Raw         string
}

// ParseError records one definition that could not be parsed, so the loader can
// keep the rest of the schema rather than failing whole.
type ParseError struct {
	// Attribute is the subschema attribute the definition came from, e.g.
	// "objectClasses".
	Attribute string
	// Definition is the raw text that failed.
	Definition string
	Err        error
}

func (e ParseError) Error() string {
	return fmt.Sprintf("schema: %s: %v: %s", e.Attribute, e.Err, truncate(e.Definition, 120))
}

func (e ParseError) Unwrap() error { return e.Err }

// Schema is a parsed subschema subentry, indexed for lookup.
//
// Every lookup is case-insensitive and accepts either a name or an OID, because
// that is how attributes are written in the wild: an entry names "objectClass",
// a filter names "2.5.4.0", and both must resolve to the same definition.
type Schema struct {
	ObjectClasses    []*ObjectClass
	AttributeTypes   []*AttributeType
	Syntaxes         []*Syntax
	MatchingRules    []*MatchingRule
	MatchingRuleUses []*MatchingRuleUse
	DITContentRules  []*DITContentRule
	NameForms        []*NameForm

	// Errors holds the definitions that failed to parse. A non-empty Errors is
	// not a failed load; it is a partial one, and the UI says so.
	Errors []ParseError

	// DN is the DN of the subschema subentry the schema was read from.
	DN string

	objectClassIndex   map[string]*ObjectClass
	attributeTypeIndex map[string]*AttributeType
	syntaxIndex        map[string]*Syntax
	matchingRuleIndex  map[string]*MatchingRule
}

// Name returns the first name of a definition, falling back to the OID for the
// definitions that carry no name at all. It is what the UI labels things with.
func (o *ObjectClass) Name() string { return firstName(o.Names, o.OID) }

// Name returns the first name of the attribute type, or its OID.
func (a *AttributeType) Name() string { return firstName(a.Names, a.OID) }

// Name returns the first name of the matching rule, or its OID.
func (m *MatchingRule) Name() string { return firstName(m.Names, m.OID) }

// Name returns the first name of the matching rule use, or its OID.
func (m *MatchingRuleUse) Name() string { return firstName(m.Names, m.OID) }

// Name returns the first name of the content rule, or its OID.
func (d *DITContentRule) Name() string { return firstName(d.Names, d.OID) }

// Name returns the first name of the name form, or its OID.
func (n *NameForm) Name() string { return firstName(n.Names, n.OID) }

// firstName returns the first usable name, falling back to the OID. It skips
// empty entries rather than trusting the slice: a server that publishes NAME ”
// must not leave the whole UI labelling that definition with a blank.
func firstName(names []string, oid string) string {
	for _, n := range names {
		if n != "" {
			return n
		}
	}
	return oid
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// index builds the lookup maps. It is called once, by the loader.
func (s *Schema) index() {
	s.objectClassIndex = make(map[string]*ObjectClass, len(s.ObjectClasses)*2)
	for _, oc := range s.ObjectClasses {
		s.objectClassIndex[fold(oc.OID)] = oc
		for _, n := range oc.Names {
			s.objectClassIndex[fold(n)] = oc
		}
	}
	s.attributeTypeIndex = make(map[string]*AttributeType, len(s.AttributeTypes)*2)
	for _, at := range s.AttributeTypes {
		s.attributeTypeIndex[fold(at.OID)] = at
		for _, n := range at.Names {
			s.attributeTypeIndex[fold(n)] = at
		}
	}
	s.syntaxIndex = make(map[string]*Syntax, len(s.Syntaxes))
	for _, sy := range s.Syntaxes {
		s.syntaxIndex[fold(sy.OID)] = sy
	}
	s.matchingRuleIndex = make(map[string]*MatchingRule, len(s.MatchingRules)*2)
	for _, mr := range s.MatchingRules {
		s.matchingRuleIndex[fold(mr.OID)] = mr
		for _, n := range mr.Names {
			s.matchingRuleIndex[fold(n)] = mr
		}
	}
}

// fold normalises a name or OID for lookup: case-folded, with any attribute
// options such as ";binary" removed, since "cn;lang-de" is still "cn".
func fold(s string) string {
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// BaseName strips attribute options from an attribute description, so callers
// can report "userCertificate" for "userCertificate;binary".
func BaseName(attrDescription string) string {
	base, _, _ := strings.Cut(attrDescription, ";")
	return base
}

// Options returns the ";"-separated attribute options of an attribute
// description, or nil when there are none.
func Options(attrDescription string) []string {
	_, opts, ok := strings.Cut(attrDescription, ";")
	if !ok || opts == "" {
		return nil
	}
	return strings.Split(opts, ";")
}

// ObjectClass looks an object class up by name or OID, case-insensitively.
func (s *Schema) ObjectClass(nameOrOID string) *ObjectClass {
	return s.objectClassIndex[fold(nameOrOID)]
}

// AttributeType looks an attribute type up by name or OID, case-insensitively.
// Attribute options are ignored, so "userCertificate;binary" resolves.
func (s *Schema) AttributeType(nameOrOID string) *AttributeType {
	return s.attributeTypeIndex[fold(nameOrOID)]
}

// Syntax looks an LDAP syntax up by OID.
func (s *Schema) Syntax(oid string) *Syntax { return s.syntaxIndex[fold(oid)] }

// MatchingRule looks a matching rule up by name or OID.
func (s *Schema) MatchingRule(nameOrOID string) *MatchingRule {
	return s.matchingRuleIndex[fold(nameOrOID)]
}

// maxSupDepth bounds superior-chain walks. A schema with a SUP cycle is
// malformed, and both target servers reject one, but a schema arrives over the
// network from a server Alder does not control and must never hang the process.
const maxSupDepth = 100

// Supers returns the transitive superior classes of oc, nearest first, without
// oc itself. Unknown superiors are skipped: a class may legitimately name a
// superior the server did not publish.
func (s *Schema) Supers(oc *ObjectClass) []*ObjectClass {
	var out []*ObjectClass
	seen := map[*ObjectClass]bool{oc: true}
	queue := []*ObjectClass{oc}
	for depth := 0; len(queue) > 0 && depth < maxSupDepth; depth++ {
		var next []*ObjectClass
		for _, cur := range queue {
			for _, name := range cur.SuperNames {
				sup := s.ObjectClass(name)
				if sup == nil || seen[sup] {
					continue
				}
				seen[sup] = true
				out = append(out, sup)
				next = append(next, sup)
			}
		}
		queue = next
	}
	return out
}

// SuperTypes returns the transitive superior attribute types of at, nearest
// first, without at itself.
func (s *Schema) SuperTypes(at *AttributeType) []*AttributeType {
	var out []*AttributeType
	seen := map[*AttributeType]bool{at: true}
	cur := at
	for depth := 0; cur.SuperName != "" && depth < maxSupDepth; depth++ {
		sup := s.AttributeType(cur.SuperName)
		if sup == nil || seen[sup] {
			break
		}
		seen[sup] = true
		out = append(out, sup)
		cur = sup
	}
	return out
}

// EffectiveSyntax resolves the syntax OID of an attribute type, walking the SUP
// chain, since a subtype inherits its supertype's syntax when it declares none.
// It returns "" when no ancestor declares one either.
func (s *Schema) EffectiveSyntax(at *AttributeType) string {
	if at.Syntax != "" {
		return at.Syntax
	}
	for _, sup := range s.SuperTypes(at) {
		if sup.Syntax != "" {
			return sup.Syntax
		}
	}
	return ""
}

// EffectiveEquality resolves the equality matching rule of an attribute type,
// walking the SUP chain for the same reason as EffectiveSyntax.
func (s *Schema) EffectiveEquality(at *AttributeType) string {
	if at.Equality != "" {
		return at.Equality
	}
	for _, sup := range s.SuperTypes(at) {
		if sup.Equality != "" {
			return sup.Equality
		}
	}
	return ""
}

// EffectiveSingleValue reports whether an attribute type is single-valued,
// walking the SUP chain.
func (s *Schema) EffectiveSingleValue(at *AttributeType) bool {
	if at.SingleValue {
		return true
	}
	for _, sup := range s.SuperTypes(at) {
		if sup.SingleValue {
			return true
		}
	}
	return false
}

// EffectiveNoUserModification reports whether an attribute type is server-owned,
// walking the SUP chain.
func (s *Schema) EffectiveNoUserModification(at *AttributeType) bool {
	if at.NoUserModification {
		return true
	}
	for _, sup := range s.SuperTypes(at) {
		if sup.NoUserModification {
			return true
		}
	}
	return false
}

// EffectiveUsage resolves the usage of an attribute type, walking the SUP chain.
func (s *Schema) EffectiveUsage(at *AttributeType) Usage {
	if at.Usage != UsageUserApplications {
		return at.Usage
	}
	for _, sup := range s.SuperTypes(at) {
		if sup.Usage != UsageUserApplications {
			return sup.Usage
		}
	}
	return UsageUserApplications
}

// AttributeRequirements is the answer to "what may and must this entry hold",
// computed from a set of object classes across their whole superior chains.
//
// This drives the entry editor: Must is what the form marks required and
// refuses to leave empty, May is what the "add attribute" menu offers, and
// anything on the entry that is in neither is flagged as unrecognised rather
// than silently dropped.
type AttributeRequirements struct {
	// Must and May hold canonical attribute type names, sorted, deduplicated,
	// and disjoint: an attribute required by one class and optional in another
	// appears only in Must.
	Must []string
	May  []string
	// Structural is the structural class of the set, if exactly one was found.
	// An entry must have exactly one structural class, so zero or several is a
	// finding the UI reports.
	Structural *ObjectClass
	// Unknown holds the named classes the schema does not define.
	Unknown []string
}

// Requirements computes the attribute requirements of a set of object class
// names, following every superior chain.
func (s *Schema) Requirements(classNames []string) AttributeRequirements {
	var req AttributeRequirements
	must := map[string]string{}
	may := map[string]string{}
	var structurals []*ObjectClass

	add := func(dst map[string]string, names []string) {
		for _, n := range names {
			key := fold(n)
			if _, ok := dst[key]; ok {
				continue
			}
			dst[key] = s.canonicalAttrName(n)
		}
	}

	top := s.ObjectClass("top")
	for _, name := range classNames {
		oc := s.ObjectClass(name)
		if oc == nil {
			req.Unknown = append(req.Unknown, name)
			continue
		}
		chain := append([]*ObjectClass{oc}, s.Supers(oc)...)
		for _, c := range chain {
			if c.Kind == KindStructural && c != top {
				structurals = append(structurals, c)
			}
			add(must, c.Must)
			add(may, c.May)
		}
	}

	// An attribute that is required anywhere is required, so May is May minus
	// Must rather than the union.
	for key := range must {
		delete(may, key)
	}
	req.Must = sortedValues(must)
	req.May = sortedValues(may)

	// The structural class of the entry is the most specific of the structural
	// chain: the one that is not a superior of any other found.
	req.Structural = mostSpecific(s, structurals)
	return req
}

// canonicalAttrName maps a written attribute name onto the schema's preferred
// spelling, so "COMMONNAME" and "2.5.4.3" both render as "cn".
func (s *Schema) canonicalAttrName(name string) string {
	if at := s.AttributeType(name); at != nil {
		return at.Name()
	}
	return name
}

// CanonicalAttrName is canonicalAttrName for callers outside the package. Any
// attribute options are preserved: "CN;binary" canonicalises to "cn;binary".
func (s *Schema) CanonicalAttrName(name string) string {
	opts := Options(name)
	base := s.canonicalAttrName(BaseName(name))
	if len(opts) == 0 {
		return base
	}
	return base + ";" + strings.Join(opts, ";")
}

// mostSpecific returns the class in the set that no other class in the set
// descends from, or nil when the set is empty or ambiguous.
func mostSpecific(s *Schema, classes []*ObjectClass) *ObjectClass {
	var candidates []*ObjectClass
	for _, c := range classes {
		isSuperOfAnother := false
		for _, other := range classes {
			if other == c {
				continue
			}
			if slices.Contains(s.Supers(other), c) {
				isSuperOfAnother = true
				break
			}
		}
		if !isSuperOfAnother && !slices.Contains(candidates, c) {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return nil
}

func sortedValues(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// StructuralClasses returns every structural object class, sorted by name. The
// "new entry" form offers exactly this list.
func (s *Schema) StructuralClasses() []*ObjectClass { return s.classesOfKind(KindStructural) }

// AuxiliaryClasses returns every auxiliary object class, sorted by name.
func (s *Schema) AuxiliaryClasses() []*ObjectClass { return s.classesOfKind(KindAuxiliary) }

func (s *Schema) classesOfKind(k Kind) []*ObjectClass {
	var out []*ObjectClass
	for _, oc := range s.ObjectClasses {
		if oc.Kind == k {
			out = append(out, oc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name()) < strings.ToLower(out[j].Name())
	})
	return out
}

// UsedBy returns the object classes that name attr in MUST or MAY, sorted. The
// schema browser cross-links an attribute type back to its classes with it.
func (s *Schema) UsedBy(attrNameOrOID string) (must, may []*ObjectClass) {
	at := s.AttributeType(attrNameOrOID)
	matches := func(names []string) bool {
		for _, n := range names {
			if at != nil {
				if s.AttributeType(n) == at {
					return true
				}
				continue
			}
			if fold(n) == fold(attrNameOrOID) {
				return true
			}
		}
		return false
	}
	for _, oc := range s.ObjectClasses {
		switch {
		case matches(oc.Must):
			must = append(must, oc)
		case matches(oc.May):
			may = append(may, oc)
		}
	}
	byName := func(list []*ObjectClass) {
		sort.Slice(list, func(i, j int) bool {
			return strings.ToLower(list[i].Name()) < strings.ToLower(list[j].Name())
		})
	}
	byName(must)
	byName(may)
	return must, may
}

// SubclassesOf returns the object classes that name oc as a direct superior.
func (s *Schema) SubclassesOf(oc *ObjectClass) []*ObjectClass {
	var out []*ObjectClass
	for _, cand := range s.ObjectClasses {
		for _, supName := range cand.SuperNames {
			if s.ObjectClass(supName) == oc {
				out = append(out, cand)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name()) < strings.ToLower(out[j].Name())
	})
	return out
}

// AttributesWithSyntax returns the attribute types whose effective syntax is
// the given OID, for the syntax page of the schema browser.
func (s *Schema) AttributesWithSyntax(oid string) []*AttributeType {
	var out []*AttributeType
	for _, at := range s.AttributeTypes {
		if fold(s.EffectiveSyntax(at)) == fold(oid) {
			out = append(out, at)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name()) < strings.ToLower(out[j].Name())
	})
	return out
}
