package project

import (
	"github.com/hazame-hub/alder/internal/dn"
)

// Unmanaged is what a plan reports about one managed subtree: the entries the
// directory holds there that no project file describes. They are reported and
// never acted on -- a project is not a deleter.
//
// It is one shape for both callers, so the client's --json and the server's
// POST /project/plan cannot drift apart.
type Unmanaged struct {
	Base string `json:"base"`
	// Entries are the DNs, parent before child, as the capture ordered them.
	Entries []string `json:"entries"`
	// Error says why the subtree could not be read whole. Entries is then
	// empty and says nothing either way.
	Error string `json:"error,omitempty"`
}

// ListUnmanaged reads each managed subtree with capture and reports what is
// there that the project does not describe. capture answers every DN at or
// below base, whole or not at all; a base the directory does not hold answers
// none. A subtree that cannot be read is reported with the reason, and the
// rest are still listed: this is a report, and failing to make one part of it
// changes nothing else it says.
//
// Membership is decided by dn.Equal, which compares as the directory does,
// so an entry written with different spelling or escaping in a file is not
// reported as unmanaged.
func ListUnmanaged(p *Project, capture func(base dn.DN) ([]dn.DN, error)) []Unmanaged {
	out := make([]Unmanaged, 0, len(p.Managed))
	for _, st := range p.Managed {
		u := Unmanaged{Base: st.Base.String(), Entries: []string{}}
		found, err := capture(st.Base)
		if err != nil {
			u.Error = err.Error()
			out = append(out, u)
			continue
		}
		described := dnSet{}
		for _, rec := range st.Records {
			described.add(rec.DN)
		}
		for _, d := range found {
			if !described.has(d) {
				u.Entries = append(u.Entries, d.String())
			}
		}
		out = append(out, u)
	}
	return out
}

// dnSet holds DNs and answers membership by dn.Equal, keyed by dn.Key, which
// two DNs share exactly when they are equal.
type dnSet map[string]struct{}

func (s dnSet) add(d dn.DN) { s[d.Key()] = struct{}{} }

func (s dnSet) has(d dn.DN) bool {
	_, ok := s[d.Key()]
	return ok
}
