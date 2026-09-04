package schema

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Rendering definitions back to RFC 4512 text.
//
// This is the half of the schema package that schema editing needs: a form the
// user filled in becomes a definition string, and that string is the value of
// an ordinary LDAP modification. The definition is built here, on the server,
// for the same reason the LDIF preview is rendered here — what the user
// confirms and what the directory receives have to be the same bytes.
//
// The ordering of the elements is the ABNF's, not a preference. Servers accept
// a good deal of variation, but a definition written in declaration order is
// the one that compares cleanly against what any of them publishes back.

// ErrInvalidDefinition reports a definition that cannot be rendered.
var ErrInvalidDefinition = errors.New("schema: invalid definition")

// Definition renders the attribute type as an RFC 4512 AttributeTypeDescription.
//
// It does not return Raw even when one is present: Raw is what some server
// published, and the caller here is building a definition to send, which is a
// different thing. Deleting or replacing an existing definition uses Raw
// directly, because a server will only match the value it stored.
func (a AttributeType) Definition() (string, error) {
	if err := validOID(a.OID); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	if len(a.Names) == 0 {
		return "", fmt.Errorf("%w: an attribute type needs at least one name", ErrInvalidDefinition)
	}
	for _, n := range a.Names {
		if err := validDescr(n); err != nil {
			return "", fmt.Errorf("%w: name %q: %w", ErrInvalidDefinition, n, err)
		}
	}

	var b strings.Builder
	b.WriteString("( ")
	b.WriteString(a.OID)
	writeNames(&b, a.Names)
	writeDesc(&b, a.Desc)
	if a.Obsolete {
		b.WriteString(" OBSOLETE")
	}
	if err := writeOID(&b, "SUP", a.SuperName); err != nil {
		return "", err
	}
	for _, m := range []struct{ kw, v string }{
		{"EQUALITY", a.Equality}, {"ORDERING", a.Ordering}, {"SUBSTR", a.Substr},
	} {
		if err := writeOID(&b, m.kw, m.v); err != nil {
			return "", err
		}
	}
	if a.Syntax != "" {
		if err := validOID(a.Syntax); err != nil {
			return "", fmt.Errorf("%w: SYNTAX: %w", ErrInvalidDefinition, err)
		}
		b.WriteString(" SYNTAX ")
		b.WriteString(a.Syntax)
		// The length suffix is advisory, and a server is free to ignore it. It
		// is written back because dropping it would silently widen a bound the
		// author set deliberately.
		if a.SyntaxLen > 0 {
			fmt.Fprintf(&b, "{%d}", a.SyntaxLen)
		}
	}
	if a.SingleValue {
		b.WriteString(" SINGLE-VALUE")
	}
	if a.Collective {
		b.WriteString(" COLLECTIVE")
	}
	if a.NoUserModification {
		b.WriteString(" NO-USER-MODIFICATION")
	}
	if a.Usage != UsageUserApplications {
		b.WriteString(" USAGE ")
		b.WriteString(a.Usage.String())
	}
	writeExtensions(&b, a.Extensions)
	b.WriteString(" )")
	return b.String(), nil
}

// Definition renders the object class as an RFC 4512 ObjectClassDescription.
func (o ObjectClass) Definition() (string, error) {
	if err := validOID(o.OID); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidDefinition, err)
	}
	if len(o.Names) == 0 {
		return "", fmt.Errorf("%w: an object class needs at least one name", ErrInvalidDefinition)
	}
	for _, n := range o.Names {
		if err := validDescr(n); err != nil {
			return "", fmt.Errorf("%w: name %q: %w", ErrInvalidDefinition, n, err)
		}
	}

	var b strings.Builder
	b.WriteString("( ")
	b.WriteString(o.OID)
	writeNames(&b, o.Names)
	writeDesc(&b, o.Desc)
	if o.Obsolete {
		b.WriteString(" OBSOLETE")
	}
	if len(o.SuperNames) > 0 {
		if err := writeOIDs(&b, "SUP", o.SuperNames); err != nil {
			return "", err
		}
	}
	// The kind is always written, including STRUCTURAL. RFC 4512 makes
	// STRUCTURAL the default when the kind is absent, so writing it changes
	// nothing — except that the definition then says what it means, which is
	// the point of a file someone is meant to read.
	b.WriteString(" ")
	b.WriteString(o.Kind.String())
	if len(o.Must) > 0 {
		if err := writeOIDs(&b, "MUST", o.Must); err != nil {
			return "", err
		}
	}
	if len(o.May) > 0 {
		if err := writeOIDs(&b, "MAY", o.May); err != nil {
			return "", err
		}
	}
	writeExtensions(&b, o.Extensions)
	b.WriteString(" )")
	return b.String(), nil
}

func writeNames(b *strings.Builder, names []string) {
	b.WriteString(" NAME ")
	if len(names) == 1 {
		b.WriteString(quote(names[0]))
		return
	}
	b.WriteString("(")
	for _, n := range names {
		b.WriteString(" ")
		b.WriteString(quote(n))
	}
	b.WriteString(" )")
}

func writeDesc(b *strings.Builder, desc string) {
	if desc == "" {
		return
	}
	b.WriteString(" DESC ")
	b.WriteString(quote(desc))
}

func writeOID(b *strings.Builder, keyword, oid string) error {
	if oid == "" {
		return nil
	}
	if err := validOIDOrDescr(oid); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidDefinition, keyword, err)
	}
	b.WriteString(" ")
	b.WriteString(keyword)
	b.WriteString(" ")
	b.WriteString(oid)
	return nil
}

func writeOIDs(b *strings.Builder, keyword string, oids []string) error {
	for _, o := range oids {
		if err := validOIDOrDescr(o); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrInvalidDefinition, keyword, err)
		}
	}
	b.WriteString(" ")
	b.WriteString(keyword)
	b.WriteString(" ")
	if len(oids) == 1 {
		b.WriteString(oids[0])
		return nil
	}
	b.WriteString("( ")
	b.WriteString(strings.Join(oids, " $ "))
	b.WriteString(" )")
	return nil
}

// writeExtensions renders X- extensions in name order.
//
// A map has no order, and a definition that renders differently on each call
// would produce a spurious difference every time it was compared against what
// the server holds.
func writeExtensions(b *strings.Builder, ext Extensions) {
	if len(ext) == 0 {
		return
	}
	names := make([]string, 0, len(ext))
	for name := range ext {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		values := ext[name]
		b.WriteString(" ")
		b.WriteString(name)
		b.WriteString(" ")
		if len(values) == 1 {
			b.WriteString(quote(values[0]))
			continue
		}
		b.WriteString("(")
		for _, v := range values {
			b.WriteString(" ")
			b.WriteString(quote(v))
		}
		b.WriteString(" )")
	}
}

// quote renders a qdstring with the two escapes RFC 4512 section 4.1 defines.
// Nothing else is escaped, and nothing else needs to be.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\5C`)
	s = strings.ReplaceAll(s, `'`, `\27`)
	return "'" + s + "'"
}

// validOID accepts a numeric OID: dot-separated numbers, no leading zeros in a
// multi-digit arc.
func validOID(oid string) error {
	if oid == "" {
		return errors.New("the OID is empty")
	}
	for _, arc := range strings.Split(oid, ".") {
		if arc == "" {
			return fmt.Errorf("%q is not a numeric OID", oid)
		}
		if len(arc) > 1 && arc[0] == '0' {
			return fmt.Errorf("%q has a leading zero in an arc", oid)
		}
		for i := 0; i < len(arc); i++ {
			if arc[i] < '0' || arc[i] > '9' {
				return fmt.Errorf("%q is not a numeric OID", oid)
			}
		}
	}
	return nil
}

// validDescr accepts a keystring: a letter followed by letters, digits and
// hyphens. This is what a NAME may be.
func validDescr(s string) error {
	if s == "" {
		return errors.New("the name is empty")
	}
	if !isAlpha(s[0]) {
		return errors.New("a name must start with a letter")
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isAlpha(c) && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("%q is not allowed in a name", string(c))
		}
	}
	return nil
}

// validOIDOrDescr accepts either form, which is what SUP, MUST, MAY and the
// matching rule keywords take: a numeric OID or the name of another definition.
func validOIDOrDescr(s string) error {
	if s == "" {
		return errors.New("the value is empty")
	}
	if s[0] >= '0' && s[0] <= '9' {
		return validOID(s)
	}
	return validDescr(s)
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
