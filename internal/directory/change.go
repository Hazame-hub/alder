package directory

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/schema"
)

// ChangeType is the kind of modification a ChangeRecord describes.
type ChangeType string

// The change types.
const (
	ChangeAdd    ChangeType = "add"
	ChangeModify ChangeType = "modify"
	ChangeDelete ChangeType = "delete"
	ChangeModRDN ChangeType = "modrdn"
)

// ModOp is the operation of one modification.
type ModOp string

// The modification operations.
const (
	ModAdd     ModOp = "add"
	ModDelete  ModOp = "delete"
	ModReplace ModOp = "replace"
)

// Mod is one modification within a ChangeRecord.
//
// A delete with no values removes the whole attribute; a delete with values
// removes exactly those.
type Mod struct {
	Op     ModOp
	Name   string
	Values [][]byte
}

// ChangeRecord is the single representation of a directory modification.
//
// Every mutation in the application is expressed as one of these before it is
// applied. It renders to LDIF, it renders to an Ansible task, and it is what
// the user confirms in the preview modal. Session.Apply is the only code path
// that writes to a directory and it takes exactly this.
type ChangeRecord struct {
	DN   dn.DN
	Type ChangeType

	// Mods applies to ChangeModify.
	Mods []Mod

	// Attrs applies to ChangeAdd: the attributes of the new entry, in order.
	Attrs []Attribute

	// NewRDN, DeleteOldRDN and NewSuperior apply to ChangeModRDN.
	NewRDN       string
	DeleteOldRDN bool
	NewSuperior  dn.DN
}

// Attribute is an attribute description and its values, in the order they
// should be written.
type Attribute struct {
	Name   string
	Values [][]byte
}

// ErrEmptyChange reports a modify that would send nothing to the server.
//
// This matters more than it looks: an editor that produces an empty modify when
// the user changed nothing will happily "apply" a no-op and report success,
// which teaches the user that the confirm button is meaningless.
var ErrEmptyChange = errors.New("directory: the change contains no modifications")

// Validate checks a change record before it is rendered or applied.
func (c ChangeRecord) Validate() error {
	if c.DN.IsEmpty() {
		return errors.New("directory: a change requires a DN")
	}
	switch c.Type {
	case ChangeAdd:
		if len(c.Attrs) == 0 {
			return errors.New("directory: an add requires at least one attribute")
		}
		var hasObjectClass bool
		for _, a := range c.Attrs {
			if err := dn.ValidateType(a.Name); err != nil {
				return err
			}
			if len(a.Values) == 0 {
				return fmt.Errorf("directory: attribute %q has no values", a.Name)
			}
			if equalFoldASCII(schema.BaseName(a.Name), "objectClass") {
				hasObjectClass = true
			}
		}
		if !hasObjectClass {
			return errors.New("directory: an add requires an objectClass")
		}
	case ChangeModify:
		if len(c.Mods) == 0 {
			return ErrEmptyChange
		}
		for _, m := range c.Mods {
			if err := dn.ValidateType(m.Name); err != nil {
				return err
			}
			switch m.Op {
			case ModAdd:
				if len(m.Values) == 0 {
					return fmt.Errorf("directory: add %s: an add requires at least one value", m.Name)
				}
			case ModDelete, ModReplace:
			default:
				return fmt.Errorf("directory: unknown modification %q", m.Op)
			}
		}
	case ChangeDelete:
		// Nothing beyond the DN.
	case ChangeModRDN:
		if c.NewRDN == "" {
			return errors.New("directory: a rename requires a new RDN")
		}
		parsed, err := dn.Parse(c.NewRDN)
		if err != nil {
			return fmt.Errorf("directory: new RDN: %w", err)
		}
		if len(parsed) != 1 {
			return fmt.Errorf("directory: new RDN %q is a DN, not a single RDN", c.NewRDN)
		}
	default:
		return fmt.Errorf("directory: unknown change type %q", c.Type)
	}
	return nil
}

// Target returns the DN the entry will have after the change, which differs
// from DN only for a rename. The UI navigates to it after applying.
func (c ChangeRecord) Target() (dn.DN, error) {
	if c.Type != ChangeModRDN {
		return c.DN, nil
	}
	rdn, err := dn.Parse(c.NewRDN)
	if err != nil {
		return nil, fmt.Errorf("directory: new RDN: %w", err)
	}
	parent := c.DN.Parent()
	if len(c.NewSuperior) > 0 {
		parent = c.NewSuperior
	}
	return parent.Child(rdn[0]), nil
}

// ldifRecord converts to the LDIF package's representation. It is the only
// place the two are mapped, so the LDIF a user confirms and the operation the
// server receives cannot describe different things.
func (c ChangeRecord) ldifRecord() *ldif.Record {
	r := &ldif.Record{DN: c.DN}
	switch c.Type {
	case ChangeAdd:
		r.Change = ldif.ChangeAdd
		for _, a := range c.Attrs {
			r.Attrs = append(r.Attrs, ldif.Attribute{Name: a.Name, Values: a.Values})
		}
	case ChangeModify:
		r.Change = ldif.ChangeModify
		for _, m := range c.Mods {
			r.Mods = append(r.Mods, ldif.Mod{Op: ldifModOp(m.Op), Name: m.Name, Values: m.Values})
		}
	case ChangeDelete:
		r.Change = ldif.ChangeDelete
	case ChangeModRDN:
		r.Change = ldif.ChangeModRDN
		r.NewRDN = c.NewRDN
		r.DeleteOldRDN = c.DeleteOldRDN
		r.NewSuperior = c.NewSuperior
	}
	return r
}

func ldifModOp(op ModOp) ldif.ModOp {
	switch op {
	case ModDelete:
		return ldif.ModDelete
	case ModReplace:
		return ldif.ModReplace
	default:
		return ldif.ModAdd
	}
}

// LDIF renders the change as an RFC 2849 change record.
//
// This is what the preview modal shows and what the confirm button confirms.
// Folding is disabled: the point of the preview is that a person reads it, and
// a value broken across lines at column 76 is harder to check, not easier.
func (c ChangeRecord) LDIF() string {
	var b strings.Builder
	w := ldif.NewWriter(&b)
	w.LineWidth = -1
	_ = w.WriteRecord(c.ldifRecord())
	return b.String()
}

// LDIFFolded renders the change folded at the RFC's 76 columns, for download
// and for copying into a file that other tools will read.
func (c ChangeRecord) LDIFFolded() string {
	var b strings.Builder
	w := ldif.NewWriter(&b)
	_ = w.WriteRecord(c.ldifRecord())
	return b.String()
}

// EntryLDIF renders an entry as an LDIF content record, for export.
//
// Sensitive attributes are omitted rather than exported. An LDIF export is a
// file that gets mailed, pasted into a ticket and committed to a repository,
// and userPassword hashes have no business in any of those.
func EntryLDIF(e *Entry) *ldif.Record {
	r := &ldif.Record{DN: e.DN}
	for _, name := range e.Order {
		if schema.IsSensitive(name) {
			continue
		}
		r.Attrs = append(r.Attrs, ldif.Attribute{Name: name, Values: e.Attributes[name]})
	}
	return r
}

// EntryLDIFWithSecrets is EntryLDIF including sensitive attributes. It exists
// for the case where the user is deliberately exporting an entry to recreate it
// elsewhere, and the caller must have asked for it explicitly.
func EntryLDIFWithSecrets(e *Entry) *ldif.Record {
	r := &ldif.Record{DN: e.DN}
	for _, name := range e.Order {
		r.Attrs = append(r.Attrs, ldif.Attribute{Name: name, Values: e.Attributes[name]})
	}
	return r
}

// Summary is a one-line description of a change, for logs and for the list of
// pending changes. It never contains an attribute value.
func (c ChangeRecord) Summary() string {
	switch c.Type {
	case ChangeAdd:
		return fmt.Sprintf("add %s", c.DN)
	case ChangeDelete:
		return fmt.Sprintf("delete %s", c.DN)
	case ChangeModRDN:
		if len(c.NewSuperior) > 0 {
			return fmt.Sprintf("move %s to %s,%s", c.DN, c.NewRDN, c.NewSuperior)
		}
		return fmt.Sprintf("rename %s to %s", c.DN, c.NewRDN)
	case ChangeModify:
		names := make([]string, 0, len(c.Mods))
		seen := map[string]bool{}
		for _, m := range c.Mods {
			key := strings.ToLower(m.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			names = append(names, m.Name)
		}
		sort.Strings(names)
		return fmt.Sprintf("modify %s (%s)", c.DN, strings.Join(names, ", "))
	default:
		return fmt.Sprintf("%s %s", c.Type, c.DN)
	}
}

// AffectedAttributes returns the attribute names the change touches, sorted and
// deduplicated. The UI uses it to highlight the fields that will change.
func (c ChangeRecord) AffectedAttributes() []string {
	seen := map[string]string{}
	add := func(name string) {
		key := strings.ToLower(name)
		if _, ok := seen[key]; !ok {
			seen[key] = name
		}
	}
	for _, m := range c.Mods {
		add(m.Name)
	}
	for _, a := range c.Attrs {
		add(a.Name)
	}
	out := make([]string, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
