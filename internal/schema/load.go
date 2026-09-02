package schema

import "strings"

// Subschema attribute descriptions, as published by the subschema subentry.
// Both target servers use these names; the case they use differs, which is why
// Load matches them case-insensitively.
const (
	AttrObjectClasses     = "objectClasses"
	AttrAttributeTypes    = "attributeTypes"
	AttrLDAPSyntaxes      = "ldapSyntaxes"
	AttrMatchingRules     = "matchingRules"
	AttrMatchingRuleUse   = "matchingRuleUse"
	AttrDITContentRules   = "dITContentRules"
	AttrNameForms         = "nameForms"
	AttrDITStructureRules = "dITStructureRules"
)

// SubschemaAttributes is the attribute list to request when reading a subschema
// subentry. Servers do not return these by default: they are operational, so a
// search that asks for "*" gets none of them. Forgetting this list is the usual
// reason a schema browser shows an empty schema.
var SubschemaAttributes = []string{
	AttrObjectClasses,
	AttrAttributeTypes,
	AttrLDAPSyntaxes,
	AttrMatchingRules,
	AttrMatchingRuleUse,
	AttrDITContentRules,
	AttrNameForms,
}

// Load parses a subschema subentry into a Schema.
//
// attrs maps subschema attribute descriptions to their raw values, exactly as
// read from the server. Definitions that fail to parse are collected in
// Schema.Errors; Load itself does not fail, because a schema with one bad
// definition is still worth browsing and refusing to show it helps nobody.
func Load(subschemaDN string, attrs map[string][]string) *Schema {
	s := &Schema{DN: subschemaDN}

	// Servers differ on the case of these attribute names, so match by fold
	// rather than by exact key.
	byFold := make(map[string][]string, len(attrs))
	for k, v := range attrs {
		byFold[strings.ToLower(k)] = append(byFold[strings.ToLower(k)], v...)
	}
	get := func(name string) []string { return byFold[strings.ToLower(name)] }

	fail := func(attr, def string, err error) {
		s.Errors = append(s.Errors, ParseError{Attribute: attr, Definition: def, Err: err})
	}

	for _, def := range get(AttrObjectClasses) {
		v, err := ParseObjectClass(def)
		if err != nil {
			fail(AttrObjectClasses, def, err)
			continue
		}
		s.ObjectClasses = append(s.ObjectClasses, v)
	}
	for _, def := range get(AttrAttributeTypes) {
		v, err := ParseAttributeType(def)
		if err != nil {
			fail(AttrAttributeTypes, def, err)
			continue
		}
		s.AttributeTypes = append(s.AttributeTypes, v)
	}
	for _, def := range get(AttrLDAPSyntaxes) {
		v, err := ParseSyntax(def)
		if err != nil {
			fail(AttrLDAPSyntaxes, def, err)
			continue
		}
		s.Syntaxes = append(s.Syntaxes, v)
	}
	for _, def := range get(AttrMatchingRules) {
		v, err := ParseMatchingRule(def)
		if err != nil {
			fail(AttrMatchingRules, def, err)
			continue
		}
		s.MatchingRules = append(s.MatchingRules, v)
	}
	for _, def := range get(AttrMatchingRuleUse) {
		v, err := ParseMatchingRuleUse(def)
		if err != nil {
			fail(AttrMatchingRuleUse, def, err)
			continue
		}
		s.MatchingRuleUses = append(s.MatchingRuleUses, v)
	}
	for _, def := range get(AttrDITContentRules) {
		v, err := ParseDITContentRule(def)
		if err != nil {
			fail(AttrDITContentRules, def, err)
			continue
		}
		s.DITContentRules = append(s.DITContentRules, v)
	}
	for _, def := range get(AttrNameForms) {
		v, err := ParseNameForm(def)
		if err != nil {
			fail(AttrNameForms, def, err)
			continue
		}
		s.NameForms = append(s.NameForms, v)
	}

	s.index()
	return s
}

// IsEmpty reports whether the schema holds no definitions at all, which means
// the subschema read returned nothing usable rather than that the server has no
// schema.
func (s *Schema) IsEmpty() bool {
	return len(s.ObjectClasses) == 0 && len(s.AttributeTypes) == 0
}

// Counts summarises the schema for the browser's landing page.
type Counts struct {
	ObjectClasses    int `json:"objectClasses"`
	AttributeTypes   int `json:"attributeTypes"`
	Syntaxes         int `json:"syntaxes"`
	MatchingRules    int `json:"matchingRules"`
	MatchingRuleUses int `json:"matchingRuleUses"`
	DITContentRules  int `json:"ditContentRules"`
	NameForms        int `json:"nameForms"`
	Errors           int `json:"errors"`
}

// Counts returns the definition counts by type.
func (s *Schema) Counts() Counts {
	return Counts{
		ObjectClasses:    len(s.ObjectClasses),
		AttributeTypes:   len(s.AttributeTypes),
		Syntaxes:         len(s.Syntaxes),
		MatchingRules:    len(s.MatchingRules),
		MatchingRuleUses: len(s.MatchingRuleUses),
		DITContentRules:  len(s.DITContentRules),
		NameForms:        len(s.NameForms),
		Errors:           len(s.Errors),
	}
}
