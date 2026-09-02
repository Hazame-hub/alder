// Package ldif implements RFC 2849 LDIF: reading and writing both content
// records and change records.
//
// This package is the reason Alder can claim its output is code. Every write
// the application performs is rendered here first, shown to the user, and only
// then applied, so the LDIF is not a report of what happened — it is the thing
// that was confirmed.
//
// Values are []byte, never string. An LDIF value is an octet string: it can
// hold a JPEG, a certificate, or a UTF-8 name, and the encoder decides between
// the plain and base64 forms by looking at the bytes. Modelling values as
// strings is how tools end up mangling binary attributes.
package ldif

import (
	"github.com/hazame-hub/alder/internal/dn"
)

// ChangeType is the LDIF changetype of a record.
type ChangeType int

// The change types. ChangeNone marks a content record, which is what an export
// produces and what a directory dump contains; the rest are change records.
const (
	ChangeNone ChangeType = iota
	ChangeAdd
	ChangeModify
	ChangeDelete
	ChangeModRDN
)

func (c ChangeType) String() string {
	switch c {
	case ChangeAdd:
		return "add"
	case ChangeModify:
		return "modify"
	case ChangeDelete:
		return "delete"
	case ChangeModRDN:
		return "modrdn"
	default:
		return ""
	}
}

// ModOp is the operation of one modification within a modify record.
type ModOp int

// The modification operations of RFC 2849 section 2.2 plus RFC 4525's
// increment, which both target servers support and neither requires.
const (
	ModAdd ModOp = iota
	ModDelete
	ModReplace
	ModIncrement
)

func (m ModOp) String() string {
	switch m {
	case ModDelete:
		return "delete"
	case ModReplace:
		return "replace"
	case ModIncrement:
		return "increment"
	default:
		return "add"
	}
}

// Attribute is an attribute description and its values.
//
// Name carries the description as written, options included, because
// "userCertificate;binary" and "userCertificate" are different attributes to a
// server and collapsing them loses information the user typed.
type Attribute struct {
	Name   string
	Values [][]byte
}

// Mod is one modification within a modify record.
//
// A delete with no values removes the whole attribute; a delete with values
// removes exactly those. That distinction is the difference between "drop the
// mail attribute" and "drop this one address", and it is preserved end to end.
type Mod struct {
	Op     ModOp
	Name   string
	Values [][]byte
}

// Record is one LDIF record.
//
// Which fields are meaningful depends on Change: Attrs for ChangeNone and
// ChangeAdd, Mods for ChangeModify, the RDN fields for ChangeModRDN, and none
// of them for ChangeDelete.
type Record struct {
	DN     dn.DN
	Change ChangeType

	// Attrs holds the attributes of a content or add record, in the order they
	// appeared. Order is preserved because an LDIF export that reorders
	// attributes produces a useless diff against the previous export.
	Attrs []Attribute

	// Mods holds the modifications of a modify record, in order.
	Mods []Mod

	// NewRDN, DeleteOldRDN and NewSuperior describe a modrdn record.
	// NewSuperior is nil when the record does not move the entry.
	NewRDN       string
	DeleteOldRDN bool
	NewSuperior  dn.DN

	// Line is the input line the record started on, for error reporting.
	Line int
}

// Attr returns the values of the named attribute in a content or add record,
// matching case-insensitively, or nil when it is absent.
func (r *Record) Attr(name string) [][]byte {
	for i := range r.Attrs {
		if equalFoldASCII(r.Attrs[i].Name, name) {
			return r.Attrs[i].Values
		}
	}
	return nil
}

// AttrStrings is Attr with the values decoded as strings, for the attributes
// that are known to be text. Binary values must not go through it.
func (r *Record) AttrStrings(name string) []string {
	vals := r.Attr(name)
	if vals == nil {
		return nil
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// equalFoldASCII compares attribute descriptions. Attribute names are ASCII by
// the grammar, so a byte-wise fold is correct here and avoids the Unicode
// special cases strings.EqualFold has to consider.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lowerASCII(a[i]) != lowerASCII(b[i]) {
			return false
		}
	}
	return true
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
