// Package filter builds and parses RFC 4515 search filters.
//
// Filters are never produced by interpolating text. A filter is a value tree
// built with the constructors below, or parsed from a user-supplied string with
// Parse, and it reaches the wire only through Render, which escapes every
// assertion value. There is no code path that concatenates a filter from
// fragments.
package filter

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/dn"
)

// Kind identifies which of the RFC 4511 Filter choices a node is.
type Kind int

const (
	KindInvalid Kind = iota
	KindAnd
	KindOr
	KindNot
	KindEquality
	KindSubstrings
	KindGreaterOrEqual
	KindLessOrEqual
	KindPresent
	KindApprox
	KindExtensible
)

// String names the kind, for errors and tests.
func (k Kind) String() string {
	switch k {
	case KindAnd:
		return "and"
	case KindOr:
		return "or"
	case KindNot:
		return "not"
	case KindEquality:
		return "equality"
	case KindSubstrings:
		return "substrings"
	case KindGreaterOrEqual:
		return "greaterOrEqual"
	case KindLessOrEqual:
		return "lessOrEqual"
	case KindPresent:
		return "present"
	case KindApprox:
		return "approx"
	case KindExtensible:
		return "extensible"
	default:
		return "invalid"
	}
}

// Filter is one node of a search filter.
//
// It is a single struct with a Kind rather than an interface with ten
// implementations, because every consumer switches on the kind anyway and a
// flat struct is easier to compare, copy and print.
//
// The zero Filter is invalid; Render reports that rather than emitting
// anything.
type Filter struct {
	Kind Kind

	// Attr is the attribute description, for every leaf kind except a
	// matching-rule-only extensible match.
	Attr string

	// Value is the assertion value for equality, approx, greaterOrEqual,
	// lessOrEqual and extensible matches. It is stored decoded and is escaped
	// by Render.
	Value string

	// Initial, Any and Final are the substring components. Each is stored
	// decoded; the "*" separators are structure, not data.
	Initial string
	Any     []string
	Final   string

	// Subs holds the operands of and, or and not. A not node has exactly one.
	Subs []Filter

	// MatchingRule and DNAttributes apply to extensible matches only.
	MatchingRule string
	DNAttributes bool

	// err records a construction error so that the constructors can be
	// composed inline. It travels up through And, Or and Not, and Render
	// refuses to emit anything while it is set.
	err error
}

// Err reports the first construction error in the tree, if any.
func (f Filter) Err() error {
	if f.err != nil {
		return f.err
	}
	for _, s := range f.Subs {
		if err := s.Err(); err != nil {
			return err
		}
	}
	return nil
}

// Equal builds (attr=value).
func Equal(attr, value string) Filter {
	return leaf(KindEquality, attr, value)
}

// Approx builds (attr~=value).
func Approx(attr, value string) Filter {
	return leaf(KindApprox, attr, value)
}

// GreaterOrEqual builds (attr>=value).
func GreaterOrEqual(attr, value string) Filter {
	return leaf(KindGreaterOrEqual, attr, value)
}

// LessOrEqual builds (attr<=value).
func LessOrEqual(attr, value string) Filter {
	return leaf(KindLessOrEqual, attr, value)
}

// Present builds (attr=*).
func Present(attr string) Filter {
	f := Filter{Kind: KindPresent, Attr: attr}
	f.err = validateAttr(attr)
	return f
}

// Substrings builds (attr=initial*any*...*final). Empty components are
// omitted. At least one component must be non-empty; a filter with none is
// exactly a present filter and should be written as one.
func Substrings(attr, initial string, any []string, final string) Filter {
	f := Filter{Kind: KindSubstrings, Attr: attr, Initial: initial, Final: final}
	if len(any) > 0 {
		f.Any = append([]string(nil), any...)
	}
	if err := validateAttr(attr); err != nil {
		f.err = err
		return f
	}
	if initial == "" && final == "" && len(f.Any) == 0 {
		f.err = fmt.Errorf("filter: substrings filter on %q has no components; use Present", attr)
		return f
	}
	if slices.Contains(f.Any, "") {
		f.err = fmt.Errorf("filter: substrings filter on %q has an empty component", attr)
	}
	return f
}

// Contains builds (attr=*value*), the common case behind a search box.
func Contains(attr, value string) Filter {
	return Substrings(attr, "", []string{value}, "")
}

// HasPrefix builds (attr=value*).
func HasPrefix(attr, value string) Filter {
	return Substrings(attr, value, nil, "")
}

// HasSuffix builds (attr=*value).
func HasSuffix(attr, value string) Filter {
	return Substrings(attr, "", nil, value)
}

// Extensible builds an extensible match, (attr:dn:rule:=value). attr and rule
// may each be empty, but not both.
func Extensible(attr, matchingRule, value string, dnAttributes bool) Filter {
	f := Filter{
		Kind:         KindExtensible,
		Attr:         attr,
		Value:        value,
		MatchingRule: matchingRule,
		DNAttributes: dnAttributes,
	}
	if attr == "" && matchingRule == "" {
		f.err = errors.New("filter: extensible match needs an attribute or a matching rule")
		return f
	}
	if attr != "" {
		if err := validateAttr(attr); err != nil {
			f.err = err
			return f
		}
	}
	if matchingRule != "" {
		if err := dn.ValidateType(matchingRule); err != nil {
			f.err = fmt.Errorf("filter: invalid matching rule %q", matchingRule)
			return f
		}
	}
	return f
}

// And builds (&(a)(b)...). Calling it with a single operand returns that
// operand; calling it with none is an error, since an empty and matches
// everything on some servers and nothing on others.
func And(subs ...Filter) Filter { return junction(KindAnd, subs) }

// Or builds (|(a)(b)...), with the same rules as And.
func Or(subs ...Filter) Filter { return junction(KindOr, subs) }

// Not builds (!(a)).
func Not(sub Filter) Filter {
	return Filter{Kind: KindNot, Subs: []Filter{sub}}
}

func junction(k Kind, subs []Filter) Filter {
	if len(subs) == 0 {
		return Filter{Kind: k, err: fmt.Errorf("filter: %s with no operands", k)}
	}
	if len(subs) == 1 {
		return subs[0]
	}
	return Filter{Kind: k, Subs: append([]Filter(nil), subs...)}
}

func leaf(k Kind, attr, value string) Filter {
	f := Filter{Kind: k, Attr: attr, Value: value}
	f.err = validateAttr(attr)
	return f
}

// Render produces the RFC 4515 string form. This is the only function in the
// package whose output may be sent to a directory server.
func (f Filter) Render() (string, error) {
	if err := f.Err(); err != nil {
		return "", err
	}
	var b strings.Builder
	if err := f.render(&b, 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

// maxDepth bounds recursion so that a hostile raw filter cannot exhaust the
// stack. Real filters are a handful of levels deep.
const maxDepth = 100

func (f Filter) render(b *strings.Builder, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("filter: nested more than %d levels deep", maxDepth)
	}
	b.WriteByte('(')
	switch f.Kind {
	case KindAnd, KindOr, KindNot:
		switch f.Kind {
		case KindAnd:
			b.WriteByte('&')
		case KindOr:
			b.WriteByte('|')
		default:
			b.WriteByte('!')
		}
		if f.Kind == KindNot && len(f.Subs) != 1 {
			return fmt.Errorf("filter: not takes exactly one operand, got %d", len(f.Subs))
		}
		if len(f.Subs) == 0 {
			return fmt.Errorf("filter: %s with no operands", f.Kind)
		}
		for _, s := range f.Subs {
			if err := s.render(b, depth+1); err != nil {
				return err
			}
		}
	case KindEquality:
		b.WriteString(f.Attr + "=" + EscapeValue(f.Value))
	case KindApprox:
		b.WriteString(f.Attr + "~=" + EscapeValue(f.Value))
	case KindGreaterOrEqual:
		b.WriteString(f.Attr + ">=" + EscapeValue(f.Value))
	case KindLessOrEqual:
		b.WriteString(f.Attr + "<=" + EscapeValue(f.Value))
	case KindPresent:
		b.WriteString(f.Attr + "=*")
	case KindSubstrings:
		b.WriteString(f.Attr + "=")
		b.WriteString(EscapeValue(f.Initial))
		b.WriteByte('*')
		for _, a := range f.Any {
			b.WriteString(EscapeValue(a))
			b.WriteByte('*')
		}
		b.WriteString(EscapeValue(f.Final))
	case KindExtensible:
		b.WriteString(f.Attr)
		if f.DNAttributes {
			b.WriteString(":dn")
		}
		if f.MatchingRule != "" {
			b.WriteString(":" + f.MatchingRule)
		}
		b.WriteString(":=" + EscapeValue(f.Value))
	default:
		return fmt.Errorf("filter: cannot render %s filter", f.Kind)
	}
	b.WriteByte(')')
	return nil
}

// String renders the filter for logs and error messages. It never returns an
// error, so an invalid filter prints a placeholder. Do not send its result to a
// server; use Render, which fails loudly.
func (f Filter) String() string {
	s, err := f.Render()
	if err != nil {
		return "<invalid filter: " + err.Error() + ">"
	}
	return s
}

// Equal reports whether two filter trees are structurally identical. Attribute
// names compare case-insensitively; values compare exactly, because their
// matching rule is a property of the schema, not of the filter.
func (f Filter) Equal(o Filter) bool {
	if f.Kind != o.Kind ||
		!strings.EqualFold(f.Attr, o.Attr) ||
		f.Value != o.Value ||
		f.Initial != o.Initial ||
		f.Final != o.Final ||
		f.DNAttributes != o.DNAttributes ||
		!strings.EqualFold(f.MatchingRule, o.MatchingRule) ||
		len(f.Any) != len(o.Any) ||
		len(f.Subs) != len(o.Subs) {
		return false
	}
	for i := range f.Any {
		if f.Any[i] != o.Any[i] {
			return false
		}
	}
	for i := range f.Subs {
		if !f.Subs[i].Equal(o.Subs[i]) {
			return false
		}
	}
	return true
}

// EscapeValue escapes an assertion value per RFC 4515 section 3. The four
// characters that carry meaning in the grammar, plus NUL, must be escaped;
// control characters and bytes that are not valid UTF-8 are escaped too, so
// that a rendered filter is always printable and always valid UTF-8.
func EscapeValue(v string) string {
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '*' || c == '(' || c == ')' || c == '\\' || c == 0:
			writeHexEscape(&b, c)
		case c < 0x20 || c == 0x7f:
			writeHexEscape(&b, c)
		case c < 0x80:
			b.WriteByte(c)
		default:
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

const hexDigits = "0123456789abcdef"

func writeHexEscape(b *strings.Builder, c byte) {
	b.WriteByte('\\')
	b.WriteByte(hexDigits[c>>4])
	b.WriteByte(hexDigits[c&0x0f])
}

// validateAttr checks an attribute description. It is the second half of the
// injection defence: escaping protects the value, and this protects the name,
// which is not escapable in the RFC 4515 grammar.
func validateAttr(attr string) error {
	if attr == "" {
		return errors.New("filter: empty attribute description")
	}
	if err := dn.ValidateType(attr); err != nil {
		return fmt.Errorf("filter: invalid attribute description %q", attr)
	}
	return nil
}
