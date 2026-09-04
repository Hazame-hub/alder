package directory

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Building the change that installs, replaces or removes a schema definition.
//
// A schema definition is a value of an attribute on an ordinary entry, so a
// schema change is an ordinary modify. That is not a trick: it is what both
// arrangements actually are, and it means schema editing arrives already
// holding everything the write path provides — an LDIF preview rendered from
// the same record that will be sent, an Ansible task, and a place in a
// changeset — rather than as a second write path that would have to earn all of
// that again. There is still exactly one method that writes.
//
// The care is all in the value being removed. A directory does not remove "the
// definition of alderTeam"; it removes a value equal to the one it holds, and
// what it holds is not what the schema browser displays:
//
//   - A server whose schema lives in its configuration prefixes each stored
//     definition with its load order, "{0}( 1.2.3 NAME ... )", and strips that
//     prefix from the schema it publishes.
//   - A server whose subschema subentry is directly writable may add its own
//     extensions when it accepts a definition; 389 DS records X-ORIGIN 'user
//     defined' on anything added at runtime.
//
// Either way, a change built from the definition the browser shows would be a
// modification the server matches nothing against and rejects — or, worse on a
// replace, one that adds a second definition of the same OID. So the value to
// remove is always read back from the target entry at the moment the change is
// built, and the OID is what identifies it.

// SchemaDefKind selects which of the two kinds of definition a change concerns.
type SchemaDefKind string

const (
	SchemaDefObjectClass   SchemaDefKind = "objectClass"
	SchemaDefAttributeType SchemaDefKind = "attributeType"
)

// SchemaOp is what a schema change does.
type SchemaOp string

const (
	SchemaOpAdd     SchemaOp = "add"
	SchemaOpReplace SchemaOp = "replace"
	SchemaOpDelete  SchemaOp = "delete"
)

// SchemaChangeRequest describes a schema change before it becomes a
// ChangeRecord.
type SchemaChangeRequest struct {
	// TargetDN is the entry holding the definitions. It must be one of the
	// targets the capabilities listed: accepting any DN here would turn this
	// into a way to aim an arbitrary modify at an arbitrary entry.
	TargetDN string
	Kind     SchemaDefKind
	Op       SchemaOp
	// OID identifies the definition to replace or remove. Ignored for an add.
	OID string
	// Definition is the new RFC 4512 definition, for an add or a replace.
	Definition string
}

// ErrSchemaNotEditable reports that this session has nowhere to write schema.
var ErrSchemaNotEditable = errors.New("directory: the schema is not editable on this connection")

// ErrDefinitionNotFound reports that the target does not hold the definition a
// replace or delete named.
var ErrDefinitionNotFound = errors.New("directory: the target holds no definition with that OID")

// Attribute returns the attribute that carries this kind of definition at the
// write targets.
func (w SchemaWrite) Attribute(kind SchemaDefKind) (string, error) {
	switch kind {
	case SchemaDefObjectClass:
		return w.ObjectClassAttr, nil
	case SchemaDefAttributeType:
		return w.AttributeTypeAttr, nil
	default:
		return "", fmt.Errorf("directory: %q is not a kind of schema definition", kind)
	}
}

// BuildSchemaChange turns a request into the modification that performs it.
//
// stored is every value the target currently holds for the relevant attribute,
// exactly as the server returned them. The caller reads it; this function does
// not, so that the one place that writes stays the one place that talks to a
// directory.
func BuildSchemaChange(w SchemaWrite, req SchemaChangeRequest, stored []string) (ChangeRecord, error) {
	if !w.Editable() {
		return ChangeRecord{}, ErrSchemaNotEditable
	}
	target, ok := w.Target(req.TargetDN)
	if !ok {
		return ChangeRecord{}, fmt.Errorf(
			"directory: %q is not one of this server's schema entries", req.TargetDN)
	}
	attr, err := w.Attribute(req.Kind)
	if err != nil {
		return ChangeRecord{}, err
	}
	targetDN, err := dn.Parse(target.DN)
	if err != nil {
		return ChangeRecord{}, fmt.Errorf("directory: the schema entry DN %q is unusable: %w", target.DN, err)
	}

	rec := ChangeRecord{DN: targetDN, Type: ChangeModify}

	switch req.Op {
	case SchemaOpAdd:
		if strings.TrimSpace(req.Definition) == "" {
			return ChangeRecord{}, errors.New("directory: an add needs a definition")
		}
		if err := definitionMatchesKind(req.Kind, req.Definition); err != nil {
			return ChangeRecord{}, err
		}
		rec.Mods = []Mod{{Op: ModAdd, Name: attr, Values: [][]byte{[]byte(req.Definition)}}}

	case SchemaOpDelete:
		existing, err := findStored(stored, req.OID)
		if err != nil {
			return ChangeRecord{}, err
		}
		rec.Mods = []Mod{{Op: ModDelete, Name: attr, Values: [][]byte{[]byte(existing)}}}

	case SchemaOpReplace:
		if strings.TrimSpace(req.Definition) == "" {
			return ChangeRecord{}, errors.New("directory: a replace needs a definition")
		}
		if err := definitionMatchesKind(req.Kind, req.Definition); err != nil {
			return ChangeRecord{}, err
		}
		existing, err := findStored(stored, req.OID)
		if err != nil {
			return ChangeRecord{}, err
		}
		// How a definition is changed depends on how the server stores it, and
		// the difference is not cosmetic: each server refuses one of the forms.
		//
		// Where the subschema subentry is the schema, adding a definition whose
		// OID and names already exist replaces it in place. Removing it first is
		// not merely unnecessary, it fails: a server will not delete an
		// attribute type that an object class still names in its MUST or MAY,
		// even when the same operation puts it straight back. Since an
		// attribute type worth editing is usually one a class uses,
		// delete-and-add made almost every real edit impossible.
		//
		// Where the schema lives in configuration entries the values are an
		// ordinary multi-valued attribute, with no notion of replacing one by
		// OID; an add on its own is refused there, and the old value has to go.
		if w.Style == SchemaStyleSubschema && sameNames(existing, req.Definition) {
			rec.Mods = []Mod{{Op: ModAdd, Name: attr, Values: [][]byte{[]byte(req.Definition)}}}
			break
		}
		// Removing and adding within one modify, rather than replacing the
		// whole attribute. The attribute holds every definition the target has
		// -- hundreds of them -- and an LDAP replace would rewrite all of them
		// to change one. It would also produce an LDIF preview nobody could
		// read, when the point of the preview is that they can.
		rec.Mods = []Mod{
			{Op: ModDelete, Name: attr, Values: [][]byte{[]byte(existing)}},
			{Op: ModAdd, Name: attr, Values: [][]byte{[]byte(req.Definition)}},
		}

	default:
		return ChangeRecord{}, fmt.Errorf("directory: %q is not a schema operation", req.Op)
	}

	return rec, rec.Validate()
}

// sameNames reports whether two definitions carry the same names.
//
// It decides which form a replace takes, and it matters because a server that
// updates a definition in place matches it by OID *and* name: the same OID
// offered under a new name is a collision, not an update, and is refused. A
// rename therefore has to remove the old definition, whatever that costs.
//
// Anything unparseable answers false, which routes to the form that removes the
// old value explicitly — the more conservative of the two.
func sameNames(oldDef, newDef string) bool {
	oldNames, ok1 := definitionNames(oldDef)
	newNames, ok2 := definitionNames(newDef)
	if !ok1 || !ok2 || len(oldNames) != len(newNames) {
		return false
	}
	for i := range oldNames {
		if !strings.EqualFold(oldNames[i], newNames[i]) {
			return false
		}
	}
	return true
}

// definitionNames returns the NAME list of a definition of either kind.
func definitionNames(def string) ([]string, bool) {
	def = strings.TrimSpace(def)
	if strings.HasPrefix(def, "{") {
		if end := strings.IndexByte(def, '}'); end > 0 {
			def = strings.TrimSpace(def[end+1:])
		}
	}
	if at, err := schema.ParseAttributeType(def); err == nil && len(at.Names) > 0 {
		return at.Names, true
	}
	if oc, err := schema.ParseObjectClass(def); err == nil && len(oc.Names) > 0 {
		return oc.Names, true
	}
	return nil, false
}

// findStored locates the value the server holds for an OID.
//
// The stored text is compared by parsing it, not by matching substrings: an OID
// appears inside other definitions that refer to it, and "1.2.3" is a prefix of
// "1.2.30".
func findStored(stored []string, oid string) (string, error) {
	if oid == "" {
		return "", errors.New("directory: this change needs the OID of the definition it acts on")
	}
	for _, v := range stored {
		if storedOID(v) == oid {
			return v, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrDefinitionNotFound, oid)
}

// storedOID reads the OID out of a stored definition.
//
// It tolerates the ordering prefix a configuration entry carries, because the
// value has to be returned to the server with that prefix intact and so cannot
// simply be stripped and reparsed.
func storedOID(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") {
		if end := strings.IndexByte(v, '}'); end > 0 {
			v = strings.TrimSpace(v[end+1:])
		}
	}
	// Both kinds start with "( oid", and the object class parser is the more
	// permissive of the two about what may follow.
	if oc, err := schema.ParseObjectClass(v); err == nil {
		return oc.OID
	}
	if at, err := schema.ParseAttributeType(v); err == nil {
		return at.OID
	}
	return ""
}

// definitionMatchesKind refuses an object class written into the attribute
// types, and the reverse.
//
// Both parse from the same grammar, so a server will often accept the wrong one
// and store something meaningless. This is the last point at which the mistake
// is still cheap to fix.
func definitionMatchesKind(kind SchemaDefKind, def string) error {
	switch kind {
	case SchemaDefObjectClass:
		if _, err := schema.ParseObjectClass(def); err != nil {
			return fmt.Errorf("directory: this is not a usable object class definition: %w", err)
		}
	case SchemaDefAttributeType:
		at, err := schema.ParseAttributeType(def)
		if err != nil {
			return fmt.Errorf("directory: this is not a usable attribute type definition: %w", err)
		}
		// An attribute type has to say how its values are shaped, by naming a
		// syntax or inheriting one. A definition with neither is accepted by
		// the grammar and is useless.
		if at.Syntax == "" && at.SuperName == "" {
			return errors.New(
				"directory: an attribute type needs either a SYNTAX or a SUP to inherit one from")
		}
	}
	return nil
}
