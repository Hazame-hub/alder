package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/hazame-hub/alder/internal/signing"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Snapshots and comparisons.
//
// Nothing in this file writes to a directory, and nothing is kept: a snapshot
// is returned to its caller, and a comparison is computed from what the request
// carries and what the directory holds right now. The only road from a
// difference to the directory is the candidate change requests a comparison
// proposes, and those go to /plan like anything else.

// snapshotProbeBudget bounds how many attribute-visibility questions one
// comparison asks the live directory. Past it, an attribute the live side lacks
// is reported unknown rather than asked about.
const snapshotProbeBudget = 500

type liveRequest struct {
	base        dn.DN
	scope       string
	rawFilter   string
	parsed      filter.Filter
	operational bool
}

// liveRequestFrom validates what a caller asked to read, answering the request
// itself when it is unusable.
func (s *Server) liveRequestFrom(c *fiber.Ctx, r directory.Reader, base, scope, rawFilter string, operational bool) (liveRequest, bool) {
	parsedBase, err := dn.Parse(strings.TrimSpace(base))
	if err != nil {
		return liveRequest{}, fail(badRequest(c, "The base is not a DN.", err.Error()))
	}
	if scope == "" {
		scope = "sub"
	}
	if _, err := directory.ParseScope(scope); err != nil {
		return liveRequest{}, fail(badRequest(c, "The scope must be base, one or sub.", err.Error()))
	}
	req := liveRequest{base: parsedBase, scope: scope, operational: operational}
	req.rawFilter = strings.TrimSpace(rawFilter)
	if req.rawFilter == "" {
		req.rawFilter = "(objectClass=*)"
	}
	if req.parsed, err = filter.Parse(req.rawFilter); err != nil {
		return liveRequest{}, fail(badRequest(c, "The filter is not a valid RFC 4515 filter.", err.Error()))
	}
	// A data snapshot only. The schema and configuration trees differ between
	// servers in ways a data comparison would misreport -- ordered values,
	// server-maintained entries -- so they are refused rather than captured as
	// if they were ordinary entries.
	if kind := targetKind(r.Capabilities(), parsedBase); kind != PlanTargetData {
		return liveRequest{}, fail(writeError(c, fiber.StatusBadRequest, ErrorErrorSnapshotScopeUnsupported,
			fmt.Sprintf("A data snapshot of the %s is not supported; its base must be directory data.", kind),
			"Capture the schema as a schema snapshot (kind schema). Server configuration is not captured."))
	}
	return req, true
}

// fail lets a validation helper answer the request and report that it did.
func fail(error) bool { return false }

// captureLive reads a subtree and builds its snapshot. truncated reports that
// the read stopped short of the whole subtree -- more entries than a snapshot
// holds, a server size limit, or referrals to entries held elsewhere -- in which
// case the snapshot holds only what was read.
func (s *Server) captureLive(ctx context.Context, r directory.Reader, req liveRequest) (*snapshot.Snapshot, bool, error) {
	sch, _ := r.Schema(ctx)
	scope, _ := directory.ParseScope(req.scope)
	attrs := append([]string{"*"}, snapshot.IdentityAttributes...)
	if req.operational {
		attrs = append(attrs, "+")
	}

	var entries []*directory.Entry
	var cookie []byte
	truncated := false
	for {
		want := snapshot.MaxEntries + 1 - len(entries)
		if want > directory.MaxPageSize {
			want = directory.MaxPageSize
		}
		res, err := r.Search(ctx, directory.SearchRequest{
			BaseDN: req.base, Scope: scope, Filter: req.parsed,
			Attributes: attrs, Limit: want, PageSize: want, Cookie: cookie,
		})
		if err != nil {
			return nil, false, err
		}
		entries = append(entries, res.Entries...)
		// Truncated with a cookie is only "there is another page", which the
		// loop fetches. Truncated with none is a server size limit, and
		// referrals are entries held somewhere else: either way the read is not
		// the whole subtree.
		if len(res.Referrals) > 0 || (res.Truncated && len(res.Cookie) == 0) {
			truncated = true
			break
		}
		if len(entries) > snapshot.MaxEntries {
			entries = entries[:snapshot.MaxEntries]
			truncated = true
			break
		}
		if len(res.Cookie) == 0 {
			break
		}
		cookie = res.Cookie
	}

	caps := r.Capabilities()
	snap, err := snapshot.Build(snapshot.Capture{
		Base: req.base, Scope: req.scope, Filter: req.rawFilter,
		Vendor: caps.VendorName, VendorVersion: caps.VendorVersion,
		Operational: req.operational, CreatedAt: time.Now(),
	}, sch, entries)
	if err != nil {
		return nil, false, err
	}
	return snap, truncated, nil
}

// CaptureSnapshot captures a subtree as a snapshot document.
func (s *Server) CaptureSnapshot(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body SnapshotCaptureRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	kind := snapshot.KindData
	if body.Kind != nil {
		kind = string(*body.Kind)
	}
	// Either of the session's servers. A capture only reads, so the reference
	// is as good a source as the session's own.
	r, _, ok := onServer(c, sess, body.On)
	if !ok {
		return nil
	}
	switch kind {
	case snapshot.KindSchema:
		return s.captureSchemaSnapshot(c, r)
	case snapshot.KindConfig:
		// A configuration is one tree, the server's own, and there is nothing
		// to narrow: a base or a filter here means the request was written for
		// another kind, so it is refused rather than ignored. Kind schema goes
		// on ignoring them, because clients from 1.10 already send them.
		if body.Base != nil || body.Scope != nil || body.Filter != nil || body.OperationalAttributes != nil {
			return badRequest(c, "A configuration snapshot takes no base, scope, filter or operational attributes.",
				"The configuration is the tree the server announced, and there is nothing in it to narrow.")
		}
		return s.captureConfigSnapshot(c, r)
	case snapshot.KindData:
	default:
		return refuseSnapshotKind(c, kind)
	}
	if body.Base == nil || strings.TrimSpace(*body.Base) == "" {
		return badRequest(c, "A data snapshot needs a base.", "Name the subtree to capture, or capture the schema with kind schema.")
	}
	scope := ""
	if body.Scope != nil {
		scope = string(*body.Scope)
	}
	req, ok := s.liveRequestFrom(c, r, *body.Base, scope, deref(body.Filter), deref(body.OperationalAttributes))
	if !ok {
		return nil
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	snap, truncated, err := s.captureLive(ctx, r, req)
	if err != nil {
		return s.snapshotFail(c, err)
	}
	if truncated {
		return writeError(c, fiber.StatusBadRequest, ErrorErrorSnapshotTooLarge,
			"The subtree could not be captured whole, so no snapshot was made.",
			fmt.Sprintf("A snapshot holds at most %d entries, and the read stopped short of the whole subtree "+
				"(too many entries, a server size limit, or referrals). Narrow the base, scope or filter.", snapshot.MaxEntries))
	}
	if snap.EntryCount == 0 && req.scope != "base" {
		// An empty subtree is a legitimate snapshot, but an empty base is not
		// there at all; say which.
		if _, readErr := r.Read(ctx, req.base, []string{"1.1"}); readErr != nil {
			return s.fail(c, readErr)
		}
	}

	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snap); err != nil {
		return s.fail(c, err)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", snapshotFilename(req.base, time.Now())))
	return c.Send(buf.Bytes())
}

var unsafeFilename = regexp.MustCompile(`[^a-z0-9]+`)

// snapshotFilename is a name safe on any filesystem: the base folded to
// letters, digits and hyphens, bounded, then the capture time.
func snapshotFilename(base dn.DN, at time.Time) string {
	slug := strings.Trim(unsafeFilename.ReplaceAllString(strings.ToLower(base.String()), "-"), "-")
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	if slug == "" {
		slug = "directory"
	}
	return fmt.Sprintf("alder-snapshot-%s-%s.json", slug, at.UTC().Format("2006-01-02T150405Z"))
}

// InspectSnapshot validates a snapshot and describes it.
func (s *Server) InspectSnapshot(c *fiber.Ctx) error {
	if sess := s.require(c); sess == nil {
		return nil
	}
	document, signature, ok := s.openDocument(c, c.Body())
	if !ok {
		return nil
	}
	kind, err := snapshot.KindOf(document)
	if err != nil {
		return snapshotRefusal(c, "", err)
	}
	if kind == snapshot.KindSchema {
		snap, integrity, err := snapshot.DecodeSchema(document)
		if err != nil {
			return snapshotRefusal(c, "", err)
		}
		out := schemaInspection(snap, integrity)
		out.Signature = signatureView(signature)
		return c.JSON(out)
	}
	snap, integrity, err := snapshot.Decode(document)
	if err != nil {
		return snapshotRefusal(c, "", err)
	}
	out := inspection(snap, integrity)
	out.Signature = signatureView(signature)
	return c.JSON(out)
}

func inspection(snap *snapshot.Snapshot, integrity snapshot.Integrity) SnapshotInspection {
	return SnapshotInspection{
		Version:               snap.Version,
		Kind:                  snap.Kind,
		CreatedAt:             snap.CreatedAt,
		Source:                snapshotSource(snap.Source),
		OperationalAttributes: snap.OperationalAttributes,
		SchemaAvailable:       snap.SchemaAvailable,
		Excluded:              excludedList(snap.Excluded),
		EntryCount:            snap.EntryCount,
		Checksum:              snap.Checksum,
		Integrity:             SnapshotIntegrity(integrity),
	}
}

func snapshotSource(src snapshot.Source) SnapshotSource {
	out := SnapshotSource{Base: src.Base, Scope: SnapshotScope(src.Scope), Filter: src.Filter}
	if src.Vendor != "" {
		out.Vendor = ptr(src.Vendor)
	}
	if src.VendorVersion != "" {
		out.VendorVersion = ptr(src.VendorVersion)
	}
	return out
}

func excludedList(excluded []string) []SnapshotExcluded {
	out := make([]SnapshotExcluded, 0, len(excluded))
	for _, x := range excluded {
		out = append(out, SnapshotExcluded(x))
	}
	return out
}

// snapshotRefusal answers with the stable code for why a document is not a
// usable snapshot.
func snapshotRefusal(c *fiber.Ctx, side string, err error) error {
	var se *snapshot.Error
	if !errors.As(err, &se) {
		return badRequest(c, "The snapshot is not usable.", err.Error())
	}
	code := ErrorErrorSnapshotInvalid
	message := "The snapshot is not a valid Alder snapshot."
	switch se.Code {
	case snapshot.CodeUnsupportedVersion:
		code, message = ErrorErrorSnapshotUnsupportedVersion, "This snapshot's format version is not one this Alder reads."
	case snapshot.CodeChecksumMismatch:
		code, message = ErrorErrorSnapshotChecksumMismatch, "The snapshot does not match its checksum."
	case snapshot.CodeTooLarge:
		code, message = ErrorErrorSnapshotTooLarge, "The snapshot is larger than Alder compares."
	}
	detail := se.Detail
	if side != "" {
		detail = side + ": " + detail
	}
	return writeError(c, fiber.StatusBadRequest, code, message, detail)
}

func (s *Server) snapshotFail(c *fiber.Ctx, err error) error {
	var se *snapshot.Error
	if errors.As(err, &se) {
		return snapshotRefusal(c, "", err)
	}
	return s.fail(c, err)
}

type diffSideBody struct {
	Snapshot  json.RawMessage `json:"snapshot"`
	Live      *DiffLiveSide   `json:"live"`
	Reference *DiffLiveSide   `json:"reference"`
}

// hasSnapshot reports whether the side carries a document.
func (b diffSideBody) hasSnapshot() bool {
	trimmed := bytes.TrimSpace(b.Snapshot)
	return len(trimmed) > 0 && string(trimmed) != "null"
}

// read is what a side that is read from a directory asks for -- the
// session's own (live) or its reference -- or nil for a document.
func (b diffSideBody) read() *DiffLiveSide {
	if b.Live != nil {
		return b.Live
	}
	return b.Reference
}

type diffBody struct {
	Source           diffSideBody `json:"source"`
	Target           diffSideBody `json:"target"`
	IncludeUnchanged bool         `json:"includeUnchanged"`

	// signatures is what verification concluded about each side that was a
	// document, filled in before anything decodes one.
	signatures map[string]signing.Result
}

// sideNames is the order sides are resolved in. A map's order is not one,
// and a read side that takes its base from the other read side needs the
// other resolved first.
var sideNames = []string{"source", "target"}

// side returns the named side of the request.
func (b *diffBody) side(name string) diffSideBody {
	if name == "target" {
		return b.Target
	}
	return b.Source
}

// readSide is a side read from a directory just now: the directory, and the
// address it was read from. Only the session's own may be the source of a
// proposal; a reference side is read-only by type.
type readSide struct {
	reader    directory.Reader
	server    string
	reference bool
}

// readSideFor is the directory a side reads, answering the request itself
// when it names a reference the session does not have.
func readSideFor(c *fiber.Ctx, sess *session.Session, side diffSideBody) (readSide, bool) {
	on := ServerSideSession
	if side.Live == nil {
		on = ServerSideReference
	}
	r, server, ok := onServer(c, sess, &on)
	if !ok {
		return readSide{}, false
	}
	return readSide{reader: r, server: server, reference: on == ServerSideReference}, true
}

type resolvedSide struct {
	side      diff.Side
	integrity snapshot.Integrity
	read      readSide
}

// DiffStates compares two states.
func (s *Server) DiffStates(c *fiber.Ctx) error {
	var body diffBody
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	for _, name := range sideNames {
		side := body.side(name)
		given := 0
		for _, present := range []bool{side.hasSnapshot(), side.Live != nil, side.Reference != nil} {
			if present {
				given++
			}
		}
		if given != 1 {
			return badRequest(c, fmt.Sprintf("The %s must be exactly one of a snapshot, the live directory or the reference.", name), "")
		}
	}
	if body.Source.Live != nil && body.Target.Live != nil {
		return badRequest(c, "Compare a snapshot with the live directory, or two snapshots.",
			"Both sides live would compare this directory with itself.")
	}
	if body.Source.Reference != nil && body.Target.Reference != nil {
		return badRequest(c, "Compare the reference with the live directory or a snapshot.",
			"Both sides on the reference would compare that directory with itself.")
	}

	// A side that is a document may be a signed one. It is unwrapped here,
	// once, so every comparison below reads the payload and nothing downstream
	// has to know signing exists.
	body.signatures = map[string]signing.Result{}
	for _, side := range []struct {
		name string
		body *diffSideBody
	}{{"source", &body.Source}, {"target", &body.Target}} {
		if len(bytes.TrimSpace(side.body.Snapshot)) == 0 {
			continue
		}
		payload, result, ok := s.openDocument(c, side.body.Snapshot)
		if !ok {
			return nil
		}
		side.body.Snapshot = payload
		body.signatures[side.name] = result
	}

	// Two snapshots are documents the caller sent. Comparing them reads no
	// directory, so it needs no session and opens none -- it is still a request,
	// bounded by the body limit, the in-flight gate and the request timeout, and
	// its snapshots are decoded exactly as strictly. A live or reference side
	// reads a directory the session holds, so it needs a session, and without
	// one is refused before either snapshot is decoded. The comparison below
	// is the same code either way.
	var sess *session.Session
	reads := map[string]readSide{}
	if body.Source.read() != nil || body.Target.read() != nil {
		if sess = s.require(c); sess == nil {
			return nil
		}
		for _, name := range sideNames {
			if body.side(name).read() == nil {
				continue
			}
			r, ok := readSideFor(c, sess, body.side(name))
			if !ok {
				return nil
			}
			reads[name] = r
		}
	}
	kind, ok := diffKind(c, body)
	if !ok {
		return nil
	}
	if kind == snapshot.KindSchema {
		return s.diffSchema(c, reads, body)
	}
	if kind == snapshot.KindConfig {
		return s.diffConfig(c, reads, body)
	}

	sides := map[string]*resolvedSide{}
	for _, name := range sideNames {
		side := body.side(name)
		if side.read() != nil {
			continue
		}
		snap, integrity, err := snapshot.Decode(side.Snapshot)
		if err != nil {
			return snapshotRefusal(c, name, err)
		}
		sides[name] = &resolvedSide{side: diff.Side{Snapshot: snap}, integrity: integrity}
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	for _, name := range sideNames {
		asked := body.side(name).read()
		if asked == nil {
			continue
		}
		otherName := "source"
		if name == "source" {
			otherName = "target"
		}
		// Omitted fields come from the other side: its snapshot's capture,
		// or -- where both sides are read from a directory -- whatever the
		// other side asked for, or what it was read with.
		var base, scope, rawFilter string
		var operational bool
		if other := sides[otherName]; other != nil {
			src := other.side.Snapshot
			base, scope, rawFilter, operational = src.Source.Base, src.Source.Scope, src.Source.Filter, src.OperationalAttributes
		} else if o := body.side(otherName).read(); o != nil {
			base, scope, rawFilter, operational = deref(o.Base), string(deref(o.Scope)), deref(o.Filter), deref(o.OperationalAttributes)
		}
		if asked.Base != nil {
			base = *asked.Base
		}
		if asked.Scope != nil {
			scope = string(*asked.Scope)
		}
		if asked.Filter != nil {
			rawFilter = *asked.Filter
		}
		if asked.OperationalAttributes != nil {
			operational = *asked.OperationalAttributes
		}
		if strings.TrimSpace(base) == "" {
			return badRequest(c, "Name the subtree to compare.",
				"Neither side is a snapshot to take the base from, so one of the two sides needs a base.")
		}
		rs := reads[name]
		req, ok := s.liveRequestFrom(c, rs.reader, base, scope, rawFilter, operational)
		if !ok {
			return nil
		}
		snap, truncated, err := s.captureLive(ctx, rs.reader, req)
		if err != nil {
			return s.snapshotFail(c, err)
		}
		// Only the session's own server is Live: it is the one a comparison
		// may propose changes to. The reference is read as freshly, and is
		// asked about what it lacks through its own connection.
		side := diff.Side{Snapshot: snap, Live: !rs.reference, Truncated: truncated}
		if rs.reference {
			side.Probe = rs.reader.VisibilityOf
		}
		sides[name] = &resolvedSide{side: side, read: rs}
	}

	opts := diff.Options{IncludeUnchanged: body.IncludeUnchanged, ProbeBudget: snapshotProbeBudget}
	if sess != nil {
		// Only a side read from a directory has one to ask about attributes
		// it lacks. A live side asks the session's own.
		opts.Probe = func(ctx context.Context, target dn.DN, attribute string) (directory.AttributeVisibility, error) {
			return sess.Conn.VisibilityOf(ctx, target, attribute)
		}
	}
	result, err := diff.Compare(ctx, sides["source"].side, sides["target"].side, opts)
	if err != nil {
		return s.fail(c, err)
	}
	return c.JSON(signedDiff(diffView(result, sides["source"], sides["target"]), body))
}

func diffView(r *diff.Result, source, target *resolvedSide) Diff {
	out := Diff{
		Kind:               StateKindData,
		Source:             sideSummary(source),
		Target:             sideSummary(target),
		Complete:           r.Complete,
		CrossVendor:        r.CrossVendor,
		OperationalIgnored: r.OperationalIgnored,
		Counts: DiffCounts{Compared: r.Counts.Compared, Added: r.Counts.Added, Removed: r.Counts.Removed,
			Modified: r.Counts.Modified, Renamed: r.Counts.Renamed, Unchanged: r.Counts.Unchanged, Unknown: r.Counts.Unknown},
		Items: make([]DiffItem, 0, len(r.Items)),
	}
	if len(r.Reasons) > 0 {
		reasons := make([]DiffReason, 0, len(r.Reasons))
		for _, reason := range r.Reasons {
			reasons = append(reasons, DiffReason{Code: DiffReasonCode(reason.Code), Detail: reason.Detail})
		}
		out.Reasons = &reasons
	}
	if len(r.ComparedByBytes) > 0 {
		out.ComparedByBytes = ptr(r.ComparedByBytes)
	}
	if len(r.RuleDifferences) > 0 {
		out.RuleDifferences = ptr(r.RuleDifferences)
	}
	for _, item := range r.Items {
		view := DiffItem{Kind: DiffKind(item.Kind)}
		if item.SourceDN != "" {
			view.SourceDn = ptr(item.SourceDN)
		}
		if item.TargetDN != "" {
			view.TargetDn = ptr(item.TargetDN)
		}
		if item.Reason != "" {
			view.Reason = ptr(DiffReasonCode(item.Reason))
		}
		if len(item.Attributes) > 0 {
			attrs := make([]DiffAttributeChange, 0, len(item.Attributes))
			for _, a := range item.Attributes {
				attrs = append(attrs, attributeChangeView(a))
			}
			view.Attributes = &attrs
		}
		if source.side.Live {
			view.Candidate = candidateView(diff.Derive(r, item))
		}
		out.Items = append(out.Items, view)
	}
	return out
}

func sideSummary(side *resolvedSide) DiffSideSummary {
	snap := side.side.Snapshot
	out := DiffSideSummary{
		Kind: DiffSideKindSnapshot, Base: snap.Source.Base, Scope: SnapshotScope(snap.Source.Scope),
		Filter: snap.Source.Filter, OperationalAttributes: snap.OperationalAttributes, EntryCount: snap.EntryCount,
	}
	if snap.Source.Vendor != "" {
		out.Vendor = ptr(snap.Source.Vendor)
	}
	if kind, ok := readKind(side.read); ok {
		out.Kind = kind
		out.Server = ptr(side.read.server)
		return out
	}
	out.CreatedAt = ptr(snap.CreatedAt)
	out.Checksum = ptr(snap.Checksum)
	out.Integrity = ptr(SnapshotIntegrity(side.integrity))
	return out
}

// readKind is how a summary names a side read from a directory: live for
// the session's own, reference for its reference. A document is neither.
func readKind(r readSide) (DiffSideKind, bool) {
	switch {
	case r.reader == nil:
		return "", false
	case r.reference:
		return DiffSideKindReference, true
	}
	return DiffSideKindLive, true
}

func attributeChangeView(a diff.AttributeChange) DiffAttributeChange {
	out := DiffAttributeChange{Name: a.Name, Kind: DiffKind(a.Kind)}
	if len(a.Added) > 0 {
		out.Added = ptr(snapshotValues(a.Added))
	}
	if len(a.Removed) > 0 {
		out.Removed = ptr(snapshotValues(a.Removed))
	}
	optionalInt := func(n int) *int {
		if n == 0 {
			return nil
		}
		return ptr(n)
	}
	optionalBool := func(b bool) *bool {
		if !b {
			return nil
		}
		return ptr(true)
	}
	out.AddedOmitted, out.RemovedOmitted = optionalInt(a.AddedOmitted), optionalInt(a.RemovedOmitted)
	out.WithheldSource, out.WithheldTarget = optionalInt(a.WithheldSource), optionalInt(a.WithheldTarget)
	out.Operational, out.Sensitive, out.ComparedByBytes = optionalBool(a.Operational), optionalBool(a.Sensitive), optionalBool(a.ComparedByBytes)
	if a.UnknownReason != "" {
		out.UnknownReason = ptr(DiffReasonCode(a.UnknownReason))
	}
	return out
}

func snapshotValues(values []snapshot.Value) []SnapshotValue {
	out := make([]SnapshotValue, 0, len(values))
	for _, v := range values {
		out = append(out, SnapshotValue{Text: v.Text, Base64: v.Base64})
	}
	return out
}

func candidateView(c diff.Candidate) *DiffCandidate {
	out := &DiffCandidate{Changes: make([]ChangeRequest, 0, len(c.Records)), Destructive: c.Destructive}
	for _, rec := range c.Records {
		out.Changes = append(out.Changes, changeRequest(rec))
	}
	if c.Blocked != "" {
		out.Blocked = ptr(DiffCandidateBlocked(c.Blocked))
	}
	return out
}
