package cli

import (
	"strings"
)

// safe makes text that came from a directory fit to print on a terminal.
//
// A value, a DN or an error message is directory data, and directory data can
// hold control characters: escape sequences that move the cursor, clear the
// screen or rewrite a window title, and bidirectional overrides that make one
// DN display as another. Printed as they are, they are acted on. So they are
// shown as escapes instead. Everything printable, in any script, is left alone.
func safe(s string) string {
	clean := true
	for _, r := range s {
		if unsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80 && unsafeRune(r):
			writef(&b, `\x%02x`, r)
		case unsafeRune(r):
			writef(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		// Bidirectional embeddings, overrides and isolates.
		return true
	case r == 0x200e, r == 0x200f, r == 0x061c:
		// The implicit directional marks, which reorder what surrounds them.
		// The same set the web interface escapes (web/src/lib/display.ts).
		return true
	}
	return false
}
