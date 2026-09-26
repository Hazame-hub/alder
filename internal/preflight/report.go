// Package preflight answers one question about a source artifact and one live
// target: what of this would carry across, and what would not?
//
// It is analysis, and only analysis. A preflight reads the target, never writes
// to it, never transforms the artifact it was given, and produces nothing an
// apply could use: no plan, no token, no prepared change. The operator decides
// what to do with a report, and whatever they decide goes through the ordinary
// path -- a schema comparison, a change package, a plan.
//
// Three rules keep a report honest:
//
//   - Compatibility is semantic. Two servers from different vendors are not
//     incompatible for that reason; a definition that means something else, a
//     syntax the target does not publish, an entry its schema refuses -- those
//     are. A vendor name never decides a finding.
//   - What cannot be seen is unknown, not missing. A hidden parent or reference
//     is reported as unknown, and an unknown that matters keeps the whole report
//     from reading as compatible.
//   - Nothing is rewritten. A DN outside the target's naming contexts is
//     reported, never mapped; an object class with a similar name is a
//     different object class.
package preflight

import (
	"fmt"
	"github.com/hazame-hub/alder/internal/signing"
	"sort"
	"strings"
	"unicode/utf8"
)

// ReportVersion is the version of the report document this build produces.
const ReportVersion = 1

// Classification is what a finding concludes about one source object.
type Classification string

const (
	// Portable: the target can take it as it is, once any prerequisite the
	// same source supplies is in place.
	Portable Classification = "portable"
	// AlreadySatisfied: the target already holds it, with the same meaning.
	AlreadySatisfied Classification = "already_satisfied"
	// PrerequisiteRequired: it can carry across once something else is done
	// first, and the finding says what.
	PrerequisiteRequired Classification = "prerequisite_required"
	// Incompatible: the target holds something that contradicts it.
	Incompatible Classification = "incompatible"
	// Unsupported: the target cannot represent it at all.
	Unsupported Classification = "unsupported"
	// Unknown: it could not be decided from what this bind can see.
	Unknown Classification = "unknown"
	// Excluded: it does not travel by design -- a server-generated value, a
	// withheld secret -- and does not by itself stop anything else.
	Excluded Classification = "excluded"
)

// Classifications lists every classification, in report order.
var Classifications = []Classification{Portable, AlreadySatisfied, PrerequisiteRequired, Incompatible, Unsupported, Unknown, Excluded}

// Overall is the report's conclusion, derived from its findings and never set
// on its own.
type Overall string

const (
	Compatible                 Overall = "compatible"
	CompatibleWithPrerequisite Overall = "compatible_with_prerequisites"
	NotCompatible              Overall = "incompatible"
	Incomplete                 Overall = "incomplete"
)

// Category is the report section a finding belongs to.
type Category string

const (
	CategoryArtifact     Category = "artifact"
	CategorySchema       Category = "schema"
	CategoryNaming       Category = "naming"
	CategoryEntries      Category = "entries"
	CategoryReferences   Category = "references"
	CategoryCapabilities Category = "capabilities"
	CategoryOperational  Category = "operational"
	CategorySensitive    Category = "sensitive"
)

// Categories lists the sections in report order.
var Categories = []Category{CategoryArtifact, CategorySchema, CategoryNaming, CategoryEntries,
	CategoryReferences, CategoryConfiguration, CategoryCapabilities, CategoryOperational, CategorySensitive}

// Finding codes. Stable identifiers: a client switches on these, never on the
// explanation.
const (
	// Schema.
	CodeDefinitionPortable          = "definition_portable"
	CodeDefinitionPresent           = "definition_present"
	CodeDefinitionExtensionsDiffer  = "definition_extensions_differ"
	CodeDefinitionDescriptionDiffer = "definition_description_differs"
	CodeDefinitionConflict          = "definition_conflict"
	CodeNameConflict                = "name_conflict"
	CodeDefinitionUnknown           = "definition_unknown"
	CodeRequiresDefinition          = "requires_definition"
	CodeUndefinedReference          = "undefined_reference"
	CodeSyntaxUnavailable           = "syntax_unavailable"
	CodeSyntaxUnknown               = "syntax_unknown"
	CodeMatchingRuleUnavailable     = "matching_rule_unavailable"
	CodeMatchingRuleUnknown         = "matching_rule_unknown"
	CodeSchemaNotWritable           = "schema_not_writable"
	CodeSchemaTargetRequired        = "schema_target_required"
	CodeSourceServerDefined         = "source_server_defined"
	CodeDependencyBlocked           = "dependency_blocked"
	CodeDependencyCycle             = "dependency_cycle"

	// Naming.
	CodeNamingContextMismatch = "naming_context_mismatch"
	CodeInvalidDN             = "invalid_dn"
	CodeParentPresent         = "parent_present"
	CodeParentProvided        = "parent_provided"
	CodeParentMissing         = "parent_missing"
	CodeParentUnknown         = "parent_unknown"

	// Entries.
	CodeEntryPortable        = "entry_portable"
	CodeEntryPresent         = "entry_present_equivalent"
	CodeEntryDiffers         = "existing_entry_differs"
	CodeEntryUnknown         = "entry_unknown"
	CodeEntryBlocked         = "entry_blocked"
	CodeObjectClassMissing   = "object_class_missing"
	CodeAttributeMissing     = "attribute_type_missing"
	CodeAttributeNotAllowed  = "attribute_not_allowed"
	CodeMissingRequired      = "missing_required_attribute"
	CodeSingleValueViolation = "single_value_violation"
	CodeChangeNotApplicable  = "change_not_applicable"
	CodeChangeSatisfied      = "change_already_satisfied"
	CodeChangePortable       = "change_portable"

	// References.
	CodeReferenceReady    = "reference_ready"
	CodeReferenceProvided = "reference_provided_by_source"
	CodeReferenceMissing  = "reference_missing"
	CodeReferenceUnknown  = "reference_unknown"
	CodeReferenceOutside  = "reference_outside_naming_contexts"

	// Capabilities.
	CodeCapabilityUnavailable = "capability_unavailable"

	// Operational.
	CodeIgnoredOperational = "ignored_operational"
	CodeTargetGenerated    = "target_generated"
	CodeServerOwned        = "server_owned_attribute"

	// Sensitive.
	CodeSensitiveNotMigratable = "sensitive_value_not_migratable"

	// Artifact.
	CodeReportTruncated = "report_truncated"
	CodeSourcePartial   = "source_partial"
	CodeSourceEmpty     = "source_empty"
)

// Limits on what one report holds. The source artifact is untrusted; the report
// is bounded however large or hostile it is.
const (
	MaxFindings        = 100000
	MaxCauses          = 16
	MaxPrerequisites   = 16
	MaxExplanationRune = 1024
	MaxFactRunes       = 512
	// MaxCauseDepth bounds how far a dependency chain is followed when causes
	// are attached.
	MaxCauseDepth = 32
)

// Scope is what a finding is about.
type Scope string

const (
	ScopeArtifact   Scope = "artifact"
	ScopeItem       Scope = "item"
	ScopeDefinition Scope = "definition"
	ScopeEntry      Scope = "entry"
	ScopeAttribute  Scope = "attribute"
	ScopeValue      Scope = "value"
)

// SourceRef names the source object a finding is about. Every field is
// optional; which are set depends on the scope.
type SourceRef struct {
	// Item is a change package item id.
	Item string `json:"item,omitempty"`
	// Element is attributeType or objectClass.
	Element   string `json:"element,omitempty"`
	OID       string `json:"oid,omitempty"`
	Name      string `json:"name,omitempty"`
	DN        string `json:"dn,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	Value     string `json:"value,omitempty"`
	// Setting and Resource identify a configuration setting and the object it
	// belongs to, in the snapshot's own terms (1.14).
	Setting  string `json:"setting,omitempty"`
	Resource string `json:"resource,omitempty"`
}

// TargetFact is what the target showed that the finding rests on.
type TargetFact struct {
	// Fact is a stable identifier: absent, present, defined_differently,
	// not_published, hidden, and so on.
	Fact   string `json:"fact"`
	Detail string `json:"detail,omitempty"`
}

// Prerequisite is something that has to be true first, as data rather than
// advice.
type Prerequisite struct {
	// Type is schema, entry or capability.
	Type string `json:"type"`
	// Element, OID and Name identify a schema definition.
	Element string `json:"element,omitempty"`
	OID     string `json:"oid,omitempty"`
	Name    string `json:"name,omitempty"`
	// DN identifies an entry.
	DN string `json:"dn,omitempty"`
	// Capability names a target capability.
	Capability string `json:"capability,omitempty"`
	// Setting and Resource name a configuration setting or object (1.14).
	Setting  string `json:"setting,omitempty"`
	Resource string `json:"resource,omitempty"`
	// ProvidedBy is the finding of the source object that supplies it, when the
	// same source does.
	ProvidedBy string `json:"providedBy,omitempty"`
}

// Finding is one conclusion about one source object.
type Finding struct {
	ID             string         `json:"id"`
	Code           string         `json:"code"`
	Classification Classification `json:"classification"`
	Category       Category       `json:"category"`
	Scope          Scope          `json:"scope"`
	Source         SourceRef      `json:"source"`
	Target         *TargetFact    `json:"target,omitempty"`
	Explanation    string         `json:"explanation"`
	// BlocksPortability: while this stands, the object it is about cannot be
	// carried to this target.
	BlocksPortability bool `json:"blocksPortability"`
	// BlocksPlan: a plan made now for the change this concerns would refuse it.
	BlocksPlan bool `json:"blocksPlan"`
	// ManualAction: an operator has something to do -- set a password, add a
	// definition, choose a schema entry -- that Alder will not do for them.
	ManualAction  bool           `json:"manualAction"`
	Prerequisites []Prerequisite `json:"prerequisites,omitempty"`
	// Causes are the ids of findings that explain this one: an entry blocked
	// because its class is blocked because its attribute type is.
	Causes []string `json:"causes,omitempty"`
	// Count is how many source objects an aggregated finding stands for.
	Count int `json:"count,omitempty"`
	// ValidationStatus is what change package validation concluded about the
	// item this finding concerns. Preflight reuses that answer rather than
	// deciding it again, and explains it.
	ValidationStatus string `json:"validationStatus,omitempty"`
}

// Counts tallies findings by classification.
type Counts struct {
	Portable             int `json:"portable"`
	AlreadySatisfied     int `json:"alreadySatisfied"`
	PrerequisiteRequired int `json:"prerequisiteRequired"`
	Incompatible         int `json:"incompatible"`
	Unsupported          int `json:"unsupported"`
	Unknown              int `json:"unknown"`
	Excluded             int `json:"excluded"`
}

func (c *Counts) add(class Classification) {
	switch class {
	case Portable:
		c.Portable++
	case AlreadySatisfied:
		c.AlreadySatisfied++
	case PrerequisiteRequired:
		c.PrerequisiteRequired++
	case Incompatible:
		c.Incompatible++
	case Unsupported:
		c.Unsupported++
	case Unknown:
		c.Unknown++
	case Excluded:
		c.Excluded++
	}
}

// Section is one category's tally.
type Section struct {
	Category Category `json:"category"`
	Counts   Counts   `json:"counts"`
}

// SourceInfo describes the artifact the report is about.
type SourceInfo struct {
	// Type is change_package, schema_snapshot or data_snapshot.
	Type    string `json:"type"`
	Format  string `json:"format"`
	Version int    `json:"version"`
	Kind    string `json:"kind,omitempty"`
	// ID is a change package's identity.
	ID       string `json:"id,omitempty"`
	Title    string `json:"title,omitempty"`
	Checksum string `json:"checksum,omitempty"`
	// Integrity is verified when the artifact carried a checksum that matched,
	// unverified when it carried none.
	Integrity     string `json:"integrity"`
	Vendor        string `json:"vendor,omitempty"`
	VendorVersion string `json:"vendorVersion,omitempty"`
	// Objects is how many source objects the artifact holds: changes,
	// definitions or entries.
	Objects int `json:"objects"`
	// Signature is what the server concluded about the artifact's signature,
	// when it was given a signed one (1.15). A preflight decides nothing from
	// it: whoever runs the report reads it.
	Signature *signing.Result `json:"signature,omitempty"`
}

// TargetInfo describes the target. Vendor is display only.
type TargetInfo struct {
	Vendor         string   `json:"vendor,omitempty"`
	VendorVersion  string   `json:"vendorVersion,omitempty"`
	NamingContexts []string `json:"namingContexts"`
	SchemaEntry    string   `json:"schemaEntry,omitempty"`
	SchemaWritable bool     `json:"schemaWritable"`
	// CrossVendor reports that source and target name different products, or
	// that one names none. It is metadata: no finding follows from it.
	CrossVendor bool `json:"crossVendor"`
}

// CapabilityRequirement is a target capability the source needs, and whether
// the target has it. Capabilities the source does not need are not listed.
type CapabilityRequirement struct {
	Capability  string `json:"capability"`
	Available   bool   `json:"available"`
	RequiredBy  string `json:"requiredBy"`
	Explanation string `json:"explanation"`
}

// NotEvaluated is an area a preflight does not assess.
type NotEvaluated struct {
	Area   string `json:"area"`
	Reason string `json:"reason"`
}

// Report is a whole preflight.
type Report struct {
	ReportVersion int        `json:"reportVersion"`
	CreatedAt     string     `json:"createdAt"`
	Source        SourceInfo `json:"source"`
	Target        TargetInfo `json:"target"`
	Overall       Overall    `json:"overall"`
	// Complete is false when some of what the report covers could not be
	// decided or the report was truncated. Reasons says why.
	Complete     bool                    `json:"complete"`
	Reasons      []string                `json:"reasons"`
	Counts       Counts                  `json:"counts"`
	Sections     []Section               `json:"sections"`
	Capabilities []CapabilityRequirement `json:"capabilities"`
	NotEvaluated []NotEvaluated          `json:"notEvaluated"`
	Findings     []Finding               `json:"findings"`
	// Truncated reports that findings past MaxFindings were dropped.
	Truncated bool `json:"truncated"`
}

// Areas never assessed, always listed.
var alwaysNotEvaluated = []NotEvaluated{
	{Area: "access_control", Reason: "Access control is read and shown against an entry (1.19) and never compared: " +
		"aci values and olcAccess rules are different mechanisms, and Alder neither translates nor evaluates them."},
	{Area: "server_configuration", Reason: "This artifact carries no server configuration, so settings the source relies on -- overlays, plugins, password policy, limits, indexes -- are not evaluated. A configuration snapshot of the same server software can be preflighted on its own."},
	{Area: "secret_values", Reason: "Passwords and other sensitive values are never read into an artifact, so whether they would carry across cannot be assessed; they must be set on the target separately."},
}

// builder accumulates findings and turns them into a report.
type builder struct {
	report   *Report
	reasons  map[string]bool
	required map[string]*CapabilityRequirement
}

func newBuilder(source SourceInfo, target TargetInfo, createdAt string) *builder {
	return &builder{
		report: &Report{ReportVersion: ReportVersion, CreatedAt: createdAt, Source: source, Target: target,
			Findings: []Finding{}, Reasons: []string{}, Capabilities: []CapabilityRequirement{},
			NotEvaluated: append([]NotEvaluated(nil), alwaysNotEvaluated...)},
		reasons: map[string]bool{}, required: map[string]*CapabilityRequirement{},
	}
}

// add records a finding and returns its id. A finding past the limit is
// dropped, and the report says it was truncated.
func (b *builder) add(f Finding) string {
	if len(b.report.Findings) >= MaxFindings {
		b.report.Truncated = true
		return ""
	}
	f.Explanation = bound(f.Explanation, MaxExplanationRune)
	if f.Target != nil {
		f.Target.Detail = bound(f.Target.Detail, MaxFactRunes)
	}
	if len(f.Causes) > MaxCauses {
		f.Causes = f.Causes[:MaxCauses]
	}
	if len(f.Prerequisites) > MaxPrerequisites {
		f.Prerequisites = f.Prerequisites[:MaxPrerequisites]
	}
	f.Causes = dedupe(f.Causes)
	b.report.Findings = append(b.report.Findings, f)
	return f.ID
}

// incomplete notes why the report cannot claim to be the whole answer.
func (b *builder) incomplete(reason string) {
	b.reasons[reason] = true
}

// require notes a capability the source needs.
func (b *builder) require(capability string, available bool, requiredBy, explanation string) {
	if existing, ok := b.required[capability]; ok {
		if !strings.Contains(existing.RequiredBy, requiredBy) {
			existing.RequiredBy = bound(existing.RequiredBy+", "+requiredBy, MaxFactRunes)
		}
		return
	}
	b.required[capability] = &CapabilityRequirement{Capability: capability, Available: available,
		RequiredBy: requiredBy, Explanation: explanation}
}

// finish sorts, identifies, counts and concludes.
//
// Ids are assigned after sorting, so the same source against the same target
// state gives the same ids; the provisional keys the checks used to link causes
// are rewritten to them.
func (b *builder) finish() *Report {
	r := b.report
	// An artifact with nothing in it is not compatible; nothing was evaluated,
	// and a pipeline must not read that as a pass.
	if r.Source.Objects == 0 {
		b.add(Finding{ID: "artifact:empty", Code: CodeSourceEmpty, Classification: Unknown, Category: CategoryArtifact,
			Scope: ScopeArtifact, Explanation: "The artifact holds nothing to evaluate."})
	}
	sort.SliceStable(r.Findings, func(i, j int) bool { return findingLess(r.Findings[i], r.Findings[j]) })
	final := make(map[string]string, len(r.Findings))
	for i := range r.Findings {
		id := fmt.Sprintf("f%d", i+1)
		final[r.Findings[i].ID] = id
		r.Findings[i].ID = id
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		causes := f.Causes[:0]
		for _, c := range f.Causes {
			if id, ok := final[c]; ok {
				causes = append(causes, id)
			}
		}
		sort.Slice(causes, func(a, c int) bool { return idLess(causes[a], causes[c]) })
		if len(causes) == 0 {
			causes = nil
		}
		f.Causes = causes
		for p := range f.Prerequisites {
			if id, ok := final[f.Prerequisites[p].ProvidedBy]; ok {
				f.Prerequisites[p].ProvidedBy = id
			} else {
				f.Prerequisites[p].ProvidedBy = ""
			}
		}
	}

	sections := map[Category]*Counts{}
	for _, f := range r.Findings {
		r.Counts.add(f.Classification)
		if sections[f.Category] == nil {
			sections[f.Category] = &Counts{}
		}
		sections[f.Category].add(f.Classification)
	}
	r.Sections = []Section{}
	for _, c := range Categories {
		if counts := sections[c]; counts != nil {
			r.Sections = append(r.Sections, Section{Category: c, Counts: *counts})
		}
	}

	names := make([]string, 0, len(b.required))
	for name := range b.required {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r.Capabilities = append(r.Capabilities, *b.required[name])
	}

	if r.Truncated {
		b.incomplete(CodeReportTruncated)
	}
	r.Overall = conclude(r.Findings)
	for reason := range b.reasons {
		r.Reasons = append(r.Reasons, reason)
	}
	for _, f := range r.Findings {
		if f.Classification == Unknown && !b.reasons[f.Code] {
			b.reasons[f.Code] = true
			r.Reasons = append(r.Reasons, f.Code)
		}
	}
	sort.Strings(r.Reasons)
	r.Complete = len(r.Reasons) == 0
	if !r.Complete && r.Overall != NotCompatible {
		r.Overall = Incomplete
	}
	return r
}

// conclude derives the overall result from the findings alone.
//
// A definite incompatibility is an answer whatever else is unknown. Otherwise
// an unknown makes the report incomplete, since the thing not known might be
// the thing that does not fit. Only then do prerequisites and manual work make
// it compatible with prerequisites.
func conclude(findings []Finding) Overall {
	incompatible, unknown, prerequisites := false, false, false
	for _, f := range findings {
		switch f.Classification {
		case Incompatible, Unsupported:
			if f.BlocksPortability {
				incompatible = true
			}
		case Unknown:
			unknown = true
		case PrerequisiteRequired:
			prerequisites = true
		}
		if f.ManualAction {
			prerequisites = true
		}
	}
	switch {
	case incompatible:
		return NotCompatible
	case unknown:
		return Incomplete
	case prerequisites:
		return CompatibleWithPrerequisite
	}
	return Compatible
}

var categoryRank = func() map[Category]int {
	m := map[Category]int{}
	for i, c := range Categories {
		m[c] = i
	}
	return m
}()

// findingLess orders findings by section, then by what they are about, then by
// code: an order that depends only on content.
func findingLess(a, b Finding) bool {
	if a.Category != b.Category {
		return categoryRank[a.Category] < categoryRank[b.Category]
	}
	for _, pair := range [][2]string{
		{a.Source.Item, b.Source.Item},
		{a.Source.Element, b.Source.Element},
		{a.Source.OID, b.Source.OID},
		{strings.ToLower(a.Source.DN), strings.ToLower(b.Source.DN)},
		{strings.ToLower(a.Source.Attribute), strings.ToLower(b.Source.Attribute)},
		{a.Source.Value, b.Source.Value},
		{a.Code, b.Code},
		{a.Source.Name, b.Source.Name},
	} {
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return false
}

func idLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func dedupe(list []string) []string {
	if len(list) < 2 {
		return list
	}
	seen := map[string]bool{}
	out := list[:0]
	for _, v := range list {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// bound cuts text to a number of runes, never inside one.
func bound(s string, runes int) string {
	if utf8.RuneCountInString(s) <= runes {
		return s
	}
	out := []rune(s)[:runes]
	return string(out) + "…"
}
