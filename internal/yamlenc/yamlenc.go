// Package yamlenc writes YAML scalars.
//
// It exists so that the two renderers which emit YAML -- the Ansible tasks and
// the tree export -- quote values the same way. Escaping is the part of
// hand-written YAML that goes wrong, and one implementation with one set of
// tests is worth more than two that agree today.
package yamlenc

import (
	"fmt"
	"strings"
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
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
