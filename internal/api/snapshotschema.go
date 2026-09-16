package api

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/diff"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/session"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Schema snapshots and schema comparisons.
//
// A schema snapshot is the published subschema, captured as a document; a
// schema comparison is two of them, or one and the live schema. Like the rest
// of the snapshot handlers nothing here writes: the only road from a schema
// difference to the directory is a candidate change request, and it goes to
// /plan like every other.

// schemaUsageBudget bounds how many "is this definition used" searches one
// comparison runs. Past it, usage is reported unknown.
const schemaUsageBudget = 50

// The read a schema side stands for, reported in the data-shaped fields every
// snapshot summary has.
const (
	schemaSideScope  = "base"
	schemaSideFilter = "(objectClass=subschema)"
)

// schemaCollections maps each definition's OID to the configuration collection
// holding it, read now rather than taken from what the session found when it
// connected: a definition added since would otherwise have no collection, and
// could not be changed again until the session reconnected. Nil when the server
// keeps no collections or they cannot all be read -- a map covering some of
// them would make a definition in an unread one look server-defined.
func schemaCollections(ctx context.Context, sess *session.Session) map[string]string {
	w := sess.Conn.Capabilities().SchemaWrite
	if w.Style != directory.SchemaStyleConfig || len(w.Targets) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, t := range w.Targets {
		for _, kind := range []directory.SchemaDefKind{directory.SchemaDefAttributeType, directory.SchemaDefObjectClass} {
			stored, err := sess.Conn.SchemaDefinitions(ctx, t.DN, kind)
			if err != nil {
				return nil
			}
			for _, def := range stored {
				if oid := storedDefinitionOID(def); oid != "" {
					out[oid] = t.Name
				}
			}
		}
	}
	return out
}

var storedOID = regexp.MustCompile(`^\s*(?:\{\d+\})?\s*\(\s*([^\s()']+)`)

// storedDefinitionOID is the OID a stored definition begins with, after any
// load-order prefix.
func storedDefinitionOID(def string) string {
	if m := storedOID.FindStringSubmatch(def); m != nil {
		return m[1]
	}
	return ""
}

// captureSchema reads the published schema as a schema snapshot.
func captureSchema(ctx context.Context, sess *session.Session, collections map[string]string) (*snapshot.SchemaSnapshot, error) {
	sch, err := sess.Conn.RefreshSchema(ctx)
	if err != nil {
		return nil, err
	}
	caps := sess.Conn.Capabilities()
	return snapshot.BuildSchema(snapshot.SchemaCapture{
		Vendor: caps.VendorName, VendorVersion: caps.VendorVersion, SubschemaEntry: caps.SubschemaSubentry,
		Collections: collections, CreatedAt: time.Now(),
	}, sch)
}

// captureSchemaSnapshot answers a capture request for kind schema.
func (s *Server) captureSchemaSnapshot(c *fiber.Ctx, sess *session.Session) error {
	ctx, cancel := reqCtx(c)
	defer cancel()
	snap, err := captureSchema(ctx, sess, schemaCollections(ctx, sess))
	if err != nil {
		return s.snapshotFail(c, err)
	}
	var buf bytes.Buffer
	if err := snapshot.EncodeSchema(&buf, snap); err != nil {
		return s.fail(c, err)
	}
	name := "alder-schema-snapshot-schema-" + time.Now().UTC().Format("2006-01-02T150405Z") + ".json"
	if entry, err := dn.Parse(snap.Source.SubschemaEntry); err == nil {
		name = strings.Replace(snapshotFilename(entry, time.Now()), "alder-snapshot-", "alder-schema-snapshot-", 1)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", name))
	return c.Send(buf.Bytes())
}

// refuseSnapshotKind answers a request for a kind Alder does not capture.
func refuseSnapshotKind(c *fiber.Ctx, kind string) error {
	if strings.EqualFold(kind, "config") {
		return writeError(c, fiber.StatusBadRequest, ErrorErrorSnapshotScopeUnsupported,
			"Server configuration is not captured.",
			"Snapshots are of directory data or of the published schema. There is no configuration snapshot kind.")
	}
	return badRequest(c, "The kind must be data or schema.", "")
}

func schemaInspection(snap *snapshot.SchemaSnapshot, integrity snapshot.Integrity) SnapshotInspection {
	src := schemaSideSource(snap)
	return SnapshotInspection{
		Version: snap.Version, Kind: snap.Kind, CreatedAt: snap.CreatedAt, Source: src,
		OperationalAttributes: true, SchemaAvailable: true, Excluded: []SnapshotExcluded{}, EntryCount: 1,
		Checksum: snap.Checksum, Integrity: SnapshotIntegrity(integrity),
		Schema: &SchemaSnapshotSummary{
			SubschemaEntry: snap.Source.SubschemaEntry, Collections: snap.Source.Collections,
			Completeness: SchemaSnapshotSummaryCompleteness(snap.Completeness),
			Counts: SchemaSnapshotCounts{
				AttributeTypes: snap.Counts.AttributeTypes, ObjectClasses: snap.Counts.ObjectClasses,
				LdapSyntaxes: snap.Counts.LDAPSyntaxes, MatchingRules: snap.Counts.MatchingRules,
				MatchingRuleUse: snap.Counts.MatchingRuleUse, DitContentRules: snap.Counts.DITContentRules,
				NameForms: snap.Counts.NameForms, Unparsed: snap.Counts.Unparsed,
			},
		},
	}
}

func schemaSideSource(snap *snapshot.SchemaSnapshot) SnapshotSource {
	out := SnapshotSource{Base: snap.Source.SubschemaEntry, Scope: SnapshotScope(schemaSideScope), Filter: schemaSideFilter}
	if snap.Source.Vendor != "" {
		out.Vendor = ptr(snap.Source.Vendor)
	}
	if snap.Source.VendorVersion != "" {
		out.VendorVersion = ptr(snap.Source.VendorVersion)
	}
	return out
}

// diffKind settles what a comparison is of, from the kinds its snapshots claim
// and its live side asks for. A live side that names no kind is the other
// side's. It answers the request itself when the sides cannot be compared.
func diffKind(c *fiber.Ctx, body diffBody) (string, bool) {
	kinds := map[string]string{}
	for _, name := range []string{"source", "target"} {
		side := body.Source
		if name == "target" {
			side = body.Target
		}
		if side.Live != nil {
			if side.Live.Kind != nil {
				k := string(*side.Live.Kind)
				if k != snapshot.KindData && k != snapshot.KindSchema {
					return "", fail(refuseSnapshotKind(c, k))
				}
				kinds[name] = k
			}
			continue
		}
		k, err := snapshot.KindOf(side.Snapshot)
		if err != nil {
			return "", fail(snapshotRefusal(c, name, err))
		}
		kinds[name] = k
	}
	if _, ok := kinds["source"]; !ok {
		kinds["source"] = kinds["target"]
	}
	if _, ok := kinds["target"]; !ok {
		kinds["target"] = kinds["source"]
	}
	source, target := kinds["source"], kinds["target"]
	if source != target && (source == snapshot.KindSchema || target == snapshot.KindSchema) {
		return "", fail(badRequest(c, "Schema is compared only with schema.",
			fmt.Sprintf("The source is %s and the target is %s.", source, target)))
	}
	if source == snapshot.KindSchema {
		return snapshot.KindSchema, true
	}
	// Anything else is read as data, and the data reader refuses what is not.
	return snapshot.KindData, true
}

type resolvedSchemaSide struct {
	side      diff.SchemaSide
	integrity snapshot.Integrity
}

// diffSchema compares two schema states.
func (s *Server) diffSchema(c *fiber.Ctx, sess *session.Session, body diffBody) error {
	sides := map[string]*resolvedSchemaSide{}
	for name, side := range map[string]diffSideBody{"source": body.Source, "target": body.Target} {
		if side.Live != nil {
			l := side.Live
			if l.Base != nil || l.Scope != nil || l.Filter != nil || l.OperationalAttributes != nil {
				return badRequest(c, "The live schema is read whole.",
					"base, scope, filter and operationalAttributes do not apply to a schema side.")
			}
			if l.SchemaTarget != nil && name != "source" {
				return badRequest(c, "schemaTarget applies to a live source only.",
					"It names where an added definition would be written, and only a live source is changed.")
			}
			continue
		}
		snap, integrity, err := snapshot.DecodeSchema(side.Snapshot)
		if err != nil {
			return snapshotRefusal(c, name, err)
		}
		sides[name] = &resolvedSchemaSide{side: diff.SchemaSide{Snapshot: snap}, integrity: integrity}
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	var target *diff.SchemaTarget
	for name, side := range map[string]diffSideBody{"source": body.Source, "target": body.Target} {
		if side.Live == nil {
			continue
		}
		collections := schemaCollections(ctx, sess)
		snap, err := captureSchema(ctx, sess, collections)
		if err != nil {
			return s.snapshotFail(c, err)
		}
		sides[name] = &resolvedSchemaSide{side: diff.SchemaSide{Snapshot: snap, Live: true}}
		if name != "source" {
			continue
		}
		write := sess.Conn.Capabilities().SchemaWrite
		write.Origin = collections
		t := &diff.SchemaTarget{Write: write, Stored: storedReader(ctx, sess), Usage: usageProbe(ctx, sess)}
		if side.Live.SchemaTarget != nil {
			wanted, err := dn.Parse(strings.TrimSpace(*side.Live.SchemaTarget))
			if err != nil {
				return badRequest(c, "schemaTarget is not a DN.", err.Error())
			}
			for _, candidate := range write.Targets {
				if parsed, perr := dn.Parse(candidate.DN); perr == nil && parsed.Equal(wanted) {
					t.AddTarget = candidate.DN
				}
			}
			if t.AddTarget == "" {
				return badRequest(c, "schemaTarget is not a schema entry this connection can write to.", "")
			}
		}
		target = t
	}

	result := diff.CompareSchema(sides["source"].side, sides["target"].side, diff.SchemaOptions{IncludeUnchanged: body.IncludeUnchanged})
	return c.JSON(schemaDiffView(result, sides["source"], sides["target"], target))
}

// storedReader reads a schema entry's stored definitions once per entry and
// kind, however many differences need them.
func storedReader(ctx context.Context, sess *session.Session) func(string, directory.SchemaDefKind) ([]string, error) {
	type key struct {
		dn   string
		kind directory.SchemaDefKind
	}
	type read struct {
		values []string
		err    error
	}
	cache := map[key]read{}
	return func(target string, kind directory.SchemaDefKind) ([]string, error) {
		k := key{strings.ToLower(target), kind}
		if r, ok := cache[k]; ok {
			return r.values, r.err
		}
		values, err := sess.Conn.SchemaDefinitions(ctx, target, kind)
		cache[k] = read{values, err}
		return values, err
	}
}

// usageProbe asks whether any entry this session can see uses a definition:
// one entry is enough, and it looks no further. It never reports a definition
// unused -- finding nothing, being refused, and running out of budget all read
// as not known, because an identity that sees no entry using an attribute has
// not shown that none does.
func usageProbe(ctx context.Context, sess *session.Session) func(string, string, []string) (bool, bool) {
	asked := 0
	return func(element, oid string, names []string) (bool, bool) {
		if asked >= schemaUsageBudget {
			return false, false
		}
		asked++
		var f filter.Filter
		switch element {
		case diff.ElementAttributeType:
			ref := oid
			if len(names) > 0 {
				ref = names[0]
			}
			f = filter.Present(ref)
		case diff.ElementObjectClass:
			alternatives := []filter.Filter{filter.Equal("objectClass", oid)}
			for _, n := range names {
				alternatives = append(alternatives, filter.Equal("objectClass", n))
			}
			f = filter.Or(alternatives...)
		default:
			return false, false
		}
		if f.Err() != nil {
			return false, false
		}
		for _, root := range sess.Conn.Capabilities().NamingContexts {
			base, err := dn.Parse(root)
			if err != nil || base.IsEmpty() {
				continue
			}
			res, err := sess.Conn.Search(ctx, directory.SearchRequest{
				BaseDN: base, Scope: directory.ScopeSubtree, Filter: f, Attributes: []string{"1.1"}, Limit: 1, PageSize: 1,
			})
			if err != nil {
				continue
			}
			if len(res.Entries) > 0 {
				return true, true
			}
		}
		return false, false
	}
}

func schemaDiffView(r *diff.SchemaResult, source, target *resolvedSchemaSide, t *diff.SchemaTarget) Diff {
	at, oc := r.AttributeTypes, r.ObjectClasses
	out := Diff{
		Kind:        StateKindSchema,
		Source:      schemaSideSummary(source),
		Target:      schemaSideSummary(target),
		Complete:    r.Complete,
		CrossVendor: r.CrossVendor,
		// The data-shaped totals, for a client that reads only those: a
		// difference only in extensions is not a change to anything.
		Counts: DiffCounts{
			Compared: at.Compared + oc.Compared, Added: at.Added + oc.Added, Removed: at.Removed + oc.Removed,
			Modified: at.Modified + oc.Modified, Unchanged: at.Unchanged + oc.Unchanged + at.MetadataOnly + oc.MetadataOnly,
			Unknown: at.Unknown + oc.Unknown,
		},
		Items: []DiffItem{},
		Schema: &SchemaDiff{
			AttributeTypes: schemaCounts(at), ObjectClasses: schemaCounts(oc),
			Items: make([]SchemaDiffItem, 0, len(r.Items)), Order: nonNil(r.Order),
		},
	}
	if len(r.Reasons) > 0 {
		reasons := make([]DiffReason, 0, len(r.Reasons))
		for _, reason := range r.Reasons {
			reasons = append(reasons, DiffReason{Code: DiffReasonCode(reason.Code), Detail: reason.Detail})
		}
		out.Reasons = &reasons
	}
	for _, item := range r.Items {
		view := SchemaDiffItem{Key: item.Key(), Element: SchemaElementKind(item.Element), Oid: item.OID, Kind: DiffKind(item.Kind)}
		if len(item.Names) > 0 {
			view.Names = ptr(item.Names)
		}
		if len(item.Fields) > 0 {
			fields := make([]SchemaFieldChange, 0, len(item.Fields))
			for _, f := range item.Fields {
				change := SchemaFieldChange{Field: f.Field, Category: SchemaFieldCategory(f.Category)}
				if len(f.Source) > 0 {
					change.Source = ptr(f.Source)
				}
				if len(f.Target) > 0 {
					change.Target = ptr(f.Target)
				}
				fields = append(fields, change)
			}
			view.Fields = &fields
		}
		view.SourceDefinition, view.TargetDefinition = ptrIfSet(item.SourceDefinition), ptrIfSet(item.TargetDefinition)
		view.SourceCollection, view.TargetCollection = ptrIfSet(item.SourceCollection), ptrIfSet(item.TargetCollection)
		if len(item.Requires) > 0 {
			view.Requires = ptr(schemaRefs(item.Requires))
		}
		if len(item.RequiredBy) > 0 {
			view.RequiredBy = ptr(schemaRefs(item.RequiredBy))
		}
		if len(item.Problems) > 0 {
			view.Problems = ptr(schemaProblems(item.Problems))
		}
		if t != nil {
			view.Candidate = schemaCandidateView(diff.DeriveSchema(r, item, *t))
		}
		out.Schema.Items = append(out.Schema.Items, view)
	}
	return out
}

func schemaCounts(c diff.SchemaCounts) SchemaDiffCounts {
	return SchemaDiffCounts{Compared: c.Compared, Added: c.Added, Removed: c.Removed, Modified: c.Modified,
		MetadataOnly: c.MetadataOnly, Unchanged: c.Unchanged, Unknown: c.Unknown}
}

func schemaRefs(refs []diff.SchemaRef) []SchemaReference {
	out := make([]SchemaReference, 0, len(refs))
	for _, ref := range refs {
		out = append(out, SchemaReference{Element: SchemaElementKind(ref.Element), Oid: ref.OID,
			Name: ptrIfSet(ref.Name), Relation: SchemaReferenceRelation(ref.Relation)})
	}
	return out
}

func schemaProblems(codes []string) []SchemaProblem {
	out := make([]SchemaProblem, 0, len(codes))
	for _, code := range codes {
		out = append(out, SchemaProblem(code))
	}
	return out
}

func schemaCandidateView(c diff.SchemaCandidate) *SchemaDiffCandidate {
	out := &SchemaDiffCandidate{Changes: make([]ChangeRequest, 0, len(c.Records)), Destructive: c.Destructive}
	for _, rec := range c.Records {
		out.Changes = append(out.Changes, changeRequest(rec))
	}
	if c.Blocked != "" {
		out.Blocked = ptr(DiffCandidateBlocked(c.Blocked))
	}
	if len(c.Impact) > 0 {
		out.Impact = ptr(schemaProblems(c.Impact))
	}
	if len(c.Requires) > 0 {
		out.Requires = ptr(c.Requires)
	}
	return out
}

func schemaSideSummary(side *resolvedSchemaSide) DiffSideSummary {
	snap := side.side.Snapshot
	out := DiffSideSummary{
		Kind: DiffSideKindSnapshot, Base: snap.Source.SubschemaEntry, Scope: SnapshotScope(schemaSideScope),
		Filter: schemaSideFilter, OperationalAttributes: true, EntryCount: 1, DefinitionCount: ptr(snap.ElementCount()),
		Vendor: ptrIfSet(snap.Source.Vendor),
	}
	if side.side.Live {
		out.Kind = DiffSideKindLive
		return out
	}
	out.CreatedAt = ptr(snap.CreatedAt)
	out.Checksum = ptr(snap.Checksum)
	out.Integrity = ptr(SnapshotIntegrity(side.integrity))
	return out
}
