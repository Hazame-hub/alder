// Package yamlenc writes YAML scalars.
//
// It exists so that the two renderers which emit YAML -- the Ansible tasks and
// the tree export -- quote values the same way. Escaping is the part of
// hand-written YAML that goes wrong, and one implementation with one set of
// tests is worth more than two that agree today.
package yamlenc

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Scalar renders a YAML double-quoted scalar.
//
// The double-quoted style is the only YAML scalar style with a complete escape
// mechanism, and its escapes are JSON's. Emitting every scalar in it means a
// renderer never has to reason about whether a particular value would be
// misread as a number, a boolean, a date, or the string "null", which is the
// entire catalogue of ways hand-written YAML goes wrong.
func Scalar(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	_ = WriteScalar(&b, s)
	return b.String()
}

// WriteScalar writes what Scalar returns, without building the string first.
//
// The same escaping: Scalar is this function with a buffer underneath it, so
// there is still one implementation and one set of tests. It exists because the
// tree export calls it about twenty times per entry, and at ten thousand
// entries the intermediate strings were a large share of everything the export
// allocated.
//
// A value with nothing to escape -- nearly all of them -- is written in one
// piece rather than a rune at a time.
func WriteScalar(w io.Writer, s string) error {
	if _, err := io.WriteString(w, `"`); err != nil {
		return err
	}
	start := 0
	for i, r := range s {
		esc := escapeOf(r)
		if esc == "" {
			continue
		}
		if start < i {
			if _, err := io.WriteString(w, s[start:i]); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, esc); err != nil {
			return err
		}
		start = i + utf8.RuneLen(r)
	}
	if start < len(s) {
		if _, err := io.WriteString(w, s[start:]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, `"`)
	return err
}

// WriteScalarBytes is WriteScalar for a value that arrives as bytes.
//
// It exists rather than a string conversion at the call site because that
// conversion copies: an attribute value handed to WriteScalar as string(v)
// escapes to the heap, which at ten thousand entries is one allocation per
// value written. Ranging a []byte as runes does not copy, so this is the same
// loop over the same escapes with nothing in between.
func WriteScalarBytes(w io.Writer, v []byte) error {
	if _, err := io.WriteString(w, `"`); err != nil {
		return err
	}
	start := 0
	// range over string(v) rather than over v: the loop needs runes, and the
	// compiler does elide the conversion in a range expression.
	for i, r := range string(v) {
		esc := escapeOf(r)
		if esc == "" {
			continue
		}
		if start < i {
			if _, err := w.Write(v[start:i]); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, esc); err != nil {
			return err
		}
		start = i + utf8.RuneLen(r)
	}
	if start < len(v) {
		if _, err := w.Write(v[start:]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, `"`)
	return err
}

// escapeOf is the escape a rune needs, or "" if it can be written as itself.
//
// The one place the escaping is decided, so the three entry points above cannot
// drift apart.
func escapeOf(r rune) string {
	switch r {
	case '"':
		return `\"`
	case '\\':
		return `\\`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	}
	if r < 0x20 || r == 0x7f {
		return fmt.Sprintf(`\x%02x`, r)
	}
	return ""
}
