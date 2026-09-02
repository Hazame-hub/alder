package filter

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Parse parses an RFC 4515 string representation into a Filter tree.
//
// This is how a raw filter typed by the user enters the system. It is parsed
// rather than passed through, so that what reaches the server is something
// Alder understood, can display, and can re-render. The grammar is applied
// strictly: the filter must be parenthesised, and every escape must be a
// backslash followed by two hex digits.
func Parse(s string) (Filter, error) {
	if !utf8.ValidString(s) {
		return Filter{}, fmt.Errorf("filter: input is not valid UTF-8")
	}
	p := &parser{in: strings.TrimSpace(s)}
	if p.in == "" {
		return Filter{}, fmt.Errorf("filter: empty filter")
	}
	if p.in[0] != '(' {
		return Filter{}, fmt.Errorf("filter: must be enclosed in parentheses, e.g. (%s)", p.in)
	}
	f, err := p.parseFilter(0)
	if err != nil {
		return Filter{}, err
	}
	if p.pos != len(p.in) {
		return Filter{}, p.errf("unexpected trailing input %q", p.in[p.pos:])
	}
	if err := f.Err(); err != nil {
		return Filter{}, err
	}
	return f, nil
}

type parser struct {
	in  string
	pos int
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("filter: at offset %d: "+format, append([]any{p.pos}, args...)...)
}

func (p *parser) eof() bool { return p.pos >= len(p.in) }

func (p *parser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.in[p.pos]
}

func (p *parser) parseFilter(depth int) (Filter, error) {
	if depth > maxDepth {
		return Filter{}, p.errf("nested more than %d levels deep", maxDepth)
	}
	if p.peek() != '(' {
		return Filter{}, p.errf("expected %q", "(")
	}
	p.pos++
	var (
		f   Filter
		err error
	)
	switch p.peek() {
	case '&':
		p.pos++
		f, err = p.parseList(KindAnd, depth)
	case '|':
		p.pos++
		f, err = p.parseList(KindOr, depth)
	case '!':
		p.pos++
		var sub Filter
		sub, err = p.parseFilter(depth + 1)
		if err == nil {
			if p.peek() != ')' {
				return Filter{}, p.errf("not takes exactly one operand")
			}
			f = Not(sub)
		}
	default:
		f, err = p.parseItem()
	}
	if err != nil {
		return Filter{}, err
	}
	if p.peek() != ')' {
		if p.eof() {
			return Filter{}, p.errf("unbalanced parentheses: missing %q", ")")
		}
		return Filter{}, p.errf("expected %q, got %q", ")", string(p.peek()))
	}
	p.pos++
	return f, nil
}

func (p *parser) parseList(k Kind, depth int) (Filter, error) {
	var subs []Filter
	for p.peek() == '(' {
		sub, err := p.parseFilter(depth + 1)
		if err != nil {
			return Filter{}, err
		}
		subs = append(subs, sub)
	}
	if len(subs) == 0 {
		return Filter{}, p.errf("%s needs at least one operand", k)
	}
	// junction collapses a single operand, which would lose the fact that the
	// user wrote (&(x)); keep the node so that Render round-trips the input.
	if len(subs) == 1 {
		return Filter{Kind: k, Subs: subs}, nil
	}
	return junction(k, subs), nil
}

func (p *parser) parseItem() (Filter, error) {
	attr := p.readAttr()
	switch p.peek() {
	case ':':
		return p.parseExtensible(attr)
	case '~', '>', '<':
		op := p.peek()
		p.pos++
		if p.peek() != '=' {
			return Filter{}, p.errf("expected %q after %q", "=", string(op))
		}
		p.pos++
		parts, starred, err := p.parseValueParts()
		if err != nil {
			return Filter{}, err
		}
		if starred {
			return Filter{}, p.errf("%q does not take a substring assertion", string(op)+"=")
		}
		switch op {
		case '~':
			return Approx(attr, parts[0]), nil
		case '>':
			return GreaterOrEqual(attr, parts[0]), nil
		default:
			return LessOrEqual(attr, parts[0]), nil
		}
	case '=':
		p.pos++
		parts, starred, err := p.parseValueParts()
		if err != nil {
			return Filter{}, err
		}
		if !starred {
			return Equal(attr, parts[0]), nil
		}
		if len(parts) == 2 && parts[0] == "" && parts[1] == "" {
			return Present(attr), nil
		}
		return Substrings(attr, parts[0], parts[1:len(parts)-1], parts[len(parts)-1]), nil
	case 0:
		return Filter{}, p.errf("unterminated filter")
	default:
		return Filter{}, p.errf("expected a comparison operator after attribute %q, got %q", attr, string(p.peek()))
	}
}

// parseExtensible handles the ":dn", ":rule" and ":=" tail of an extensible
// match. The caller has already consumed the attribute, which may be empty.
func (p *parser) parseExtensible(attr string) (Filter, error) {
	var (
		rule    string
		dnAttrs bool
	)
	for {
		if p.peek() != ':' {
			return Filter{}, p.errf("expected %q in extensible match", ":")
		}
		p.pos++
		if p.peek() == '=' {
			p.pos++
			break
		}
		start := p.pos
		for !p.eof() && p.in[p.pos] != ':' && p.in[p.pos] != ')' {
			p.pos++
		}
		token := p.in[start:p.pos]
		switch {
		case token == "":
			return Filter{}, p.errf("empty component in extensible match")
		case strings.EqualFold(token, "dn"):
			// ":dn" is a flag, and it must come before the matching rule.
			if dnAttrs || rule != "" {
				return Filter{}, p.errf("unexpected %q in extensible match", token)
			}
			dnAttrs = true
		case rule == "":
			rule = token
		default:
			return Filter{}, p.errf("unexpected %q in extensible match", token)
		}
	}
	parts, starred, err := p.parseValueParts()
	if err != nil {
		return Filter{}, err
	}
	if starred {
		return Filter{}, p.errf("an extensible match does not take a substring assertion")
	}
	return Extensible(attr, rule, parts[0], dnAttrs), nil
}

// readAttr consumes an attribute description. Validation is left to the
// constructors so that the error message names the offending attribute.
func (p *parser) readAttr() string {
	start := p.pos
	for !p.eof() {
		c := p.in[p.pos]
		if isAlpha(c) || isDigit(c) || c == '-' || c == '.' || c == ';' {
			p.pos++
			continue
		}
		break
	}
	return p.in[start:p.pos]
}

// parseValueParts consumes an assertion value up to the closing parenthesis,
// decoding escapes and splitting on unescaped asterisks. It returns at least
// one part; starred reports whether any asterisk was present, which is what
// separates an equality match from a substring or present match.
func (p *parser) parseValueParts() (parts []string, starred bool, err error) {
	var cur strings.Builder
	for {
		if p.eof() {
			return nil, false, p.errf("unterminated assertion value")
		}
		c := p.in[p.pos]
		switch c {
		case ')':
			parts = append(parts, cur.String())
			return parts, starred, nil
		case '(':
			return nil, false, p.errf("unescaped %q in an assertion value; write it as %q", "(", "\\28")
		case '*':
			p.pos++
			starred = true
			parts = append(parts, cur.String())
			cur.Reset()
		case '\\':
			b, err := p.parseEscape()
			if err != nil {
				return nil, false, err
			}
			cur.WriteByte(b)
		case 0:
			return nil, false, p.errf("NUL in an assertion value; write it as %q", "\\00")
		default:
			cur.WriteByte(c)
			p.pos++
		}
	}
}

// parseEscape consumes a backslash and the two hex digits that RFC 4515
// requires after it. Single-character escapes such as "\*" are a LDAPv2 habit
// and are rejected, because accepting them would make the meaning of a
// backslash depend on what follows it.
func (p *parser) parseEscape() (byte, error) {
	p.pos++ // consume '\'
	if p.pos+1 >= len(p.in) {
		return 0, p.errf("truncated escape sequence")
	}
	hi, ok1 := hexVal(p.in[p.pos])
	lo, ok2 := hexVal(p.in[p.pos+1])
	if !ok1 || !ok2 {
		return 0, p.errf("invalid escape %q: a backslash must be followed by two hex digits", p.in[p.pos-1:min(p.pos+2, len(p.in))])
	}
	p.pos += 2
	return hi<<4 | lo, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
