package api

import (
	"errors"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/entrytemplate"
)

// Entry templates (1.43): "New user" and "New group" without knowing which
// object classes a person needs.
//
// A template composes an add change and nothing more, exactly as the creation
// wizard does; the change goes through the same plan and review. What it adds
// is a number read from the directory -- the next free uidNumber or gidNumber
// -- found when the template is rendered and shown to the reviewer as a value
// like any other.

func (s *Server) templates() *entrytemplate.Set {
	if s.cfg.Templates != nil {
		return s.cfg.Templates
	}
	set, _ := entrytemplate.Load("")
	return set
}

// ListTemplates lists the templates, each checked against this schema.
func (s *Server) ListTemplates(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	out := TemplateList{Templates: []EntryTemplate{}}
	for _, t := range s.templates().All() {
		problems := t.Problems(sch)
		// A built-in that does not fit is a variant for the other server.
		if t.Hidden && len(problems) > 0 {
			continue
		}
		out.Templates = append(out.Templates, templateView(t, problems))
	}
	return c.JSON(out)
}

func templateView(t *entrytemplate.Template, problems []string) EntryTemplate {
	v := EntryTemplate{
		Name:          t.Name,
		Title:         t.Title,
		View:          t.View,
		Rdn:           t.RDN,
		ObjectClasses: append([]string{}, t.ObjectClasses...),
		Fields:        []TemplateField{},
		Attributes:    t.AttributeNames(),
		Allocates:     []TemplateAllocationRule{},
		Source:        t.Source,
		Usable:        len(problems) == 0,
	}
	if t.Description != "" {
		v.Description = ptr(t.Description)
	}
	if t.Parent != "" {
		v.Parent = ptr(t.Parent)
	}
	if len(problems) > 0 {
		v.Problems = ptr(problems)
	}
	for _, f := range t.Fields {
		field := TemplateField{
			Name:     f.Name,
			Label:    f.Name,
			Required: f.Required,
			Kind:     TemplateFieldKind(f.FieldKindOf()),
		}
		if f.Label != "" {
			field.Label = f.Label
		}
		if f.Help != "" {
			field.Help = ptr(f.Help)
		}
		if f.Default != "" {
			field.Default = ptr(f.Default)
		}
		v.Fields = append(v.Fields, field)
	}
	for _, name := range t.AllocatedNames() {
		r := t.Allocate[name]
		rule := TemplateAllocationRule{Attribute: name, From: r.From, To: r.To}
		if r.Base != "" {
			rule.Base = ptr(r.Base)
		}
		v.Allocates = append(v.Allocates, rule)
	}
	return v
}

// RenderTemplate fills a template in and allocates its numbers.
func (s *Server) RenderTemplate(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body TemplateRenderRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not a template rendering.", err.Error())
	}
	t := s.templates().Get(body.Template)
	if t == nil {
		return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound,
			"No template by that name.", "GET /templates lists the templates this Alder offers.")
	}
	parent, ok := parseDNParam(c, strings.TrimSpace(body.Parent))
	if !ok {
		return nil
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}
	if problems := t.Problems(sch); len(problems) > 0 {
		return badRequest(c, "This template does not fit this directory's schema.", strings.Join(problems, "; "))
	}
	// Checked before anything is searched: a mistyped field should not cost
	// a read of every uidNumber in the directory.
	placeholder := map[string]int64{}
	for _, name := range t.AllocatedNames() {
		placeholder[name] = t.Allocate[name].From
	}
	if _, err := t.Render(body.Values, placeholder); err != nil {
		return templateInputError(c, err)
	}

	allocations, err := t.AllocateNumbers(ctx, sess.Conn, sess.Conn.Capabilities().NamingContexts)
	if err != nil {
		if errors.Is(err, entrytemplate.ErrAllocation) {
			return writeError(c, fiber.StatusConflict, ErrorErrorConflict,
				"No number could be allocated.", err.Error())
		}
		return s.fail(c, err)
	}
	numbers := map[string]int64{}
	for name, a := range allocations {
		numbers[name] = a.Value
	}
	rendered, err := t.Render(body.Values, numbers)
	if err != nil {
		return templateInputError(c, err)
	}

	target, err := parent.ChildAttr(rendered.RDNType, rendered.RDNValue)
	if err != nil {
		return badRequest(c, "The new entry's name is not usable.", err.Error())
	}
	attrs := make([]ChangeAttribute, 0, len(rendered.Attributes))
	for _, a := range rendered.Attributes {
		values := make([]AttributeValue, 0, len(a.Values))
		for _, v := range a.Values {
			values = append(values, AttributeValue{Text: ptr(v)})
		}
		attrs = append(attrs, ChangeAttribute{Name: a.Name, Values: values})
	}

	out := TemplateRendering{
		Change: ChangeRequest{
			Dn:         target.String(),
			Type:       ChangeRequestTypeAdd,
			Attributes: &attrs,
		},
		Allocated: []TemplateAllocation{},
	}
	names := make([]string, 0, len(allocations))
	for n := range allocations {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := allocations[n]
		row := TemplateAllocation{Attribute: n, Value: a.Value, Scanned: a.Scanned, Bases: a.Bases}
		if a.Highest >= 0 {
			row.HighestInUse = ptr(a.Highest)
		}
		out.Allocated = append(out.Allocated, row)
	}
	return c.JSON(out)
}

func templateInputError(c *fiber.Ctx, err error) error {
	if errors.Is(err, entrytemplate.ErrInput) {
		msg := strings.TrimPrefix(err.Error(), entrytemplate.ErrInput.Error()+": ")
		return badRequest(c, "Some of the values cannot be used.", msg)
	}
	return badRequest(c, "The template could not be filled in.", err.Error())
}
