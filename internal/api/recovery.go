package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/plan"
	"github.com/hazame-hub/alder/internal/recovery"
	"github.com/hazame-hub/alder/internal/schema"
)

// Recovery bundles, on the wire.
//
// A bundle is made by the apply handlers, from the entries as they were read
// immediately before each change ran, and handed to the client. Alder keeps no
// copy. The only thing that reads one back is InspectRecovery, and all it does
// is turn it into ordinary change requests carrying expectations: from there a
// compensation is planned, reviewed and applied exactly like any other change.
// There is deliberately no endpoint that applies a bundle.

// InspectRecovery validates a bundle and returns its changes to plan.
func (s *Server) InspectRecovery(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	bundle, integrity, err := recovery.Decode(c.Body())
	if err != nil {
		return recoveryRefusal(c, err)
	}

	changes := bundle.Changes()
	requests := make([]ChangeRequest, 0, len(changes))
	proposals := make([]plan.Proposal, 0, len(changes))
	for _, change := range changes {
		record, expect, convErr := change.Record()
		if convErr != nil {
			// Decode already converted every change, so this is unreachable
			// unless the two disagree -- which is exactly when to refuse.
			return recoveryRefusal(c, &recovery.Error{Code: recovery.CodeInvalid, Detail: convErr.Error()})
		}
		requests = append(requests, expectingRequest(record, expect))
		proposals = append(proposals, plan.Proposal{Record: record, Intent: plan.IntentExact, Expect: expect})
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	sch, _ := sess.Conn.Schema(ctx)
	computed, err := s.planner.ForSession(sessionScope(sess)).ComputeProposals(ctx, planReader{sess.Conn}, sch,
		proposals, plan.Options{})
	if err != nil {
		return s.fail(c, err)
	}

	caps := sess.Conn.Capabilities()
	same, differences := recovery.SameOrigin(bundle.Origin, originOf(caps))
	steps, err := wireSteps(bundle.Steps)
	if err != nil {
		return s.fail(c, err)
	}
	out := RecoveryInspection{
		Version:        bundle.Version,
		CreatedAt:      bundle.CreatedAt,
		Origin:         wireOrigin(bundle.Origin),
		OriginMatches:  same,
		Recoverability: RecoveryRecoverability(bundle.Recoverability),
		Integrity:      SnapshotIntegrity(integrity),
		Checksum:       bundle.Checksum,
		Steps:          steps,
		Changes:        requests,
		Drift:          make([]RecoveryDrift, 0, len(computed.Items)),
	}
	if len(differences) > 0 {
		out.OriginDifferences = ptr(differences)
	}
	for _, item := range computed.Items {
		drift := RecoveryDrift{Index: item.Index, Dn: item.DN.String(), State: RecoveryDriftReady}
		switch item.Action {
		case plan.ActionConflict:
			drift.State = RecoveryDriftDrifted
		case plan.ActionInvalid:
			drift.State = RecoveryDriftBlocked
		case plan.ActionUnchanged:
			drift.State = RecoveryDriftAlreadyRecovered
		}
		if item.Problem != nil {
			problem := PlanProblem{Code: PlanProblemCode(item.Problem.Code)}
			if item.Problem.Attribute != "" {
				problem.Attribute = ptr(item.Problem.Attribute)
			}
			drift.Problem = &problem
		}
		out.Drift = append(out.Drift, drift)
	}
	s.logger.Info("recovery bundle inspected", "steps", len(bundle.Steps), "changes", len(requests),
		"integrity", string(integrity), "origin_matches", same)
	return c.JSON(out)
}

// recoveryRefusal answers with the stable code for why a document is not a
// usable bundle. The detail never repeats a value from it.
func recoveryRefusal(c *fiber.Ctx, err error) error {
	var re *recovery.Error
	if !errors.As(err, &re) {
		return badRequest(c, "The recovery bundle is not usable.", "")
	}
	code := ErrorErrorRecoveryInvalid
	message := "This is not a valid Alder recovery bundle."
	switch re.Code {
	case recovery.CodeUnsupportedVersion:
		code, message = ErrorErrorRecoveryUnsupportedVersion, "This recovery bundle's format version is not one this Alder reads."
	case recovery.CodeChecksumMismatch:
		code, message = ErrorErrorRecoveryChecksumMismatch, "The recovery bundle does not match its checksum."
	case recovery.CodeTooLarge:
		code, message = ErrorErrorRecoveryTooLarge, "The recovery bundle is larger than Alder reads."
	}
	return writeError(c, fiber.StatusBadRequest, code, message, re.Detail)
}

// originOf is what a bundle records about the directory: what it announces,
// never where it was reached or as whom.
func originOf(caps directory.Capabilities) recovery.Origin {
	contexts := append([]string{}, caps.NamingContexts...)
	return recovery.Origin{Vendor: caps.VendorName, VendorVersion: caps.VendorVersion, NamingContexts: contexts}
}

func wireOrigin(o recovery.Origin) RecoveryOrigin {
	out := RecoveryOrigin{NamingContexts: append([]string{}, o.NamingContexts...)}
	if o.Vendor != "" {
		out.Vendor = ptr(o.Vendor)
	}
	if o.VendorVersion != "" {
		out.VendorVersion = ptr(o.VendorVersion)
	}
	return out
}

// wireSteps carries the recovery package's steps onto the generated type
// through their JSON, which the spec and the package share field for field.
// Converting by hand would be a second description of the format to keep in
// step with the first.
func wireSteps(steps []recovery.Step) ([]RecoveryStep, error) {
	raw, err := json.Marshal(steps)
	if err != nil {
		return nil, err
	}
	out := []RecoveryStep{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("recovery: the steps do not fit the API's shape: %w", err)
	}
	return out, nil
}

// expectingRequest is a change request carrying its expectation.
func expectingRequest(record directory.ChangeRecord, expect *plan.Expectation) ChangeRequest {
	req := changeRequest(record)
	if expect != nil {
		attrs := make([]ChangeAttribute, 0, len(expect.Attributes))
		for _, a := range expect.Attributes {
			attrs = append(attrs, ChangeAttribute{Name: a.Name, Values: encodeRawValues(a.Values)})
		}
		req.Expect = &ChangeExpectation{Attributes: attrs}
		if expect.Exhaustive {
			req.Expect.Exhaustive = ptr(true)
		}
	}
	return req
}

// maxExpectedAttributes bounds one expectation. An entry with more user
// attributes than this is not one a compensation was derived for.
const maxExpectedAttributes = 1000

// changeExpectation converts a request's expectation. A sensitive attribute is
// refused rather than compared: its values are never held, so an expectation
// about one could only have come from somewhere that holds them.
func changeExpectation(req ChangeRequest) (*plan.Expectation, error) {
	if req.Expect == nil {
		return nil, nil
	}
	if len(req.Expect.Attributes) > maxExpectedAttributes {
		return nil, fmt.Errorf("expect: %d attributes, and an expectation names at most %d",
			len(req.Expect.Attributes), maxExpectedAttributes)
	}
	out := &plan.Expectation{Exhaustive: req.Expect.Exhaustive != nil && *req.Expect.Exhaustive}
	for _, a := range req.Expect.Attributes {
		if err := dn.ValidateType(schema.BaseName(a.Name)); err != nil {
			return nil, fmt.Errorf("expect: %q is not an attribute description", a.Name)
		}
		if schema.IsSensitive(a.Name) {
			return nil, fmt.Errorf("expect: %s is a sensitive attribute, whose values are never compared", a.Name)
		}
		values, err := decodeValues(a.Values)
		if err != nil {
			return nil, fmt.Errorf("expect: %s: %w", a.Name, err)
		}
		out.Attributes = append(out.Attributes, directory.Attribute{Name: a.Name, Values: values})
	}
	return out, nil
}

func changeExpectations(changes []ChangeRequest) ([]*plan.Expectation, error) {
	out := make([]*plan.Expectation, len(changes))
	for i, ch := range changes {
		expect, err := changeExpectation(ch)
		if err != nil {
			return nil, fmt.Errorf("change %d: %w", i+1, err)
		}
		out[i] = expect
	}
	return out, nil
}

// recoveryAssessment is what a plan shows beside a change it would apply.
func recoveryAssessment(record directory.ChangeRecord, sch *schema.Schema, kind PlanTargetKind) *RecoveryAssessment {
	recoverability, reasons := recovery.Assess(record, sch, string(kind))
	out := &RecoveryAssessment{Recoverability: RecoveryRecoverability(recoverability)}
	if len(reasons) > 0 {
		wire := make([]RecoveryReason, 0, len(reasons))
		for _, r := range reasons {
			reason := RecoveryReason{Code: RecoveryReasonCode(r.Code)}
			if r.Attribute != "" {
				reason.Attribute = ptr(r.Attribute)
			}
			wire = append(wire, reason)
		}
		out.Reasons = &wire
	}
	return out
}

// recoveryRecorder derives a recovery bundle while a request applies changes.
//
// Every method is safe on a nil recorder, which is what a request that did not
// ask for recovery has, so the apply loops read the same either way.
type recoveryRecorder struct {
	conn  directory.Session
	sch   *schema.Schema
	caps  directory.Capabilities
	steps []recovery.Step
	// touched holds the folded DNs of the changes that have run. An entry read
	// while checking the plan is still the entry a later change meets unless an
	// earlier change was that entry or one above it.
	touched map[string]bool
	// unsettled is set once a change other than a modification has run. An
	// add, a delete or a rename is what servers answer with changes of their
	// own elsewhere -- referential integrity rewriting member values, a
	// memberOf plugin -- so from then on every entry is read again. The read
	// the plan check made is reused only while nothing like that can have
	// happened, which is what keeps a large set of modifications at one read
	// per change.
	unsettled bool
}

func newRecoveryRecorder(ctx context.Context, conn directory.Session) *recoveryRecorder {
	sch, _ := conn.Schema(ctx)
	return &recoveryRecorder{conn: conn, sch: sch, caps: conn.Capabilities(), touched: map[string]bool{}}
}

// readAttributes is what the plan check should read beyond its own needs.
func (r *recoveryRecorder) readAttributes(record directory.ChangeRecord) []string {
	if r == nil {
		return nil
	}
	return recovery.ReadAttributes(record)
}

// before returns the entry as it is immediately before record runs: the read
// the plan check made when nothing has run since and it covers enough, and a
// fresh read otherwise. Nil when there is none, or when it cannot be read --
// which Derive reports as pre_state_unavailable rather than guessing.
func (r *recoveryRecorder) before(ctx context.Context, record directory.ChangeRecord, verified *verifiedRead) *directory.Entry {
	if r == nil || string(targetKind(r.caps, record.DN)) != recovery.KindData {
		return nil
	}
	switch record.Type {
	case directory.ChangeModify, directory.ChangeDelete, directory.ChangeModRDN:
	default:
		// An add that succeeds had no entry before it; a password change is
		// never recoverable, and its entry is not read for it.
		return nil
	}
	want := recovery.ReadAttributes(record)
	if verified != nil && !r.unsettled && !r.disturbed(record) && recovery.Covers(verified.attrs, want) {
		return verified.entry
	}
	entry, err := r.conn.Read(ctx, record.DN, want)
	if err != nil {
		return nil
	}
	return entry
}

// after records a change that has been applied.
func (r *recoveryRecorder) after(index int, record directory.ChangeRecord, pre *directory.Entry) {
	if r == nil {
		return
	}
	if record.Type != directory.ChangeModify {
		r.unsettled = true
	}
	for _, d := range recovery.Touched(record) {
		r.touched[strings.ToLower(d.String())] = true
	}
	r.steps = append(r.steps, recovery.Derive(index, record, pre, r.sch, string(targetKind(r.caps, record.DN))))
}

// disturbed reports whether a change that has run was the entry record reads,
// or one above it.
func (r *recoveryRecorder) disturbed(record directory.ChangeRecord) bool {
	for d := record.DN; !d.IsEmpty(); d = d.Parent() {
		if r.touched[strings.ToLower(d.String())] {
			return true
		}
	}
	return false
}

// bundle assembles what was recorded, as the JSON the response carries: the
// recovery package's own encoding, whose checksum is over exactly these
// fields, rather than a copy passed through the generated types. Nil when
// nothing was recorded, and when assembling fails: the changes have been
// applied by then, so the response still reports them, without a bundle rather
// than with a broken one.
func (r *recoveryRecorder) bundle(logger *slog.Logger) *json.RawMessage {
	if r == nil || len(r.steps) == 0 {
		return nil
	}
	b, err := recovery.New(originOf(r.caps), r.steps, time.Now())
	if err == nil {
		var raw []byte
		if raw, err = json.Marshal(b); err == nil {
			logger.Info("recovery bundle prepared", "steps", len(b.Steps), "recoverability", string(b.Recoverability))
			msg := json.RawMessage(raw)
			return &msg
		}
	}
	logger.Error("recovery bundle could not be assembled", "steps", len(r.steps), "error", err)
	return nil
}
