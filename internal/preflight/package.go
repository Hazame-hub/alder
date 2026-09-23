package preflight

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/diff"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Options steer a preflight.
type Options struct {
	// NotFound recognises the read error that means "no such entry". Without
	// it, every unread entry is unknown.
	NotFound func(error) bool
	// SchemaEntry is the schema entry definitions would be added to, where the
	// target keeps schema in several.
	SchemaEntry string
	// Now is the clock the report is stamped with.
	Now func() time.Time
	// Capture reads the target's entries under a base, for a data snapshot
	// preflight: the entries, and whether the read stopped at a bound.
	Capture func(ctx context.Context, base dn.DN, scope, filter string) (*snapshot.Snapshot, bool, error)
	// CaptureConfig reads the target's configuration, for a configuration
	// snapshot preflight (1.14).
	CaptureConfig func(ctx context.Context) (*snapshot.ConfigSnapshot, error)
}

func (o Options) now() string {
	if o.Now == nil {
		return time.Now().UTC().Format(time.RFC3339)
	}
	return o.Now().UTC().Format(time.RFC3339)
}

// Source types.
const (
	SourceChangePackage  = "change_package"
	SourceSchemaSnapshot = "schema_snapshot"
	SourceDataSnapshot   = "data_snapshot"
)

func targetInfo(caps directory.Capabilities, sourceVendor string) TargetInfo {
	info := TargetInfo{Vendor: caps.VendorName, VendorVersion: caps.VendorVersion, NamingContexts: caps.NamingContexts,
		SchemaEntry: caps.SubschemaSubentry, SchemaWritable: caps.SchemaWrite.Editable()}
	if info.NamingContexts == nil {
		info.NamingContexts = []string{}
	}
	info.CrossVendor = sourceVendor == "" || caps.VendorName == "" || !strings.EqualFold(sourceVendor, caps.VendorName)
	return info
}

// liveSchemaSnapshot is the target's schema in the form a schema comparison
// reads.
func liveSchemaSnapshot(caps directory.Capabilities, sch *schema.Schema) (*snapshot.SchemaSnapshot, error) {
	return snapshot.BuildSchema(snapshot.SchemaCapture{Vendor: caps.VendorName, VendorVersion: caps.VendorVersion,
		SubschemaEntry: caps.SubschemaSubentry, CreatedAt: time.Unix(0, 0)}, sch)
}

// Package preflights a change package against a target.
//
// Package validation is the first step and is not repeated: its answer for
// each item is carried on every finding about that item, and preflight adds
// what validation does not ask -- whether the target publishes what the
// definitions need, whether entries meet the schema they would meet, what
// their references name, and why one item's trouble is another's.
func Package(ctx context.Context, p *changepkg.Package, integrity string, t Target, opts Options) (*Report, error) {
	validation, err := changepkg.Validate(ctx, p, t, changepkg.Options{SchemaEntry: opts.SchemaEntry, NotFound: opts.NotFound})
	if err != nil {
		return nil, err
	}
	caps := t.Capabilities()
	target, schemaErr := t.RefreshSchema(ctx)
	b := newBuilder(SourceInfo{Type: SourceChangePackage, Format: p.Format, Version: p.Version, ID: p.ID, Title: p.Title,
		Checksum: p.Checksum, Integrity: integrity, Vendor: p.Source.Vendor, VendorVersion: p.Source.VendorVersion,
		Objects: len(p.Changes)}, targetInfo(caps, p.Source.Vendor), opts.now())
	if schemaErr != nil || target == nil {
		b.incomplete("target_schema_unreadable")
	}

	results := map[string]*changepkg.ItemResult{}
	for i := range validation.Items {
		results[validation.Items[i].ID] = &validation.Items[i]
	}
	order, err := p.Order()
	if err != nil {
		return nil, err
	}
	items := map[string]*changepkg.Item{}
	for i := range p.Changes {
		items[p.Changes[i].ID] = &p.Changes[i]
	}

	// Schema: what the package adds or replaces, compared with the target as
	// the package would leave it, so what a definition names resolves whether
	// the target or the package defines it.
	var defs []*sourceDefinition
	var defOrder []string
	for _, id := range order {
		item := items[id]
		if item == nil || item.Kind != changepkg.KindSchema || item.Schema == nil || item.Schema.Op == changepkg.SchemaDelete {
			continue
		}
		d := packageDefinition(item)
		if d == nil {
			continue
		}
		if r := results[id]; r != nil {
			d.validation = r.Status
		}
		defs = append(defs, d)
		defOrder = append(defOrder, d.key())
	}
	var compared *diff.SchemaResult
	if target != nil {
		live, err := liveSchemaSnapshot(caps, target)
		if err != nil {
			return nil, err
		}
		overlay, err := overlaySnapshot(caps, target, defs)
		if err != nil {
			return nil, err
		}
		compared = diff.CompareSchema(diff.SchemaSide{Snapshot: live, Live: true}, diff.SchemaSide{Snapshot: overlay}, diff.SchemaOptions{IncludeUnchanged: true, WithoutOrder: true})
	}
	eval := newSchemaEval(b, target, caps, compared, defs)
	for _, d := range defs {
		if r := results[d.item]; r != nil {
			for _, problem := range r.Problems {
				if problem.Code == changepkg.ProblemSchemaTargetRequired {
					eval.schemaTargetProblem[d.key()] = true
				}
			}
		}
	}
	eval.all(defOrder)

	after := schemaAfter(target, defs, eval)
	check := newEntryCheck(b, caps, after, eval, newPresence(t, opts.NotFound))
	itemFinding := map[string]string{}
	itemClass := map[string]Classification{}
	for _, d := range defs {
		if out := eval.outcomes[d.key()]; out != nil {
			itemFinding[d.item], itemClass[d.item] = out.finding, out.class
		}
	}

	var dataInputs []*entryInput
	for _, id := range order {
		item := items[id]
		r := results[id]
		if item == nil || r == nil {
			continue
		}
		switch {
		case item.Kind == changepkg.KindSchema && item.Schema != nil && item.Schema.Op == changepkg.SchemaDelete:
			itemFinding[id], itemClass[id] = schemaRemoval(b, item, r)
		case item.Kind == changepkg.KindData && item.Data != nil:
			in, key, class := dataItem(ctx, check, item, r, itemFinding, itemClass)
			itemFinding[id], itemClass[id] = key, class
			if in != nil {
				dataInputs = append(dataInputs, in)
				if item.Data.Type == changepkg.OpAdd && in.valid {
					check.provided[foldDN(in.parsed)] = key
					check.outcome[foldDN(in.parsed)] = class
				}
			}
		}
	}
	for _, in := range dataInputs {
		check.references(ctx, in)
	}
	check.flush()

	for _, o := range p.Omitted {
		if o.Reason != changepkg.OmittedSecret {
			continue
		}
		b.add(Finding{ID: "omitted:" + o.Subject + ":" + o.Kind, Code: CodeSensitiveNotMigratable, Classification: Excluded,
			Category: CategorySensitive, Scope: ScopeArtifact, Source: SourceRef{DN: o.Subject}, ManualAction: true,
			Explanation: "The package was made without a change that sets a secret for " + o.Subject + ", because a secret cannot travel in a package. Set it on the target separately."})
	}
	return b.finish(), nil
}

// packageDefinition parses a package schema item into a source definition.
func packageDefinition(item *changepkg.Item) *sourceDefinition {
	s := item.Schema
	d := &sourceDefinition{element: s.Element, oid: s.OID, item: item.ID, op: s.Op}
	switch s.Element {
	case changepkg.ElementAttributeType:
		d.element = diff.ElementAttributeType
		at, err := schema.ParseAttributeType(s.Definition)
		if err != nil {
			return nil
		}
		d.at, d.names = at, at.Names
	case changepkg.ElementObjectClass:
		d.element = diff.ElementObjectClass
		oc, err := schema.ParseObjectClass(s.Definition)
		if err != nil {
			return nil
		}
		d.oc, d.names = oc, oc.Names
	default:
		return nil
	}
	return d
}

// overlaySnapshot is the target's schema with the package's definitions in
// place of any the target has under the same OID.
func overlaySnapshot(caps directory.Capabilities, target *schema.Schema, defs []*sourceDefinition) (*snapshot.SchemaSnapshot, error) {
	replaced := map[string]bool{}
	attrs := map[string][]string{}
	for _, d := range defs {
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
	return snapshot.BuildSchema(snapshot.SchemaCapture{Vendor: caps.VendorName, SubschemaEntry: caps.SubschemaSubentry,
		CreatedAt: time.Unix(0, 0)}, schema.Load(target.DN, attrs))
}

// schemaRemoval reports a package item that removes a definition, from what
// validation concluded.
func schemaRemoval(b *builder, item *changepkg.Item, r *changepkg.ItemResult) (string, Classification) {
	element := diff.ElementAttributeType
	if item.Schema.Element == changepkg.ElementObjectClass {
		element = diff.ElementObjectClass
	}
	f := Finding{ID: "schema:" + element + ":" + strings.ToLower(item.Schema.OID) + ":remove", Category: CategorySchema, Scope: ScopeItem,
		Source: SourceRef{Item: item.ID, Element: element, OID: item.Schema.OID}, ValidationStatus: r.Status, BlocksPlan: blocksPlan(r.Status)}
	switch r.Status {
	case changepkg.StatusReady:
		f.Code, f.Classification = CodeChangePortable, Portable
		f.Explanation = "The package removes this definition, and the target can remove it. The removal is destructive."
	case changepkg.StatusAlreadySatisfied:
		f.Code, f.Classification = CodeChangeSatisfied, AlreadySatisfied
		f.Target = &TargetFact{Fact: "absent"}
		f.Explanation = "The package removes this definition, and the target does not define it."
	case changepkg.StatusUnknown:
		f.Code, f.Classification = CodeDefinitionUnknown, Unknown
		f.Explanation = "Whether the target can remove this definition could not be decided."
	default:
		f.Code, f.Classification, f.BlocksPortability = CodeChangeNotApplicable, Incompatible, true
		if r.Status == changepkg.StatusUnsupported {
			f.Classification = Unsupported
		}
		f.Target = problemFact(r)
		f.Explanation = "The package removes this definition, and the target refuses: " + problemText(r) + "."
	}
	return b.add(f), f.Classification
}

func problemFact(r *changepkg.ItemResult) *TargetFact {
	if len(r.Problems) == 0 {
		return nil
	}
	return &TargetFact{Fact: r.Problems[0].Code, Detail: r.Problems[0].Subject}
}

func problemText(r *changepkg.ItemResult) string {
	if len(r.Problems) == 0 {
		return r.Status
	}
	parts := make([]string, 0, len(r.Problems))
	for _, p := range r.Problems {
		text := p.Code
		if p.Subject != "" {
			text += " " + p.Subject
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "; ")
}

// dataItem judges one data change of a package.
func dataItem(ctx context.Context, c *entryCheck, item *changepkg.Item, r *changepkg.ItemResult,
	itemFinding map[string]string, itemClass map[string]Classification) (*entryInput, string, Classification) {
	d := item.Data
	parsed, err := dn.Parse(d.DN)
	in := &entryInput{dn: d.DN, parsed: parsed, valid: err == nil, item: item.ID, validation: r.Status, mode: "package"}
	key := "entry:" + item.ID
	main := Finding{ID: key, Category: CategoryEntries, Scope: ScopeItem, Source: SourceRef{Item: item.ID, DN: d.DN},
		ValidationStatus: r.Status, BlocksPlan: blocksPlan(r.Status)}

	var worst Classification
	var causes []string
	block := func(class Classification, id string) {
		if id == "" {
			return
		}
		worst = worse(worst, class)
		causes = append(causes, id)
	}
	blockAll := func(class Classification, ids []string) {
		for _, id := range ids {
			block(class, id)
		}
	}
	for _, dep := range item.DependsOn {
		switch itemClass[dep] {
		case Portable, AlreadySatisfied:
		case "":
		default:
			block(worse(PrerequisiteRequired, itemClass[dep]), itemFinding[dep])
		}
	}
	block(c.naming(in))

	switch d.Type {
	case changepkg.OpAdd:
		for _, a := range d.Attributes {
			in.attrs = append(in.attrs, contentAttribute{name: a.Name, values: decodeValues(a.Values)})
		}
	case changepkg.OpModify:
		for _, m := range d.Mods {
			if m.Op == string(directory.ModDelete) {
				continue
			}
			in.attrs = append(in.attrs, contentAttribute{name: m.Name, values: decodeValues(m.Values)})
		}
	}

	if worst == "" && in.valid {
		switch r.Status {
		case changepkg.StatusAlreadySatisfied, changepkg.StatusNoOp:
			main.Code, main.Classification = CodeChangeSatisfied, AlreadySatisfied
			main.Target = &TargetFact{Fact: "satisfied"}
			main.Explanation = "The target already holds what this change intends."
			return in, c.b.add(main), AlreadySatisfied
		case changepkg.StatusUnknown:
			main.Code, main.Classification = CodeEntryUnknown, Unknown
			main.Target = problemFact(r)
			main.Explanation = "What the target holds for this entry could not be read: " + problemText(r) + "."
			return in, c.b.add(main), Unknown
		}
		switch d.Type {
		case changepkg.OpAdd:
			if hasProblem(r, changepkg.ProblemEntryExists) {
				block(PrerequisiteRequired, c.b.add(Finding{ID: key + ":exists", Code: CodeEntryDiffers, Classification: PrerequisiteRequired,
					Category: CategoryEntries, Scope: ScopeEntry, Source: in.ref(), BlocksPlan: true, ManualAction: true, ValidationStatus: r.Status,
					Target:      &TargetFact{Fact: "present_different"},
					Explanation: "The target already has an entry at this DN, and it does not hold what the package describes. Whether to change it is a decision for a plan, not for a preflight."}))
			} else {
				block(c.parent(ctx, in))
			}
			blockAll(c.content(in, nil, directory.ChangeAdd, nil))
		case changepkg.OpModify:
			state := c.presence.of(ctx, parsed)
			switch state.state {
			case PresencePresent:
				mods := make([]directory.Mod, 0, len(d.Mods))
				for _, m := range d.Mods {
					mods = append(mods, directory.Mod{Op: directory.ModOp(m.Op), Name: m.Name, Values: decodeValues(m.Values)})
				}
				blockAll(c.content(in, state.entry, directory.ChangeModify, mods))
			case PresenceAbsent:
				block(PrerequisiteRequired, c.b.add(Finding{ID: key + ":missing", Code: CodeChangeNotApplicable, Classification: PrerequisiteRequired,
					Category: CategoryEntries, Scope: ScopeEntry, Source: in.ref(), BlocksPortability: true, BlocksPlan: true, ManualAction: true,
					ValidationStatus: r.Status, Target: &TargetFact{Fact: "absent"},
					Prerequisites: []Prerequisite{{Type: "entry", DN: d.DN}},
					Explanation:   "The package modifies an entry the target does not have, as the server reports it to this bind."}))
			default:
				block(Unknown, c.b.add(Finding{ID: key + ":unseen", Code: CodeEntryUnknown, Classification: Unknown,
					Category: CategoryEntries, Scope: ScopeEntry, Source: in.ref(), ValidationStatus: r.Status,
					Target:      &TargetFact{Fact: "hidden_or_unreadable"},
					Explanation: "The package modifies this entry, and whether the target has it could not be seen."}))
			}
		default:
			if r.Status != changepkg.StatusReady {
				class := Incompatible
				switch {
				case hasProblem(r, changepkg.ProblemParentMissing):
					block(c.parent(ctx, renamedInput(in, d)))
					class = ""
				case hasProblem(r, changepkg.ProblemEntryMissing):
					class = PrerequisiteRequired
				case r.Status == changepkg.StatusUnsupported:
					class = Unsupported
				}
				if class != "" {
					block(class, c.b.add(Finding{ID: key + ":refused", Code: CodeChangeNotApplicable, Classification: class,
						Category: CategoryEntries, Scope: ScopeEntry, Source: in.ref(), BlocksPortability: true, BlocksPlan: true,
						ManualAction: class == PrerequisiteRequired, ValidationStatus: r.Status, Target: problemFact(r),
						Explanation: "The target cannot take this change as written: " + problemText(r) + "."}))
				}
			}
		}
	}

	main.Causes = causes
	switch {
	case worst != "":
		main.Code, main.Classification = CodeEntryBlocked, worst
		main.BlocksPortability = worst != Unknown
		main.Explanation = "This change cannot be carried to this target as it stands; the findings it links to say why."
	case r.Status == changepkg.StatusReady || r.Status == changepkg.StatusDependencyMissing || r.Status == changepkg.StatusConflict:
		main.Code, main.Classification = CodeChangePortable, Portable
		main.Explanation = "The target can take this change."
		if item.Destructive {
			main.Explanation = "The target can take this change. It removes something."
		}
	default:
		main.Code, main.Classification = CodeEntryUnknown, Unknown
		main.Target = problemFact(r)
		main.Explanation = "Whether the target can take this change could not be decided: " + problemText(r) + "."
	}
	class := main.Classification
	return in, c.b.add(main), class
}

// renamedInput is where a rename would put the entry, for the parent check.
func renamedInput(in *entryInput, d *changepkg.DataChange) *entryInput {
	out := *in
	if d.NewSuperior == "" {
		return &out
	}
	superior, err := dn.Parse(d.NewSuperior)
	if err != nil || len(in.parsed) == 0 {
		return &out
	}
	out.parsed = superior.Child(in.parsed.RDN())
	out.dn = out.parsed.String()
	return &out
}

func hasProblem(r *changepkg.ItemResult, code string) bool {
	for _, p := range r.Problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

func decodeValues(values []changepkg.Value) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		if v.Base64 != "" {
			raw, err := base64.StdEncoding.DecodeString(v.Base64)
			if err == nil {
				out = append(out, raw)
			}
			continue
		}
		out = append(out, []byte(v.Text))
	}
	return out
}
