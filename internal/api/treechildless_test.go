package api

import (
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
)

// knownChildless only ever saves a probe. Every case that is not a definite
// "no children at all" must fall through to the probe, which asks with the
// bind's own access.
func TestKnownChildlessOnlyTrustsAnAnswerOfNone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs map[string]string
		want  bool
	}{
		{"hasSubordinates FALSE", map[string]string{"hasSubordinates": "FALSE"}, true},
		{"spelled in any case", map[string]string{"hassubordinates": "false"}, true},
		{"numSubordinates 0", map[string]string{"numSubordinates": "0"}, true},
		// The counts ignore access, so these may name children this bind
		// cannot see. The probe decides.
		{"hasSubordinates TRUE", map[string]string{"hasSubordinates": "TRUE"}, false},
		{"numSubordinates 3", map[string]string{"numSubordinates": "3"}, false},
		// Neither published, or not readable by this bind: nothing is known.
		{"neither", map[string]string{}, false},
		{"unparseable", map[string]string{"numSubordinates": "lots"}, false},
		// hasSubordinates is the direct answer and wins over a count.
		{"both, disagreeing", map[string]string{"hasSubordinates": "TRUE", "numSubordinates": "0"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := directory.NewEntry(mustParse(t, "uid=alice,ou=people,dc=alder,dc=test"))
			for k, v := range tc.attrs {
				e.Set(k, [][]byte{[]byte(v)})
			}
			if got := knownChildless(e); got != tc.want {
				t.Errorf("knownChildless(%v) = %v, want %v", tc.attrs, got, tc.want)
			}
		})
	}
}
