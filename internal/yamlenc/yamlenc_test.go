package yamlenc

import (
	"strings"
	"testing"
)

// The values worth pinning: the five escapes, the control range that becomes
// \xNN, and the cases where getting it wrong produces valid YAML meaning
// something else.
var cases = []struct {
	name string
	in   string
	want string
}{
	{"plain", "alice", `"alice"`},
	{"empty", "", `""`},
	{"quote", `say "hi"`, `"say \"hi\""`},
	{"backslash", `a\b`, `"a\\b"`},
	{"newline", "one\ntwo", `"one\ntwo"`},
	{"carriage return", "one\rtwo", `"one\rtwo"`},
	{"tab", "one\ttwo", `"one\ttwo"`},
	{"nul", "a\x00b", `"a\x00b"`},
	{"delete", "a\x7fb", `"a\x7fb"`},
	{"bell", "\a", "\"\\x07\""},
	// Quoting is what stops these being read as something other than text.
	{"looks numeric", "0755", `"0755"`},
	{"looks boolean", "yes", `"yes"`},
	{"looks null", "null", `"null"`},
	{"looks like a date", "2026-09-12", `"2026-09-12"`},
	// Multi-byte runes are written as themselves, and the byte offsets around
	// an escape have to survive them.
	{"non-ascii", "Ünïcodé", `"Ünïcodé"`},
	{"non-ascii around an escape", "é\né", `"é\né"`},
	{"escape at the end", "trailing\\", `"trailing\\"`},
	{"escape at the start", "\\leading", `"\\leading"`},
	{"only escapes", "\n\t\"", `"\n\t\""`},
}

func TestScalar(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Scalar(tc.in); got != tc.want {
				t.Errorf("Scalar(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// The three entry points exist so that a caller with a string, a caller with a
// buffer, and a caller with bytes are not each tempted to escape it themselves.
// That is only worth anything while they agree.
func TestTheThreeEntryPointsAgree(t *testing.T) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := Scalar(tc.in)

			var s strings.Builder
			if err := WriteScalar(&s, tc.in); err != nil {
				t.Fatalf("WriteScalar: %v", err)
			}
			if s.String() != want {
				t.Errorf("WriteScalar wrote %s, Scalar returned %s", s.String(), want)
			}

			var b strings.Builder
			if err := WriteScalarBytes(&b, []byte(tc.in)); err != nil {
				t.Fatalf("WriteScalarBytes: %v", err)
			}
			if b.String() != want {
				t.Errorf("WriteScalarBytes wrote %s, Scalar returned %s", b.String(), want)
			}
		})
	}
}

// A short write has to come back as an error rather than as a scalar missing
// its closing quote, which is a YAML document that will not parse and does not
// say why.
func TestAFailedWriteIsReported(t *testing.T) {
	for i := 0; i < 4; i++ {
		w := &shortWriter{allow: i}
		if err := WriteScalar(w, "a\nb"); err == nil {
			t.Errorf("WriteScalar returned no error after %d writes", i)
		}
		w = &shortWriter{allow: i}
		if err := WriteScalarBytes(w, []byte("a\nb")); err == nil {
			t.Errorf("WriteScalarBytes returned no error after %d writes", i)
		}
	}
}

// shortWriter fails once it has accepted allow writes.
type shortWriter struct {
	allow int
	n     int
}

func (s *shortWriter) Write(p []byte) (int, error) {
	if s.n >= s.allow {
		return 0, errBroken
	}
	s.n++
	return len(p), nil
}

type brokenError struct{}

func (brokenError) Error() string { return "broken" }

var errBroken = brokenError{}
