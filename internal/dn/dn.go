// Package dn implements RFC 4514 distinguished names.
//
// DNs are never strings in Alder. Anything that names an entry carries a DN
// value, and the only way to produce one is Parse or the New constructors,
// both of which escape correctly. There is deliberately no exported way to
// build a DN by concatenating text.
package dn

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrEmpty is returned when a DN is required but the input is empty. The empty
// DN is legal in LDAP (it names the RootDSE), so callers that accept it should
// use ParseAllowEmpty rather than treating this as a hard error.
var ErrEmpty = errors.New("dn: empty distinguished name")

// AttributeTypeAndValue is one type=value assertion inside an RDN.
type AttributeTypeAndValue struct {
	// Type is the attribute description, e.g. "cn" or "2.5.4.3". Case is
	// preserved as written; comparison is case-insensitive.
	Type string
	// Value is the decoded value, with all RFC 4514 escaping removed.
	Value string
	// Hex reports that the value was written in the "#" hexstring form, that
	// is, a BER-encoded value. Value then holds the raw decoded bytes and
	// String renders the hexstring form back.
	Hex bool
}

// RDN is a relative distinguished name: one or more type=value assertions
// joined by "+". Multi-valued RDNs are rare but legal, and both target servers
// accept them, so they are modelled rather than rejected.
type RDN []AttributeTypeAndValue

// DN is a distinguished name, most specific RDN first, matching the order in
// which it is written.
type DN []RDN

// New builds a DN from single-valued RDNs given as alternating type and value
// arguments, most specific first. Values are taken literally and escaped when
// rendered.
//
//	dn.New("uid", "alice", "ou", "people", "dc", "alder", "dc", "test")
func New(typeValuePairs ...string) (DN, error) {
	if len(typeValuePairs)%2 != 0 {
		return nil, errors.New("dn: New requires an even number of arguments")
	}
	d := make(DN, 0, len(typeValuePairs)/2)
	for i := 0; i < len(typeValuePairs); i += 2 {
		t := typeValuePairs[i]
		if err := ValidateType(t); err != nil {
			return nil, err
		}
		d = append(d, RDN{{Type: t, Value: typeValuePairs[i+1]}})
	}
	return d, nil
}

// MustNew is New for package-level values and tests. It panics on error.
func MustNew(typeValuePairs ...string) DN {
	d, err := New(typeValuePairs...)
	if err != nil {
		panic(err)
	}
	return d
}

// MustParse is Parse for package-level values and tests. It panics on error.
func MustParse(s string) DN {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Parse parses an RFC 4514 string representation. It rejects the empty DN; use
// ParseAllowEmpty where the RootDSE is a valid target.
func Parse(s string) (DN, error) {
	d, err := ParseAllowEmpty(s)
	if err != nil {
		return nil, err
	}
	if len(d) == 0 {
		return nil, ErrEmpty
	}
	return d, nil
}

// ParseAllowEmpty parses an RFC 4514 string representation, accepting "" as the
// empty DN that names the RootDSE.
func ParseAllowEmpty(s string) (DN, error) {
	if !utf8.ValidString(s) {
		return nil, errors.New("dn: input is not valid UTF-8")
	}
	if strings.TrimSpace(s) == "" {
		return DN{}, nil
	}
	p := &parser{in: s}
	return p.parseDN()
}

// String renders the DN per RFC 4514, escaping every value.
func (d DN) String() string {
	parts := make([]string, len(d))
	for i, r := range d {
		parts[i] = r.String()
	}
	return strings.Join(parts, ",")
}

// String renders the RDN per RFC 4514. Assertions are rendered in the order
// held; Equal is order-insensitive, so a multi-valued RDN still compares equal
// to the same RDN written the other way round.
func (r RDN) String() string {
	parts := make([]string, len(r))
	for i, atv := range r {
		if atv.Hex {
			parts[i] = atv.Type + "=#" + hexEncode(atv.Value)
			continue
		}
		parts[i] = atv.Type + "=" + EscapeValue(atv.Value)
	}
	return strings.Join(parts, "+")
}

// IsEmpty reports whether this is the empty DN naming the RootDSE.
func (d DN) IsEmpty() bool { return len(d) == 0 }

// RDN returns the leftmost, most specific RDN, or nil for the empty DN.
func (d DN) RDN() RDN {
	if len(d) == 0 {
		return nil
	}
	return d[0]
}

// Parent returns the DN one level up. The parent of a one-RDN DN is the empty
// DN, and the parent of the empty DN is the empty DN.
func (d DN) Parent() DN {
	if len(d) == 0 {
		return DN{}
	}
	return append(DN{}, d[1:]...)
}

// Child returns a new DN with rdn prepended as the most specific component. The
// receiver is not modified.
func (d DN) Child(rdn RDN) DN {
	out := make(DN, 0, len(d)+1)
	out = append(out, rdn)
	return append(out, d...)
}

// ChildAttr is Child for the common single-valued case.
func (d DN) ChildAttr(attrType, value string) (DN, error) {
	if err := ValidateType(attrType); err != nil {
		return nil, err
	}
	return d.Child(RDN{{Type: attrType, Value: value}}), nil
}

// Equal reports whether two DNs name the same entry.
//
// Attribute types compare case-insensitively. Values compare with a
// caseIgnoreMatch approximation: case-folded, with leading, trailing and
// repeated inner spaces collapsed. That is correct for the attribute types that
// appear in real DNs (cn, ou, dc, uid, o, l, c), all of which are directory
// strings with caseIgnoreMatch equality. Exact matching for an arbitrary syntax
// needs the schema, and the schema is deliberately not reachable from here:
// this package has no dependencies. Compare through the schema where one is
// available.
func (d DN) Equal(other DN) bool {
	if len(d) != len(other) {
		return false
	}
	for i := range d {
		if !d[i].Equal(other[i]) {
			return false
		}
	}
	return true
}

// Equal reports whether two RDNs are equivalent, ignoring the order of the
// assertions within a multi-valued RDN.
func (r RDN) Equal(other RDN) bool {
	if len(r) != len(other) {
		return false
	}
	used := make([]bool, len(other))
	for _, a := range r {
		found := false
		for j, b := range other {
			if used[j] || !a.equal(b) {
				continue
			}
			used[j] = true
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func (a AttributeTypeAndValue) equal(b AttributeTypeAndValue) bool {
	if !strings.EqualFold(a.Type, b.Type) {
		return false
	}
	if a.Hex != b.Hex {
		return false
	}
	if a.Hex {
		return a.Value == b.Value
	}
	return strings.EqualFold(foldSpace(a.Value), foldSpace(b.Value))
}

// HasSuffix reports whether d is at or below suffix in the tree. Every DN,
// including the empty DN, has the empty DN as a suffix.
func (d DN) HasSuffix(suffix DN) bool {
	if len(suffix) > len(d) {
		return false
	}
	return d[len(d)-len(suffix):].Equal(suffix)
}

// IsChildOf reports whether d is an immediate child of parent.
func (d DN) IsChildOf(parent DN) bool {
	return len(d) == len(parent)+1 && d.HasSuffix(parent)
}

// EscapeValue escapes an attribute value for use in a DN, per RFC 4514 section
// 2.4: a leading "#" or space, a trailing space, and the characters
// " + , ; < > \ are escaped. Control characters, and any byte that is not part
// of a valid UTF-8 sequence, are escaped as hex pairs so that the rendered DN
// is always valid UTF-8 and can be parsed back.
func EscapeValue(v string) string {
	if v == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(v) + 8)
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '\\' || c == '"' || c == '+' || c == ',' || c == ';' || c == '<' || c == '>':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == ' ' && (i == 0 || i == len(v)-1):
			b.WriteString("\\ ")
		case c == '#' && i == 0:
			b.WriteString("\\#")
		case c < 0x20 || c == 0x7f:
			writeHexEscape(&b, c)
		case c < 0x80:
			b.WriteByte(c)
		default:
			// Multi-byte UTF-8 passes through intact; a stray byte that does
			// not start a valid sequence is escaped.
			r, size := utf8.DecodeRuneInString(v[i:])
			if r == utf8.RuneError && size <= 1 {
				writeHexEscape(&b, c)
				continue
			}
			b.WriteString(v[i : i+size])
			i += size - 1
		}
	}
	return b.String()
}

func writeHexEscape(b *strings.Builder, c byte) {
	b.WriteByte('\\')
	b.WriteByte(hexDigits[c>>4])
	b.WriteByte(hexDigits[c&0x0f])
}

const hexDigits = "0123456789abcdef"

func hexEncode(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := 0; i < len(s); i++ {
		b.WriteByte(hexDigits[s[i]>>4])
		b.WriteByte(hexDigits[s[i]&0x0f])
	}
	return b.String()
}

// foldSpace trims and collapses runs of spaces, approximating the insignificant
// space handling of RFC 4518.
func foldSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ValidateType checks an attribute description: either a keystring
// (ALPHA *(ALPHA / DIGIT / HYPHEN)) or a numericoid, optionally followed by
// ";"-separated options such as ";binary".
func ValidateType(t string) error {
	if t == "" {
		return errors.New("dn: empty attribute type")
	}
	base, opts, hasOpts := strings.Cut(t, ";")
	if base == "" {
		return fmt.Errorf("dn: invalid attribute type %q", t)
	}
	if isDigit(base[0]) {
		if !isNumericOID(base) {
			return fmt.Errorf("dn: invalid numeric OID %q", t)
		}
	} else if !isKeystring(base) {
		return fmt.Errorf("dn: invalid attribute type %q", t)
	}
	if hasOpts {
		for _, o := range strings.Split(opts, ";") {
			if !isKeystring(o) {
				return fmt.Errorf("dn: invalid attribute option in %q", t)
			}
		}
	}
	return nil
}

func isKeystring(s string) bool {
	if s == "" || !isAlpha(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !isAlpha(c) && !isDigit(c) && c != '-' {
			return false
		}
	}
	return true
}

func isNumericOID(s string) bool {
	for _, arc := range strings.Split(s, ".") {
		if arc == "" {
			return false
		}
		if len(arc) > 1 && arc[0] == '0' {
			return false
		}
		for i := 0; i < len(arc); i++ {
			if !isDigit(arc[i]) {
				return false
			}
		}
	}
	return true
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
