package schema

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// This file parses the RFC 4512 section 4.1 definition grammar. Every
// definition shares one shape:
//
//	( <oid> [KEYWORD [value]]... [X-EXTENSION qdstrings]... )
//
// so there is one tokeniser and one keyword loop per definition type, and the
// per-keyword bodies are the only thing that differs.
//
// Tolerance is deliberate and bounded. Unknown keywords are skipped along with
// the value that follows them, and a non-numeric OID in the leading position is
// accepted, because servers ship both. What is not tolerated is a definition
// that does not close its parenthesis or that runs out of input mid-value:
// those are reported, because they mean the caller was handed something that is
// not a schema definition at all.

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokLParen
	tokRParen
	tokDollar
	tokString // a qdstring, already unescaped
	tokBare   // an oid, keyword, or numericoid with a {len} suffix
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

type lexer struct {
	in  string
	pos int
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.in) && isSpace(l.in[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.in) {
		return token{kind: tokEOF, pos: l.pos}, nil
	}
	start := l.pos
	switch c := l.in[l.pos]; c {
	case '(':
		l.pos++
		return token{kind: tokLParen, text: "(", pos: start}, nil
	case ')':
		l.pos++
		return token{kind: tokRParen, text: ")", pos: start}, nil
	case '$':
		l.pos++
		return token{kind: tokDollar, text: "$", pos: start}, nil
	case '\'':
		return l.lexQDString()
	default:
		for l.pos < len(l.in) && !isSpace(l.in[l.pos]) && !strings.ContainsRune("()$'", rune(l.in[l.pos])) {
			l.pos++
		}
		return token{kind: tokBare, text: l.in[start:l.pos], pos: start}, nil
	}
}

// lexQDString reads a single-quoted string, decoding the two escapes RFC 4512
// defines: \5C for a backslash and \27 for an apostrophe. Any other \XX pair is
// decoded the same way rather than rejected, since that is what servers that
// escape more than the minimum expect.
func (l *lexer) lexQDString() (token, error) {
	start := l.pos
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.in) {
		c := l.in[l.pos]
		switch {
		case c == '\'':
			l.pos++
			return token{kind: tokString, text: b.String(), pos: start}, nil
		case c == '\\' && l.pos+2 < len(l.in):
			hi, ok1 := hexVal(l.in[l.pos+1])
			lo, ok2 := hexVal(l.in[l.pos+2])
			if !ok1 || !ok2 {
				b.WriteByte(c)
				l.pos++
				continue
			}
			b.WriteByte(hi<<4 | lo)
			l.pos += 3
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	return token{}, fmt.Errorf("unterminated quoted string at offset %d", start)
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

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

// defParser wraps the lexer with one token of lookahead.
type defParser struct {
	lex  *lexer
	peek *token
}

func newDefParser(s string) *defParser { return &defParser{lex: &lexer{in: s}} }

func (p *defParser) next() (token, error) {
	if p.peek != nil {
		t := *p.peek
		p.peek = nil
		return t, nil
	}
	return p.lex.next()
}

func (p *defParser) peekToken() (token, error) {
	if p.peek == nil {
		t, err := p.lex.next()
		if err != nil {
			return token{}, err
		}
		p.peek = &t
	}
	return *p.peek, nil
}

var errNoOpenParen = errors.New("definition does not start with '('")

// open consumes the leading "(" and the OID that follows it.
func (p *defParser) open() (string, error) {
	t, err := p.next()
	if err != nil {
		return "", err
	}
	if t.kind != tokLParen {
		return "", errNoOpenParen
	}
	oid, err := p.next()
	if err != nil {
		return "", err
	}
	// A quoted OID is out of grammar but does appear; accept both forms.
	if oid.kind != tokBare && oid.kind != tokString {
		return "", fmt.Errorf("expected an OID at offset %d, got %q", oid.pos, oid.text)
	}
	if oid.text == "" {
		return "", fmt.Errorf("empty OID at offset %d", oid.pos)
	}
	return oid.text, nil
}

// errTruncated reports a definition that ended before its closing parenthesis.
// Accepting one would mean silently keeping whatever had been parsed so far and
// calling a half-read definition a whole one.
var errTruncated = errors.New("definition ended before its closing ')'")

// keyword returns the next keyword, uppercased, or "" once the closing
// parenthesis is reached. Running out of input instead is an error.
func (p *defParser) keyword() (string, error) {
	t, err := p.next()
	if err != nil {
		return "", err
	}
	switch t.kind {
	case tokRParen:
		return "", nil
	case tokEOF:
		return "", errTruncated
	case tokBare:
		return strings.ToUpper(t.text), nil
	default:
		return "", fmt.Errorf("expected a keyword at offset %d, got %q", t.pos, t.text)
	}
}

// value reads a single bare or quoted token.
func (p *defParser) value() (string, error) {
	t, err := p.next()
	if err != nil {
		return "", err
	}
	if t.kind != tokBare && t.kind != tokString {
		return "", fmt.Errorf("expected a value at offset %d, got %q", t.pos, t.text)
	}
	return t.text, nil
}

// list reads either a single value or a parenthesised list of them. The "$"
// separators of an oidlist and the bare whitespace separators of a qdescrlist
// are both accepted for either, since telling them apart buys nothing.
func (p *defParser) list() ([]string, error) {
	t, err := p.peekToken()
	if err != nil {
		return nil, err
	}
	if t.kind != tokLParen {
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, nil
		}
		return []string{v}, nil
	}
	if _, err := p.next(); err != nil { // consume "("
		return nil, err
	}
	var out []string
	for {
		t, err := p.next()
		if err != nil {
			return nil, err
		}
		switch t.kind {
		case tokRParen:
			return out, nil
		case tokDollar:
			continue
		case tokBare, tokString:
			// An empty qdescr is not in the grammar and names nothing; dropping
			// it keeps the rest of a sloppy list usable.
			if t.text != "" {
				out = append(out, t.text)
			}
		case tokEOF:
			return nil, errors.New("unterminated list")
		default:
			return nil, fmt.Errorf("unexpected %q at offset %d in list", t.text, t.pos)
		}
	}
}

// skipValue consumes whatever follows an unrecognised keyword, so one unknown
// extension does not derail the rest of the definition. A keyword with no value
// at all is left alone: the next peek is a keyword or a closing parenthesis.
func (p *defParser) skipValue() error {
	t, err := p.peekToken()
	if err != nil {
		return err
	}
	switch t.kind {
	case tokLParen, tokString:
		_, err := p.list()
		return err
	case tokBare:
		// A bare token here is ambiguous: it is either this keyword's value or
		// the next keyword. Treat an all-caps token as the next keyword and
		// anything else as a value, which is how the grammar actually reads.
		if t.text == strings.ToUpper(t.text) && !strings.ContainsAny(t.text, ".0123456789") {
			return nil
		}
		_, err := p.next()
		return err
	default:
		return nil
	}
}

// extension records an X-... keyword and its values.
func addExtension(ext *Extensions, name string, values []string) {
	if *ext == nil {
		*ext = Extensions{}
	}
	(*ext)[name] = append((*ext)[name], values...)
}

// ParseObjectClass parses one RFC 4512 ObjectClassDescription.
func ParseObjectClass(def string) (*ObjectClass, error) {
	p := newDefParser(def)
	oid, err := p.open()
	if err != nil {
		return nil, err
	}
	oc := &ObjectClass{OID: oid, Kind: KindStructural, Raw: strings.TrimSpace(def)}
	for {
		kw, err := p.keyword()
		if err != nil {
			return nil, err
		}
		if kw == "" {
			return oc, nil
		}
		switch kw {
		case "NAME":
			if oc.Names, err = p.list(); err != nil {
				return nil, err
			}
		case "DESC":
			if oc.Desc, err = p.value(); err != nil {
				return nil, err
			}
		case "OBSOLETE":
			oc.Obsolete = true
		case "SUP":
			if oc.SuperNames, err = p.list(); err != nil {
				return nil, err
			}
		case "ABSTRACT":
			oc.Kind = KindAbstract
		case "STRUCTURAL":
			oc.Kind = KindStructural
		case "AUXILIARY":
			oc.Kind = KindAuxiliary
		case "MUST":
			if oc.Must, err = p.list(); err != nil {
				return nil, err
			}
		case "MAY":
			if oc.May, err = p.list(); err != nil {
				return nil, err
			}
		default:
			if err := handleUnknown(p, kw, &oc.Extensions); err != nil {
				return nil, err
			}
		}
	}
}

// ParseAttributeType parses one RFC 4512 AttributeTypeDescription.
func ParseAttributeType(def string) (*AttributeType, error) {
	p := newDefParser(def)
	oid, err := p.open()
	if err != nil {
		return nil, err
	}
	at := &AttributeType{OID: oid, Raw: strings.TrimSpace(def)}
	for {
		kw, err := p.keyword()
		if err != nil {
			return nil, err
		}
		if kw == "" {
			return at, nil
		}
		switch kw {
		case "NAME":
			if at.Names, err = p.list(); err != nil {
				return nil, err
			}
		case "DESC":
			if at.Desc, err = p.value(); err != nil {
				return nil, err
			}
		case "OBSOLETE":
			at.Obsolete = true
		case "SUP":
			if at.SuperName, err = p.value(); err != nil {
				return nil, err
			}
		case "EQUALITY":
			if at.Equality, err = p.value(); err != nil {
				return nil, err
			}
		case "ORDERING":
			if at.Ordering, err = p.value(); err != nil {
				return nil, err
			}
		case "SUBSTR", "SUBSTRINGS":
			if at.Substr, err = p.value(); err != nil {
				return nil, err
			}
		case "SYNTAX":
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			at.Syntax, at.SyntaxLen = splitNOIDLen(v)
		case "SINGLE-VALUE":
			at.SingleValue = true
		case "COLLECTIVE":
			at.Collective = true
		case "NO-USER-MODIFICATION":
			at.NoUserModification = true
		case "USAGE":
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			at.Usage = parseUsage(v)
		default:
			if err := handleUnknown(p, kw, &at.Extensions); err != nil {
				return nil, err
			}
		}
	}
}

// ParseSyntax parses one RFC 4512 SyntaxDescription.
func ParseSyntax(def string) (*Syntax, error) {
	p := newDefParser(def)
	oid, err := p.open()
	if err != nil {
		return nil, err
	}
	sy := &Syntax{OID: oid, Raw: strings.TrimSpace(def)}
	for {
		kw, err := p.keyword()
		if err != nil {
			return nil, err
		}
		if kw == "" {
			return sy, nil
		}
		switch kw {
		case "DESC":
			if sy.Desc, err = p.value(); err != nil {
				return nil, err
			}
		default:
			if err := handleUnknown(p, kw, &sy.Extensions); err != nil {
				return nil, err
			}
		}
	}
}

// ParseMatchingRule parses one RFC 4512 MatchingRuleDescription.
func ParseMatchingRule(def string) (*MatchingRule, error) {
	p := newDefParser(def)
	oid, err := p.open()
	if err != nil {
		return nil, err
	}
	mr := &MatchingRule{OID: oid, Raw: strings.TrimSpace(def)}
	for {
		kw, err := p.keyword()
		if err != nil {
			return nil, err
		}
		if kw == "" {
			return mr, nil
		}
		switch kw {
		case "NAME":
			if mr.Names, err = p.list(); err != nil {
				return nil, err
			}
		case "DESC":
			if mr.Desc, err = p.value(); err != nil {
				return nil, err
			}
		case "OBSOLETE":
			mr.Obsolete = true
		case "SYNTAX":
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			mr.Syntax, _ = splitNOIDLen(v)
		default:
			if err := handleUnknown(p, kw, &mr.Extensions); err != nil {
				return nil, err
			}
		}
	}
}

// ParseMatchingRuleUse parses one RFC 4512 MatchingRuleUseDescription.
func ParseMatchingRuleUse(def string) (*MatchingRuleUse, error) {
	p := newDefParser(def)
	oid, err := p.open()
	if err != nil {
		return nil, err
	}
	mru := &MatchingRuleUse{OID: oid, Raw: strings.TrimSpace(def)}
	for {
		kw, err := p.keyword()
		if err != nil {
			return nil, err
		}
		if kw == "" {
			return mru, nil
		}
		switch kw {
		case "NAME":
			if mru.Names, err = p.list(); err != nil {
				return nil, err
			}
		case "DESC":
			if mru.Desc, err = p.value(); err != nil {
				return nil, err
			}
		case "OBSOLETE":
			mru.Obsolete = true
		case "APPLIES":
			if mru.Applies, err = p.list(); err != nil {
				return nil, err
			}
		default:
			if err := handleUnknown(p, kw, &mru.Extensions); err != nil {
				return nil, err
			}
		}
	}
}

// ParseDITContentRule parses one RFC 4512 DITContentRuleDescription.
func ParseDITContentRule(def string) (*DITContentRule, error) {
	p := newDefParser(def)
	oid, err := p.open()
	if err != nil {
		return nil, err
	}
	cr := &DITContentRule{OID: oid, Raw: strings.TrimSpace(def)}
	for {
		kw, err := p.keyword()
		if err != nil {
			return nil, err
		}
		if kw == "" {
			return cr, nil
		}
		switch kw {
		case "NAME":
			if cr.Names, err = p.list(); err != nil {
				return nil, err
			}
		case "DESC":
			if cr.Desc, err = p.value(); err != nil {
				return nil, err
			}
		case "OBSOLETE":
			cr.Obsolete = true
		case "AUX":
			if cr.Aux, err = p.list(); err != nil {
				return nil, err
			}
		case "MUST":
			if cr.Must, err = p.list(); err != nil {
				return nil, err
			}
		case "MAY":
			if cr.May, err = p.list(); err != nil {
				return nil, err
			}
		case "NOT":
			if cr.Not, err = p.list(); err != nil {
				return nil, err
			}
		default:
			if err := handleUnknown(p, kw, &cr.Extensions); err != nil {
				return nil, err
			}
		}
	}
}

// ParseNameForm parses one RFC 4512 NameFormDescription.
func ParseNameForm(def string) (*NameForm, error) {
	p := newDefParser(def)
	oid, err := p.open()
	if err != nil {
		return nil, err
	}
	nf := &NameForm{OID: oid, Raw: strings.TrimSpace(def)}
	for {
		kw, err := p.keyword()
		if err != nil {
			return nil, err
		}
		if kw == "" {
			return nf, nil
		}
		switch kw {
		case "NAME":
			if nf.Names, err = p.list(); err != nil {
				return nil, err
			}
		case "DESC":
			if nf.Desc, err = p.value(); err != nil {
				return nil, err
			}
		case "OBSOLETE":
			nf.Obsolete = true
		case "OC":
			if nf.ObjectClass, err = p.value(); err != nil {
				return nil, err
			}
		case "MUST":
			if nf.Must, err = p.list(); err != nil {
				return nil, err
			}
		case "MAY":
			if nf.May, err = p.list(); err != nil {
				return nil, err
			}
		default:
			if err := handleUnknown(p, kw, &nf.Extensions); err != nil {
				return nil, err
			}
		}
	}
}

// handleUnknown records an X- extension and skips anything else, so a keyword
// this parser does not know about costs the definition its value, not its life.
func handleUnknown(p *defParser, kw string, ext *Extensions) error {
	if strings.HasPrefix(kw, "X-") {
		values, err := p.list()
		if err != nil {
			return err
		}
		addExtension(ext, kw, values)
		return nil
	}
	return p.skipValue()
}

// splitNOIDLen splits "1.3.6.1.4.1.1466.115.121.1.15{32768}" into the syntax
// OID and the advisory length. A missing or unparsable suffix gives a zero
// length, which callers read as "no advisory limit".
func splitNOIDLen(v string) (string, int) {
	open := strings.IndexByte(v, '{')
	if open < 0 || !strings.HasSuffix(v, "}") {
		return v, 0
	}
	n, err := strconv.Atoi(v[open+1 : len(v)-1])
	if err != nil || n < 0 {
		return v[:open], 0
	}
	return v[:open], n
}

func parseUsage(v string) Usage {
	switch strings.ToLower(v) {
	case "directoryoperation":
		return UsageDirectoryOperation
	case "distributedoperation":
		return UsageDistributedOperation
	case "dsaoperation":
		return UsageDSAOperation
	default:
		return UsageUserApplications
	}
}
