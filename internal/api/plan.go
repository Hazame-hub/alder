package api

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/plan"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/session"
)

// The dry run.
//
// One change could always be previewed exactly, because the LDIF in the
// confirmation dialog is rendered from the record Apply receives. What was
// missing was the question above it: given a set of changes and a directory,
// what would happen. The import path answered a version of it for LDIF
// documents; this answers it for any set, with a classification a client can
// switch on.
//
// The invariant that makes it worth having is that the plan hands back the
// records themselves. A plan that described what would happen, and an apply
// that worked it out again from the same input, would agree right up until the
// day they did not.

// PlanChanges reports what a set of changes would do.
func (s *Server) PlanChanges(c *fiber.Ctx) error {
	// require, not requireWritable: planning writes nothing, and a read-only
	// Alder is exactly where "what would this do" is worth asking.
	sess := s.require(c)
	if sess == nil {
		return nil
	}

	var body PlanRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if !changesetSizeOK(c, body.Changes) {
		return nil
	}

	// Converted and validated before anything is read, exactly as the apply
	// path does it. A plan made from a record Apply would refuse describes
	// something that cannot happen.
	records, err := changeRecords(body.Changes)
	if err != nil {
		return badRequest(c, "The set contains a change that is not usable.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, _ := sess.Conn.Schema(ctx)

	computed, err := s.planner.Compute(ctx, planReader{sess.Conn}, sch, records, plan.Options{
		Reconcile: body.Reconcile != nil && *body.Reconcile,
	})
	if err != nil {
		return s.fail(c, err)
	}

	out := Plan{
		Counts: planCounts(computed.Counts),
		Items:  make([]PlanItem, 0, len(computed.Items)),
	}
	for _, item := range computed.Items {
		view, err := s.planItem(item, sch, sess.Conn.Capabilities())
		if err != nil {
			return s.fail(c, err)
		}
		out.Items = append(out.Items, view)
	}
	// The same cross-record findings the changeset preview reports. They are
	// about ordering and overlap, which no single item can see.
	if warnings := changesetWarnings(records); len(warnings) > 0 {
		out.Warnings = ptr(warnings)
	}
	return c.JSON(out)
}

func (s *Server) planItem(item plan.Item, sch *schema.Schema, caps directory.Capabilities) (PlanItem, error) {
	view := PlanItem{
		Index:  item.Index,
		Dn:     item.DN.String(),
		Action: planAction(item.Action),
		Exists: item.Exists,
	}
	if item.Reason != "" {
		view.Reason = ptr(item.Reason)
	}
	if len(item.SkippedAttributes) > 0 {
		view.SkippedAttributes = ptr(item.SkippedAttributes)
	}
	// Nothing to apply means nothing to show and nothing to check against
	// later, so neither the record nor the baseline is emitted.
	switch item.Action {
	case plan.ActionUnchanged, plan.ActionConflict:
		return view, nil
	}

	preview, err := s.renderPreview(item.Record, sch, caps)
	if err != nil {
		return PlanItem{}, err
	}
	request := changeRequest(item.Record)
	view.Record = &request
	view.Preview = &preview
	view.Baseline = ptr(string(item.Baseline))
	return view, nil
}

func planAction(a plan.Action) PlanAction {
	switch a {
	case plan.ActionAdd:
		return PlanActionAdd
	case plan.ActionModify:
		return PlanActionModify
	case plan.ActionDelete:
		return PlanActionDelete
	case plan.ActionRename:
		return PlanActionRename
	case plan.ActionSetPassword:
		return PlanActionSetPassword
	case plan.ActionUnchanged:
		return PlanActionUnchanged
	}
	return PlanActionConflict
}

func planCounts(c plan.Counts) PlanCounts {
	return PlanCounts{
		Examined:    c.Examined,
		Add:         c.Add,
		Modify:      c.Modify,
		Delete:      c.Delete,
		Rename:      c.Rename,
		SetPassword: c.SetPassword,
		Unchanged:   c.Unchanged,
		Conflict:    c.Conflict,
	}
}

// planReader narrows a session to what a plan is allowed to do.
//
// The planner takes a Reader rather than a Session so that it cannot write, and
// this is where the one becomes the other. HasChildren rides along when the
// driver has it, which is how a delete that the directory would refuse is
// reported as a conflict instead of being attempted.
type planReader struct{ conn directory.Session }

func (p planReader) Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error) {
	return p.conn.Read(ctx, target, attrs)
}

func (p planReader) HasChildren(ctx context.Context, target dn.DN) (bool, error) {
	browser, ok := p.conn.(treeBrowser)
	if !ok {
		return false, nil
	}
	return browser.HasChildren(ctx, target)
}

// verifyBaselines checks every change that carries one before any of them run.
//
// All of them first, deliberately. A changeset is applied in order and stops at
// the first failure; discovering at change twelve that the directory has moved
// would leave eleven applied against assumptions nobody rechecked. This is the
// one class of failure that is knowable in advance, which is the same reason
// changeRecords validates the whole set before applying any of it.
func (s *Server) verifyBaselines(
	ctx context.Context,
	sess *session.Session,
	requests []ChangeRequest,
	records []directory.ChangeRecord,
) (stale *dn.DN, err error) {
	for i, req := range requests {
		if req.Baseline == nil || *req.Baseline == "" {
			continue
		}
		verifyErr := s.planner.Verify(ctx, planReader{sess.Conn}, records[i],
			plan.Baseline(*req.Baseline))
		if verifyErr == nil {
			continue
		}
		if plan.IsStale(verifyErr) {
			at := records[i].DN
			return &at, nil
		}
		return nil, verifyErr
	}
	return nil, nil
}

// refuseStale writes the 409 a moved directory earns.
func refuseStale(c *fiber.Ctx, at dn.DN) error {
	return writeError(c, fiber.StatusConflict, ErrorErrorConflict,
		"The directory has changed since this was planned, so nothing was applied.",
		"Something this plan depended on at "+at.String()+" has been changed by "+
			"somebody else. Plan again to see what these changes would do now.")
}
