package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/project"
)

// unmanagedSubtree is what project plan reports about one managed subtree:
// the entries the directory holds there that no project file describes.
// They are reported and never acted on -- a project is not a deleter.
type unmanagedSubtree struct {
	Base string `json:"base"`
	// Entries are the DNs, parent before child, as the capture ordered them.
	Entries []string `json:"entries"`
	// Error says why the subtree could not be read whole. Entries is then
	// empty and says nothing either way.
	Error string `json:"error,omitempty"`
}

// listUnmanaged reads each managed subtree with the snapshot capture, which
// is all or nothing -- a subtree it cannot read whole is refused, never
// half listed -- and reports what is there that the project does not
// describe. A subtree that cannot be read is said to be unreadable, and the
// plan beside it stands: this is a report, and failing to make it changes
// nothing the plan says.
func listUnmanaged(ctx context.Context, r *remote, p *project.Project) []unmanagedSubtree {
	out := make([]unmanagedSubtree, 0, len(p.Managed))
	for _, st := range p.Managed {
		u := unmanagedSubtree{Base: st.Base.String(), Entries: []string{}}
		found, err := captureDNs(ctx, r, st.Base)
		if err != nil {
			u.Error = err.Error()
			out = append(out, u)
			continue
		}
		described := newDNSet()
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

// captureDNs returns the DNs of every entry at or below base. A base the
// directory does not hold has nothing below it, so it answers none.
func captureDNs(ctx context.Context, r *remote, base dn.DN) ([]dn.DN, error) {
	baseText := base.String()
	scope := apiclient.SnapshotScope("sub")
	what := "listing the entries in " + baseText
	res, err := r.api.CaptureSnapshot(ctx, apiclient.SnapshotCaptureRequest{Base: &baseText, Scope: &scope})
	if err != nil {
		return nil, transportFailure(ctx, what, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		var apiErr apiclient.Error
		if res.StatusCode == http.StatusNotFound && isJSON(res.Header) &&
			json.Unmarshal(body, &apiErr) == nil && apiErr.Error == apiclient.ErrorErrorNotFound {
			return nil, nil
		}
		return nil, r.refusal(ctx, what, res, body)
	}
	// Only the names are kept; the values are read past.
	var snap struct {
		Entries []struct {
			DN string `json:"dn"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&snap); err != nil {
		return nil, failf("incomplete_snapshot", "what Alder sent while %s is not a snapshot: %v", what, err)
	}
	out := make([]dn.DN, 0, len(snap.Entries))
	for _, e := range snap.Entries {
		d, err := dn.Parse(e.DN)
		if err != nil {
			return nil, failf("unexpected_response", "Alder sent %q while %s, which is not a DN", safe(e.DN), what)
		}
		out = append(out, d)
	}
	return out, nil
}

// dnSet holds DNs and answers membership by dn.Equal, which compares as the
// directory does. It buckets by depth and the leaf's attribute types, which
// two equal DNs always share, so a lookup compares against a handful rather
// than every DN in the project.
type dnSet map[string][]dn.DN

func newDNSet() dnSet { return dnSet{} }

func dnBucket(d dn.DN) string {
	types := make([]string, 0, len(d.RDN()))
	for _, ava := range d.RDN() {
		types = append(types, strings.ToLower(ava.Type))
	}
	// A multi-valued RDN may list its types in any order and be the same name.
	sort.Strings(types)
	return fmt.Sprintf("%d|%s", len(d), strings.Join(types, "+"))
}

func (s dnSet) add(d dn.DN) {
	k := dnBucket(d)
	s[k] = append(s[k], d)
}

func (s dnSet) has(d dn.DN) bool {
	for _, x := range s[dnBucket(d)] {
		if x.Equal(d) {
			return true
		}
	}
	return false
}

// renderUnmanaged prints the report under the plan. Subtrees that could not
// be read are said on errs, with the rest of the commentary.
func renderUnmanaged(w, errs io.Writer, subtrees []unmanagedSubtree, showItems bool) {
	unreadable := reportUnreadable(errs, subtrees)
	total := 0
	for _, u := range subtrees {
		total += len(u.Entries)
	}
	writeln(w)
	if total == 0 {
		if unreadable == 0 {
			writeln(w, "Unmanaged: none. Every entry in the managed subtrees is one the project describes.")
		} else {
			writeln(w, "Unmanaged: none in the subtrees that could be read.")
		}
		return
	}
	writef(w, "Unmanaged: %s the directory holds that no project file describes. Reported, never deleted.\n",
		plural(total, "entry", "entries"))
	for _, u := range subtrees {
		if len(u.Entries) == 0 {
			continue
		}
		writef(w, "  %s in %s\n", plural(len(u.Entries), "entry", "entries"), safe(u.Base))
		if showItems {
			for _, d := range u.Entries {
				writef(w, "    %s\n", safe(d))
			}
		}
	}
}

// reportUnreadable says which subtrees could not be read, and how many.
func reportUnreadable(errs io.Writer, subtrees []unmanagedSubtree) int {
	n := 0
	for _, u := range subtrees {
		if u.Error != "" {
			n++
			writef(errs, "alder: the unmanaged entries in %s could not be listed: %s\n", safe(u.Base), safe(u.Error))
		}
	}
	return n
}
