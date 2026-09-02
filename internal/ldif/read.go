package ldif

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/hazame-hub/alder/internal/dn"
)

// maxLineLength bounds one physical input line. LDIF has no line length limit,
// but an unbounded scanner on network input is a memory exhaustion bug waiting
// to be filed, and a megabyte holds a certificate several times over.
const maxLineLength = 1 << 20

// ErrURLReference reports an "attr:< url" value reference, which this reader
// always refuses.
//
// RFC 2849 lets a value be given as a URL for the reader to fetch. Alder never
// fetches one: the reader runs inside a server process holding a privileged
// bind, so following a URL out of a user-supplied LDIF would be arbitrary file
// disclosure via file:// and server-side request forgery via http:// in the
// same feature. The refusal names the line so the UI can point at it.
type ErrURLReference struct {
	Line      int
	Attribute string
	URL       string
}

func (e *ErrURLReference) Error() string {
	return fmt.Sprintf("ldif: line %d: %s:< references a URL, which Alder does not fetch; "+
		"inline the value instead", e.Line, e.Attribute)
}

// SyntaxError is a malformed LDIF input, with the line it was found on.
type SyntaxError struct {
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("ldif: line %d: %s", e.Line, e.Msg) }

func syntaxErrorf(line int, format string, args ...any) error {
	return &SyntaxError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

// Reader reads LDIF records from an input stream.
type Reader struct {
	lines   *lineReader
	version int
	started bool
}

// NewReader returns a Reader that reads LDIF from r.
func NewReader(r io.Reader) *Reader {
	return &Reader{lines: newLineReader(r)}
}

// Version returns the document version from a "version:" header, or 0 when the
// document had none. It is meaningful only after the first Read.
func (r *Reader) Version() int { return r.version }

// Read returns the next record, or io.EOF when the input is exhausted.
func (r *Reader) Read() (*Record, error) {
	for {
		block, err := r.lines.nextBlock()
		if err != nil {
			return nil, err
		}
		if len(block) == 0 {
			continue
		}
		// The version header sits alone at the top of the document, before the
		// first record, separated by a blank line or not.
		if !r.started {
			if name, value, _, lineErr := splitLine(block[0].text); lineErr == nil && equalFoldASCII(name, "version") {
				v, convErr := parseVersion(string(value))
				if convErr != nil {
					return nil, syntaxErrorf(block[0].num, "%v", convErr)
				}
				r.version = v
				block = block[1:]
				if len(block) == 0 {
					continue
				}
			}
			r.started = true
		}
		return parseRecord(block)
	}
}

// ReadAll reads every record in the input.
func (r *Reader) ReadAll() ([]*Record, error) {
	var out []*Record
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
}

// Unmarshal parses a whole LDIF document.
func Unmarshal(b []byte) ([]*Record, error) {
	return NewReader(strings.NewReader(string(b))).ReadAll()
}

func parseVersion(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s != "1" {
		return 0, fmt.Errorf("unsupported LDIF version %q, only 1 is defined", s)
	}
	return 1, nil
}

// logicalLine is one unfolded line and the input line it started on.
type logicalLine struct {
	text string
	num  int
}

// lineReader unfolds continuation lines, drops comments, and groups the result
// into blocks separated by blank lines.
//
// The order matters: unfolding happens before comments are dropped, because a
// continuation line following a comment belongs to that comment, and dropping
// the comment first would leave its continuation looking like an attribute.
type lineReader struct {
	sc      *bufio.Scanner
	pending *logicalLine
	lineNum int
	done    bool
	err     error
}

func newLineReader(r io.Reader) *lineReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineLength)
	return &lineReader{sc: sc}
}

// nextBlock returns the logical lines of the next record, or io.EOF.
func (lr *lineReader) nextBlock() ([]logicalLine, error) {
	var block []logicalLine
	for {
		line, sep, err := lr.nextLogical()
		if errors.Is(err, io.EOF) {
			if len(block) > 0 {
				return block, nil
			}
			return nil, io.EOF
		}
		if err != nil {
			return nil, err
		}
		if sep {
			if len(block) > 0 {
				return block, nil
			}
			continue
		}
		block = append(block, line)
	}
}

// nextLogical returns the next unfolded, non-comment line. The bool reports a
// record separator: a blank line.
func (lr *lineReader) nextLogical() (logicalLine, bool, error) {
	for {
		line, sep, err := lr.unfoldOne()
		if err != nil {
			return logicalLine{}, false, err
		}
		if sep {
			return logicalLine{}, true, nil
		}
		if strings.HasPrefix(line.text, "#") {
			continue // a comment, with its continuations already absorbed
		}
		return line, false, nil
	}
}

// unfoldOne reads one logical line, absorbing continuations.
func (lr *lineReader) unfoldOne() (logicalLine, bool, error) {
	start := lr.pending
	lr.pending = nil
	if start == nil {
		raw, ok := lr.scan()
		if !ok {
			return logicalLine{}, false, lr.finish()
		}
		if after, isCont := strings.CutPrefix(raw, " "); isCont {
			// A continuation with nothing to continue. Treating it as a line of
			// its own gives a clearer error downstream than silently dropping.
			return logicalLine{text: after, num: lr.lineNum}, false, nil
		}
		start = &logicalLine{text: raw, num: lr.lineNum}
	}
	// A blank line is a record separator, whether it was just scanned or held
	// over from the previous call. Reading continuations onto it instead is how
	// every record after the first ends up merged into its predecessor.
	if start.text == "" {
		return logicalLine{}, true, nil
	}

	var b strings.Builder
	b.WriteString(start.text)
	for {
		raw, ok := lr.scan()
		if !ok {
			return logicalLine{text: b.String(), num: start.num}, false, nil
		}
		if strings.HasPrefix(raw, " ") {
			b.WriteString(raw[1:])
			continue
		}
		// Not a continuation: hold it for the next call. A blank line is held
		// the same way so the separator is not lost.
		if raw == "" {
			lr.pending = &logicalLine{text: "", num: lr.lineNum}
		} else {
			lr.pending = &logicalLine{text: raw, num: lr.lineNum}
		}
		return logicalLine{text: b.String(), num: start.num}, false, nil
	}
}

// scan returns the next physical line with any trailing CR removed.
func (lr *lineReader) scan() (string, bool) {
	if lr.done {
		return "", false
	}
	if !lr.sc.Scan() {
		lr.done = true
		lr.err = lr.sc.Err()
		return "", false
	}
	lr.lineNum++
	return strings.TrimSuffix(lr.sc.Text(), "\r"), true
}

// finish reports the scanner's error, or io.EOF. A held pending line is
// returned to the caller before this is ever reached.
func (lr *lineReader) finish() error {
	if lr.pending != nil {
		return nil
	}
	if lr.err != nil {
		if errors.Is(lr.err, bufio.ErrTooLong) {
			return fmt.Errorf("ldif: a line exceeds the %d byte limit", maxLineLength)
		}
		return fmt.Errorf("ldif: reading input: %w", lr.err)
	}
	return io.EOF
}

// errNotAttrLine reports a line with no colon at all, which is never an
// attribute line. Callers turn it into a message naming what they expected.
var errNotAttrLine = errors.New("line is not \"attribute: value\"")

// splitLine splits "name: value", "name:: base64" or "name:< url" into its
// parts. isURL reports the third form; every caller refuses it.
func splitLine(line string) (name string, value []byte, isURL bool, err error) {
	name, rest, found := strings.Cut(line, ":")
	if !found {
		return "", nil, false, errNotAttrLine
	}
	switch {
	case strings.HasPrefix(rest, ":"):
		// FILL is *SPACE: every space after the colons is separator, which is
		// exactly why a value with a leading space must be base64 encoded.
		enc := strings.TrimLeft(rest[1:], " ")
		decoded, decErr := base64.StdEncoding.DecodeString(enc)
		if decErr != nil {
			return name, nil, false, fmt.Errorf("%s:: is not valid base64: %w", name, decErr)
		}
		return name, decoded, false, nil
	case strings.HasPrefix(rest, "<"):
		return name, []byte(strings.TrimLeft(rest[1:], " ")), true, nil
	default:
		return name, []byte(strings.TrimLeft(rest, " ")), false, nil
	}
}

// parseRecord parses one block of logical lines into a record.
func parseRecord(block []logicalLine) (*Record, error) {
	first := block[0]
	name, value, isURL, err := splitLine(first.text)
	if errors.Is(err, errNotAttrLine) {
		return nil, syntaxErrorf(first.num, "expected \"dn:\", got %q", truncate(first.text))
	}
	if err != nil {
		return nil, syntaxErrorf(first.num, "%v", err)
	}
	if !equalFoldASCII(name, "dn") {
		return nil, syntaxErrorf(first.num, "a record must start with \"dn:\", got %q", name)
	}
	if isURL {
		return nil, &ErrURLReference{Line: first.num, Attribute: "dn", URL: string(value)}
	}
	parsed, dnErr := dn.ParseAllowEmpty(string(value))
	if dnErr != nil {
		return nil, syntaxErrorf(first.num, "%v", dnErr)
	}

	rec := &Record{DN: parsed, Line: first.num}
	rest := block[1:]

	// Controls apply to change records and change what the server does with
	// them. Ignoring one silently would mean applying a different change than
	// the LDIF describes, so they are refused rather than dropped.
	for _, l := range rest {
		if n, _, _, lineErr := splitLine(l.text); lineErr == nil && equalFoldASCII(n, "control") {
			return nil, syntaxErrorf(l.num, "LDAP controls in LDIF are not supported")
		}
	}

	if len(rest) == 0 {
		return rec, nil
	}
	if n, v, u, lineErr := splitLine(rest[0].text); lineErr == nil && equalFoldASCII(n, "changetype") {
		if u {
			return nil, syntaxErrorf(rest[0].num, "changetype cannot be a URL reference")
		}
		ct, ctErr := parseChangeType(string(v))
		if ctErr != nil {
			return nil, syntaxErrorf(rest[0].num, "%v", ctErr)
		}
		rec.Change = ct
		rest = rest[1:]
	}

	switch rec.Change {
	case ChangeNone, ChangeAdd:
		attrs, attrErr := parseAttrLines(rest)
		if attrErr != nil {
			return nil, attrErr
		}
		rec.Attrs = attrs
	case ChangeDelete:
		if len(rest) > 0 {
			return nil, syntaxErrorf(rest[0].num, "a delete record takes no attributes, got %q", truncate(rest[0].text))
		}
	case ChangeModRDN:
		if err := parseModRDN(rec, rest); err != nil {
			return nil, err
		}
	case ChangeModify:
		mods, modErr := parseMods(rest)
		if modErr != nil {
			return nil, modErr
		}
		rec.Mods = mods
	}
	return rec, nil
}

func parseChangeType(s string) (ChangeType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "add":
		return ChangeAdd, nil
	case "delete":
		return ChangeDelete, nil
	case "modify":
		return ChangeModify, nil
	// RFC 2849 defines "modrdn"; "moddn" is the RFC 4511 operation name and
	// appears in LDIF that other tools produce.
	case "modrdn", "moddn":
		return ChangeModRDN, nil
	default:
		return ChangeNone, fmt.Errorf("unknown changetype %q", s)
	}
}

// parseAttrLines parses attrval-specs, merging repeated names into one
// attribute while preserving the order in which names first appeared.
func parseAttrLines(lines []logicalLine) ([]Attribute, error) {
	var attrs []Attribute
	index := map[string]int{}
	for _, l := range lines {
		name, value, isURL, lineErr := splitLine(l.text)
		if errors.Is(lineErr, errNotAttrLine) {
			return nil, syntaxErrorf(l.num, "expected \"attribute: value\", got %q", truncate(l.text))
		}
		if lineErr != nil {
			return nil, syntaxErrorf(l.num, "%v", lineErr)
		}
		if isURL {
			return nil, &ErrURLReference{Line: l.num, Attribute: name, URL: string(value)}
		}
		if err := validateAttrName(l.num, name); err != nil {
			return nil, err
		}
		key := strings.ToLower(name)
		if i, seen := index[key]; seen {
			attrs[i].Values = append(attrs[i].Values, value)
			continue
		}
		index[key] = len(attrs)
		attrs = append(attrs, Attribute{Name: name, Values: [][]byte{value}})
	}
	return attrs, nil
}

func parseModRDN(rec *Record, lines []logicalLine) error {
	var sawNewRDN, sawDeleteOld bool
	for _, l := range lines {
		name, value, isURL, lineErr := splitLine(l.text)
		if errors.Is(lineErr, errNotAttrLine) {
			return syntaxErrorf(l.num, "expected \"attribute: value\", got %q", truncate(l.text))
		}
		if lineErr != nil {
			return syntaxErrorf(l.num, "%v", lineErr)
		}
		if isURL {
			return &ErrURLReference{Line: l.num, Attribute: name, URL: string(value)}
		}
		switch {
		case equalFoldASCII(name, "newrdn"):
			rec.NewRDN = string(value)
			sawNewRDN = true
		case equalFoldASCII(name, "deleteoldrdn"):
			switch strings.TrimSpace(string(value)) {
			case "1":
				rec.DeleteOldRDN = true
			case "0":
				rec.DeleteOldRDN = false
			default:
				return syntaxErrorf(l.num, "deleteoldrdn must be 0 or 1, got %q", value)
			}
			sawDeleteOld = true
		case equalFoldASCII(name, "newsuperior"):
			sup, err := dn.Parse(string(value))
			if err != nil {
				return syntaxErrorf(l.num, "newsuperior: %v", err)
			}
			rec.NewSuperior = sup
		default:
			return syntaxErrorf(l.num, "unexpected %q in a modrdn record", name)
		}
	}
	if !sawNewRDN {
		return syntaxErrorf(rec.Line, "a modrdn record requires newrdn")
	}
	if !sawDeleteOld {
		return syntaxErrorf(rec.Line, "a modrdn record requires deleteoldrdn")
	}
	// The new RDN has to parse as one, or the rename cannot be previewed.
	if _, err := dn.Parse(rec.NewRDN); err != nil {
		return syntaxErrorf(rec.Line, "newrdn: %v", err)
	}
	return nil
}

// parseMods parses mod-specs. Each begins with an operation line and ends with
// a "-" separator; RFC 2849 requires the separator even after the last one, and
// this reader requires it too rather than guessing where a record ends.
func parseMods(lines []logicalLine) ([]Mod, error) {
	var mods []Mod
	i := 0
	for i < len(lines) {
		l := lines[i]
		opName, value, isURL, lineErr := splitLine(l.text)
		if errors.Is(lineErr, errNotAttrLine) {
			return nil, syntaxErrorf(l.num, "expected a modification, got %q", truncate(l.text))
		}
		if lineErr != nil {
			return nil, syntaxErrorf(l.num, "%v", lineErr)
		}
		if isURL {
			return nil, &ErrURLReference{Line: l.num, Attribute: opName, URL: string(value)}
		}
		op, err := parseModOp(opName)
		if err != nil {
			return nil, syntaxErrorf(l.num, "%v", err)
		}
		attrName := strings.TrimSpace(string(value))
		if err := validateAttrName(l.num, attrName); err != nil {
			return nil, err
		}

		mod := Mod{Op: op, Name: attrName}
		i++
		closed := false
		for i < len(lines) {
			vl := lines[i]
			if vl.text == "-" {
				closed = true
				i++
				break
			}
			vName, vValue, vIsURL, vErr := splitLine(vl.text)
			if errors.Is(vErr, errNotAttrLine) {
				return nil, syntaxErrorf(vl.num, "expected \"attribute: value\" or \"-\", got %q", truncate(vl.text))
			}
			if vErr != nil {
				return nil, syntaxErrorf(vl.num, "%v", vErr)
			}
			if vIsURL {
				return nil, &ErrURLReference{Line: vl.num, Attribute: vName, URL: string(vValue)}
			}
			if !equalFoldASCII(vName, attrName) {
				return nil, syntaxErrorf(vl.num,
					"%s: %s modifies %q but the value line names %q", op, op, attrName, vName)
			}
			mod.Values = append(mod.Values, vValue)
			i++
		}
		if !closed {
			return nil, syntaxErrorf(l.num, "%s: %s is not terminated by a \"-\" line", op, attrName)
		}
		if op == ModAdd && len(mod.Values) == 0 {
			return nil, syntaxErrorf(l.num, "add: %s has no values; an add must supply at least one", attrName)
		}
		mods = append(mods, mod)
	}
	return mods, nil
}

func parseModOp(s string) (ModOp, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "add":
		return ModAdd, nil
	case "delete":
		return ModDelete, nil
	case "replace":
		return ModReplace, nil
	case "increment":
		return ModIncrement, nil
	default:
		return 0, fmt.Errorf("unknown modification %q, want add, delete, replace or increment", s)
	}
}

// validateAttrName rejects an attribute description that is not one. It reuses
// the dn package's check so an attribute name means the same thing everywhere
// in the application.
func validateAttrName(line int, name string) error {
	if err := dn.ValidateType(name); err != nil {
		return syntaxErrorf(line, "%v", err)
	}
	return nil
}

func truncate(s string) string {
	const n = 60
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
