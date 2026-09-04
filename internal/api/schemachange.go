package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// Schema editing.
//
// This endpoint builds a change; it does not apply one. What comes back is an
// ordinary ChangeRequest, and it goes on through the same preview, the same
// confirmation dialog and the same changeset as every other write, because a
// schema definition really is a value of an attribute on an entry. Nothing here
// is a second write path, and there is still exactly one method that writes.
//
// Two things are done here rather than in the browser, for the same reason:
//
//   - The definition text is rendered here, so that what the person confirms in
//     the preview and what the directory receives are the same bytes.
//   - The value a replace or a delete removes is read from the target here,
//     because it is not always the value the schema browser displays.

// BuildSchemaChange renders a definition and returns the change that applies it.
func (s *Server) BuildSchemaChange(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body SchemaChangeRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}

	write := sess.Conn.Capabilities().SchemaWrite
	if !write.Editable() {
		detail := write.Unavailable
		if detail == "" {
			detail = "This connection has no writable schema location."
		}
		return badRequest(c, "The schema cannot be edited on this connection.", detail)
	}

	kind := directory.SchemaDefKind(body.Kind)
	op := directory.SchemaOp(body.Op)

	definition, err := definitionFor(body, kind, op)
	if err != nil {
		return badRequest(c, "This definition cannot be used.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	// Read the target even for an add: BuildSchemaChange takes the stored
	// values, and reading them here keeps the one function that talks to a
	// directory the one that talks to a directory.
	stored, err := sess.Conn.SchemaDefinitions(ctx, body.TargetDn, kind)
	if err != nil {
		return badRequest(c, "The schema entry could not be read.", err.Error())
	}

	// A definition being added must not collide with one already there. Both
	// servers refuse it, but they refuse it with a message about OIDs that does
	// not say which of the two fields is the problem.
	if op == directory.SchemaOpAdd {
		if err := refuseDuplicate(definition, stored); err != nil {
			return badRequest(c, "This definition is already in the schema.", err.Error())
		}
	}

	record, err := directory.BuildSchemaChange(write, directory.SchemaChangeRequest{
		TargetDN:   body.TargetDn,
		Kind:       kind,
		Op:         op,
		OID:        deref(body.Oid),
		Definition: definition,
	}, stored)
	if err != nil {
		if errors.Is(err, directory.ErrDefinitionNotFound) {
			return badRequest(c, "The schema entry no longer holds that definition.",
				"Someone may have changed it since this page was loaded. Reload the schema and try again.")
		}
		return badRequest(c, "This schema change cannot be built.", err.Error())
	}

	out := SchemaChangeBuild{Change: changeRequestFromRecord(record)}
	if definition != "" {
		out.Definition = ptr(definition)
	}
	// The value being removed, so a replace reads as the substitution it is
	// rather than as two unrelated operations.
	for _, m := range record.Mods {
		if m.Op == directory.ModDelete && len(m.Values) == 1 {
			out.Removes = ptr(string(m.Values[0]))
		}
	}
	return c.JSON(out)
}

// definitionFor produces the RFC 4512 text this change installs.
//
// A delete installs nothing and needs none. Otherwise a definition written out
// by hand wins over the form, because someone who typed one meant it — but it
// is parsed and checked exactly as a rendered one is, so the hand-written path
// is a shortcut through the form, never around its checks.
func definitionFor(body SchemaChangeRequest, kind directory.SchemaDefKind, op directory.SchemaOp) (string, error) {
	if op == directory.SchemaOpDelete {
		return "", nil
	}
	if raw := strings.TrimSpace(deref(body.Definition)); raw != "" {
		return raw, nil
	}
	switch kind {
	case directory.SchemaDefObjectClass:
		if body.ObjectClass == nil {
			return "", errors.New("this change describes no object class")
		}
		return objectClassFrom(*body.ObjectClass).Definition()
	case directory.SchemaDefAttributeType:
		if body.AttributeType == nil {
			return "", errors.New("this change describes no attribute type")
		}
		return attributeTypeFrom(*body.AttributeType).Definition()
	default:
		return "", errors.New("this is not a kind of schema definition")
	}
}

func objectClassFrom(in ObjectClassInput) schema.ObjectClass {
	oc := schema.ObjectClass{
		OID:        in.Oid,
		Names:      in.Names,
		Desc:       deref(in.Desc),
		Obsolete:   in.Obsolete != nil && *in.Obsolete,
		Extensions: extensionsFrom(in.Extensions),
	}
	if in.SuperNames != nil {
		oc.SuperNames = *in.SuperNames
	}
	if in.Must != nil {
		oc.Must = *in.Must
	}
	if in.May != nil {
		oc.May = *in.May
	}
	if in.Kind != nil {
		switch *in.Kind {
		case KindAbstract:
			oc.Kind = schema.KindAbstract
		case KindAuxiliary:
			oc.Kind = schema.KindAuxiliary
		default:
			oc.Kind = schema.KindStructural
		}
	}
	return oc
}

func attributeTypeFrom(in AttributeTypeInput) schema.AttributeType {
	at := schema.AttributeType{
		OID:                in.Oid,
		Names:              in.Names,
		Desc:               deref(in.Desc),
		Obsolete:           in.Obsolete != nil && *in.Obsolete,
		SuperName:          deref(in.SuperName),
		Equality:           deref(in.Equality),
		Ordering:           deref(in.Ordering),
		Substr:             deref(in.Substr),
		Syntax:             deref(in.Syntax),
		SingleValue:        in.SingleValue != nil && *in.SingleValue,
		Collective:         in.Collective != nil && *in.Collective,
		NoUserModification: in.NoUserModification != nil && *in.NoUserModification,
		Extensions:         extensionsFrom(in.Extensions),
	}
	if in.SyntaxLen != nil {
		at.SyntaxLen = *in.SyntaxLen
	}
	if in.Usage != nil {
		switch *in.Usage {
		case UsageDirectoryOperation:
			at.Usage = schema.UsageDirectoryOperation
		case UsageDistributedOperation:
			at.Usage = schema.UsageDistributedOperation
		case UsageDSAOperation:
			at.Usage = schema.UsageDSAOperation
		default:
			at.Usage = schema.UsageUserApplications
		}
	}
	return at
}

func extensionsFrom(in *map[string][]string) schema.Extensions {
	if in == nil || len(*in) == 0 {
		return nil
	}
	out := make(schema.Extensions, len(*in))
	for k, v := range *in {
		out[k] = v
	}
	return out
}

// refuseDuplicate reports an OID or a name already present at this target.
//
// A name collision matters as much as an OID collision and is easier to make:
// OIDs are copied from a plan, names are typed.
func refuseDuplicate(definition string, stored []string) error {
	oid, names := identityOf(definition)
	if oid == "" {
		return nil // unparseable definitions are refused elsewhere, with a better message
	}
	lower := make(map[string]bool, len(names))
	for _, n := range names {
		lower[strings.ToLower(n)] = true
	}
	for _, v := range stored {
		existingOID, existingNames := identityOf(stripOrderingPrefix(v))
		if existingOID == oid {
			return errors.New("the OID " + oid + " is already defined at this schema entry")
		}
		for _, n := range existingNames {
			if lower[strings.ToLower(n)] {
				return errors.New("the name " + n + " is already used at this schema entry, by " + existingOID)
			}
		}
	}
	return nil
}

// identityOf returns the OID and names of a definition of either kind.
func identityOf(def string) (string, []string) {
	if oc, err := schema.ParseObjectClass(def); err == nil {
		return oc.OID, oc.Names
	}
	if at, err := schema.ParseAttributeType(def); err == nil {
		return at.OID, at.Names
	}
	return "", nil
}

// stripOrderingPrefix removes the load-order prefix a configuration entry keeps
// on each stored definition. Only for reading: a value sent back to the server
// keeps it.
func stripOrderingPrefix(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") {
		if end := strings.IndexByte(v, '}'); end > 0 {
			return strings.TrimSpace(v[end+1:])
		}
	}
	return v
}

// changeRequestFromRecord renders a record back into the wire shape, so the
// schema editor hands the ordinary dialog an ordinary change.
func changeRequestFromRecord(rec directory.ChangeRecord) ChangeRequest {
	out := ChangeRequest{Dn: rec.DN.String(), Type: ChangeRequestType(rec.Type)}
	mods := make([]ChangeMod, 0, len(rec.Mods))
	for _, m := range rec.Mods {
		// A definition is printable text by construction, so encodeRawValues
		// sends it as text; the base64 branch stays reachable for anything that
		// somehow is not, rather than being assumed away.
		values := encodeRawValues(m.Values)
		mods = append(mods, ChangeMod{Name: m.Name, Op: ChangeModOp(m.Op), Values: &values})
	}
	out.Mods = &mods
	return out
}
