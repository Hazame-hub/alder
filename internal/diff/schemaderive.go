package diff

import (
	"errors"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Turning a schema difference into plan input.
//
// The same rules as for data, and the same single destination: the change
// records a candidate holds are the ones the schema editor builds, through
// directory.BuildSchemaChange, and they go to the plan like any other change.
// Nothing here writes, and there is no schema apply of its own.
//
//   - The source must be the live directory, and its schema writable.
//   - An added definition becomes an add of the target's definition, without
//     the extensions that only record where a definition came from.
//   - A modified definition becomes a replace, in whatever form the server's
//     arrangement needs; that decision belongs to BuildSchemaChange.
//   - A removed definition becomes a delete, marked destructive, only when the
//     comparison is complete, and never selected on anyone's behalf.
//   - A difference only in metadata proposes nothing.
//   - A definition the server itself defines -- not held in a collection this
//     session can write, or not marked as added by an administrator where the
//     server marks that -- is reported, and not proposed as a modification or
//     a removal.

// Reasons a schema difference proposes no change.
const (
	BlockedMetadataOnly         = "metadata_only"
	BlockedSchemaNotEditable    = "schema_not_editable"
	BlockedSchemaTargetRequired = "schema_target_required"
	BlockedServerDefined        = "server_defined"
	BlockedNonNumericOID        = "non_numeric_oid"
	BlockedUnresolvedReference  = "unresolved_reference"
	BlockedDependencyCycle      = "dependency_cycle"
	BlockedUnbuildable          = "unbuildable_definition"
)

// provenanceExtensions record where a definition came from rather than what
// it is. A definition carried to another server does not carry them: the
// server that holds it records its own.
var provenanceExtensions = map[string]bool{"X-ORIGIN": true, "X-SCHEMA-FILE": true}

// userDefinedOrigin is the X-ORIGIN value a server whose subschema is
// directly writable gives the definitions an administrator added, and the
// only ones it lets be changed or removed.
const userDefinedOrigin = "user defined"

// SchemaTarget is what deriving a schema change needs from the live session.
type SchemaTarget struct {
	Write directory.SchemaWrite
	// Stored returns the definitions a schema entry holds, as stored.
	Stored func(targetDN string, kind directory.SchemaDefKind) ([]string, error)
	// AddTarget is the schema entry an added definition is written to. Empty
	// is only enough where the server has one.
	AddTarget string
	// Usage reports whether directory entries use a definition. It is asked
	// only about removals.
	Usage func(element, oid string, names []string) (used bool, known bool)
}

// SchemaCandidate is what a schema difference would become as plan input.
type SchemaCandidate struct {
	Records     []directory.ChangeRecord
	Destructive bool
	Blocked     string
	// Impact is what is known about the consequences, as problem codes:
	// referenced_by_schema, used_by_entries, usage_unknown.
	Impact []string
	// Requires lists the keys of other items whose change must be applied
	// before this one.
	Requires []string
}

// DeriveSchema turns one schema difference into change records that would move
// the live source toward the target.
func DeriveSchema(r *SchemaResult, item SchemaItem, t SchemaTarget) SchemaCandidate {
	if !r.source.Live {
		return SchemaCandidate{Blocked: BlockedSourceNotLive}
	}
	destructive := item.Kind == Removed
	switch item.Kind {
	case Unchanged:
		return SchemaCandidate{Blocked: BlockedNothingToChange}
	case Unknown:
		return SchemaCandidate{Destructive: destructive, Blocked: BlockedUnknown}
	case MetadataOnly:
		return SchemaCandidate{Blocked: BlockedMetadataOnly}
	}
	if hasProblem(item.Problems, ProblemNonNumericOID) {
		return SchemaCandidate{Destructive: destructive, Blocked: BlockedNonNumericOID}
	}
	if hasProblem(item.Problems, ProblemDependencyCycle) {
		return SchemaCandidate{Destructive: destructive, Blocked: BlockedDependencyCycle}
	}
	if item.Kind != Removed && hasProblem(item.Problems, ProblemUnresolvedReference) {
		return SchemaCandidate{Blocked: BlockedUnresolvedReference}
	}
	if destructive && !r.Complete {
		return SchemaCandidate{Destructive: true, Blocked: BlockedIncomplete}
	}
	if !t.Write.Editable() {
		return SchemaCandidate{Destructive: destructive, Blocked: BlockedSchemaNotEditable}
	}

	kind := directory.SchemaDefAttributeType
	if item.Element == ElementObjectClass {
		kind = directory.SchemaDefObjectClass
	}
	cand := SchemaCandidate{Destructive: destructive, Requires: requiredKeys(r, item)}

	req := directory.SchemaChangeRequest{Kind: kind}
	switch item.Kind {
	case Added:
		target, ok := addTarget(t)
		if !ok {
			return SchemaCandidate{Blocked: BlockedSchemaTargetRequired}
		}
		def, err := targetDefinition(item)
		if err != nil {
			return SchemaCandidate{Blocked: BlockedUnbuildable}
		}
		req.TargetDN, req.Op, req.Definition = target, directory.SchemaOpAdd, def
	case Modified, Removed:
		target, ok := existingTarget(t.Write, item)
		if !ok {
			return SchemaCandidate{Destructive: destructive, Blocked: BlockedServerDefined}
		}
		req.TargetDN, req.OID = target, item.OID
		if item.Kind == Modified {
			def, err := targetDefinition(item)
			if err != nil {
				return SchemaCandidate{Blocked: BlockedUnbuildable}
			}
			req.Op, req.Definition = directory.SchemaOpReplace, def
		} else {
			req.Op = directory.SchemaOpDelete
			if len(item.RequiredBy) > 0 {
				cand.Impact = append(cand.Impact, ProblemReferencedBySchema)
			}
			if t.Usage != nil {
				switch used, known := t.Usage(item.Element, item.OID, item.Names); {
				case used:
					cand.Impact = append(cand.Impact, ProblemUsedByEntries)
				case !known:
					cand.Impact = append(cand.Impact, ProblemUsageUnknown)
				default:
					// A search that finds nothing proves nothing about entries
					// this session cannot see.
					cand.Impact = append(cand.Impact, ProblemUsageUnknown)
				}
			}
		}
	default:
		return SchemaCandidate{Blocked: BlockedNothingToChange}
	}

	var stored []string
	if t.Stored != nil {
		var err error
		if stored, err = t.Stored(req.TargetDN, kind); err != nil {
			return SchemaCandidate{Destructive: destructive, Blocked: BlockedUnbuildable}
		}
	}
	rec, err := directory.BuildSchemaChange(t.Write, req, stored)
	if err != nil {
		if errors.Is(err, directory.ErrDefinitionNotFound) {
			return SchemaCandidate{Destructive: destructive, Blocked: BlockedServerDefined}
		}
		return SchemaCandidate{Destructive: destructive, Blocked: BlockedUnbuildable}
	}
	cand.Records = []directory.ChangeRecord{rec}
	return cand
}

func requiredKeys(r *SchemaResult, item SchemaItem) []string {
	var out []string
	refs := item.Requires
	if item.Kind == Removed {
		refs = item.RequiredBy
	}
	for _, ref := range refs {
		if ref.OID == "" {
			continue
		}
		key := ref.Element + ":" + strings.ToLower(ref.OID)
		dep, ok := r.Item(key)
		if !ok {
			continue
		}
		switch {
		case item.Kind == Removed && (dep.Kind == Removed || dep.Kind == Modified),
			item.Kind != Removed && (dep.Kind == Added || dep.Kind == Modified):
			out = append(out, key)
		}
	}
	return out
}

func addTarget(t SchemaTarget) (string, bool) {
	if t.AddTarget != "" {
		_, ok := t.Write.Target(t.AddTarget)
		return t.AddTarget, ok
	}
	if len(t.Write.Targets) == 1 {
		return t.Write.Targets[0].DN, true
	}
	return "", false
}

// existingTarget finds the schema entry that holds a definition this session
// may change: the collection holding it, where schema is kept in collections;
// the one writable entry otherwise, for a definition the server marks as added
// by an administrator.
func existingTarget(w directory.SchemaWrite, item SchemaItem) (string, bool) {
	switch w.Style {
	case directory.SchemaStyleConfig:
		collection := w.Origin[item.OID]
		if collection == "" {
			return "", false
		}
		for _, t := range w.Targets {
			if t.Name == collection {
				return t.DN, true
			}
		}
		return "", false
	case directory.SchemaStyleSubschema:
		if len(w.Targets) != 1 || !userDefined(item) {
			return "", false
		}
		return w.Targets[0].DN, true
	}
	return "", false
}

// userDefined reports whether the source definition carries the X-ORIGIN value
// a directly writable subschema gives administrator-added definitions.
func userDefined(item SchemaItem) bool {
	var ext map[string][]string
	switch {
	case item.sourceAT != nil:
		ext = item.sourceAT.Extensions
	case item.sourceOC != nil:
		ext = item.sourceOC.Extensions
	}
	for _, v := range ext["X-ORIGIN"] {
		if strings.EqualFold(strings.TrimSpace(v), userDefinedOrigin) {
			return true
		}
	}
	return false
}

// targetDefinition renders the target's definition for sending: its parsed
// form without provenance extensions, rendered by the same code the schema
// editor uses.
func targetDefinition(item SchemaItem) (string, error) {
	switch {
	case item.targetAT != nil:
		return AttributeTypeFor(item.targetAT).Definition()
	case item.targetOC != nil:
		return ObjectClassFor(item.targetOC).Definition()
	}
	return "", errors.New("diff: the item has no target definition")
}

func withoutProvenance(ext map[string][]string) schema.Extensions {
	if len(ext) == 0 {
		return nil
	}
	out := schema.Extensions{}
	for k, v := range ext {
		if provenanceExtensions[strings.ToUpper(k)] {
			continue
		}
		out[k] = append([]string{}, v...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AttributeTypeFor is a snapshot attribute type as the schema package's type,
// without provenance extensions.
func AttributeTypeFor(at *snapshot.SchemaAttributeType) schema.AttributeType {
	usage := schema.UsageUserApplications
	switch strings.ToLower(at.Usage) {
	case "directoryoperation":
		usage = schema.UsageDirectoryOperation
	case "distributedoperation":
		usage = schema.UsageDistributedOperation
	case "dsaoperation":
		usage = schema.UsageDSAOperation
	}
	return schema.AttributeType{
		OID: at.OID, Names: at.Names, Desc: at.Desc, Obsolete: at.Obsolete, SuperName: at.Sup,
		Equality: at.Equality, Ordering: at.Ordering, Substr: at.Substr, Syntax: at.Syntax, SyntaxLen: at.SyntaxLength,
		SingleValue: at.SingleValue, Collective: at.Collective, NoUserModification: at.NoUserModification,
		Usage: usage, Extensions: withoutProvenance(at.Extensions),
	}
}

// ObjectClassFor is a snapshot object class as the schema package's type,
// without provenance extensions.
func ObjectClassFor(oc *snapshot.SchemaObjectClass) schema.ObjectClass {
	kind := schema.KindStructural
	switch strings.ToUpper(oc.Kind) {
	case "ABSTRACT":
		kind = schema.KindAbstract
	case "AUXILIARY":
		kind = schema.KindAuxiliary
	}
	return schema.ObjectClass{
		OID: oc.OID, Names: oc.Names, Desc: oc.Desc, Obsolete: oc.Obsolete, SuperNames: oc.Sup, Kind: kind,
		Must: oc.Must, May: oc.May, Extensions: withoutProvenance(oc.Extensions),
	}
}
