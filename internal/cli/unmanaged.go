package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/project"
)

// unmanagedSubtree is what project plan reports about one managed subtree.
// The shape and the comparison are internal/project's, shared with the
// server's POST /project/plan so the two reports cannot drift apart.
type unmanagedSubtree = project.Unmanaged

// listUnmanaged reads each managed subtree with the snapshot capture, which
// is all or nothing -- a subtree it cannot read whole is refused, never
// half listed -- and reports what is there that the project does not
// describe. A subtree that cannot be read is said to be unreadable, and the
// plan beside it stands: this is a report, and failing to make it changes
// nothing the plan says.
func listUnmanaged(ctx context.Context, r *remote, p *project.Project) []unmanagedSubtree {
	return project.ListUnmanaged(p, func(base dn.DN) ([]dn.DN, error) {
		return captureDNs(ctx, r, base)
	})
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
