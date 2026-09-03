package api

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/ansible"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// A changeset is an ordered list of changes reviewed and applied together.
//
// It is the feature the product positioning actually rests on. Applying changes
// one at a time gives one LDIF snippet per click; real directory work is twenty
// related changes that a person wants to read as one document and hand to
// someone else as one playbook.
//
// Alder holds none of it. The list arrives with each request, is rendered or
// applied, and is forgotten. That keeps v1 stateless -- there is no per-session
// basket on the server to expire, leak between tabs, or need cleaning up -- at
// the cost of the browser losing its staged work on a refresh, which the UI
// says plainly rather than pretending otherwise.

// PreviewChangeset renders several changes as one document.
func (s *Server) PreviewChangeset(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body ChangesetRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if len(body.Changes) == 0 {
		return badRequest(c, "A changeset needs at least one change.", "")
	}

	records, err := changeRecords(body.Changes)
	if err != nil {
		return badRequest(c, "The changeset contains a change that is not usable.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, _ := sess.Conn.Schema(ctx)

	out := ChangesetPreview{Changes: make([]ChangePreview, 0, len(records))}
	var tasks []string
	for i, record := range records {
		preview, prevErr := s.renderPreview(record, sch)
		if prevErr != nil {
			return badRequest(c, fmt.Sprintf("Change %d cannot be rendered.", i+1), prevErr.Error())
		}
		out.Changes = append(out.Changes, preview)
		tasks = append(tasks, preview.Ansible)
	}

	out.Ldif = changesetLDIF(records)
	out.AnsiblePlaybook = ptr(ldifJoinPlaybook(tasks))
	if warnings := changesetWarnings(records); len(warnings) > 0 {
		out.Warnings = ptr(warnings)
	}
	return c.JSON(out)
}

// ApplyChangeset applies each change in order, stopping at the first failure.
func (s *Server) ApplyChangeset(c *fiber.Ctx) error {
	sess := s.requireWritable(c)
	if sess == nil {
		return nil
	}
	var body ChangesetRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if len(body.Changes) == 0 {
		return badRequest(c, "A changeset needs at least one change.", "")
	}

	records, err := changeRecords(body.Changes)
	if err != nil {
		return badRequest(c, "The changeset contains a change that is not usable.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	result := ChangesetResult{Outcomes: make([]ChangesetOutcome, 0, len(records))}
	failed := -1

	for i, record := range records {
		// Everything after a failure is reported as not applied rather than
		// omitted. A caller fixing the broken change needs to know what is left,
		// and a shorter list than they submitted invites re-running the lot.
		if failed >= 0 {
			// Named, not just counted. The panel is meant to be checked against
			// the list the user submitted, and a row saying only "not attempted"
			// cannot be. An outcome that is neither applied nor carrying an
			// error is what "not attempted" means on the wire.
			result.Outcomes = append(result.Outcomes, ChangesetOutcome{
				Index:   i,
				Dn:      record.DN.String(),
				Applied: false,
				Summary: ptr(record.Summary()),
			})
			continue
		}

		if applyErr := sess.Conn.Apply(ctx, record); applyErr != nil {
			failed = i
			result.Outcomes = append(result.Outcomes, ChangesetOutcome{
				Index:   i,
				Dn:      record.DN.String(),
				Applied: false,
				Summary: ptr(record.Summary()),
				Error:   ptr(errorBody(applyErr)),
			})
			continue
		}
		result.Outcomes = append(result.Outcomes, ChangesetOutcome{
			Index:   i,
			Dn:      record.DN.String(),
			Applied: true,
			Summary: ptr(record.Summary()),
		})
		result.AppliedCount++
	}

	if failed >= 0 {
		result.FailedIndex = ptr(failed)
		s.logger.Warn("changeset stopped at a failure",
			"applied", result.AppliedCount, "failed_at", failed, "total", len(records))
	} else {
		s.logger.Info("changeset applied", "changes", result.AppliedCount)
	}
	// A partial run is an outcome, not a transport error. The body says where it
	// stopped; a non-200 would push callers towards retrying the whole set.
	return c.JSON(result)
}

// changeRecords converts and validates every change before any of them run.
//
// Validating the whole set first means a changeset with a malformed change at
// position twelve does not apply the first eleven and then stop. It cannot make
// the run atomic -- nothing can, LDAP has no transaction across entries -- but
// it removes the failures that were knowable in advance.
func changeRecords(changes []ChangeRequest) ([]directory.ChangeRecord, error) {
	out := make([]directory.ChangeRecord, 0, len(changes))
	for i, ch := range changes {
		record, err := changeRecord(ch)
		if err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		if err := record.Validate(); err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		out = append(out, record)
	}
	return out, nil
}

// changesetLDIF renders the whole set as one document.
//
// A password change has no LDIF form, so its notice is emitted as comments in
// place of a record. Omitting it would produce a document that silently
// describes fewer steps than the run performs.
func changesetLDIF(records []directory.ChangeRecord) string {
	var b strings.Builder
	b.WriteString("version: 1\n")
	for _, record := range records {
		b.WriteString("\n")
		// LDIFFolded already yields the notice for a password change, so a
		// changeset containing one still describes every step it performs.
		b.WriteString(record.LDIFFolded())
	}
	return b.String()
}

// ldifJoinPlaybook wraps every task in one playbook, in order.
func ldifJoinPlaybook(tasks []string) string {
	var b strings.Builder
	for i, t := range tasks {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimRight(t, "\n"))
		b.WriteString("\n")
	}
	return ansible.Playbook(b.String())
}

// changesetWarnings reports what is wrong with the set as a whole.
//
// These are findings about ordering and overlap, which no single change can see
// on its own. None of them block: the directory is the authority on whether a
// run works, and a warning that turns out to be wrong should cost a reading,
// not a refusal.
func changesetWarnings(records []directory.ChangeRecord) []string {
	var warnings []string

	// An entry created before its parent fails, and the fix is to reorder.
	// Sorting the set automatically would be guessing at intent: a rename that
	// moves an entry under something created later is legitimate, and
	// rearranging it silently would change what the user reviewed.
	created := map[string]int{}
	for i, r := range records {
		if r.Type == directory.ChangeAdd {
			created[foldDN(r.DN)] = i
		}
	}
	for i, r := range records {
		if r.Type != directory.ChangeAdd {
			continue
		}
		parent := r.DN.Parent()
		if parent.IsEmpty() {
			continue
		}
		if at, ok := created[foldDN(parent)]; ok && at > i {
			warnings = append(warnings, fmt.Sprintf(
				"Change %d creates %s, but its parent is created later, at change %d. "+
					"Move the parent earlier or the directory will refuse it.",
				i+1, r.DN, at+1))
		}
	}

	// Deleting an entry that a later change still refers to.
	deleted := map[string]int{}
	for i, r := range records {
		if r.Type == directory.ChangeDelete {
			deleted[foldDN(r.DN)] = i
		}
	}
	for i, r := range records {
		if at, ok := deleted[foldDN(r.DN)]; ok && at < i {
			warnings = append(warnings, fmt.Sprintf(
				"Change %d acts on %s, which change %d deletes first.", i+1, r.DN, at+1))
		}
	}

	// The same entry changed more than once is legal and often deliberate, but
	// it is worth saying, because it is also what a double-click looks like.
	seen := map[string][]int{}
	for i, r := range records {
		key := foldDN(r.DN)
		seen[key] = append(seen[key], i+1)
	}
	for _, r := range records {
		positions := seen[foldDN(r.DN)]
		if len(positions) > 1 {
			warnings = append(warnings, fmt.Sprintf(
				"%s is changed %d times, at %s.", r.DN, len(positions), joinInts(positions)))
			delete(seen, foldDN(r.DN))
		}
	}
	return warnings
}

func foldDN(d dn.DN) string { return strings.ToLower(d.String()) }

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = fmt.Sprintf("%d", n)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// errorBody renders an apply failure into the shape the API uses everywhere
// else, so a caller parses one error type rather than two.
func errorBody(err error) Error {
	body := Error{Error: ErrorErrorUpstream, Message: err.Error()}
	return body
}
