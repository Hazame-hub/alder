package api

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/ansible"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/outline"
	"github.com/hazame-hub/alder/internal/session"
)

// Exporting live entries as a playbook that enforces them.
//
// A separate path from /export/ldif rather than a format parameter on it. The
// two are not one operation with two serialisations: an LDIF export transcribes
// entries, this asserts what they should be — which is why it is ordered parent
// first, why it omits everything the directory owns, and why it has no
// "include sensitive" option at all. A URL saying "ldif" while returning YAML
// would have been the smaller change and the less honest one.

// exportQuery is the part both exports do identically: work out the scope,
// parse the filter, search, and tell the caller apart from an empty result.
type exportQuery struct {
	Base       dn.DN
	Scope      string
	RawFilter  string
	Attributes []string
	Limit      int
}

type exportResult struct {
	Base   dn.DN
	Scope  directory.Scope
	Filter filter.Filter
	Result *directory.SearchResult
}

// searchForExport runs the search behind an export, writing the response itself
// and reporting false when it has.
//
// Shared so the two exports cannot come to disagree about what "nothing
// matched" means, which is the part with the least obvious answer: without a
// filter it is a missing entry, with one it is a present entry holding nothing
// that matches, and those are different things to be told.
func (s *Server) searchForExport(c *fiber.Ctx, sess *session.Session, q exportQuery) (exportResult, bool) {
	scope, err := directory.ParseScope(q.Scope)
	if err != nil {
		_ = badRequest(c, "Unknown export scope.", err.Error())
		return exportResult{}, false
	}

	// Parsed into a tree, never pasted into one — the same rule the search
	// endpoint follows, and for the same reason: this value comes from a URL.
	exportFilter := filter.Present("objectClass")
	if raw := strings.TrimSpace(q.RawFilter); raw != "" {
		parsed, parseErr := filter.Parse(raw)
		if parseErr != nil {
			_ = badRequest(c, "The export filter is not a valid RFC 4515 filter.", parseErr.Error())
			return exportResult{}, false
		}
		exportFilter = parsed
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	res, err := sess.Conn.Search(ctx, directory.SearchRequest{
		BaseDN:     q.Base,
		Scope:      scope,
		Filter:     exportFilter,
		Attributes: q.Attributes,
		Limit:      q.Limit,
	})
	if err != nil {
		_ = s.fail(c, err)
		return exportResult{}, false
	}
	if len(res.Entries) == 0 {
		// Without a filter this means the base is not there. With one it means
		// the base holds nothing matching, which is a different thing to be
		// told — and not a 404, because the entry the caller named does exist.
		if strings.TrimSpace(q.RawFilter) == "" {
			_ = writeError(c, fiber.StatusNotFound, ErrorErrorNotFound, "No such entry.", "")
			return exportResult{}, false
		}
		_ = badRequest(c, "Nothing matched, so there is nothing to export.",
			"The filter is valid and the base exists; no entry under it satisfies the filter.")
		return exportResult{}, false
	}

	return exportResult{Base: q.Base, Scope: scope, Filter: exportFilter, Result: res}, true
}

// ExportAnsible exports an entry or a subtree as a playbook.
func (s *Server) ExportAnsible(c *fiber.Ctx, params ExportAnsibleParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	base, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	scopeName := "base"
	if params.Scope != nil {
		scopeName = string(*params.Scope)
	}

	found, ok := s.searchForExport(c, sess, exportQuery{
		Base:  base,
		Scope: scopeName,
		// "*" only. Operational attributes are never asked for here: they
		// cannot be written back, so a task enforcing one fails every run.
		Attributes: []string{"*"},
		RawFilter:  deref(params.Filter),
		Limit:      clamp(deref(params.Limit), 1000, 1, directory.MaxResults),
	})
	if !ok {
		return nil
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	// The schema is what tells an attribute the directory owns from one an
	// operator set. Without it the playbook would try to enforce entryUUID.
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	rendered, _ := found.Filter.Render()
	tasks := ansible.EnforceTasks(found.Result.Entries, sch, ansible.EnforceOptions{
		Base:      found.Base.String(),
		Scope:     found.Scope.String(),
		Filter:    rendered,
		Truncated: found.Result.Truncated,
	})

	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf("attachment; filename=%q", playbookFilename(found.Base, found.Scope)))
	return c.SendString(ansible.Playbook(tasks))
}

// playbookFilename is exportFilename's name with a .yml extension, so a
// downloaded playbook opens as one.
func playbookFilename(base dn.DN, scope directory.Scope) string {
	name := exportFilename(base, scope)
	return strings.TrimSuffix(name, ".ldif") + ".yml"
}

// ExportOutline draws a subtree as the tree it is.
//
// The LDIF export streams because a record is complete on its own. This cannot:
// a tree is not drawable until the last entry has arrived, since the entry that
// decides whether a node is a leaf may be the final one to come back. So it is
// bounded like the Ansible export, and for a reason of the same kind.
func (s *Server) ExportOutline(c *fiber.Ctx, params ExportOutlineParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	base, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	scopeName := "sub"
	if params.Scope != nil {
		scopeName = string(*params.Scope)
	}

	found, ok := s.searchForExport(c, sess, exportQuery{
		Base:      base,
		Scope:     scopeName,
		RawFilter: deref(params.Filter),
		// objectClass so each node can say what kind of thing it is, which is
		// most of what a shape view is asked. Nothing else is read: the outline
		// is about where entries sit, not what they hold.
		Attributes: []string{"objectClass"},
		Limit:      clamp(deref(params.Limit), 1000, 1, directory.MaxResults),
	})
	if !ok {
		return nil
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, _ := sess.Conn.Schema(ctx)

	entries := make([]outline.Entry, 0, len(found.Result.Entries))
	for _, e := range found.Result.Entries {
		node := outline.Entry{DN: e.DN}
		if sch != nil {
			node.Structural = structuralName(sch, e.ObjectClasses())
		}
		entries = append(entries, node)
	}

	rendered := ""
	if f, err := found.Filter.Render(); err == nil {
		rendered = f
	}
	doc := outline.Render(entries, outline.Options{
		Base:      base.String(),
		Scope:     found.Scope.String(),
		Filter:    rendered,
		Truncated: found.Result.Truncated,
		Limit:     clamp(deref(params.Limit), 1000, 1, directory.MaxResults),
	})

	c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition,
		fmt.Sprintf("attachment; filename=%q", outlineFilename(base)))
	return c.SendString(doc)
}

// outlineFilename names the download after the entry it is a picture of.
func outlineFilename(base dn.DN) string {
	name := exportFilename(base, directory.ScopeSubtree)
	return strings.TrimSuffix(name, ".ldif") + "-outline.txt"
}
