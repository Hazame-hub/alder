package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/plan"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/session"
)

// The dry run, and the only place a plan is turned into an answer.
//
// One change could always be previewed exactly, because the LDIF in the
// confirmation dialog is rendered from the record Apply receives. What this
// adds is the question above it: given a set of changes -- or an LDIF document
// -- and a directory, what would actually happen.
//
// The invariant that makes it worth having is that the plan hands back the
// records themselves, each bound by a token to the exact operation it is. The
// apply endpoints check that token before anything runs, so a request that is
// not what was planned is refused rather than executed.

// PlanChanges reports what a set of changes, or an LDIF document, would do.
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
	if (body.Changes != nil) == (body.Ldif != nil) {
		return badRequest(c, "Send either changes or an LDIF document to plan, not both and not neither.", "")
	}

	var proposals []plan.Proposal
	if body.Changes != nil {
		if !changesetSizeOK(c, *body.Changes) {
			return nil
		}
		// Converted and validated before anything is read, exactly as the apply
		// path does it. A plan made from a record Apply would refuse describes
		// something that cannot happen.
		records, err := changeRecords(*body.Changes)
		if err != nil {
			return badRequest(c, "The set contains a change that is not usable.", err.Error())
		}
		// The 1.4 reading, unchanged: exact, except that `reconcile` makes an
		// add desired state.
		reconcile := body.Reconcile != nil && *body.Reconcile
		for _, rec := range records {
			intent := plan.IntentExact
			if reconcile && rec.Type == directory.ChangeAdd {
				intent = plan.IntentDesired
			}
			proposals = append(proposals, plan.Proposal{Record: rec, Intent: intent})
		}
	} else {
		var ok bool
		proposals, ok = parseLdifForPlan(c, *body.Ldif, body.Mode)
		if !ok {
			return nil
		}
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, _ := sess.Conn.Schema(ctx)

	computed, err := s.planner.ForSession(sessionScope(sess)).ComputeProposals(ctx, planReader{sess.Conn}, sch, proposals,
		plan.Options{MembershipAttributes: membershipAttrs})
	if err != nil {
		return s.fail(c, err)
	}

	caps := sess.Conn.Capabilities()
	refs, refSummary, err := s.referenceImpact(ctx, sess.Conn, sch, caps, computed)
	if err != nil {
		return s.fail(c, err)
	}

	out := Plan{
		Counts: planCounts(computed.Counts),
		Items:  make([]PlanItem, 0, len(computed.Items)),
		Impact: &PlanImpact{References: refSummary},
	}
	groups := map[string]bool{}
	for _, item := range computed.Items {
		view, viewErr := s.planItem(item, sch, caps)
		if viewErr != nil {
			return s.fail(c, viewErr)
		}
		if r, ok := refs[item.Index]; ok {
			view.References = r
		}
		if view.Kind != nil && !item.Action.AppliesNothing() {
			switch *view.Kind {
			case PlanTargetSchema:
				out.Impact.Kinds.Schema++
			case PlanTargetConfig:
				out.Impact.Kinds.Config++
			default:
				out.Impact.Kinds.Data++
			}
		}
		for _, m := range item.Membership {
			out.Impact.Membership.Gained += len(m.Gained)
			out.Impact.Membership.Removed += len(m.Removed)
			groups[strings.ToLower(item.DN.String())] = true
		}
		out.Items = append(out.Items, view)
	}
	out.Impact.Membership.Groups = len(groups)

	if len(computed.Subtrees) > 0 {
		subtrees := make([]PlanSubtree, 0, len(computed.Subtrees))
		for _, st := range computed.Subtrees {
			subtrees = append(subtrees, PlanSubtree{Root: st.Root.String(), Entries: st.Entries})
		}
		out.Subtrees = &subtrees
	}

	records := make([]directory.ChangeRecord, 0, len(proposals))
	for _, p := range proposals {
		records = append(records, p.Record)
	}
	// The same cross-record findings the changeset preview reports. They are
	// about ordering and overlap, which no single item can see.
	if warnings := changesetWarnings(records); len(warnings) > 0 {
		out.Warnings = ptr(warnings)
	}
	return c.JSON(out)
}

// parseLdifForPlan turns a document into proposals, writing the refusal and
// returning false when it cannot.
func parseLdifForPlan(c *fiber.Ctx, text string, mode *PlanLdifMode) ([]plan.Proposal, bool) {
	if len(text) > maxImportBytes {
		_ = badRequest(c, fmt.Sprintf(
			"The LDIF is larger than the %d MB import limit.", maxImportBytes>>20), "")
		return nil, false
	}
	records, err := ldif.Unmarshal([]byte(text))
	if err != nil {
		var syntaxErr *ldif.SyntaxError
		var urlErr *ldif.ErrURLReference
		switch {
		case errors.As(err, &syntaxErr):
			_ = writeError(c, fiber.StatusBadRequest, ErrorErrorBadRequest,
				"The LDIF could not be parsed.", syntaxErr.Error())
		case errors.As(err, &urlErr):
			_ = writeError(c, fiber.StatusBadRequest, ErrorErrorBadRequest,
				"The LDIF references a URL, which Alder does not fetch.", urlErr.Error())
		default:
			_ = badRequest(c, "The LDIF could not be read.", err.Error())
		}
		return nil, false
	}
	if len(records) == 0 {
		_ = badRequest(c, "The LDIF contains no records.", "")
		return nil, false
	}
	if len(records) > MaxChangesetChanges {
		_ = badRequest(c,
			fmt.Sprintf("A plan covers at most %d changes, and this document has %d records.",
				MaxChangesetChanges, len(records)),
			"The whole document is read and classified in one request, so it is bounded like a changeset.")
		return nil, false
	}
	desired := mode != nil && *mode == PlanLdifModeDesired
	proposals, err := ldifProposals(records, desired)
	if err != nil {
		_ = writeLdifError(c, err)
		return nil, false
	}
	return proposals, true
}

func (s *Server) planItem(item plan.Item, sch *schema.Schema, caps directory.Capabilities) (PlanItem, error) {
	view := PlanItem{
		Index:  item.Index,
		Dn:     item.DN.String(),
		Action: planAction(item.Action),
		Exists: item.Exists,
		Intent: ptr(planIntent(item.Intent)),
		Kind:   ptr(targetKind(caps, item.DN)),
	}
	if item.Reason != "" {
		view.Reason = ptr(item.Reason)
	}
	if item.Problem != nil {
		problem := PlanProblem{Code: PlanProblemCode(item.Problem.Code)}
		if item.Problem.Attribute != "" {
			problem.Attribute = ptr(item.Problem.Attribute)
		}
		view.Problem = &problem
	}
	if len(item.SkippedAttributes) > 0 {
		view.SkippedAttributes = ptr(item.SkippedAttributes)
	}
	if len(item.Membership) > 0 {
		changes := make([]PlanMembershipChange, 0, len(item.Membership))
		for _, m := range item.Membership {
			changes = append(changes, PlanMembershipChange{
				Attribute: m.Attribute,
				Gained:    nonNil(m.Gained),
				Removed:   nonNil(m.Removed),
			})
		}
		view.Membership = &changes
	}
	// Nothing to apply means nothing to show and nothing to check against
	// later, so neither the record nor the baseline is emitted.
	if item.Action.AppliesNothing() {
		return view, nil
	}

	// Rendered from a copy with sensitive values withheld, and returned the
	// same way. A plan is a document people read, share and paste into
	// tickets; a password hash -- or a plaintext password, which is what an
	// LDIF file sometimes holds -- has no business in one. The token binds
	// those attributes by shape, not by bytes, so a client applying the plan
	// supplies the values from its own copy of the change.
	preview, err := s.renderPreview(withholdSensitive(item.Record), sch, caps)
	if err != nil {
		return PlanItem{}, err
	}
	request := withholdSensitiveRequest(changeRequest(item.Record))
	view.Record = &request
	view.Preview = &preview
	view.Baseline = ptr(string(item.Baseline))
	return view, nil
}

// withholdSensitive copies a record with every sensitive value replaced by a
// placeholder that says what it is. For rendering only: this record is never
// applied, and the placeholder is plain text that cannot be mistaken for a
// hash.
func withholdSensitive(rec directory.ChangeRecord) directory.ChangeRecord {
	placeholder := func(values [][]byte) [][]byte {
		out := make([][]byte, 0, len(values))
		for _, v := range values {
			out = append(out, []byte(fmt.Sprintf("withheld (%d bytes)", len(v))))
		}
		return out
	}
	out := rec
	if len(rec.Attrs) > 0 {
		out.Attrs = make([]directory.Attribute, 0, len(rec.Attrs))
		for _, a := range rec.Attrs {
			if schema.IsSensitive(a.Name) {
				a.Values = placeholder(a.Values)
			}
			out.Attrs = append(out.Attrs, a)
		}
	}
	if len(rec.Mods) > 0 {
		out.Mods = make([]directory.Mod, 0, len(rec.Mods))
		for _, m := range rec.Mods {
			if schema.IsSensitive(m.Name) {
				m.Values = placeholder(m.Values)
			}
			out.Mods = append(out.Mods, m)
		}
	}
	return out
}

// withholdSensitiveRequest replaces sensitive values in a request with their
// length alone. The decoder refuses such a value, so a client that posts the
// record back without supplying the value is told, rather than having an empty
// value written in its place.
func withholdSensitiveRequest(req ChangeRequest) ChangeRequest {
	sizes := func(values []AttributeValue) []AttributeValue {
		out := make([]AttributeValue, 0, len(values))
		for _, v := range values {
			n := 0
			if b, err := decodeValue(v); err == nil {
				n = len(b)
			}
			out = append(out, AttributeValue{Size: ptr(n)})
		}
		return out
	}
	if req.Attributes != nil {
		attrs := make([]ChangeAttribute, 0, len(*req.Attributes))
		for _, a := range *req.Attributes {
			if schema.IsSensitive(a.Name) {
				a.Values = sizes(a.Values)
			}
			attrs = append(attrs, a)
		}
		req.Attributes = &attrs
	}
	if req.Mods != nil {
		mods := make([]ChangeMod, 0, len(*req.Mods))
		for _, m := range *req.Mods {
			if schema.IsSensitive(m.Name) && m.Values != nil {
				m.Values = ptr(sizes(*m.Values))
			}
			mods = append(mods, m)
		}
		req.Mods = &mods
	}
	return req
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
	case plan.ActionInvalid:
		return PlanActionInvalid
	}
	return PlanActionConflict
}

func planIntent(i plan.Intent) PlanIntent {
	if i == plan.IntentDesired {
		return PlanIntentDesired
	}
	return PlanIntentExact
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
		Invalid:     ptr(c.Invalid),
	}
}

// targetKind says which area of the server a DN is in, from the locations the
// server itself announces -- never from what a DN looks like.
//
// Schema is checked first. On a server that keeps its schema inside its
// configuration tree, a schema entry is both, and "schema" is the more precise
// and more consequential of the two: a write there changes what every entry
// may hold.
func targetKind(caps directory.Capabilities, target dn.DN) PlanTargetKind {
	// Compared as DNs rather than through SchemaWrite.Target, which matches
	// the string as the server spelled it: the same entry named with different
	// spacing or case would otherwise land in the wrong area.
	for _, t := range caps.SchemaWrite.Targets {
		if parsed, err := dn.Parse(t.DN); err == nil && target.Equal(parsed) {
			return PlanTargetSchema
		}
	}
	if subschema, err := dn.Parse(caps.SubschemaSubentry); err == nil && !subschema.IsEmpty() &&
		target.Equal(subschema) {
		return PlanTargetSchema
	}
	// The configuration tree as the server announced it, or -- for a server
	// that announces none, which is 389 DS -- where Alder found it answering at
	// the conventional location when the session connected. Either is a place
	// the server itself put there; neither is a guess from what a DN looks like.
	for _, root := range []string{caps.ConfigContext, caps.Config.DN} {
		if config, err := dn.Parse(root); err == nil && !config.IsEmpty() && target.HasSuffix(config) {
			return PlanTargetConfig
		}
	}
	return PlanTargetData
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// --- references --------------------------------------------------------------

// maxReferenceSubjects is how many deleted or renamed DNs one plan will search
// references for. Past it no count is offered: a number that covers some of
// the deletions would read as covering all of them.
const maxReferenceSubjects = 2000

// referenceChunk is how many subjects share one search filter. Each subject
// adds an equality assertion per reference attribute, so the filter grows with
// both; this keeps any one request an ordinary size.
const referenceChunk = 50

// referenceSearchLimit bounds each chunk's search.
const referenceSearchLimit = 5000

// referenceImpact finds the entries naming what a plan deletes or renames away,
// and how many of those references the plan leaves dangling.
//
// The reverse-reference search the entry page already uses -- same attribute
// vocabulary, same filter builder, same search base, same DN comparison --
// batched: one search per naming context per chunk of subjects rather than one
// per deletion, so a subtree of four hundred entries is eight searches and not
// four hundred.
func (s *Server) referenceImpact(
	ctx context.Context,
	conn directory.Session,
	sch *schema.Schema,
	caps directory.Capabilities,
	computed plan.Plan,
) (map[int]*PlanReferenceImpact, PlanImpactReferences, error) {
	summary := PlanImpactReferences{}

	type subject struct {
		index int
		dn    dn.DN
		key   string
	}
	var subjects []subject
	for _, item := range computed.Items {
		if item.Action == plan.ActionDelete || item.Action == plan.ActionRename {
			subjects = append(subjects, subject{
				index: item.Index, dn: item.DN, key: strings.ToLower(item.DN.String()),
			})
		}
	}
	switch {
	case len(subjects) == 0:
		summary.Reason = ptr("The plan deletes and renames nothing, so no reference can be left dangling.")
		return nil, summary, nil
	case len(subjects) > maxReferenceSubjects:
		summary.Reason = ptr(fmt.Sprintf(
			"The plan deletes or renames %d entries, more than the %d a single plan searches references for.",
			len(subjects), maxReferenceSubjects))
		return nil, summary, nil
	case sch == nil:
		summary.Reason = ptr("The schema could not be read, so the reference attributes are not known.")
		return nil, summary, nil
	}
	attrs := definedReferenceAttrs(sch)
	if len(attrs) == 0 {
		summary.Reason = ptr("This server defines none of the attributes Alder treats as references.")
		return nil, summary, nil
	}

	bySubject := make(map[string]int, len(subjects))
	for i, sub := range subjects {
		bySubject[sub.key] = i
	}

	// Group subjects by the naming context their references would be in.
	type batch struct {
		base  dn.DN
		items []int
	}
	var order []string
	batches := map[string]*batch{}
	for i, sub := range subjects {
		base, _ := referenceSearchBase(caps, sub.dn)
		if base.IsEmpty() {
			continue // schema or configuration; nothing in the data tree names it
		}
		key := strings.ToLower(base.String())
		if batches[key] == nil {
			batches[key] = &batch{base: base}
			order = append(order, key)
		}
		batches[key].items = append(batches[key].items, i)
	}

	type found struct {
		referrer dn.DN
		attr     string
		value    string
		subject  int
	}
	seen := map[string]bool{}
	var refs []found

	for _, key := range order {
		b := batches[key]
		for start := 0; start < len(b.items); start += referenceChunk {
			end := min(start+referenceChunk, len(b.items))
			var subs []filter.Filter
			for _, i := range b.items[start:end] {
				if tree, ok := referencedByFilterTree(sch, subjects[i].dn.String()); ok {
					subs = append(subs, tree)
				}
			}
			if len(subs) == 0 {
				continue
			}
			res, err := searchReferences(ctx, conn, b.base, filter.Or(subs...), attrs, referenceSearchLimit)
			if err != nil {
				return nil, PlanImpactReferences{}, err
			}
			if res.Truncated {
				summary.Truncated = true
			}
			for _, entry := range res.Entries {
				for name, values := range entry.Attributes {
					for _, raw := range values {
						target, ok := referenceKey(string(raw))
						if !ok {
							continue
						}
						i, named := bySubject[target]
						if !named || strings.EqualFold(entry.DN.String(), subjects[i].dn.String()) {
							continue
						}
						id := strings.ToLower(entry.DN.String()) + "|" + strings.ToLower(name) + "|" + target
						if seen[id] {
							continue
						}
						seen[id] = true
						refs = append(refs, found{referrer: entry.DN, attr: name, value: string(raw), subject: i})
					}
				}
			}
		}
	}

	resolved := resolvedReferences(computed)
	perItem := map[int]*PlanReferenceImpact{}
	byAttr := map[int]map[string]int{}
	referrers := map[int]map[string]bool{}
	for _, r := range refs {
		idx := subjects[r.subject].index
		impact := perItem[idx]
		if impact == nil {
			impact = &PlanReferenceImpact{ByAttribute: []PlanReferenceCount{}}
			perItem[idx] = impact
			byAttr[idx] = map[string]int{}
			referrers[idx] = map[string]bool{}
		}
		impact.Count++
		summary.Found++
		byAttr[idx][r.attr]++
		if len(referrers[idx]) < 50 {
			referrers[idx][r.referrer.String()] = true
		}
		if !resolved(r.referrer, r.attr, r.value) {
			impact.Dangling++
			summary.Dangling++
		}
	}
	for idx, impact := range perItem {
		names := make([]string, 0, len(byAttr[idx]))
		for name := range byAttr[idx] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			impact.ByAttribute = append(impact.ByAttribute, PlanReferenceCount{Attribute: name, Count: byAttr[idx][name]})
		}
		list := make([]string, 0, len(referrers[idx]))
		for d := range referrers[idx] {
			list = append(list, d)
		}
		sort.Strings(list)
		impact.Referrers = &list
	}
	summary.Analysed = true
	return perItem, summary, nil
}

// resolvedReferences returns a test for whether a reference is dealt with by
// the plan itself: its holder is also deleted, or another change removes that
// value from that attribute.
func resolvedReferences(computed plan.Plan) func(referrer dn.DN, attr, value string) bool {
	deleted := map[string]bool{}
	mods := map[string][]directory.Mod{}
	for _, item := range computed.Items {
		key := strings.ToLower(item.DN.String())
		switch item.Action {
		case plan.ActionDelete:
			deleted[key] = true
		case plan.ActionModify:
			mods[key] = append(mods[key], item.Record.Mods...)
		}
	}
	return func(referrer dn.DN, attr, value string) bool {
		key := strings.ToLower(referrer.String())
		if deleted[key] {
			return true
		}
		want, ok := referenceKey(value)
		if !ok {
			return false
		}
		for _, m := range mods[key] {
			if !strings.EqualFold(schema.BaseName(m.Name), schema.BaseName(attr)) {
				continue
			}
			switch m.Op {
			case directory.ModDelete:
				if len(m.Values) == 0 {
					return true
				}
				for _, v := range m.Values {
					if got, ok := referenceKey(string(v)); ok && got == want {
						return true
					}
				}
			case directory.ModReplace:
				kept := false
				for _, v := range m.Values {
					if got, ok := referenceKey(string(v)); ok && got == want {
						kept = true
					}
				}
				if !kept {
					return true
				}
			}
		}
		return false
	}
}

// referenceKey is the folded DN a stored reference value names, with
// uniqueMember's optional "#uid" set aside -- the same comparison the reference
// panel makes, as a map key.
func referenceKey(value string) (string, bool) {
	if parsed, err := dn.Parse(value); err == nil && !parsed.IsEmpty() {
		return strings.ToLower(parsed.String()), true
	}
	if hash := strings.LastIndexByte(value, '#'); hash > 0 && !strings.ContainsRune(value[hash:], ',') {
		if parsed, err := dn.Parse(value[:hash]); err == nil && !parsed.IsEmpty() {
			return strings.ToLower(parsed.String()), true
		}
	}
	return "", false
}

// --- the reader a plan is given ----------------------------------------------

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

// --- checking a request against its plan -------------------------------------

// checkPlannedChanges verifies every change that carries a baseline, before any
// of them run, and returns the refusal to send if one fails.
//
// All of them first, deliberately. A changeset is applied in order and stops at
// the first failure; discovering at change twelve that the directory has moved
// would leave eleven applied against assumptions nobody rechecked.
//
// And all of them to the end, not just to the first stale one, so the refusal
// can name every change the operator needs to look at again.
//
// A mismatch outranks staleness: a request that is not its plan is a different
// problem from a plan the directory has outgrown, and saying "the directory
// moved" about a change nobody planned would send the operator looking in the
// wrong place.
// sessionScope is what separates one session's secret bindings from another's:
// a token that binds a password is only reproducible in the session that
// planned it. The session ID never leaves the server in this form -- it is an
// input to a key derivation under the process's fingerprint key.
func sessionScope(sess *session.Session) []byte {
	return []byte(sess.ID)
}

func (s *Server) checkPlannedChanges(
	ctx context.Context,
	sess *session.Session,
	requests []ChangeRequest,
	records []directory.ChangeRecord,
) (refusal *Error, status int, err error) {
	var stale, mismatched []ErrorAffected
	planner := s.planner.ForSession(sessionScope(sess))
	for i, req := range requests {
		if req.Baseline == nil || *req.Baseline == "" {
			continue
		}
		verifyErr := planner.Verify(ctx, planReader{sess.Conn}, records[i], plan.Baseline(*req.Baseline))
		switch {
		case verifyErr == nil:
		case plan.IsMismatch(verifyErr):
			mismatched = append(mismatched, ErrorAffected{Index: i, Dn: records[i].DN.String()})
		case plan.IsStale(verifyErr):
			stale = append(stale, ErrorAffected{Index: i, Dn: records[i].DN.String()})
		default:
			return nil, 0, verifyErr
		}
	}

	switch {
	case len(mismatched) > 0:
		return &Error{
			Error: ErrorErrorPlanMismatch,
			Message: "A change carries a plan token for a different change, so nothing was applied. " +
				"What is applied has to be what was planned.",
			Detail: ptr(fmt.Sprintf("%d change(s) do not match the operation their baseline was issued for, "+
				"starting with change %d (%s). Plan again and apply the records the plan returns.",
				len(mismatched), mismatched[0].Index+1, mismatched[0].Dn)),
			Affected: &mismatched,
		}, fiber.StatusBadRequest, nil
	case len(stale) > 0:
		return &Error{
			// conflict, as in 1.4, so a client switching on it keeps working;
			// the precision rides in cause.
			Error:   ErrorErrorConflict,
			Cause:   ptr(ErrorCausePlanStale),
			Message: "The directory has changed since this was planned, so nothing was applied.",
			Detail: ptr(fmt.Sprintf("%d change(s) depended on something that has since been changed "+
				"by somebody else, starting with change %d (%s). Plan again to see what these "+
				"changes would do now.", len(stale), stale[0].Index+1, stale[0].Dn)),
			Affected: &stale,
		}, fiber.StatusConflict, nil
	}
	return nil, 0, nil
}
