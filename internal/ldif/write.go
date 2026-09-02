package ldif

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// DefaultLineWidth is the column at which the writer folds. RFC 2849 says
// output lines SHOULD be no wider than 76 characters.
const DefaultLineWidth = 76

// Writer renders records as RFC 2849 LDIF.
//
// The zero value is not usable; call NewWriter.
type Writer struct {
	w   io.Writer
	err error

	// LineWidth is the fold column. Zero means DefaultLineWidth; a negative
	// value disables folding, which is what the UI's preview pane uses, because
	// folding a value the user is about to read makes it harder to check.
	LineWidth int

	// Comments, when true, prefixes each record with a "# <dn>" line. Exports
	// set it; the preview modal does not, since the DN is already on screen.
	Comments bool

	wroteVersion bool
	wroteRecord  bool
}

// NewWriter returns a Writer that writes LDIF to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Err returns the first error the writer encountered. Every method is a no-op
// once one has occurred, so a caller can write a whole document and check once.
func (w *Writer) Err() error { return w.err }

func (w *Writer) write(s string) {
	if w.err != nil {
		return
	}
	_, w.err = io.WriteString(w.w, s)
}

// WriteVersion writes the "version: 1" header. It writes it at most once, and
// only before the first record.
func (w *Writer) WriteVersion() {
	if w.wroteVersion || w.wroteRecord {
		return
	}
	w.wroteVersion = true
	w.write("version: 1\n")
}

// WriteComment writes a comment line, folding it like any other line. The text
// is written as-is apart from the "# " prefix; embedded newlines start a new
// comment line rather than breaking out of the comment.
func (w *Writer) WriteComment(text string) {
	for _, line := range strings.Split(text, "\n") {
		w.writeFolded("# " + line)
	}
}

// WriteRecord writes one record, preceded by a blank line if it is not the
// first. It returns the writer's error so a caller writing one record can check
// immediately.
func (w *Writer) WriteRecord(r *Record) error {
	if w.err != nil {
		return w.err
	}
	if w.wroteRecord || w.wroteVersion {
		w.write("\n")
	}
	w.wroteRecord = true

	if w.Comments {
		w.WriteComment(r.DN.String())
	}
	w.writeAttrLine("dn", []byte(r.DN.String()))

	if ct := r.Change.String(); ct != "" {
		w.write("changetype: " + ct + "\n")
	}

	switch r.Change {
	case ChangeNone, ChangeAdd:
		for _, a := range r.Attrs {
			w.writeAttr(a)
		}
	case ChangeModify:
		for _, m := range r.Mods {
			w.writeMod(m)
		}
	case ChangeDelete:
		// A delete record is the DN and the changetype, nothing else.
	case ChangeModRDN:
		w.writeAttrLine("newrdn", []byte(r.NewRDN))
		if r.DeleteOldRDN {
			w.write("deleteoldrdn: 1\n")
		} else {
			w.write("deleteoldrdn: 0\n")
		}
		if len(r.NewSuperior) > 0 {
			w.writeAttrLine("newsuperior", []byte(r.NewSuperior.String()))
		}
	}
	return w.err
}

func (w *Writer) writeAttr(a Attribute) {
	if len(a.Values) == 0 {
		// An attribute with no values cannot be written as a content record
		// line at all; skipping it is the only honest option, and the callers
		// that build records do not produce one.
		return
	}
	for _, v := range a.Values {
		w.writeAttrLine(a.Name, v)
	}
}

func (w *Writer) writeMod(m Mod) {
	w.write(m.Op.String() + ": " + m.Name + "\n")
	for _, v := range m.Values {
		w.writeAttrLine(m.Name, v)
	}
	// RFC 2849 requires the "-" separator after every mod-spec, including the
	// last one. Servers that accept its omission have made this a common bug.
	w.write("-\n")
}

// writeAttrLine writes one "name: value" or "name:: base64" line, folded.
func (w *Writer) writeAttrLine(name string, value []byte) {
	if NeedsBase64(value) {
		w.writeFolded(name + ":: " + base64.StdEncoding.EncodeToString(value))
		return
	}
	w.writeFolded(name + ": " + string(value))
}

// writeFolded writes a logical line, folding it at LineWidth with a leading
// space on each continuation, per RFC 2849 section 2.6.
//
// Folding is byte-wise, which is safe because every line reaching here is
// ASCII: a value with any byte above 0x7F took the base64 path.
func (w *Writer) writeFolded(line string) {
	width := w.LineWidth
	if width == 0 {
		width = DefaultLineWidth
	}
	if width < 0 || len(line) <= width {
		w.write(line + "\n")
		return
	}
	w.write(line[:width] + "\n")
	rest := line[width:]
	// Each continuation line spends one column on its leading space.
	for len(rest) > width-1 {
		w.write(" " + rest[:width-1] + "\n")
		rest = rest[width-1:]
	}
	w.write(" " + rest + "\n")
}

// NeedsBase64 reports whether a value must be written in the base64 form.
//
// RFC 2849 permits the plain form only for a SAFE-STRING: no NUL, LF or CR
// anywhere, no byte above 0x7F anywhere, and no leading space, colon or
// less-than. A trailing space is added to that list here. The grammar allows
// one, but folding makes a trailing space impossible to see and several readers
// strip it, so writing it as base64 is the difference between a value that
// round-trips and one that quietly loses a byte.
func NeedsBase64(v []byte) bool {
	if len(v) == 0 {
		return false
	}
	switch v[0] {
	case ' ', ':', '<':
		return true
	}
	if v[len(v)-1] == ' ' {
		return true
	}
	for _, c := range v {
		if c == 0 || c == '\n' || c == '\r' || c >= 0x80 {
			return true
		}
	}
	return false
}

// String renders a single record as LDIF, folded at the default width. It is
// the convenience the API layer uses; a document of several records goes
// through a Writer.
func (r *Record) String() string {
	var b strings.Builder
	w := NewWriter(&b)
	// A parse error is impossible here: strings.Builder never fails.
	_ = w.WriteRecord(r)
	return b.String()
}

// Marshal renders records as an LDIF document with a version header.
func Marshal(records []*Record) ([]byte, error) {
	var b strings.Builder
	w := NewWriter(&b)
	w.WriteVersion()
	for _, r := range records {
		if err := w.WriteRecord(r); err != nil {
			return nil, fmt.Errorf("ldif: writing %s: %w", r.DN, err)
		}
	}
	return []byte(b.String()), w.Err()
}
