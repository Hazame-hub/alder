package dn

import "fmt"

// parser is a recursive-descent parser for the RFC 4514 distinguishedName
// grammar.
//
// It is lenient in one respect only: unescaped leading and trailing spaces
// around a value are accepted and dropped, because "ou=people, dc=alder" is
// written that way constantly even though RFC 4514 requires the space to be
// escaped. Everything else is strict. In particular an unescaped double quote
// is an error rather than an RFC 1779 quoted value, since silently accepting
// quoting is exactly how a value smuggles a comma past a naive parser.
type parser struct {
	in  string
	pos int
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("dn: at offset %d: "+format, append([]any{p.pos}, args...)...)
}

func (p *parser) parseDN() (DN, error) {
	var d DN
	for {
		rdn, err := p.parseRDN()
		if err != nil {
			return nil, err
		}
		d = append(d, rdn)
		if p.pos >= len(p.in) {
			return d, nil
		}
		if p.in[p.pos] != ',' {
			return nil, p.errf("expected %q, got %q", ",", p.in[p.pos])
		}
		p.pos++
		if p.pos >= len(p.in) {
			return nil, p.errf("trailing comma")
		}
	}
}

func (p *parser) parseRDN() (RDN, error) {
	var r RDN
	for {
		atv, err := p.parseATV()
		if err != nil {
			return nil, err
		}
		r = append(r, atv)
		if p.pos < len(p.in) && p.in[p.pos] == '+' {
			p.pos++
			if p.pos >= len(p.in) {
				return nil, p.errf("trailing plus")
			}
			continue
		}
		return r, nil
	}
}

func (p *parser) parseATV() (AttributeTypeAndValue, error) {
	var zero AttributeTypeAndValue
	p.skipSpace()
	start := p.pos
	for p.pos < len(p.in) && p.in[p.pos] != '=' {
		c := p.in[p.pos]
		if c == ',' || c == '+' {
			return zero, p.errf("attribute type %q is missing a value", p.in[start:p.pos])
		}
		p.pos++
	}
	if p.pos >= len(p.in) {
		return zero, p.errf("attribute type %q is missing a value", p.in[start:])
	}
	attrType := trimSpace(p.in[start:p.pos])
	if err := ValidateType(attrType); err != nil {
		return zero, err
	}
	p.pos++ // consume '='
	p.skipSpace()
	if p.pos < len(p.in) && p.in[p.pos] == '#' {
		return p.parseHexValue(attrType)
	}
	value, err := p.parseStringValue()
	if err != nil {
		return zero, err
	}
	return AttributeTypeAndValue{Type: attrType, Value: value}, nil
}

func (p *parser) parseHexValue(attrType string) (AttributeTypeAndValue, error) {
	var zero AttributeTypeAndValue
	p.pos++ // consume '#'
	start := p.pos
	var out []byte
	for p.pos+1 < len(p.in) {
		hi, ok1 := hexVal(p.in[p.pos])
		lo, ok2 := hexVal(p.in[p.pos+1])
		if !ok1 || !ok2 {
			break
		}
		out = append(out, hi<<4|lo)
		p.pos += 2
	}
	if len(out) == 0 {
		return zero, p.errf("empty or malformed hexstring value")
	}
	p.skipSpace()
	if p.pos < len(p.in) && p.in[p.pos] != ',' && p.in[p.pos] != '+' {
		return zero, p.errf("malformed hexstring value %q", p.in[start:])
	}
	return AttributeTypeAndValue{Type: attrType, Value: string(out), Hex: true}, nil
}

// parseStringValue consumes up to the next unescaped separator, decoding
// escapes as it goes. It tracks whether each decoded byte came from an escape
// so that trailing spaces can be dropped only when they were unescaped.
func (p *parser) parseStringValue() (string, error) {
	var (
		buf     []byte
		escaped []bool
	)
	for p.pos < len(p.in) {
		c := p.in[p.pos]
		switch c {
		case ',', '+':
			// End of value.
			return string(trimUnescapedTrailingSpace(buf, escaped)), nil
		case '\\':
			b, err := p.parseEscape()
			if err != nil {
				return "", err
			}
			buf = append(buf, b)
			escaped = append(escaped, true)
		case '"', ';', '<', '>':
			return "", p.errf("character %q must be escaped in a DN value", c)
		default:
			buf = append(buf, c)
			escaped = append(escaped, false)
			p.pos++
		}
	}
	return string(trimUnescapedTrailingSpace(buf, escaped)), nil
}

// parseEscape consumes a pair: a backslash followed by either a hex pair or a
// single special character. It returns one byte; a multi-byte UTF-8 character
// escaped byte by byte reassembles correctly because each byte is appended in
// order.
func (p *parser) parseEscape() (byte, error) {
	p.pos++ // consume '\'
	if p.pos >= len(p.in) {
		return 0, p.errf("trailing backslash")
	}
	c := p.in[p.pos]
	if hi, ok := hexVal(c); ok && p.pos+1 < len(p.in) {
		if lo, ok2 := hexVal(p.in[p.pos+1]); ok2 {
			p.pos += 2
			return hi<<4 | lo, nil
		}
	}
	switch c {
	case ' ', '"', '#', '+', ',', ';', '<', '=', '>', '\\':
		p.pos++
		return c, nil
	}
	return 0, p.errf("invalid escape %q", "\\"+string(c))
}

func (p *parser) skipSpace() {
	for p.pos < len(p.in) && p.in[p.pos] == ' ' {
		p.pos++
	}
}

func trimUnescapedTrailingSpace(buf []byte, escaped []bool) []byte {
	end := len(buf)
	for end > 0 && buf[end-1] == ' ' && !escaped[end-1] {
		end--
	}
	return buf[:end]
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
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
