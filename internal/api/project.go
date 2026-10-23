package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/project"
	"github.com/hazame-hub/alder/internal/session"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// A project, sent as files and planned against this session's directory
// (docs/proposals/web-projects.md, accepted 2026-10-13).
//
// Nothing here is new about projects: internal/project reads the files, by
// the rules the command-line client reads them with, and the plan is
// POST /plan's, of the same document. What this adds is where the files come
// from -- the request, held in memory for its length and nowhere else -- and
// what is ignored: every environment in alder.yaml. The plan is the
// session's, as its bind, against its directory. An uploaded file never
// decides where a connection goes or which secret the server reads.

// PlanProject validates a project sent as files, plans it as desired state
// against this session's directory, and reports its unmanaged entries.
func (s *Server) PlanProject(c *fiber.Ctx) error {
	// require, not requireWritable: nothing is written, and a read-only Alder
	// is where "what would this project change" is most worth asking.
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body ProjectPlanRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if len(body.Files) == 0 {
		return badRequest(c, "A project needs at least its alder.yaml.", "")
	}
	if len(body.Files) > project.MaxFiles {
		return badRequest(c, fmt.Sprintf("A project sent as files holds at most %d files, and this one has %d.",
			project.MaxFiles, len(body.Files)), "Plan a project this large with alder project plan.")
	}
	files := make(map[string][]byte, len(body.Files))
	for _, f := range body.Files {
		if err := project.CheckPath(f.Path); err != nil {
			return badRequest(c, "A file's path is not a clean relative path.", err.Error())
		}
		if _, dup := files[f.Path]; dup {
			return badRequest(c, "The same path was sent twice.", f.Path)
		}
		files[f.Path] = []byte(f.Content)
	}
	if _, ok := files[project.FileName]; !ok {
		return badRequest(c, "There is no alder.yaml at the top of the project.",
			"Choose the folder that holds alder.yaml itself, not one above it.")
	}
	fsys, err := project.NewFS(files)
	if err != nil {
		return badRequest(c, "The files cannot be read as one project.", err.Error())
	}

	p, err := project.LoadFS(fsys)
	var invalid *project.Invalid
	var tooLarge *project.TooLarge
	switch {
	case errors.As(err, &invalid):
		return projectInvalid(c, invalid)
	case errors.As(err, &tooLarge):
		return tooManyRecords(c, tooLarge.Records)
	case err != nil:
		return badRequest(c, "A project file could not be read.", err.Error())
	}

	document, err := p.Document()
	if err != nil {
		return badRequest(c, "The project could not be rendered as LDIF.", err.Error())
	}
	if len(document) > maxImportBytes {
		return badRequest(c, fmt.Sprintf("The project renders to %d bytes of LDIF, more than one plan carries (%d MB).",
			len(document), maxImportBytes>>20), "Plan a project this large with alder project plan.")
	}
	// The document the command-line client sends to POST /plan, read the way
	// POST /plan reads it, so the two plans cannot differ in anything but the
	// tokens a session issues.
	desired := PlanLdifModeDesired
	proposals, ok := parseLdifForPlan(c, document, &desired)
	if !ok {
		return nil
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	planned, err := s.planProposals(ctx, sess, proposals)
	if err != nil {
		return s.fail(c, err)
	}
	unmanaged := project.ListUnmanaged(p, func(base dn.DN) ([]dn.DN, error) {
		return s.subtreeDNs(ctx, sess, base)
	})

	out := ProjectPlan{
		Project: projectSummary(p),
		// The session's own, never anything from the files: this is the line
		// that tells a person which directory the plan is about.
		Host:      net.JoinHostPort(sess.Host(), strconv.Itoa(sess.Port())),
		BindDn:    sess.BindDN(),
		Plan:      planned,
		Unmanaged: make([]ProjectUnmanaged, 0, len(unmanaged)),
	}
	for _, u := range unmanaged {
		view := ProjectUnmanaged{Base: u.Base, Entries: u.Entries}
		if u.Error != "" {
			view.Error = ptr(u.Error)
		}
		out.Unmanaged = append(out.Unmanaged, view)
	}
	if len(p.Searches) > 0 {
		searches := savedSearches(p.Searches)
		out.Searches = &searches
	}
	return c.JSON(out)
}

// savedSearches are a project's searches as the files write them. The plan
// beside them is under the files' own names, and so are these.
func savedSearches(in []project.Search) []SavedSearch {
	out := make([]SavedSearch, 0, len(in))
	for _, s := range in {
		view := SavedSearch{
			Name:       s.Name,
			File:       s.File,
			Base:       s.Base.String(),
			Scope:      SavedSearchScope(s.Scope),
			Filter:     s.Filter,
			Attributes: append([]string{}, s.Attributes...),
			Columns:    append([]string{}, s.Columns...),
			Limit:      s.Limit,
		}
		if s.Title != "" {
			view.Title = ptr(s.Title)
		}
		if s.Description != "" {
			view.Description = ptr(s.Description)
		}
		out = append(out, view)
	}
	return out
}

// projectInvalid answers with every problem, in the words alder project
// validate prints them.
func projectInvalid(c *fiber.Ctx, invalid *project.Invalid) error {
	problems := make([]ProjectProblem, 0, len(invalid.Problems))
	for _, pr := range invalid.Problems {
		view := ProjectProblem{Message: pr.Message}
		if pr.File != "" {
			view.File = ptr(pr.File)
		}
		if pr.Line > 0 {
			view.Line = ptr(pr.Line)
		}
		problems = append(problems, view)
	}
	return c.Status(fiber.StatusBadRequest).JSON(Error{
		Error:    ErrorErrorProjectInvalid,
		Message:  invalid.Error(),
		Problems: &problems,
	})
}

func projectSummary(p *project.Project) ProjectSummary {
	out := ProjectSummary{
		File:         project.FileName,
		Entries:      p.Entries(),
		Subtrees:     make([]ProjectSubtree, 0, len(p.Managed)),
		Environments: p.EnvironmentNames(),
	}
	for _, st := range p.Managed {
		out.Subtrees = append(out.Subtrees, ProjectSubtree{
			Base: st.Base.String(), Files: append([]string{}, st.Files...), Entries: len(st.Records),
		})
	}
	return out
}

// subtreeDNs lists every entry at or below base the way the client's
// unmanaged report does through POST /snapshots/capture: a data capture of
// the whole subtree, whole or refused, and a base the directory does not hold
// answers none. Only the names are kept.
func (s *Server) subtreeDNs(ctx context.Context, sess *session.Session, base dn.DN) ([]dn.DN, error) {
	if kind := targetKind(sess.Conn.Capabilities(), base); kind != PlanTargetData {
		return nil, fmt.Errorf("the %s is not directory data, and only data is listed", kind)
	}
	const everything = "(objectClass=*)"
	parsed, err := filter.Parse(everything)
	if err != nil {
		return nil, err
	}
	snap, truncated, err := s.captureLive(ctx, sess.Conn, liveRequest{base: base, scope: "sub", rawFilter: everything, parsed: parsed})
	if err != nil {
		if noSuchObject(err) {
			return nil, nil
		}
		return nil, readFailure(err)
	}
	if truncated {
		return nil, fmt.Errorf("the subtree could not be read whole: a snapshot holds at most %d entries, and the "+
			"read stopped short (too many entries, a server size limit, or referrals)", snapshot.MaxEntries)
	}
	if len(snap.Entries) == 0 {
		// Empty, or not there at all; only the second has nothing below it
		// for certain, and both answer none.
		if _, err := sess.Conn.Read(ctx, base, []string{"1.1"}); err != nil && !noSuchObject(err) {
			return nil, readFailure(err)
		}
		return nil, nil
	}
	out := make([]dn.DN, 0, len(snap.Entries))
	for _, e := range snap.Entries {
		d, err := dn.Parse(e.DN)
		if err != nil {
			return nil, fmt.Errorf("the directory returned %q, which is not a DN", e.DN)
		}
		out = append(out, d)
	}
	return out, nil
}

func noSuchObject(err error) bool {
	var ldapErr *ldapdriver.Error
	return errors.As(err, &ldapErr) && ldapErr.IsNoSuchObject()
}

// readFailure says why a subtree could not be listed, as a sentence for the
// report beside the plan.
func readFailure(err error) error {
	var ldapErr *ldapdriver.Error
	var snapErr *snapshot.Error
	switch {
	case errors.As(err, &ldapErr) && ldapErr.IsInsufficientAccess():
		return fmt.Errorf("the directory refused to list it for the account this session is bound as: %w", err)
	case errors.As(err, &snapErr):
		return fmt.Errorf("the subtree could not be captured: %s", snapErr.Detail)
	}
	return fmt.Errorf("the directory could not list it: %w", err)
}
