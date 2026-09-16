package changepkg

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Validating a package against a target.
//
// This answers one question: can this intent be interpreted here, and what
// would it mean? It is not a plan. A plan reads the directory as it is at the
// moment it runs, issues the baselines an apply checks, and is the only thing
// that decides what gets written. Validation prepares the ordinary change
// requests a plan would take, says which items have nothing to do here and
// which cannot be done at all, and stops.
//
// The same package validated against two directories gives two different
// answers, and neither changes the package. That is what promotion is: the
// same intent, revalidated, never a replay of the operations another
// environment produced.

// What validation concluded about one item.
const (
	// StatusReady: the target can take this change, and the change requests
	// are prepared.
	StatusReady = "ready"
	// StatusAlreadySatisfied: the target already holds what the item intends.
	// Nothing is prepared, and this is a success, not a failure: a change
	// applied here by hand last week is not a problem to report.
	StatusAlreadySatisfied = "already_satisfied"
	// StatusNoOp: the item resolves to no operation at all, without the target
	// necessarily matching it -- a reconciling add whose attributes are all
	// already as it asks.
	StatusNoOp = "no_op"
	// StatusConflict: the target's current state refuses the change as
	// written. A plan would say the same, with its own problem code.
	StatusConflict = "conflict"
	// StatusDependencyMissing: the change names something that is not there
	// and that this package does not provide -- a parent entry, a schema
	// element, an item that is itself unusable.
	StatusDependencyMissing = "dependency_missing"
	// StatusUnsupported: this target cannot perform this kind of change at
	// all, whatever its state.
	StatusUnsupported = "unsupported"
	// StatusTargetIncompatible: an assumption the package states is false
	// here, or the change is outside what this directory holds.
	StatusTargetIncompatible = "target_incompatible"
	// StatusUnknown: it could not be decided. Never read as ready.
	StatusUnknown = "unknown"
)

// Problem codes, reusing the plan's and the schema comparison's vocabulary
// where the situation is the same one.
const (
	ProblemEntryMissing          = "entry_missing"
	ProblemEntryExists           = "entry_exists"
	ProblemParentMissing         = "parent_missing"
	ProblemRenameTargetExists    = "rename_target_exists"
	ProblemAttributeUndefined    = "attribute_undefined"
	ProblemObjectClassUndefined  = "object_class_undefined"
	ProblemDefinitionMissing     = "definition_missing"
	ProblemDefinitionDiffers     = "definition_differs"
	ProblemDependencyRequired    = "dependency_required"
	ProblemReferencedBySchema    = "referenced_by_schema"
	ProblemSchemaNotEditable     = "schema_not_editable"
	ProblemSchemaTargetRequired  = "schema_target_required"
	ProblemServerDefined         = "server_defined"
	ProblemOutsideNamingContexts = "outside_naming_contexts"
	ProblemAssumptionUnmet       = "assumption_unmet"
	ProblemReadFailed            = "read_failed"
	ProblemUnbuildable           = "unbuildable_change"
)

// Target is what validation reads. It is the part of a directory session a
// package needs, and nothing else: no write of any kind.
type Target interface {
	Capabilities() directory.Capabilities
	RefreshSchema(ctx context.Context) (*schema.Schema, error)
	Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error)
	SchemaDefinitions(ctx context.Context, targetDN string, kind directory.SchemaDefKind) ([]string, error)
}

// Options steer validation.
type Options struct {
	// SchemaEntry is the schema entry an added definition should be written
	// to, where the server keeps schema in several. Empty is enough when there
	// is one.
	SchemaEntry string
	// NotFound recognises the read error that means "no such entry" rather
	// than a failure. Without it a missing entry reads as unknown.
	NotFound func(error) bool
}

// Problem is one reason an item is not simply ready.
type Problem struct {
	Code string `json:"code"`
	// Subject is what the problem is about: a DN, an attribute, an OID.
	Subject string `json:"subject,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ItemResult is what validation concluded about one change.
type ItemResult struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label,omitempty"`
	Destructive bool      `json:"destructive"`
	Status      string    `json:"status"`
	Problems    []Problem `json:"problems,omitempty"`
	// DependsOn is the item's own dependency list, repeated here so a reader
	// of the validation alone can see the graph.
	DependsOn []string `json:"dependsOn,omitempty"`
	// Records are the ordinary changes this item resolves to on this target,
	// for a ready item. They are plan input: nothing here has been applied,
	// and nothing here carries a baseline.
	Records []directory.ChangeRecord `json:"-"`
	// Intent is how the records should be planned.
	Intent string `json:"intent,omitempty"`
}

// AssumptionResult is one stated assumption, checked.
type AssumptionResult struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	Satisfied bool   `json:"satisfied"`
	Detail    string `json:"detail,omitempty"`
}

// ResultCounts tallies the item statuses.
type ResultCounts struct {
	Ready              int `json:"ready"`
	AlreadySatisfied   int `json:"alreadySatisfied"`
	NoOp               int `json:"noOp"`
	Conflict           int `json:"conflict"`
	DependencyMissing  int `json:"dependencyMissing"`
	Unsupported        int `json:"unsupported"`
	TargetIncompatible int `json:"targetIncompatible"`
	Unknown            int `json:"unknown"`
}

// TargetInfo describes the directory a package was validated against. Display
// only, as everywhere else: a server announces these.
type TargetInfo struct {
	Vendor         string   `json:"vendor,omitempty"`
	VendorVersion  string   `json:"vendorVersion,omitempty"`
	NamingContexts []string `json:"namingContexts"`
	SchemaWritable bool     `json:"schemaWritable"`
	SchemaEntries  []string `json:"schemaEntries,omitempty"`
}

// Result is a whole validation.
type Result struct {
	PackageID   string             `json:"packageId"`
	Target      TargetInfo         `json:"target"`
	Assumptions []AssumptionResult `json:"assumptions"`
	Counts      ResultCounts       `json:"counts"`
	Items       []ItemResult       `json:"items"`
	// Order is the identifiers of the items that have something to do, in the
	// order they must be applied.
	Order []string `json:"order"`
}

// Applicable reports whether anything is ready to be planned.
func (r *Result) Applicable() bool { return r.Counts.Ready > 0 }

// Records returns the prepared changes of the ready items, in dependency
// order, with the intent each should be planned under.
func (r *Result) Records() ([]directory.ChangeRecord, []string) {
	byID := map[string]*ItemResult{}
	for i := range r.Items {
		byID[r.Items[i].ID] = &r.Items[i]
	}
	var records []directory.ChangeRecord
	var intents []string
	for _, id := range r.Order {
		item := byID[id]
		if item == nil || item.Status != StatusReady {
			continue
		}
		for _, rec := range item.Records {
			records = append(records, rec)
			intents = append(intents, item.Intent)
		}
	}
	return records, intents
}

// Validate checks a package against a target and prepares what is ready.
func Validate(ctx context.Context, p *Package, t Target, opts Options) (*Result, error) {
	caps := t.Capabilities()
	sch, schemaErr := t.RefreshSchema(ctx)
	res := &Result{
		PackageID: p.ID,
		Target: TargetInfo{
			Vendor: caps.VendorName, VendorVersion: caps.VendorVersion,
			NamingContexts: caps.NamingContexts, SchemaWritable: caps.SchemaWrite.Editable(),
		},
		Assumptions: []AssumptionResult{},
		Items:       make([]ItemResult, 0, len(p.Changes)),
		Order:       []string{},
	}
	if res.Target.NamingContexts == nil {
		res.Target.NamingContexts = []string{}
	}
	for _, target := range caps.SchemaWrite.Targets {
		res.Target.SchemaEntries = append(res.Target.SchemaEntries, target.DN)
	}

	v := &validation{
		pkg: p, target: t, caps: caps, sch: sch, schemaErr: schemaErr, opts: opts,
		provided: map[string]bool{}, status: map[string]string{},
	}
	res.Assumptions = v.assumptions()
	order, err := p.Order()
	if err != nil {
		return nil, err
	}

	// In dependency order, so an item can see what the ones before it would
	// have created: a class that an earlier item adds is not missing.
	for _, id := range order {
		item := v.item(id)
		result := v.check(ctx, item)
		v.status[id] = result.Status
		if result.Status == StatusReady {
			v.record(item)
		}
		res.Items = append(res.Items, result)
	}
	for i := range res.Items {
		switch res.Items[i].Status {
		case StatusReady:
			res.Counts.Ready++
			res.Order = append(res.Order, res.Items[i].ID)
		case StatusAlreadySatisfied:
			res.Counts.AlreadySatisfied++
		case StatusNoOp:
			res.Counts.NoOp++
		case StatusConflict:
			res.Counts.Conflict++
		case StatusDependencyMissing:
			res.Counts.DependencyMissing++
		case StatusUnsupported:
			res.Counts.Unsupported++
		case StatusTargetIncompatible:
			res.Counts.TargetIncompatible++
		default:
			res.Counts.Unknown++
		}
	}
	return res, nil
}

type validation struct {
	pkg       *Package
	target    Target
	caps      directory.Capabilities
	sch       *schema.Schema
	schemaErr error
	opts      Options
	// provided holds what the items validated so far would create: entry DNs,
	// folded, and schema elements by folded OID and name.
	provided map[string]bool
	status   map[string]string
	// collections maps a folded OID to the schema entry holding it, read once.
	collections    map[string]string
	collectionsSet bool
}

func (v *validation) item(id string) Item {
	for _, item := range v.pkg.Changes {
		if item.ID == id {
			return item
		}
	}
	return Item{ID: id}
}

// record notes what an item that will be applied leaves behind, so later items
// can depend on it.
func (v *validation) record(item Item) {
	switch {
	case item.Kind == KindData && item.Data != nil:
		switch item.Data.Type {
		case OpAdd:
			v.provided["dn:"+foldDN(item.Data.DN)] = true
		case OpDelete:
			delete(v.provided, "dn:"+foldDN(item.Data.DN))
		}
	case item.Kind == KindSchema && item.Schema != nil:
		key := "oid:" + strings.ToLower(item.Schema.OID)
		if item.Schema.Op == SchemaDelete {
			delete(v.provided, key)
			return
		}
		v.provided[key] = true
		if names, err := definitionNames(item.Schema); err == nil {
			for _, n := range names {
				v.provided["name:"+strings.ToLower(n)] = true
			}
		}
	}
}

func definitionNames(s *SchemaChange) ([]string, error) {
	switch s.Element {
	case ElementAttributeType:
		at, err := schema.ParseAttributeType(s.Definition)
		if err != nil {
			return nil, err
		}
		return at.Names, nil
	case ElementObjectClass:
		oc, err := schema.ParseObjectClass(s.Definition)
		if err != nil {
			return nil, err
		}
		return oc.Names, nil
	}
	return nil, fmt.Errorf("not a schema element")
}

func foldDN(text string) string {
	parsed, err := dn.Parse(text)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(text))
	}
	return strings.ToLower(parsed.String())
}

// assumptions checks what the package says it expects of a target.
func (v *validation) assumptions() []AssumptionResult {
	out := []AssumptionResult{}
	for _, context := range v.pkg.Assumptions.NamingContexts {
		satisfied := false
		for _, held := range v.caps.NamingContexts {
			if sameDN(context, held) {
				satisfied = true
			}
		}
		detail := ""
		if !satisfied {
			detail = "this directory does not hold that naming context"
		}
		out = append(out, AssumptionResult{Kind: "namingContext", Value: context, Satisfied: satisfied, Detail: detail})
	}
	for _, oid := range v.pkg.Assumptions.SchemaOIDs {
		satisfied := v.schemaHas(oid)
		detail := ""
		if !satisfied {
			detail = "the schema defines nothing with that OID"
		}
		out = append(out, AssumptionResult{Kind: "schemaOid", Value: oid, Satisfied: satisfied, Detail: detail})
	}
	for _, name := range v.pkg.Assumptions.ObjectClasses {
		satisfied := v.sch != nil && v.sch.ObjectClass(name) != nil
		detail := ""
		if !satisfied {
			detail = "the schema does not define that object class"
		}
		out = append(out, AssumptionResult{Kind: "objectClass", Value: name, Satisfied: satisfied, Detail: detail})
	}
	return out
}

func (v *validation) schemaHas(oidOrName string) bool {
	if v.sch == nil {
		return false
	}
	return v.sch.AttributeType(oidOrName) != nil || v.sch.ObjectClass(oidOrName) != nil
}

func sameDN(a, b string) bool {
	left, err := dn.Parse(a)
	if err != nil {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	right, err := dn.Parse(b)
	if err != nil {
		return false
	}
	return left.Equal(right)
}

// check validates one item against the target.
func (v *validation) check(ctx context.Context, item Item) ItemResult {
	out := ItemResult{ID: item.ID, Kind: item.Kind, Label: item.Label,
		Destructive: item.Destructive, DependsOn: item.DependsOn, Intent: item.Intent}

	// An item whose dependency is not going to happen cannot be ready, however
	// the target looks: the thing it needs will not be there.
	for _, needs := range item.DependsOn {
		switch v.status[needs] {
		case StatusReady, StatusAlreadySatisfied, StatusNoOp:
		case "":
			out.Status = StatusUnknown
			out.Problems = append(out.Problems, Problem{Code: ProblemDependencyRequired, Subject: needs,
				Detail: "this package does not place that change before this one"})
			return out
		default:
			out.Status = StatusDependencyMissing
			out.Problems = append(out.Problems, Problem{Code: ProblemDependencyRequired, Subject: needs,
				Detail: "the change it depends on is " + v.status[needs]})
			return out
		}
	}

	switch item.Kind {
	case KindData:
		v.checkData(ctx, item, &out)
	case KindSchema:
		v.checkSchema(ctx, item, &out)
	default:
		out.Status = StatusUnknown
	}
	return out
}

// entryState is what the target holds for one DN.
type entryState struct {
	entry   *directory.Entry
	exists  bool
	unknown bool
}

func (v *validation) read(ctx context.Context, text string) entryState {
	target, err := dn.Parse(text)
	if err != nil {
		return entryState{unknown: true}
	}
	entry, err := v.target.Read(ctx, target, []string{"*"})
	switch {
	case err == nil:
		return entryState{entry: entry, exists: true}
	case v.opts.NotFound != nil && v.opts.NotFound(err):
		return entryState{}
	default:
		return entryState{unknown: true}
	}
}

func (v *validation) checkData(ctx context.Context, item Item, out *ItemResult) {
	d := item.Data
	if !v.inNamingContext(d.DN) {
		out.Status = StatusTargetIncompatible
		out.Problems = append(out.Problems, Problem{Code: ProblemOutsideNamingContexts, Subject: d.DN,
			Detail: "this directory holds no naming context that DN is under"})
		return
	}
	state := v.read(ctx, d.DN)
	if state.unknown {
		out.Status = StatusUnknown
		out.Problems = append(out.Problems, Problem{Code: ProblemReadFailed, Subject: d.DN})
		return
	}

	switch d.Type {
	case OpAdd:
		v.checkAdd(ctx, item, state, out)
	case OpModify:
		v.checkModify(item, state, out)
	case OpDelete:
		if !state.exists {
			out.Status = StatusAlreadySatisfied
			return
		}
		out.Status = StatusReady
	case OpRename:
		v.checkRename(ctx, item, state, out)
	default:
		out.Status = StatusUnsupported
	}
	if out.Status == StatusReady {
		record, err := item.Record()
		if err != nil {
			out.Status = StatusUnknown
			out.Problems = append(out.Problems, Problem{Code: ProblemUnbuildable, Subject: d.DN, Detail: err.Error()})
			return
		}
		out.Records = []directory.ChangeRecord{record}
	}
}

func (v *validation) checkAdd(ctx context.Context, item Item, state entryState, out *ItemResult) {
	d := item.Data
	if missing := v.undefinedAttributes(attributeNames(d)); len(missing) > 0 {
		out.Status = StatusDependencyMissing
		for _, name := range missing {
			out.Problems = append(out.Problems, Problem{Code: ProblemAttributeUndefined, Subject: name})
		}
		return
	}
	if missing := v.undefinedClasses(objectClassValues(d)); len(missing) > 0 {
		out.Status = StatusDependencyMissing
		for _, name := range missing {
			out.Problems = append(out.Problems, Problem{Code: ProblemObjectClassUndefined, Subject: name})
		}
		return
	}
	if state.exists {
		// An entry that already holds everything the package describes is the
		// intent, satisfied. A promoted change someone applied here last week
		// is not a failure to report, and this is true however the item asked
		// to be planned.
		if entryHolds(state.entry, d.Attributes) {
			out.Status = StatusAlreadySatisfied
			return
		}
		// It is there and it is not what the package describes. A reconciling
		// add says which attributes it means, so it can go ahead; an exact add
		// cannot, and saying so is the whole point -- the alternative is
		// overwriting an entry because another environment agreed.
		if item.Intent == IntentDesired {
			out.Status = StatusReady
			return
		}
		out.Status = StatusConflict
		out.Problems = append(out.Problems, Problem{Code: ProblemEntryExists, Subject: d.DN})
		return
	}
	if parent, ok := parentOf(d.DN); ok && !v.willExist(ctx, parent) {
		out.Status = StatusDependencyMissing
		out.Problems = append(out.Problems, Problem{Code: ProblemParentMissing, Subject: parent})
		return
	}
	out.Status = StatusReady
}

func (v *validation) checkModify(item Item, state entryState, out *ItemResult) {
	d := item.Data
	if !state.exists {
		out.Status = StatusConflict
		out.Problems = append(out.Problems, Problem{Code: ProblemEntryMissing, Subject: d.DN})
		return
	}
	var names []string
	for _, m := range d.Mods {
		names = append(names, m.Name)
	}
	if missing := v.undefinedAttributes(names); len(missing) > 0 {
		out.Status = StatusDependencyMissing
		for _, name := range missing {
			out.Problems = append(out.Problems, Problem{Code: ProblemAttributeUndefined, Subject: name})
		}
		return
	}
	if modsSatisfied(state.entry, d.Mods) {
		out.Status = StatusAlreadySatisfied
		return
	}
	out.Status = StatusReady
}

func (v *validation) checkRename(ctx context.Context, item Item, state entryState, out *ItemResult) {
	d := item.Data
	if !state.exists {
		// The entry may already be where the rename would put it, which is
		// what a promoted rename looks like on a target that has had it.
		if target, ok := renameTarget(d); ok {
			if after := v.read(ctx, target); after.exists {
				out.Status = StatusAlreadySatisfied
				return
			}
		}
		out.Status = StatusConflict
		out.Problems = append(out.Problems, Problem{Code: ProblemEntryMissing, Subject: d.DN})
		return
	}
	if target, ok := renameTarget(d); ok {
		if after := v.read(ctx, target); after.exists {
			out.Status = StatusConflict
			out.Problems = append(out.Problems, Problem{Code: ProblemRenameTargetExists, Subject: target})
			return
		}
		if parent, ok := parentOf(target); ok && !v.willExist(ctx, parent) {
			out.Status = StatusDependencyMissing
			out.Problems = append(out.Problems, Problem{Code: ProblemParentMissing, Subject: parent})
			return
		}
	}
	out.Status = StatusReady
}

// willExist reports whether a DN is there now or will be by the time this item
// runs.
func (v *validation) willExist(ctx context.Context, target string) bool {
	if v.provided["dn:"+foldDN(target)] {
		return true
	}
	return v.read(ctx, target).exists
}

func (v *validation) inNamingContext(target string) bool {
	if len(v.caps.NamingContexts) == 0 {
		return true
	}
	parsed, err := dn.Parse(target)
	if err != nil {
		return false
	}
	for _, context := range v.caps.NamingContexts {
		suffix, err := dn.Parse(context)
		if err != nil {
			continue
		}
		if parsed.HasSuffix(suffix) {
			return true
		}
	}
	return false
}

// undefinedAttributes are the attribute types the target's schema does not
// define and no earlier item adds.
func (v *validation) undefinedAttributes(names []string) []string {
	if v.sch == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		base := schema.BaseName(name)
		key := strings.ToLower(base)
		if seen[key] || v.sch.AttributeType(base) != nil || v.provided["name:"+key] || v.provided["oid:"+key] {
			continue
		}
		seen[key] = true
		out = append(out, base)
	}
	return out
}

func (v *validation) undefinedClasses(names []string) []string {
	if v.sch == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(name)
		if seen[key] || v.sch.ObjectClass(name) != nil || v.provided["name:"+key] || v.provided["oid:"+key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

func attributeNames(d *DataChange) []string {
	out := make([]string, 0, len(d.Attributes))
	for _, a := range d.Attributes {
		out = append(out, a.Name)
	}
	return out
}

func objectClassValues(d *DataChange) []string {
	var out []string
	for _, a := range d.Attributes {
		if !strings.EqualFold(schema.BaseName(a.Name), "objectClass") {
			continue
		}
		for _, value := range a.Values {
			if value.Text != "" {
				out = append(out, value.Text)
			}
		}
	}
	return out
}

func parentOf(target string) (string, bool) {
	parsed, err := dn.Parse(target)
	if err != nil || len(parsed) < 2 {
		return "", false
	}
	return parsed.Parent().String(), true
}

func renameTarget(d *DataChange) (string, bool) {
	parsed, err := dn.Parse(d.DN)
	if err != nil {
		return "", false
	}
	if len(parsed) == 0 {
		return "", false
	}
	rdn := parsed[0].String()
	if d.NewRDN != "" {
		rdn = d.NewRDN
	}
	parent := ""
	if d.NewSuperior != "" {
		parent = d.NewSuperior
	} else if len(parsed) > 1 {
		parent = parsed.Parent().String()
	}
	if parent == "" {
		return rdn, true
	}
	return rdn + "," + parent, true
}

// entryHolds reports whether an entry already has exactly these attributes'
// values, which is what a reconciling add asks for.
func entryHolds(entry *directory.Entry, attrs []Attribute) bool {
	if entry == nil {
		return false
	}
	for _, a := range attrs {
		want, err := rawValues(a.Values)
		if err != nil {
			return false
		}
		if !sameValues(entry.Get(a.Name), want) {
			return false
		}
	}
	return true
}

// modsSatisfied reports whether every modification is already true of the
// entry: values to add present, values to delete absent, a replace equal.
func modsSatisfied(entry *directory.Entry, mods []Mod) bool {
	if entry == nil {
		return false
	}
	for _, m := range mods {
		have := entry.Get(m.Name)
		want, err := rawValues(m.Values)
		if err != nil {
			return false
		}
		switch m.Op {
		case string(directory.ModAdd):
			for _, value := range want {
				if !containsValue(have, value) {
					return false
				}
			}
		case string(directory.ModDelete):
			if len(want) == 0 {
				if len(have) > 0 {
					return false
				}
				continue
			}
			for _, value := range want {
				if containsValue(have, value) {
					return false
				}
			}
		case string(directory.ModReplace):
			if !sameValues(have, want) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func containsValue(have [][]byte, want []byte) bool {
	for _, value := range have {
		if string(value) == string(want) {
			return true
		}
	}
	return false
}

func sameValues(have, want [][]byte) bool {
	if len(have) != len(want) {
		return false
	}
	left := append([]string(nil), asStrings(have)...)
	right := append([]string(nil), asStrings(want)...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func asStrings(values [][]byte) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, string(v))
	}
	return out
}

// checkSchema validates a schema item and, when it is ready, builds the
// modification this target needs through the ordinary schema write path.
func (v *validation) checkSchema(ctx context.Context, item Item, out *ItemResult) {
	s := item.Schema
	write := v.caps.SchemaWrite
	if !write.Editable() {
		out.Status = StatusUnsupported
		detail := write.Unavailable
		if detail == "" {
			detail = "this connection found no writable schema location"
		}
		out.Problems = append(out.Problems, Problem{Code: ProblemSchemaNotEditable, Subject: s.OID, Detail: detail})
		return
	}
	if v.sch == nil {
		out.Status = StatusUnknown
		out.Problems = append(out.Problems, Problem{Code: ProblemReadFailed, Subject: s.OID,
			Detail: schemaErrText(v.schemaErr)})
		return
	}

	present := v.definition(s)
	switch s.Op {
	case SchemaAdd, SchemaReplace:
		if missing := v.undefinedReferences(s); len(missing) > 0 {
			out.Status = StatusDependencyMissing
			for _, ref := range missing {
				out.Problems = append(out.Problems, Problem{Code: ProblemDependencyRequired, Subject: ref,
					Detail: "the definition names it and the target's schema does not define it"})
			}
			return
		}
		switch {
		case present != nil && sameDefinition(present, s.Definition):
			out.Status = StatusAlreadySatisfied
			return
		case present == nil && s.Op == SchemaReplace:
			out.Status = StatusConflict
			out.Problems = append(out.Problems, Problem{Code: ProblemDefinitionMissing, Subject: s.OID,
				Detail: "there is nothing here to replace"})
			return
		case present != nil && s.Op == SchemaAdd:
			// Adding what is already there, differently: the intent is the
			// definition, so this is a replacement the package did not ask
			// for. Report it rather than quietly changing the operation.
			out.Status = StatusConflict
			out.Problems = append(out.Problems, Problem{Code: ProblemDefinitionDiffers, Subject: s.OID,
				Detail: "the target defines this OID as something else"})
			return
		}
	case SchemaDelete:
		if present == nil {
			out.Status = StatusAlreadySatisfied
			return
		}
		if referrer := v.referrer(s); referrer != "" {
			out.Status = StatusConflict
			out.Problems = append(out.Problems, Problem{Code: ProblemReferencedBySchema, Subject: referrer,
				Detail: "the schema still names this definition"})
			return
		}
	}

	targetDN, problem := v.schemaTargetFor(ctx, s, present != nil)
	if problem != nil {
		out.Status = StatusUnsupported
		if problem.Code == ProblemSchemaTargetRequired {
			out.Status = StatusConflict
		}
		out.Problems = append(out.Problems, *problem)
		return
	}
	kind := directory.SchemaDefAttributeType
	if s.Element == ElementObjectClass {
		kind = directory.SchemaDefObjectClass
	}
	stored, err := v.target.SchemaDefinitions(ctx, targetDN, kind)
	if err != nil {
		out.Status = StatusUnknown
		out.Problems = append(out.Problems, Problem{Code: ProblemReadFailed, Subject: targetDN, Detail: err.Error()})
		return
	}
	record, err := directory.BuildSchemaChange(write, directory.SchemaChangeRequest{
		TargetDN: targetDN, Kind: kind, Op: directory.SchemaOp(s.Op), OID: s.OID, Definition: s.Definition,
	}, stored)
	if err != nil {
		out.Status = StatusConflict
		out.Problems = append(out.Problems, Problem{Code: ProblemUnbuildable, Subject: s.OID, Detail: err.Error()})
		return
	}
	out.Status = StatusReady
	out.Records = []directory.ChangeRecord{record}
}

func schemaErrText(err error) string {
	if err == nil {
		return "the target published no schema"
	}
	return err.Error()
}

// definition is the target's own definition of this element, or nil.
func (v *validation) definition(s *SchemaChange) *string {
	switch s.Element {
	case ElementAttributeType:
		if at := v.sch.AttributeType(s.OID); at != nil {
			text, err := at.Definition()
			if err != nil {
				text = at.Raw
			}
			return &text
		}
	case ElementObjectClass:
		if oc := v.sch.ObjectClass(s.OID); oc != nil {
			text, err := oc.Definition()
			if err != nil {
				text = oc.Raw
			}
			return &text
		}
	}
	return nil
}

// sameDefinition compares what the target holds with what the package wants,
// both canonicalised and without provenance, so a server's own X-ORIGIN does
// not read as a difference.
func sameDefinition(present *string, wanted string) bool {
	if present == nil || wanted == "" {
		return false
	}
	element := ElementAttributeType
	if _, err := schema.ParseAttributeType(*present); err != nil {
		element = ElementObjectClass
	}
	left, _, err := canonicalDefinition(element, *present)
	if err != nil {
		return false
	}
	right, _, err := canonicalDefinition(element, wanted)
	if err != nil {
		return false
	}
	return left == right
}

// undefinedReferences are the definitions this one names that the target does
// not have and the package does not add first.
func (v *validation) undefinedReferences(s *SchemaChange) []string {
	var refs []string
	switch s.Element {
	case ElementAttributeType:
		at, err := schema.ParseAttributeType(s.Definition)
		if err != nil {
			return nil
		}
		if at.SuperName != "" {
			refs = append(refs, at.SuperName)
		}
		var out []string
		for _, ref := range refs {
			if v.sch.AttributeType(ref) == nil && !v.provided["name:"+strings.ToLower(ref)] && !v.provided["oid:"+strings.ToLower(ref)] {
				out = append(out, ref)
			}
		}
		return out
	case ElementObjectClass:
		oc, err := schema.ParseObjectClass(s.Definition)
		if err != nil {
			return nil
		}
		var out []string
		for _, ref := range oc.SuperNames {
			if v.sch.ObjectClass(ref) == nil && !v.provided["name:"+strings.ToLower(ref)] && !v.provided["oid:"+strings.ToLower(ref)] {
				out = append(out, ref)
			}
		}
		for _, ref := range append(append([]string{}, oc.Must...), oc.May...) {
			if v.sch.AttributeType(ref) == nil && !v.provided["name:"+strings.ToLower(ref)] && !v.provided["oid:"+strings.ToLower(ref)] {
				out = append(out, ref)
			}
		}
		return out
	}
	return nil
}

// referrer names a definition in the target's schema that still refers to the
// one being removed, or "".
func (v *validation) referrer(s *SchemaChange) string {
	switch s.Element {
	case ElementAttributeType:
		must, may := v.sch.UsedBy(s.OID)
		for _, oc := range append(must, may...) {
			return oc.Name()
		}
	case ElementObjectClass:
		if oc := v.sch.ObjectClass(s.OID); oc != nil {
			for _, sub := range v.sch.SubclassesOf(oc) {
				return sub.Name()
			}
		}
	}
	return ""
}

// schemaTargetFor is the schema entry this change is aimed at: the one holding
// the definition, for a change to an existing one, and the chosen entry for an
// addition.
func (v *validation) schemaTargetFor(ctx context.Context, s *SchemaChange, exists bool) (string, *Problem) {
	write := v.caps.SchemaWrite
	if exists && write.Style == directory.SchemaStyleConfig {
		holder := v.collectionOf(ctx, s.OID)
		if holder == "" {
			return "", &Problem{Code: ProblemServerDefined, Subject: s.OID,
				Detail: "this connection cannot see which schema entry holds that definition"}
		}
		return holder, nil
	}
	if v.opts.SchemaEntry != "" {
		if _, ok := write.Target(v.opts.SchemaEntry); !ok {
			return "", &Problem{Code: ProblemSchemaTargetRequired, Subject: v.opts.SchemaEntry,
				Detail: "that is not a schema entry this connection can write to"}
		}
		return v.opts.SchemaEntry, nil
	}
	if len(write.Targets) == 1 {
		return write.Targets[0].DN, nil
	}
	return "", &Problem{Code: ProblemSchemaTargetRequired, Subject: s.OID,
		Detail: "this server keeps schema in several entries, and none was chosen"}
}

// collectionOf finds the schema entry holding an OID, reading the entries once
// per validation.
func (v *validation) collectionOf(ctx context.Context, oid string) string {
	if !v.collectionsSet {
		v.collections = map[string]string{}
		v.collectionsSet = true
		for _, target := range v.caps.SchemaWrite.Targets {
			for _, kind := range []directory.SchemaDefKind{directory.SchemaDefAttributeType, directory.SchemaDefObjectClass} {
				stored, err := v.target.SchemaDefinitions(ctx, target.DN, kind)
				if err != nil {
					continue
				}
				for _, def := range stored {
					if found := definitionOID(def); found != "" {
						v.collections[strings.ToLower(found)] = target.DN
					}
				}
			}
		}
	}
	return v.collections[strings.ToLower(oid)]
}

// definitionOID is the OID a stored definition begins with, after any load
// order prefix the server keeps.
func definitionOID(def string) string {
	text := strings.TrimSpace(def)
	if strings.HasPrefix(text, "{") {
		if end := strings.IndexByte(text, '}'); end > 0 {
			text = strings.TrimSpace(text[end+1:])
		}
	}
	text = strings.TrimPrefix(text, "(")
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, " \t"); i > 0 {
		return text[:i]
	}
	return ""
}
