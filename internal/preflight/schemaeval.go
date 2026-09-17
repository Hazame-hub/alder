package preflight

import (
	"strings"

	"github.com/hazame-hub/alder/internal/diff"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Judging schema definitions against a target.
//
// Identity is the OID, and meaning is compared the way a schema comparison
// compares it -- by diff.CompareSchema, not by text -- so whitespace, name order
// and a reference written as a name rather than an OID make no difference, and
// X-ORIGIN never decides anything. On top of that comparison a preflight asks
// what a comparison does not: whether the target publishes the syntax and
// matching rules a definition needs, whether what the definition names exists
// there or is supplied by the same source, and whether the target's schema can
// be written at all.
//
// A definition's outcome is decided once, and every definition and entry that
// depends on it links to that finding rather than repeating it.

// sourceDefinition is one attribute type or object class the source carries.
type sourceDefinition struct {
	element string // diff.ElementAttributeType or diff.ElementObjectClass
	oid     string
	names   []string
	at      *schema.AttributeType
	oc      *schema.ObjectClass
	// item is the change package item carrying it, and op its operation.
	item string
	op   string
	// validation is the package validation status for the item, if any.
	validation string
	// serverDefined marks a source snapshot definition its server supplied
	// itself rather than one an administrator added.
	serverDefined bool
}

func (d *sourceDefinition) key() string { return d.element + ":" + strings.ToLower(d.oid) }

func (d *sourceDefinition) label() string {
	if len(d.names) > 0 {
		return d.names[0]
	}
	return d.oid
}

// outcome is what was decided about one definition.
type outcome struct {
	finding string
	class   Classification
	// available: after migration the target defines this OID with the
	// source's meaning.
	available bool
}

// schemaEval decides definitions.
type schemaEval struct {
	b      *builder
	target *schema.Schema
	caps   directory.Capabilities
	// compared is the semantic comparison, keyed by diff item key.
	compared map[string]diff.SchemaItem
	defs     map[string]*sourceDefinition
	names    map[string]map[string]string // element -> folded name -> key
	outcomes map[string]*outcome
	visiting map[string]bool
	// schemaTargetProblem is set when the package's validation could not choose
	// a schema entry to write to.
	schemaTargetProblem map[string]bool
}

func newSchemaEval(b *builder, target *schema.Schema, caps directory.Capabilities, compared *diff.SchemaResult, defs []*sourceDefinition) *schemaEval {
	e := &schemaEval{b: b, target: target, caps: caps, compared: map[string]diff.SchemaItem{},
		defs: map[string]*sourceDefinition{}, outcomes: map[string]*outcome{}, visiting: map[string]bool{},
		names:               map[string]map[string]string{diff.ElementAttributeType: {}, diff.ElementObjectClass: {}},
		schemaTargetProblem: map[string]bool{}}
	if compared != nil {
		for _, it := range compared.Items {
			e.compared[it.Key()] = it
		}
	}
	for _, d := range defs {
		e.defs[d.key()] = d
		e.names[d.element][strings.ToLower(d.oid)] = d.key()
		for _, n := range d.names {
			e.names[d.element][strings.ToLower(n)] = d.key()
		}
	}
	return e
}

// sourceKey finds the source definition a reference names.
func (e *schemaEval) sourceKey(element, ref string) (string, bool) {
	key, ok := e.names[element][strings.ToLower(strings.TrimSpace(ref))]
	return key, ok
}

// targetDefines reports whether the target defines a reference, by name or OID.
func (e *schemaEval) targetDefines(element, ref string) bool {
	if e.target == nil {
		return false
	}
	if element == diff.ElementObjectClass {
		return e.target.ObjectClass(ref) != nil
	}
	return e.target.AttributeType(ref) != nil
}

// all decides every definition, in a stable order.
func (e *schemaEval) all(order []string) {
	for _, key := range order {
		e.decide(key, 0)
	}
}

// decide returns the outcome of one definition, deciding it first if need be.
func (e *schemaEval) decide(key string, depth int) *outcome {
	if got := e.outcomes[key]; got != nil {
		return got
	}
	d := e.defs[key]
	if d == nil {
		return nil
	}
	ref := SourceRef{Item: d.item, Element: d.element, OID: d.oid, Name: d.label()}
	if e.visiting[key] || depth > MaxCauseDepth {
		out := &outcome{class: Unknown}
		out.finding = e.b.add(Finding{ID: "schema:" + key + ":cycle", Code: CodeDependencyCycle, Classification: Unknown,
			Category: CategorySchema, Scope: ScopeDefinition, Source: ref, ValidationStatus: d.validation,
			Explanation: "This definition depends, through others, on itself, or on a chain too long to follow; what it needs cannot be decided."})
		e.outcomes[key] = out
		return out
	}
	e.visiting[key] = true
	defer delete(e.visiting, key)

	out := e.judge(d, ref, depth)
	e.outcomes[key] = out
	return out
}

func (e *schemaEval) judge(d *sourceDefinition, ref SourceRef, depth int) *outcome {
	id := "schema:" + d.key()
	finding := Finding{ID: id, Category: CategorySchema, Scope: ScopeDefinition, Source: ref, ValidationStatus: d.validation}
	emit := func(code string, class Classification, fact *TargetFact, explanation string) *outcome {
		finding.Code, finding.Classification, finding.Target, finding.Explanation = code, class, fact, explanation
		switch class {
		case Incompatible, Unsupported, PrerequisiteRequired:
			finding.BlocksPortability = true
		}
		if d.validation != "" {
			finding.BlocksPlan = blocksPlan(d.validation)
		}
		available := class == Portable || class == AlreadySatisfied
		return &outcome{finding: e.b.add(finding), class: class, available: available}
	}

	// A name the target already gives a different definition: both cannot
	// hold it. Nothing is renamed or paired by resemblance.
	for _, name := range d.names {
		var other string
		switch d.element {
		case diff.ElementAttributeType:
			if at := e.targetAttribute(name); at != nil && !strings.EqualFold(at.OID, d.oid) {
				other = at.OID
			}
		case diff.ElementObjectClass:
			if oc := e.targetClass(name); oc != nil && !strings.EqualFold(oc.OID, d.oid) {
				other = oc.OID
			}
		}
		if other != "" && d.serverDefined {
			return emit(CodeSourceServerDefined, Excluded, &TargetFact{Fact: "name_taken", Detail: name + " is " + other + " on the target"},
				"The source server defines this "+elementWord(d.element)+" itself, and the target uses its name "+name+" for a different definition ("+other+"). A server's own definitions are not carried across.")
		}
		if other != "" {
			finding.BlocksPortability = true
			return emit(CodeNameConflict, Incompatible, &TargetFact{Fact: "name_taken", Detail: name + " is " + other + " on the target"},
				"The target already uses the name "+name+" for a different "+elementWord(d.element)+" ("+other+"). Two definitions cannot share a name, and nothing is renamed.")
		}
	}

	item, compared := e.compared[d.key()]
	if !compared {
		return emit(CodeDefinitionUnknown, Unknown, nil, "This definition could not be compared with the target's schema.")
	}
	switch item.Kind {
	case diff.Unknown:
		return emit(CodeDefinitionUnknown, Unknown, &TargetFact{Fact: "not_comparable", Detail: strings.Join(item.Problems, ", ")},
			"Whether the target holds this definition could not be decided: "+strings.Join(item.Problems, ", ")+".")
	case diff.Unchanged:
		return emit(CodeDefinitionPresent, AlreadySatisfied, &TargetFact{Fact: "present"},
			"The target already defines this "+elementWord(d.element)+" with the same meaning.")
	case diff.MetadataOnly:
		material := materialExtensions(item.Fields)
		if len(material) == 0 {
			return emit(CodeDefinitionPresent, AlreadySatisfied, &TargetFact{Fact: "present", Detail: "only provenance extensions differ"},
				"The target already defines this "+elementWord(d.element)+" with the same meaning; only where each server says it came from differs.")
		}
		return emit(CodeDefinitionExtensionsDiffer, AlreadySatisfied, &TargetFact{Fact: "extensions_differ", Detail: strings.Join(material, ", ")},
			"The target defines this "+elementWord(d.element)+" with the same meaning, and different extensions: "+strings.Join(material, ", ")+
				". Extensions are not part of the comparison, and some change how a server behaves; check them.")
	case diff.Modified:
		core := coreFields(item.Fields)
		if len(core) == 0 {
			return emit(CodeDefinitionDescriptionDiffer, AlreadySatisfied, &TargetFact{Fact: "description_differs"},
				"The target defines this "+elementWord(d.element)+" with the same meaning and a different description.")
		}
		if d.op == "replace" && d.validation == "ready" {
			return e.prerequisites(d, emit, &finding, depth,
				&TargetFact{Fact: "defined_differently", Detail: strings.Join(core, ", ")},
				"The package replaces the target's definition of this "+elementWord(d.element)+", which differs in "+strings.Join(core, ", ")+".")
		}
		if d.serverDefined {
			return emit(CodeSourceServerDefined, Excluded, &TargetFact{Fact: "defined_differently", Detail: strings.Join(core, ", ")},
				"The source server defines this "+elementWord(d.element)+" itself, and the target defines it differently ("+strings.Join(core, ", ")+
					"). A server's own definitions are not carried across; entries are judged against the target's.")
		}
		return emit(CodeDefinitionConflict, Incompatible, &TargetFact{Fact: "defined_differently", Detail: strings.Join(core, ", ")},
			"The target already defines "+d.oid+" with a different meaning (it differs in "+strings.Join(core, ", ")+
				"). The same OID cannot mean two things, and the target's definition is not changed by a preflight.")
	case diff.Added:
		if d.serverDefined {
			return emit(CodeSourceServerDefined, Excluded, &TargetFact{Fact: "absent"},
				"The source server defines this "+elementWord(d.element)+" itself and the target does not. A server's own definitions are not carried across.")
		}
		return e.prerequisites(d, emit, &finding, depth, &TargetFact{Fact: "absent"},
			"The target does not define this "+elementWord(d.element)+".")
	}
	return emit(CodeDefinitionUnknown, Unknown, nil, "This definition could not be classified.")
}

// prerequisites judges a definition the target lacks, or that the source means
// to replace: what it needs, and whether it can be written.
func (e *schemaEval) prerequisites(d *sourceDefinition, emit func(string, Classification, *TargetFact, string) *outcome,
	finding *Finding, depth int, fact *TargetFact, lead string) *outcome {
	// Syntax and matching rules are not definitions anyone can add over LDAP:
	// a server has the code for them or it does not.
	if d.at != nil {
		if d.at.Syntax != "" {
			switch {
			case e.target == nil || len(e.target.Syntaxes) == 0:
				return emit(CodeSyntaxUnknown, Unknown, &TargetFact{Fact: "not_published", Detail: "the target publishes no syntaxes"},
					lead+" It uses syntax "+d.at.Syntax+", and the target publishes no list of syntaxes to check it against.")
			case e.target.Syntax(d.at.Syntax) == nil:
				finding.Prerequisites = append(finding.Prerequisites, Prerequisite{Type: "syntax", OID: d.at.Syntax})
				return emit(CodeSyntaxUnavailable, Unsupported, &TargetFact{Fact: "syntax_absent", Detail: d.at.Syntax},
					lead+" It uses syntax "+d.at.Syntax+", which the target does not publish. A syntax is implemented by the server and cannot be added as a definition.")
			}
		}
		for _, rule := range []struct{ kind, name string }{{"equality", d.at.Equality}, {"ordering", d.at.Ordering}, {"substring", d.at.Substr}} {
			if rule.name == "" {
				continue
			}
			switch {
			case e.target == nil || len(e.target.MatchingRules) == 0:
				return emit(CodeMatchingRuleUnknown, Unknown, &TargetFact{Fact: "not_published", Detail: "the target publishes no matching rules"},
					lead+" Its "+rule.kind+" rule is "+rule.name+", and the target publishes no list of matching rules to check it against.")
			case e.target.MatchingRule(rule.name) == nil:
				finding.Prerequisites = append(finding.Prerequisites, Prerequisite{Type: "matching_rule", Name: rule.name})
				return emit(CodeMatchingRuleUnavailable, Unsupported, &TargetFact{Fact: "matching_rule_absent", Detail: rule.kind + " " + rule.name},
					lead+" Its "+rule.kind+" rule "+rule.name+" is not one the target publishes. A matching rule is implemented by the server and cannot be added as a definition.")
			}
		}
	}

	// What the definition names: the target's, or supplied by the source.
	for _, ref := range definitionRefs(d) {
		// The source's own definition of what it names comes first: a class
		// written against the source's attribute type means that one, even
		// where the target has something else under the same name or OID.
		key, supplied := e.sourceKey(ref.element, ref.name)
		if !supplied && e.targetDefines(ref.element, ref.name) {
			continue
		}
		if !supplied {
			finding.Prerequisites = append(finding.Prerequisites, Prerequisite{Type: "schema", Element: ref.element, Name: ref.name})
			finding.ManualAction = true
			return emit(CodeUndefinedReference, PrerequisiteRequired, &TargetFact{Fact: "reference_absent", Detail: ref.relation + " " + ref.name},
				lead+" Its "+ref.relation+" names "+ref.name+", which neither the target nor this source defines. It has to be defined first.")
		}
		needed := e.decide(key, depth+1)
		if needed == nil {
			continue
		}
		dep := e.defs[key]
		if needed.class == AlreadySatisfied {
			// The source supplies it and the target already has it: not a
			// prerequisite of anything.
			continue
		}
		pre := Prerequisite{Type: "schema", Element: dep.element, OID: dep.oid, Name: dep.label(), ProvidedBy: needed.finding}
		finding.Prerequisites = append(finding.Prerequisites, pre)
		if !needed.available {
			finding.Causes = append(finding.Causes, needed.finding)
			class := needed.class
			switch class {
			case Excluded, AlreadySatisfied, Portable:
				class = PrerequisiteRequired
				finding.ManualAction = true
			}
			return emit(CodeDependencyBlocked, class, &TargetFact{Fact: "dependency_unavailable", Detail: ref.relation + " " + dep.label()},
				lead+" It needs "+dep.label()+" ("+ref.relation+"), which cannot be carried to this target, so neither can this.")
		}
	}

	if !e.caps.SchemaWrite.Editable() {
		detail := e.caps.SchemaWrite.Unavailable
		if detail == "" {
			detail = "no writable schema location was found for this connection"
		}
		e.b.require("schema_write", false, "schema definitions", "The source adds schema definitions, which needs a schema this connection can write.")
		return emit(CodeSchemaNotWritable, Unsupported, &TargetFact{Fact: "schema_not_writable", Detail: detail},
			lead+" Adding it needs a schema this connection can write, and "+detail+".")
	}
	e.b.require("schema_write", true, "schema definitions", "The source adds schema definitions, which needs a schema this connection can write.")
	if e.schemaTargetProblem[d.key()] {
		finding.ManualAction = true
		return emit(CodeSchemaTargetRequired, PrerequisiteRequired, &TargetFact{Fact: "several_schema_entries"},
			lead+" The target keeps schema in several entries, and none was chosen to add it to.")
	}
	return emit(CodeDefinitionPortable, Portable, fact, lead+" It can be added: everything it needs is on the target or supplied by this source.")
}

func (e *schemaEval) targetAttribute(name string) *schema.AttributeType {
	if e.target == nil {
		return nil
	}
	return e.target.AttributeType(name)
}

func (e *schemaEval) targetClass(name string) *schema.ObjectClass {
	if e.target == nil {
		return nil
	}
	return e.target.ObjectClass(name)
}

// available reports whether a class or attribute type named in content will be
// defined on the target, and if not, the finding that explains why.
func (e *schemaEval) available(element, name string) (bool, string) {
	if key, ok := e.sourceKey(element, name); ok {
		if out := e.outcomes[key]; out != nil {
			return out.available, out.finding
		}
	}
	return e.targetDefines(element, name), ""
}

type definitionRef struct {
	element, name, relation string
}

func definitionRefs(d *sourceDefinition) []definitionRef {
	var out []definitionRef
	switch {
	case d.at != nil:
		if d.at.SuperName != "" {
			out = append(out, definitionRef{diff.ElementAttributeType, d.at.SuperName, "SUP"})
		}
	case d.oc != nil:
		for _, sup := range d.oc.SuperNames {
			out = append(out, definitionRef{diff.ElementObjectClass, sup, "SUP"})
		}
		for _, name := range d.oc.Must {
			out = append(out, definitionRef{diff.ElementAttributeType, name, "MUST"})
		}
		for _, name := range d.oc.May {
			out = append(out, definitionRef{diff.ElementAttributeType, name, "MAY"})
		}
	}
	return out
}

func elementWord(element string) string {
	if element == diff.ElementObjectClass {
		return "object class"
	}
	return "attribute type"
}

// provenance extensions say where a definition came from, not what it does.
var provenance = map[string]bool{"x-origin": true, "x-schema-file": true}

func materialExtensions(fields []diff.FieldChange) []string {
	var out []string
	for _, f := range fields {
		if f.Category == diff.FieldExtension && !provenance[strings.ToLower(f.Field)] {
			out = append(out, f.Field)
		}
	}
	return out
}

func coreFields(fields []diff.FieldChange) []string {
	var out []string
	for _, f := range fields {
		if f.Category == diff.FieldCore {
			out = append(out, f.Field)
		}
	}
	return out
}

// blocksPlan reports whether a package validation status keeps an item out of
// what would be staged for a plan.
func blocksPlan(status string) bool {
	switch status {
	case "", "ready", "already_satisfied", "no_op":
		return false
	}
	return true
}

// definitionsOfSnapshot turns a schema snapshot's definitions into source
// definitions, marking the ones its server supplied itself.
//
// Which those are is read from what the snapshot recorded, never from the
// vendor: a server that keeps schema in collections records the collection of
// every definition an administrator loaded, and one that keeps a single
// subschema marks the ones an administrator added with X-ORIGIN 'user
// defined'. A snapshot that records neither says nothing either way, and every
// definition in it is judged.
func definitionsOfSnapshot(s *snapshot.SchemaSnapshot) []*sourceDefinition {
	mode := "none"
	switch {
	case s.Source.Collections:
		mode = "collections"
	default:
		for _, at := range s.AttributeTypes {
			if len(at.Extensions["X-ORIGIN"]) > 0 {
				mode = "origin"
				break
			}
		}
	}
	userDefined := func(ext map[string][]string, collection string) bool {
		switch mode {
		case "collections":
			return collection != ""
		case "origin":
			for _, v := range ext["X-ORIGIN"] {
				if strings.EqualFold(strings.TrimSpace(v), "user defined") {
					return true
				}
			}
			return false
		}
		return true
	}
	out := make([]*sourceDefinition, 0, len(s.AttributeTypes)+len(s.ObjectClasses))
	for i := range s.AttributeTypes {
		at := diff.AttributeTypeFor(&s.AttributeTypes[i])
		out = append(out, &sourceDefinition{element: diff.ElementAttributeType, oid: at.OID, names: at.Names, at: &at,
			serverDefined: !userDefined(s.AttributeTypes[i].Extensions, s.AttributeTypes[i].Collection)})
	}
	for i := range s.ObjectClasses {
		oc := diff.ObjectClassFor(&s.ObjectClasses[i])
		out = append(out, &sourceDefinition{element: diff.ElementObjectClass, oid: oc.OID, names: oc.Names, oc: &oc,
			serverDefined: !userDefined(s.ObjectClasses[i].Extensions, s.ObjectClasses[i].Collection)})
	}
	return out
}

// schemaAfter is the target's schema with the definitions that will be
// available added, which is what content is judged against: an entry that uses
// a class the same source adds is judged with that class, and one whose class
// cannot be added is judged without it.
func schemaAfter(target *schema.Schema, defs []*sourceDefinition, e *schemaEval) *schema.Schema {
	if target == nil {
		return nil
	}
	attrs := map[string][]string{}
	replaced := map[string]bool{}
	for _, d := range defs {
		out := e.outcomes[d.key()]
		if out == nil || !out.available || out.class == AlreadySatisfied {
			continue
		}
		text, err := definitionText(d)
		if err != nil {
			continue
		}
		replaced[d.key()] = true
		if d.element == diff.ElementObjectClass {
			attrs[schema.AttrObjectClasses] = append(attrs[schema.AttrObjectClasses], text)
		} else {
			attrs[schema.AttrAttributeTypes] = append(attrs[schema.AttrAttributeTypes], text)
		}
	}
	if len(replaced) == 0 {
		return target
	}
	for _, at := range target.AttributeTypes {
		if !replaced[diff.ElementAttributeType+":"+strings.ToLower(at.OID)] {
			attrs[schema.AttrAttributeTypes] = append(attrs[schema.AttrAttributeTypes], at.Raw)
		}
	}
	for _, oc := range target.ObjectClasses {
		if !replaced[diff.ElementObjectClass+":"+strings.ToLower(oc.OID)] {
			attrs[schema.AttrObjectClasses] = append(attrs[schema.AttrObjectClasses], oc.Raw)
		}
	}
	for _, s := range target.Syntaxes {
		attrs[schema.AttrLDAPSyntaxes] = append(attrs[schema.AttrLDAPSyntaxes], s.Raw)
	}
	for _, r := range target.MatchingRules {
		attrs[schema.AttrMatchingRules] = append(attrs[schema.AttrMatchingRules], r.Raw)
	}
	for _, u := range target.MatchingRuleUses {
		attrs[schema.AttrMatchingRuleUse] = append(attrs[schema.AttrMatchingRuleUse], u.Raw)
	}
	for _, c := range target.DITContentRules {
		attrs[schema.AttrDITContentRules] = append(attrs[schema.AttrDITContentRules], c.Raw)
	}
	for _, f := range target.NameForms {
		attrs[schema.AttrNameForms] = append(attrs[schema.AttrNameForms], f.Raw)
	}
	return schema.Load(target.DN, attrs)
}

func definitionText(d *sourceDefinition) (string, error) {
	if d.oc != nil {
		return d.oc.Definition()
	}
	return d.at.Definition()
}
