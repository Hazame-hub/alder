package changepkg

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Between a package's changes and Alder's one change record.
//
// A package carries the same operations the rest of Alder does, in the same
// shape, minus everything that belongs to one environment: no baseline, no
// expectation, no password. Turning a record into an item drops nothing
// silently -- a record that cannot be carried comes back as an error, and the
// caller records it as an omission with a reason.

// Record is the ordinary change record a data item describes.
func (i Item) Record() (directory.ChangeRecord, error) {
	if i.Kind != KindData || i.Data == nil {
		return directory.ChangeRecord{}, fmt.Errorf("change %s is not a data change", i.ID)
	}
	target, err := dn.Parse(i.Data.DN)
	if err != nil {
		return directory.ChangeRecord{}, fmt.Errorf("dn: %w", err)
	}
	out := directory.ChangeRecord{DN: target}
	switch i.Data.Type {
	case OpAdd:
		out.Type = directory.ChangeAdd
		for _, a := range i.Data.Attributes {
			values, err := rawValues(a.Values)
			if err != nil {
				return out, fmt.Errorf("%s: %w", a.Name, err)
			}
			out.Attrs = append(out.Attrs, directory.Attribute{Name: a.Name, Values: values})
		}
	case OpModify:
		out.Type = directory.ChangeModify
		for _, m := range i.Data.Mods {
			values, err := rawValues(m.Values)
			if err != nil {
				return out, fmt.Errorf("%s: %w", m.Name, err)
			}
			out.Mods = append(out.Mods, directory.Mod{Op: directory.ModOp(m.Op), Name: m.Name, Values: values})
		}
	case OpDelete:
		out.Type = directory.ChangeDelete
	case OpRename:
		out.Type = directory.ChangeModRDN
		out.NewRDN = i.Data.NewRDN
		out.DeleteOldRDN = i.Data.DeleteOldRDN
		if i.Data.NewRDN == "" {
			// A move keeps the name it has.
			out.NewRDN = target[0].String()
		}
		if i.Data.NewSuperior != "" {
			superior, err := dn.Parse(i.Data.NewSuperior)
			if err != nil {
				return out, fmt.Errorf("newSuperior: %w", err)
			}
			out.NewSuperior = superior
		}
	default:
		return out, fmt.Errorf("%q is not an operation a package carries", i.Data.Type)
	}
	if err := out.Validate(); err != nil {
		return out, err
	}
	return out, nil
}

// FromRecord turns a change record into a package item.
//
// It refuses what a package must not carry rather than trimming it: a password
// change needs its secret to mean anything, and an item that quietly lost it
// would promote an account with no password set.
func FromRecord(id string, rec directory.ChangeRecord, label string) (Item, error) {
	item := Item{ID: id, Kind: KindData, Label: label}
	data := &DataChange{DN: rec.DN.String()}
	switch rec.Type {
	case directory.ChangeAdd:
		data.Type = OpAdd
		for _, a := range rec.Attrs {
			if schema.IsSensitive(a.Name) {
				return Item{}, secretError(a.Name)
			}
			data.Attributes = append(data.Attributes, Attribute{Name: a.Name, Values: values(a.Values)})
		}
	case directory.ChangeModify:
		data.Type = OpModify
		for _, m := range rec.Mods {
			if schema.IsSensitive(m.Name) {
				return Item{}, secretError(m.Name)
			}
			data.Mods = append(data.Mods, Mod{Op: string(m.Op), Name: m.Name, Values: values(m.Values)})
		}
	case directory.ChangeDelete:
		data.Type = OpDelete
	case directory.ChangeModRDN:
		data.Type = OpRename
		data.NewRDN = rec.NewRDN
		data.DeleteOldRDN = rec.DeleteOldRDN
		if !rec.NewSuperior.IsEmpty() {
			data.NewSuperior = rec.NewSuperior.String()
		}
	case directory.ChangeSetPassword:
		return Item{}, &NotPortableError{Reason: OmittedSecret, Subject: rec.DN.String(),
			Detail: "a password change is an extended operation carrying the new password, and a package never holds a secret"}
	default:
		return Item{}, &NotPortableError{Reason: OmittedUnsupported, Subject: rec.DN.String(),
			Detail: fmt.Sprintf("%q is not a change a package carries", rec.Type)}
	}
	item.Data = data
	item.Destructive = destructive(item)
	return item, nil
}

// SchemaItem is a schema change as package intent: the element, the operation
// and the definition, with no reference to where a server keeps it.
func SchemaItem(id, element, op, oid, definition, label string) (Item, error) {
	item := Item{ID: id, Kind: KindSchema, Label: label,
		Schema: &SchemaChange{Element: element, Op: op, OID: oid, Definition: definition}}
	if definition != "" {
		canonical, found, err := canonicalDefinition(element, definition)
		if err != nil {
			return Item{}, &NotPortableError{Reason: OmittedUnsupported, Subject: oid, Detail: err.Error()}
		}
		item.Schema.Definition = canonical
		if item.Schema.OID == "" {
			item.Schema.OID = found
		}
	}
	item.Destructive = destructive(item)
	if err := validateItem(item); err != nil {
		return Item{}, err
	}
	return item, nil
}

// NotPortableError is a change that cannot be packaged, with the reason a
// package records for it.
type NotPortableError struct {
	Reason  string
	Subject string
	Detail  string
}

func (e *NotPortableError) Error() string { return e.Reason + ": " + e.Detail }
func (e *NotPortableError) Unwrap() error { return ErrNotPortable }

// Omission is how a caller records a refused change in the package.
func (e *NotPortableError) Omission(kind string) Omitted {
	return Omitted{Subject: e.Subject, Kind: kind, Reason: e.Reason, Detail: e.Detail}
}

func secretError(attribute string) *NotPortableError {
	return &NotPortableError{Reason: OmittedSecret, Subject: attribute,
		Detail: "the change names " + attribute + ", and a package never carries a secret"}
}

// values encodes attribute values the way every Alder document does: text when
// it is printable UTF-8, base64 otherwise.
func values(raw [][]byte) []Value {
	out := make([]Value, 0, len(raw))
	for _, v := range raw {
		if printable(v) {
			out = append(out, Value{Text: string(v)})
			continue
		}
		out = append(out, Value{Base64: base64.StdEncoding.EncodeToString(v)})
	}
	return out
}

func printable(v []byte) bool {
	if !utf8.Valid(v) {
		return false
	}
	return !strings.ContainsFunc(string(v), func(r rune) bool {
		return r < 0x20 || r == 0x7f
	})
}

func rawValues(in []Value) ([][]byte, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([][]byte, 0, len(in))
	for _, v := range in {
		switch {
		case v.Base64 != "":
			raw, err := base64.StdEncoding.DecodeString(v.Base64)
			if err != nil {
				return nil, fmt.Errorf("a value is not base64: %w", err)
			}
			out = append(out, raw)
		default:
			out = append(out, []byte(v.Text))
		}
	}
	return out, nil
}
