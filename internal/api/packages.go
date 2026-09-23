package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/session"
)

// Change packages.
//
// A package is intent leaving the building: the changes an operator means to
// make, written so another environment can read them. Nothing here applies
// anything, and nothing here is kept. Building one strips everything that
// belongs to this environment -- baselines, expectations, the shape of this
// server's schema entry -- and refuses what cannot travel at all rather than
// quietly leaving it out. Validating one asks a directory what the intent
// would mean against its current state, and stops there: the plan is still the
// only thing that decides what is written, and it is made fresh in each
// target.

// BuildPackage turns changes into a change package.
func (s *Server) BuildPackage(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body PackageBuildRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if len(body.Changes) == 0 {
		return badRequest(c, "A package needs at least one change.", "")
	}
	if len(body.Changes) > MaxChangesetChanges {
		return badRequest(c, fmt.Sprintf("A package holds at most %d changes, and this one has %d.",
			MaxChangesetChanges, len(body.Changes)), "")
	}

	caps := sess.Conn.Capabilities()
	items, omitted, err := packageItems(body.Changes, caps)
	if err != nil {
		return badRequest(c, "This change cannot be packaged.", err.Error())
	}
	if len(items) == 0 {
		return badRequest(c, "None of these changes can be packaged.",
			"Every one of them is something a package does not carry; see the reasons in the refusal.")
	}

	doc := &changepkg.Package{
		Title:   deref(body.Title),
		Changes: changepkg.Derive(items),
		Omitted: omitted,
		Source:  changepkg.Provenance{AlderVersion: s.cfg.Version, Method: buildMethod(body.Method)},
	}
	if body.Description != nil {
		doc.Description = *body.Description
	}
	if body.Assumptions != nil {
		doc.Assumptions = changepkg.Assumptions{
			NamingContexts: derefList(body.Assumptions.NamingContexts),
			SchemaOIDs:     derefList(body.Assumptions.SchemaOids),
			ObjectClasses:  derefList(body.Assumptions.ObjectClasses),
		}
	}
	// Provenance is informational and off unless asked for: a package that says
	// nothing about where it was made is still a valid package.
	if body.RecordSource != nil && *body.RecordSource {
		doc.Source.Vendor, doc.Source.VendorVersion = caps.VendorName, caps.VendorVersion
		doc.Source.NamingContexts = caps.NamingContexts
	}

	built, err := changepkg.Build(doc, time.Now())
	if err != nil {
		return packageRefusal(c, err)
	}
	var buf bytes.Buffer
	if err := changepkg.Encode(&buf, built); err != nil {
		return s.fail(c, err)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", packageFilename(built, time.Now())))
	return c.Send(buf.Bytes())
}

func buildMethod(method *PackageBuildRequestMethod) string {
	if method == nil {
		return changepkg.MethodChangeset
	}
	return string(*method)
}

func derefList(list *[]string) []string {
	if list == nil {
		return nil
	}
	return *list
}

// packageFilename names the download after the package, not after the
// directory: a package belongs to the change, not to where it was made.
func packageFilename(p *changepkg.Package, at time.Time) string {
	short := p.ID
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("alder-change-package-%s-%s.json", short, at.UTC().Format("2006-01-02T150405Z"))
}

// packageItems turns change requests into package items, recognising the ones
// that are really schema changes on this server and recording what cannot
// travel.
func packageItems(changes []ChangeRequest, caps directory.Capabilities) ([]changepkg.Item, []changepkg.Omitted, error) {
	var items []changepkg.Item
	omitted := []changepkg.Omitted{}
	next := 0
	id := func() string {
		next++
		return fmt.Sprintf("c%d", next)
	}

	for i, ch := range changes {
		record, err := changeRecord(ch)
		if err != nil {
			return nil, nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		if err := record.Validate(); err != nil {
			return nil, nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		label := ""
		// A schema change on this server is a modification of a schema entry.
		// What it means -- this element, this operation, this definition -- is
		// what travels; the entry and attribute are this server's business.
		if record.Type == directory.ChangeModify && targetKind(caps, record.DN) == PlanTargetSchema {
			schemaItems, err := schemaIntent(record, caps, id, label)
			if err != nil {
				var notPortable *changepkg.NotPortableError
				if errors.As(err, &notPortable) {
					omitted = append(omitted, notPortable.Omission(changepkg.KindSchema))
					continue
				}
				return nil, nil, fmt.Errorf("change %d: %w", i+1, err)
			}
			items = append(items, schemaItems...)
			continue
		}
		item, err := changepkg.FromRecord(id(), record, label)
		if err != nil {
			var notPortable *changepkg.NotPortableError
			if errors.As(err, &notPortable) {
				next--
				omitted = append(omitted, notPortable.Omission(changepkg.KindData))
				continue
			}
			return nil, nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		items = append(items, item)
	}
	return items, omitted, nil
}

// schemaIntent reads a modification of a schema entry back into the intent it
// expresses. A delete and an add of the same OID in one modification is what a
// replacement looks like on the wire, and it is recorded as one.
func schemaIntent(record directory.ChangeRecord, caps directory.Capabilities,
	id func() string, label string) ([]changepkg.Item, error) {
	write := caps.SchemaWrite
	type intent struct {
		element    string
		definition string
		deleted    bool
		added      bool
	}
	order := []string{}
	byOID := map[string]*intent{}

	for _, mod := range record.Mods {
		element := ""
		switch {
		case write.AttributeTypeAttr != "" && strings.EqualFold(mod.Name, write.AttributeTypeAttr):
			element = changepkg.ElementAttributeType
		case write.ObjectClassAttr != "" && strings.EqualFold(mod.Name, write.ObjectClassAttr):
			element = changepkg.ElementObjectClass
		default:
			return nil, &changepkg.NotPortableError{Reason: changepkg.OmittedServerSpecific,
				Subject: record.DN.String(),
				Detail: fmt.Sprintf("the change modifies %s on a schema entry, which is not a definition Alder packages",
					mod.Name)}
		}
		for _, value := range mod.Values {
			text := storedDefinition(string(value))
			oid := storedDefinitionOID(string(value))
			if oid == "" {
				return nil, &changepkg.NotPortableError{Reason: changepkg.OmittedUnsupported,
					Subject: record.DN.String(), Detail: "a definition in the change has no OID"}
			}
			key := strings.ToLower(oid)
			held, seen := byOID[key]
			if !seen {
				held = &intent{element: element}
				byOID[key] = held
				order = append(order, key)
			}
			switch mod.Op {
			case directory.ModDelete:
				held.deleted = true
			default:
				held.added = true
				held.definition = text
			}
		}
	}

	out := make([]changepkg.Item, 0, len(order))
	for _, key := range order {
		held := byOID[key]
		op := changepkg.SchemaAdd
		switch {
		case held.deleted && held.added:
			op = changepkg.SchemaReplace
		case held.deleted:
			op = changepkg.SchemaDelete
		}
		item, err := changepkg.SchemaItem(id(), held.element, op, key, held.definition, label)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// storedDefinition is a definition as the schema publishes it, without the
// load order prefix a configuration keeps in front of it.
func storedDefinition(value string) string {
	text := strings.TrimSpace(value)
	if strings.HasPrefix(text, "{") {
		if end := strings.IndexByte(text, '}'); end > 0 {
			text = strings.TrimSpace(text[end+1:])
		}
	}
	return text
}

// InspectPackage reads a package and reports what it holds.
//
// No session: this reads a document and touches no directory, like comparing
// two snapshots. It is the check a pipeline can run before it has credentials.
func (s *Server) InspectPackage(c *fiber.Ctx) error {
	document, signature, ok := s.openDocument(c, c.Body())
	if !ok {
		return nil
	}
	p, integrity, err := changepkg.Decode(document)
	if err != nil {
		return packageRefusal(c, err)
	}
	order, err := p.Order()
	if err != nil {
		return packageRefusal(c, err)
	}
	out := PackageInspection{
		PackageId: p.ID, Version: p.Version, CreatedAt: p.CreatedAt,
		Integrity: SnapshotIntegrity(integrity), Source: packageProvenance(p.Source),
		Assumptions: packageAssumptions(p.Assumptions), Counts: packageCounts(p.Counts),
		Changes: packageChanges(p.Changes), Omitted: packageOmissions(p.Omitted), Order: order,
	}
	out.Title, out.Description = ptrIfSet(p.Title), ptrIfSet(p.Description)
	out.Signature = signatureView(signature)
	return c.JSON(out)
}

type packageValidateBody struct {
	Package      json.RawMessage `json:"package"`
	SchemaTarget *string         `json:"schemaTarget"`
}

// ValidatePackage checks a package against the directory this session is
// connected to.
func (s *Server) ValidatePackage(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body packageValidateBody
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if len(bytes.TrimSpace(body.Package)) == 0 {
		return badRequest(c, "The request carries no package.", "")
	}
	document, _, ok := s.openDocument(c, body.Package)
	if !ok {
		return nil
	}
	p, integrity, err := changepkg.Decode(document)
	if err != nil {
		return packageRefusal(c, err)
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	opts := changepkg.Options{NotFound: isNoSuchObject}
	if body.SchemaTarget != nil {
		target := strings.TrimSpace(*body.SchemaTarget)
		if target != "" {
			if _, err := dn.Parse(target); err != nil {
				return badRequest(c, "schemaTarget is not a DN.", err.Error())
			}
			opts.SchemaEntry = target
		}
	}
	res, err := changepkg.Validate(ctx, p, sess.Conn, opts)
	if err != nil {
		return packageRefusal(c, err)
	}
	return c.JSON(validationView(res, integrity))
}

func validationView(res *changepkg.Result, integrity changepkg.Integrity) PackageValidation {
	out := PackageValidation{
		PackageId: res.PackageID, Integrity: SnapshotIntegrity(integrity),
		Target: PackageTarget{
			NamingContexts: res.Target.NamingContexts, SchemaWritable: res.Target.SchemaWritable,
			Vendor: ptrIfSet(res.Target.Vendor), VendorVersion: ptrIfSet(res.Target.VendorVersion),
		},
		Assumptions: make([]PackageAssumptionResult, 0, len(res.Assumptions)),
		Counts: PackageValidationCounts{
			Ready: res.Counts.Ready, AlreadySatisfied: res.Counts.AlreadySatisfied, NoOp: res.Counts.NoOp,
			Conflict: res.Counts.Conflict, DependencyMissing: res.Counts.DependencyMissing,
			Unsupported: res.Counts.Unsupported, TargetIncompatible: res.Counts.TargetIncompatible,
			Unknown: res.Counts.Unknown,
		},
		Items: make([]PackageValidationItem, 0, len(res.Items)),
		Order: res.Order,
	}
	if len(res.Target.SchemaEntries) > 0 {
		out.Target.SchemaEntries = ptr(res.Target.SchemaEntries)
	}
	for _, a := range res.Assumptions {
		out.Assumptions = append(out.Assumptions, PackageAssumptionResult{
			Kind: a.Kind, Value: a.Value, Satisfied: a.Satisfied, Detail: ptrIfSet(a.Detail)})
	}
	for _, item := range res.Items {
		view := PackageValidationItem{
			Id: item.ID, Kind: item.Kind, Destructive: item.Destructive,
			Status: PackageItemStatus(item.Status), Label: ptrIfSet(item.Label), Intent: ptrIfSet(item.Intent),
		}
		if len(item.DependsOn) > 0 {
			view.DependsOn = ptr(item.DependsOn)
		}
		if len(item.Problems) > 0 {
			problems := make([]PackageProblem, 0, len(item.Problems))
			for _, p := range item.Problems {
				problems = append(problems, PackageProblem{Code: PackageProblemCode(p.Code),
					Subject: ptrIfSet(p.Subject), Detail: ptrIfSet(p.Detail)})
			}
			view.Problems = &problems
		}
		if len(item.Records) > 0 {
			changes := make([]ChangeRequest, 0, len(item.Records))
			for _, rec := range item.Records {
				changes = append(changes, changeRequest(rec))
			}
			view.Changes = &changes
		}
		out.Items = append(out.Items, view)
	}
	return out
}

func packageProvenance(p changepkg.Provenance) PackageProvenance {
	out := PackageProvenance{
		AlderVersion: ptrIfSet(p.AlderVersion), Vendor: ptrIfSet(p.Vendor), VendorVersion: ptrIfSet(p.VendorVersion),
		SnapshotChecksum: ptrIfSet(p.SnapshotChecksum), SchemaSnapshotChecksum: ptrIfSet(p.SchemaSnapshotChecksum),
	}
	if p.Method != "" {
		out.Method = ptr(PackageProvenanceMethod(p.Method))
	}
	if len(p.NamingContexts) > 0 {
		out.NamingContexts = ptr(p.NamingContexts)
	}
	return out
}

func packageAssumptions(a changepkg.Assumptions) PackageAssumptions {
	out := PackageAssumptions{}
	if len(a.NamingContexts) > 0 {
		out.NamingContexts = ptr(a.NamingContexts)
	}
	if len(a.SchemaOIDs) > 0 {
		out.SchemaOids = ptr(a.SchemaOIDs)
	}
	if len(a.ObjectClasses) > 0 {
		out.ObjectClasses = ptr(a.ObjectClasses)
	}
	return out
}

func packageCounts(c changepkg.Counts) PackageCounts {
	return PackageCounts{Changes: c.Changes, Data: c.Data, Schema: c.Schema,
		Destructive: c.Destructive, Omitted: c.Omitted}
}

func packageChanges(items []changepkg.Item) []PackageChange {
	out := make([]PackageChange, 0, len(items))
	for _, item := range items {
		view := PackageChange{Id: item.ID, Kind: PackageChangeKind(item.Kind), Destructive: item.Destructive,
			Label: ptrIfSet(item.Label)}
		if item.Intent != "" {
			view.Intent = ptr(PackageChangeIntent(item.Intent))
		}
		if len(item.DependsOn) > 0 {
			view.DependsOn = ptr(item.DependsOn)
		}
		if item.Data != nil {
			data := PackageDataChange{Dn: item.Data.DN, Type: PackageDataChangeType(item.Data.Type),
				NewRdn: ptrIfSet(item.Data.NewRDN), NewSuperior: ptrIfSet(item.Data.NewSuperior)}
			if item.Data.DeleteOldRDN {
				data.DeleteOldRdn = ptr(true)
			}
			if len(item.Data.Attributes) > 0 {
				attrs := make([]PackageAttribute, 0, len(item.Data.Attributes))
				for _, a := range item.Data.Attributes {
					attrs = append(attrs, PackageAttribute{Name: a.Name, Values: packageValues(a.Values)})
				}
				data.Attributes = &attrs
			}
			if len(item.Data.Mods) > 0 {
				mods := make([]PackageMod, 0, len(item.Data.Mods))
				for _, m := range item.Data.Mods {
					mod := PackageMod{Name: m.Name, Op: PackageModOp(m.Op)}
					if len(m.Values) > 0 {
						mod.Values = ptr(packageValues(m.Values))
					}
					mods = append(mods, mod)
				}
				data.Mods = &mods
			}
			view.Data = &data
		}
		if item.Schema != nil {
			view.Schema = &PackageSchemaChange{
				Element: PackageSchemaChangeElement(item.Schema.Element), Op: PackageSchemaChangeOp(item.Schema.Op),
				Oid: item.Schema.OID, Definition: ptrIfSet(item.Schema.Definition),
			}
		}
		out = append(out, view)
	}
	return out
}

func packageValues(values []changepkg.Value) []PackageValue {
	out := make([]PackageValue, 0, len(values))
	for _, v := range values {
		out = append(out, PackageValue{Text: ptrIfSet(v.Text), Base64: ptrIfSet(v.Base64)})
	}
	return out
}

func packageOmissions(omitted []changepkg.Omitted) []PackageOmitted {
	out := make([]PackageOmitted, 0, len(omitted))
	for _, o := range omitted {
		out = append(out, PackageOmitted{Reason: PackageOmittedReason(o.Reason),
			Subject: ptrIfSet(o.Subject), Kind: ptrIfSet(o.Kind), Detail: ptrIfSet(o.Detail)})
	}
	return out
}

// packageRefusal answers with the stable code for why a package is not usable.
func packageRefusal(c *fiber.Ctx, err error) error {
	var pe *changepkg.Error
	if !errors.As(err, &pe) {
		return badRequest(c, "The package is not usable.", err.Error())
	}
	code := ErrorErrorPackageInvalid
	message := "The package is not a valid Alder change package."
	switch pe.Code {
	case changepkg.CodeUnsupportedVersion:
		code, message = ErrorErrorPackageUnsupportedVersion, "This package's format version is not one this Alder reads."
	case changepkg.CodeChecksumMismatch:
		code, message = ErrorErrorPackageChecksumMismatch, "The package does not match its checksum."
	case changepkg.CodeTooLarge:
		code, message = ErrorErrorPackageTooLarge, "The package is larger than Alder reads."
	}
	return writeError(c, fiber.StatusBadRequest, code, message, pe.Detail)
}

// packageSession is the part of a session a package validation uses. It exists
// so the compiler says so: validation gets a reader, never a writer.
var _ = func(sess *session.Session) changepkg.Target { return sess.Conn }
