package preflight

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/hazame-hub/alder/internal/diff"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/plan"
	"github.com/hazame-hub/alder/internal/schema"
)

// Judging entry content against a target.
//
// An entry is checked against the schema the target would have once the
// source's own definitions that can be carried across are in place -- never
// against a schema that pretends the rest are there too. The rules are the
// planner's, called as a pure function: nothing is planned, and no baseline is
// issued to find out.

// Syntaxes whose values name another entry.
const (
	syntaxDN                 = "1.3.6.1.4.1.1466.115.121.1.12"
	syntaxNameAndOptionalUID = "1.3.6.1.4.1.1466.115.121.1.34"
)

// Bounds on reference checking.
const (
	// maxReferenceReads bounds the reads spent resolving references one
	// preflight makes. Past it, an unresolved reference is unknown.
	maxReferenceReads = 2000
	// maxValueFindings bounds per-value reference findings for one attribute of
	// one entry; the rest are counted in one finding.
	maxValueFindings = 50
)

// contentAttribute is one attribute of source content.
type contentAttribute struct {
	name   string
	values [][]byte
	// withheld is how many values a snapshot recorded without their content.
	withheld int
	// operational is what the source recorded about the attribute.
	operational bool
}

// entryInput is one entry the source carries.
type entryInput struct {
	dn     string
	parsed dn.DN
	valid  bool
	attrs  []contentAttribute
	// item and validation identify a change package item.
	item       string
	validation string
	// mode is snapshot or package: what a server-owned attribute means differs.
	mode string
}

func (in *entryInput) ref() SourceRef { return SourceRef{Item: in.item, DN: in.dn} }

// entryCheck holds what content checks share.
type entryCheck struct {
	b        *builder
	caps     directory.Capabilities
	after    *schema.Schema
	eval     *schemaEval
	presence *presence
	// provided maps a folded DN the source supplies to the key of the finding
	// about that entry, and outcome records how that entry was judged.
	provided map[string]string
	outcome  map[string]Classification
	// targetDNs holds DNs already known to be on the target, from a capture.
	targetDNs map[string]bool
	// aggregates collects per-attribute findings across entries.
	aggregates map[string]*Finding
	refReads   int
}

func newEntryCheck(b *builder, caps directory.Capabilities, after *schema.Schema, eval *schemaEval, p *presence) *entryCheck {
	return &entryCheck{b: b, caps: caps, after: after, eval: eval, presence: p, provided: map[string]string{},
		outcome: map[string]Classification{}, aggregates: map[string]*Finding{}}
}

func foldDN(d dn.DN) string { return strings.ToLower(d.String()) }

// worst ranks the classifications that stop something.
func worse(a, b Classification) Classification {
	rank := map[Classification]int{"": 0, Portable: 1, AlreadySatisfied: 1, Excluded: 1, PrerequisiteRequired: 2, Unknown: 3, Unsupported: 4, Incompatible: 5}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// naming checks that a DN is one the target could hold, and returns the
// classification that stops the entry, if any, with the finding explaining it.
func (c *entryCheck) naming(in *entryInput) (Classification, string) {
	if !in.valid {
		return Incompatible, c.b.add(Finding{ID: "naming:" + in.item + ":" + in.dn + ":invalid", Code: CodeInvalidDN, Classification: Incompatible,
			Category: CategoryNaming, Scope: ScopeEntry, Source: in.ref(), BlocksPortability: true, BlocksPlan: true,
			ValidationStatus: in.validation, Explanation: "This is not a valid DN."})
	}
	inside, known := inNamingContexts(c.caps, in.parsed)
	if !known {
		c.b.incomplete("naming_contexts_unknown")
		return "", ""
	}
	if inside {
		return "", ""
	}
	return Incompatible, c.b.add(Finding{ID: "naming:" + in.item + ":" + in.dn + ":context", Code: CodeNamingContextMismatch,
		Classification: Incompatible, Category: CategoryNaming, Scope: ScopeEntry, Source: in.ref(), BlocksPortability: true, BlocksPlan: true,
		ValidationStatus: in.validation,
		Target:           &TargetFact{Fact: "naming_contexts", Detail: strings.Join(c.caps.NamingContexts, "; ")},
		Explanation: "This DN is outside every naming context the target holds (" + strings.Join(c.caps.NamingContexts, "; ") +
			"). It is not rewritten to fit: a DN that should move to another suffix has to be changed in the source, deliberately."})
}

// parent checks that an absent entry's parent is there, or is supplied by the
// source.
func (c *entryCheck) parent(ctx context.Context, in *entryInput) (Classification, string) {
	if len(in.parsed) < 2 {
		return "", ""
	}
	for _, context := range c.caps.NamingContexts {
		if suffix, err := dn.Parse(context); err == nil && in.parsed.Equal(suffix) {
			return "", ""
		}
	}
	parent := in.parsed.Parent()
	key := foldDN(parent)
	id := "naming:" + in.item + ":" + in.dn + ":parent"
	if provider, ok := c.provided[key]; ok {
		class := c.outcome[key]
		switch class {
		case Portable, AlreadySatisfied:
			return "", ""
		}
		return worse(PrerequisiteRequired, class), c.b.add(Finding{ID: id, Code: CodeDependencyBlocked, Classification: worse(PrerequisiteRequired, class),
			Category: CategoryNaming, Scope: ScopeEntry, Source: in.ref(), BlocksPortability: true, ValidationStatus: in.validation,
			Causes:        []string{provider},
			Prerequisites: []Prerequisite{{Type: "entry", DN: parent.String(), ProvidedBy: provider}},
			Explanation:   "Its parent " + parent.String() + " is supplied by this source, and cannot itself be carried across."})
	}
	if c.targetDNs[key] {
		return "", ""
	}
	switch c.presence.of(ctx, parent).state {
	case PresencePresent:
		return "", ""
	case PresenceAbsent:
		return PrerequisiteRequired, c.b.add(Finding{ID: id, Code: CodeParentMissing, Classification: PrerequisiteRequired,
			Category: CategoryNaming, Scope: ScopeEntry, Source: in.ref(), BlocksPortability: true, BlocksPlan: true, ManualAction: true,
			ValidationStatus: in.validation, Target: &TargetFact{Fact: "absent", Detail: parent.String()},
			Prerequisites: []Prerequisite{{Type: "entry", DN: parent.String()}},
			Explanation:   "Its parent " + parent.String() + " is not on the target, as the server reports it to this bind, and this source does not supply it."})
	case PresenceHidden:
		return Unknown, c.b.add(Finding{ID: id, Code: CodeParentUnknown, Classification: Unknown,
			Category: CategoryNaming, Scope: ScopeEntry, Source: in.ref(), ValidationStatus: in.validation,
			Target:      &TargetFact{Fact: "hidden", Detail: parent.String()},
			Explanation: "Its parent " + parent.String() + " exists on the target and this bind may not read it, so whether the entry could be placed there is not known."})
	}
	return Unknown, c.b.add(Finding{ID: id, Code: CodeParentUnknown, Classification: Unknown,
		Category: CategoryNaming, Scope: ScopeEntry, Source: in.ref(), ValidationStatus: in.validation,
		Target:      &TargetFact{Fact: "unreadable", Detail: parent.String()},
		Explanation: "Whether its parent " + parent.String() + " is on the target could not be read."})
}

// content checks an entry's attributes and object classes against the schema
// it would meet, and notes what does not travel. It returns the classification
// that stops the entry, if any, and the findings explaining it.
func (c *entryCheck) content(in *entryInput, live *directory.Entry, op directory.ChangeType, mods []directory.Mod) (Classification, []string) {
	var worst Classification
	var causes []string
	block := func(class Classification, id string) {
		if id == "" {
			return
		}
		worst = worse(worst, class)
		causes = append(causes, id)
	}

	record := directory.ChangeRecord{DN: in.parsed, Type: op, Mods: mods}
	for _, a := range in.attrs {
		base := schema.BaseName(a.name)
		if c.sensitive(in, a) {
			if len(a.values) > 0 || a.withheld > 0 {
				record.Attrs = append(record.Attrs, directory.Attribute{Name: a.name, Values: make([][]byte, max(len(a.values), a.withheld))})
			}
			continue
		}
		at := c.attribute(base)
		if skip := c.operational(in, a, at); skip {
			continue
		}
		if at == nil {
			available, why := c.eval.available(diff.ElementAttributeType, base)
			if !available {
				block(c.missingDefinition(in, diff.ElementAttributeType, base, why))
				continue
			}
		}
		if strings.EqualFold(base, "objectClass") {
			for _, v := range a.values {
				name := string(v)
				if c.after != nil && c.after.ObjectClass(name) != nil {
					continue
				}
				available, why := c.eval.available(diff.ElementObjectClass, name)
				if !available {
					block(c.missingDefinition(in, diff.ElementObjectClass, name, why))
				}
			}
		}
		record.Attrs = append(record.Attrs, directory.Attribute{Name: a.name, Values: a.values})
	}
	if worst != "" || c.after == nil {
		return worst, causes
	}
	if op != directory.ChangeAdd {
		record.Attrs = nil
	}
	if problem := plan.SchemaProblem(c.after, record, live); problem != nil {
		code, explanation := CodeAttributeNotAllowed, ""
		switch problem.Code {
		case plan.ProblemAttributeNotPermitted:
			explanation = "No object class this entry would carry on the target permits " + problem.Attribute + "."
		case plan.ProblemSingleValue:
			code = CodeSingleValueViolation
			explanation = problem.Attribute + " is single-valued on the target, and this entry holds more than one value."
		case plan.ProblemMissingRequired:
			code = CodeMissingRequired
			explanation = "The entry's object classes require " + problem.Attribute + " on the target, and the entry does not hold it."
		case plan.ProblemObjectClassUndefined:
			code = CodeObjectClassMissing
			explanation = "The target does not define the object class " + problem.Attribute + "."
		case plan.ProblemAttributeUndefined:
			code = CodeAttributeMissing
			explanation = "The target does not define the attribute " + problem.Attribute + "."
		default:
			explanation = "The target's schema does not accept this entry: " + string(problem.Code) + " " + problem.Attribute + "."
		}
		block(Incompatible, c.b.add(Finding{ID: "entry:" + in.item + ":" + in.dn + ":schema", Code: code, Classification: Incompatible,
			Category: CategoryEntries, Scope: ScopeAttribute, Source: SourceRef{Item: in.item, DN: in.dn, Attribute: problem.Attribute},
			BlocksPortability: true, BlocksPlan: true, ValidationStatus: in.validation,
			Target:      &TargetFact{Fact: string(problem.Code), Detail: problem.Attribute},
			Explanation: explanation}))
	}
	return worst, causes
}

func (c *entryCheck) attribute(name string) *schema.AttributeType {
	if c.after == nil {
		return nil
	}
	return c.after.AttributeType(name)
}

// missingDefinition reports content that names a class or attribute type the
// target will not have.
func (c *entryCheck) missingDefinition(in *entryInput, element, name, why string) (Classification, string) {
	code := CodeAttributeMissing
	if element == diff.ElementObjectClass {
		code = CodeObjectClassMissing
	}
	f := Finding{ID: "entry:" + in.item + ":" + in.dn + ":" + element + ":" + strings.ToLower(name), Code: code,
		Category: CategoryEntries, Scope: ScopeAttribute, Source: SourceRef{Item: in.item, DN: in.dn, Attribute: name},
		BlocksPortability: true, BlocksPlan: true, ValidationStatus: in.validation,
		Prerequisites: []Prerequisite{{Type: "schema", Element: element, Name: name, ProvidedBy: why}}}
	if why != "" {
		out := c.eval.outcomes[c.eval.names[element][strings.ToLower(name)]]
		f.Classification = PrerequisiteRequired
		if out != nil {
			f.Classification = worse(PrerequisiteRequired, out.class)
		}
		f.Code = CodeDependencyBlocked
		f.Causes = []string{why}
		f.Explanation = "It uses the " + elementWord(element) + " " + name + ", which this source supplies and which cannot be carried to this target."
		return f.Classification, c.b.add(f)
	}
	f.Classification = PrerequisiteRequired
	f.ManualAction = true
	f.Target = &TargetFact{Fact: "absent", Detail: name}
	f.Explanation = "It uses the " + elementWord(element) + " " + name + ", which the target does not define and this source does not supply."
	return PrerequisiteRequired, c.b.add(f)
}

// notes records what does not travel in an entry that is not otherwise
// checked -- one the target already holds -- so a report about data that is
// already there still says which of its values were never compared.
func (c *entryCheck) notes(in *entryInput) {
	for _, a := range in.attrs {
		if c.sensitive(in, a) {
			continue
		}
		c.operational(in, a, c.attribute(schema.BaseName(a.name)))
	}
}

// sensitive notes a value that cannot travel. It reports whether the attribute
// is one.
func (c *entryCheck) sensitive(in *entryInput, a contentAttribute) bool {
	if a.withheld == 0 && !schema.IsSensitive(a.name) {
		return false
	}
	if a.withheld == 0 && len(a.values) == 0 {
		return true
	}
	c.aggregate(Finding{Code: CodeSensitiveNotMigratable, Classification: Excluded, Category: CategorySensitive, Scope: ScopeAttribute,
		Source: SourceRef{Attribute: schema.BaseName(a.name)}, ManualAction: true,
		Explanation: "Values of " + schema.BaseName(a.name) + " are never carried in an artifact, so they cannot be migrated. Set them on the target separately; Alder does not compare, copy or invent them."})
	c.b.require("password_modify", c.caps.PasswordModify, "withheld "+schema.BaseName(a.name)+" values",
		"Secrets are set on the target separately. Where the target offers the password modify extended operation, Alder sets passwords with it.")
	return true
}

// operational notes an attribute the target generates or owns, and reports
// whether it should be left out of the content check.
func (c *entryCheck) operational(in *entryInput, a contentAttribute, at *schema.AttributeType) bool {
	base := schema.BaseName(a.name)
	serverOwned := at != nil && c.after != nil && c.after.EffectiveNoUserModification(at)
	operational := a.operational || (at != nil && c.after != nil && c.after.EffectiveUsage(at).Operational())
	switch {
	case serverOwned && in.mode == "package":
		c.aggregate(Finding{Code: CodeServerOwned, Classification: Unsupported, Category: CategoryOperational, Scope: ScopeAttribute,
			Source: SourceRef{Attribute: base}, BlocksPortability: true, BlocksPlan: true,
			Target:      &TargetFact{Fact: "no_user_modification"},
			Explanation: base + " is maintained by the target server itself and cannot be written."})
		return true
	case serverOwned || operational:
		code := CodeIgnoredOperational
		if serverOwned {
			code = CodeServerOwned
		}
		c.aggregate(Finding{Code: code, Classification: Excluded, Category: CategoryOperational, Scope: ScopeAttribute,
			Source:      SourceRef{Attribute: base},
			Explanation: base + " is operational: servers generate or maintain it, and its source values are not migrated. The target keeps its own, so a migrated entry is not byte-identical to its source."})
		return true
	}
	return false
}

// aggregate folds per-entry findings about one attribute into one, counted.
func (c *entryCheck) aggregate(f Finding) {
	key := f.Code + ":" + strings.ToLower(f.Source.Attribute) + ":" + string(f.Classification)
	if existing := c.aggregates[key]; existing != nil {
		existing.Count++
		return
	}
	f.Count = 1
	f.ID = "aggregate:" + key
	c.aggregates[key] = &f
}

// flush adds the aggregated findings to the report.
func (c *entryCheck) flush() {
	keys := make([]string, 0, len(c.aggregates))
	for k := range c.aggregates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f := *c.aggregates[k]
		if f.Count > 1 {
			f.Explanation = f.Explanation + " (" + strconv.Itoa(f.Count) + " occurrences)"
		}
		c.b.add(f)
	}
}

// references checks DN-valued attributes: whether what they name is on the
// target, supplied by the source, outside the target, or cannot be seen.
func (c *entryCheck) references(ctx context.Context, in *entryInput) {
	if c.after == nil || !in.valid {
		return
	}
	for _, a := range in.attrs {
		at := c.after.AttributeType(schema.BaseName(a.name))
		if at == nil || len(a.values) == 0 {
			continue
		}
		syntax := c.after.EffectiveSyntax(at)
		if syntax != syntaxDN && syntax != syntaxNameAndOptionalUID {
			continue
		}
		if c.after.EffectiveUsage(at).Operational() {
			continue
		}
		c.referenceValues(ctx, in, at.Name(), syntax, a.values)
	}
}

func (c *entryCheck) referenceValues(ctx context.Context, in *entryInput, attribute, syntax string, values [][]byte) {
	ready, provided, overflow := 0, 0, 0
	emitted := 0
	valueFinding := func(f Finding) {
		if emitted >= maxValueFindings {
			overflow++
			return
		}
		emitted++
		c.b.add(f)
	}
	for _, raw := range values {
		text := string(raw)
		if syntax == syntaxNameAndOptionalUID {
			text = stripOptionalUID(text)
		}
		ref := SourceRef{Item: in.item, DN: in.dn, Attribute: attribute, Value: text}
		id := "reference:" + in.item + ":" + in.dn + ":" + strings.ToLower(attribute) + ":" + strings.ToLower(text)
		target, err := dn.Parse(text)
		if err != nil {
			valueFinding(Finding{ID: id, Code: CodeInvalidDN, Classification: Incompatible, Category: CategoryReferences, Scope: ScopeValue,
				Source: ref, BlocksPortability: true, BlocksPlan: true, ValidationStatus: in.validation,
				Explanation: attribute + " holds DNs, and this value is not one."})
			continue
		}
		key := foldDN(target)
		if provider, ok := c.provided[key]; ok {
			switch class := c.outcome[key]; class {
			case Portable, AlreadySatisfied:
				provided++
			default:
				valueFinding(Finding{ID: id, Code: CodeDependencyBlocked, Classification: worse(PrerequisiteRequired, class),
					Category: CategoryReferences, Scope: ScopeValue, Source: ref, BlocksPortability: true, ValidationStatus: in.validation,
					Causes: []string{provider}, Prerequisites: []Prerequisite{{Type: "entry", DN: target.String(), ProvidedBy: provider}},
					Explanation: "It names " + target.String() + ", which this source supplies and which cannot itself be carried across."})
			}
			continue
		}
		if inside, known := inNamingContexts(c.caps, target); known && !inside {
			valueFinding(Finding{ID: id, Code: CodeReferenceOutside, Classification: Incompatible, Category: CategoryReferences, Scope: ScopeValue,
				Source: ref, BlocksPortability: true, ValidationStatus: in.validation,
				Target:      &TargetFact{Fact: "naming_contexts", Detail: strings.Join(c.caps.NamingContexts, "; ")},
				Explanation: "It names " + target.String() + ", which is outside every naming context on the target. The reference is not rewritten."})
			continue
		}
		if c.targetDNs[key] {
			ready++
			continue
		}
		if _, cached := c.presence.cache[key]; !cached {
			if c.refReads >= maxReferenceReads {
				c.b.incomplete("reference_read_budget")
				valueFinding(Finding{ID: id, Code: CodeReferenceUnknown, Classification: Unknown, Category: CategoryReferences, Scope: ScopeValue,
					Source: ref, ValidationStatus: in.validation, Target: &TargetFact{Fact: "not_checked", Detail: "read budget spent"},
					Explanation: "It names " + target.String() + ", and this preflight had already spent its budget of reads on references."})
				continue
			}
			c.refReads++
		}
		switch c.presence.of(ctx, target).state {
		case PresencePresent:
			ready++
		case PresenceAbsent:
			valueFinding(Finding{ID: id, Code: CodeReferenceMissing, Classification: PrerequisiteRequired, Category: CategoryReferences,
				Scope: ScopeValue, Source: ref, BlocksPortability: true, ManualAction: true, ValidationStatus: in.validation,
				Target:        &TargetFact{Fact: "absent", Detail: target.String()},
				Prerequisites: []Prerequisite{{Type: "entry", DN: target.String()}},
				Explanation:   "It names " + target.String() + ", which is not on the target as the server reports it to this bind, and which this source does not supply."})
		case PresenceHidden:
			valueFinding(Finding{ID: id, Code: CodeReferenceUnknown, Classification: Unknown, Category: CategoryReferences, Scope: ScopeValue,
				Source: ref, ValidationStatus: in.validation, Target: &TargetFact{Fact: "hidden", Detail: target.String()},
				Explanation: "It names " + target.String() + ", which exists on the target and which this bind may not read. That it cannot be seen does not mean it is missing."})
		default:
			valueFinding(Finding{ID: id, Code: CodeReferenceUnknown, Classification: Unknown, Category: CategoryReferences, Scope: ScopeValue,
				Source: ref, ValidationStatus: in.validation, Target: &TargetFact{Fact: "unreadable", Detail: target.String()},
				Explanation: "It names " + target.String() + ", and whether that is on the target could not be read."})
		}
	}
	base := "reference:" + in.item + ":" + in.dn + ":" + strings.ToLower(attribute)
	if ready > 0 {
		c.b.add(Finding{ID: base + ":ready", Code: CodeReferenceReady, Classification: Portable, Category: CategoryReferences, Scope: ScopeAttribute,
			Source: SourceRef{Item: in.item, DN: in.dn, Attribute: attribute}, Count: ready, ValidationStatus: in.validation,
			Target:      &TargetFact{Fact: "present"},
			Explanation: strconv.Itoa(ready) + " value(s) of " + attribute + " name entries the target holds."})
	}
	if provided > 0 {
		c.b.add(Finding{ID: base + ":provided", Code: CodeReferenceProvided, Classification: Portable, Category: CategoryReferences, Scope: ScopeAttribute,
			Source: SourceRef{Item: in.item, DN: in.dn, Attribute: attribute}, Count: provided, ValidationStatus: in.validation,
			Explanation: strconv.Itoa(provided) + " value(s) of " + attribute + " name entries this source supplies."})
	}
	if overflow > 0 {
		c.b.incomplete("reference_findings_summarised")
		c.b.add(Finding{ID: base + ":more", Code: CodeReferenceUnknown, Classification: Unknown, Category: CategoryReferences, Scope: ScopeAttribute,
			Source: SourceRef{Item: in.item, DN: in.dn, Attribute: attribute}, Count: overflow, ValidationStatus: in.validation,
			Explanation: strconv.Itoa(overflow) + " more value(s) of " + attribute + " were not reported one by one."})
	}
}

// stripOptionalUID removes the #'...'B unique identifier a Name and Optional
// UID value may carry.
func stripOptionalUID(v string) string {
	if i := strings.LastIndex(v, "#'"); i > 0 && strings.HasSuffix(v, "'B") {
		return v[:i]
	}
	return v
}
