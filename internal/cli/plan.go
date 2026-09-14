package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/envflags"
)

type planOptions struct {
	mode     string
	changes  string
	recovery string
	json     bool
	summary  bool

	yes                 bool
	allowDeletes        bool
	recoveryOut         string
	force               bool
	allowOriginMismatch bool
}

func (o *planOptions) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&o.mode, "mode", "changes",
		"how the LDIF is read: changes (every record is the operation it states) or desired (the state entries should be in)")
	f.StringVar(&o.changes, "changes", "", "a JSON array of change requests to use instead of LDIF, or - for standard input")
	f.StringVar(&o.recovery, "recovery", "", "a recovery bundle whose compensating changes to plan instead, or - for standard input")
	f.BoolVar(&o.json, "json", false, "write JSON to standard output")
	f.BoolVar(&o.summary, "summary", false, "print the counts and not each change")
	envflags.Exclude(f, "mode", "changes", "recovery", "json", "summary")
}

func planCmd(env *Env) *cobra.Command {
	var conn connection
	var o planOptions
	var cmd *cobra.Command
	cmd = command(env, &cobra.Command{
		Use:   "plan [LDIF_FILE | -] | plan --changes FILE | plan --recovery FILE",
		Short: "Show what a set of changes would do, without doing it",
		Long: "Plans an LDIF document, or a JSON array of change requests, against the\n" +
			"directory through a running Alder server. It is the plan the web interface\n" +
			"shows: what each change would do, what cannot be applied and why, what it\n" +
			"touches, and the exact LDIF. Nothing is written.\n\n" +
			"--mode changes (the default, as in the API) reads every record as the exact\n" +
			"operation it states. --mode desired reads the document as the state entries\n" +
			"should be in; an entry it does not mention is never deleted.\n\n" +
			"--recovery plans the compensating changes in a recovery bundle written by\n" +
			"alder apply --recovery-out, against the directory as it is now. A change\n" +
			"whose entry has moved on since the original apply is a conflict, never an\n" +
			"overwrite.\n\n" +
			"Exit status: 0 planned, 3 some change cannot be applied as written, 7 usage,\n" +
			"8 failure.",
		Args: argsBetween(0, 1, "at most one LDIF file, or - for standard input"),
	}, func(ctx context.Context, args []string) error {
		return runPlan(ctx, env, &conn, o, cmd.Flags(), args)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	o.register(cmd)
	return cmd
}

func applyCmd(env *Env) *cobra.Command {
	var conn connection
	var o planOptions
	var cmd *cobra.Command
	cmd = command(env, &cobra.Command{
		Use:   "apply [LDIF_FILE | -] | apply --changes FILE | apply --recovery FILE",
		Short: "Plan a set of changes, show the plan, and apply exactly that plan",
		Long: "Plans the input exactly as alder plan does, shows the plan, and asks before\n" +
			"applying it. What is applied is the plan that was shown: each change carries\n" +
			"the token Alder issued for it, and if the directory has moved since, Alder\n" +
			"refuses the whole set and nothing is written. A refused plan is never planned\n" +
			"again and applied without being shown.\n\n" +
			"At a terminal the question is asked on standard error and the default answer\n" +
			"is no. Anywhere else -- a pipe, a CI job, input on standard input -- nothing\n" +
			"is applied unless --yes is given. --yes answers the question and nothing\n" +
			"else: the plan, its checks and the refusal of a stale plan all still apply.\n" +
			"A plan that deletes entries also needs --allow-deletes when --yes answers.\n\n" +
			"--recovery-out FILE asks Alder for a recovery bundle: the compensating changes\n" +
			"it can derive from each entry as it was immediately before its change. It is\n" +
			"written only for changes that were applied -- on a run that stops partway,\n" +
			"for those before the failure -- and never replaces a file without --force.\n" +
			"It is not a backup, and it holds no password. apply --recovery FILE plans and\n" +
			"applies a bundle's changes like any other; a bundle made against a directory\n" +
			"that announces itself differently also needs --allow-origin-mismatch.\n\n" +
			"Exit status: 0 applied (or nothing to apply), 3 some change cannot be applied\n" +
			"as written, 4 the plan went stale, 5 not confirmed, 6 stopped partway,\n" +
			"7 usage, 8 failure.",
		Args: argsBetween(0, 1, "at most one LDIF file, or - for standard input"),
	}, func(ctx context.Context, args []string) error {
		return runApply(ctx, env, &conn, o, cmd.Flags(), args)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	o.register(cmd)
	f := cmd.Flags()
	f.BoolVar(&o.yes, "yes", false, "apply without asking; required when no terminal can be asked")
	f.BoolVar(&o.allowDeletes, "allow-deletes", false, "with --yes, permit a plan that deletes entries")
	f.StringVar(&o.recoveryOut, "recovery-out", "", "write a recovery bundle for the changes that were applied to this file")
	f.BoolVar(&o.force, "force", false, "replace --recovery-out if it already exists")
	f.BoolVar(&o.allowOriginMismatch, "allow-origin-mismatch", false,
		"apply a --recovery bundle made against a directory that announces itself differently")
	envflags.Exclude(f, "yes", "allow-deletes", "recovery-out", "force", "allow-origin-mismatch")
	return cmd
}

// planInput is what a plan was asked for, and the client's own copy of each
// exact change.
type planInput struct {
	request    api.PlanRequest
	ldif       *string
	mode       api.PlanLdifMode
	staged     []api.ChangeRequest
	readsStdin bool
	// bundle is a recovery bundle as read, turned into changes once there is
	// a server to ask.
	bundle     []byte
	inspection *api.RecoveryInspection
}

func (o planOptions) input(env *Env, flags *pflag.FlagSet, args []string) (*planInput, error) {
	hasLDIF := len(args) == 1
	if o.recovery != "" {
		if hasLDIF || o.changes != "" {
			return nil, usagef("--recovery is the input: give no LDIF document and no --changes with it")
		}
		if flags.Changed("mode") {
			return nil, usagef("--mode says how to read LDIF; a recovery bundle holds exact change requests")
		}
		return &planInput{mode: api.PlanLdifModeChanges, readsStdin: o.recovery == "-"}, nil
	}
	if hasLDIF == (o.changes != "") {
		return nil, usagef("give an LDIF document or --changes, and only one of them")
	}
	if o.changes != "" && flags.Changed("mode") {
		return nil, usagef("--mode says how to read LDIF; --changes are already exact change requests")
	}
	mode := api.PlanLdifMode(o.mode)
	if mode != api.PlanLdifModeChanges && mode != api.PlanLdifModeDesired {
		return nil, usagef("--mode must be changes or desired, not %q", o.mode)
	}
	in := &planInput{mode: mode, readsStdin: (hasLDIF && args[0] == "-") || o.changes == "-"}
	return in, nil
}

// load reads the input. It is separate from input so a command can refuse its
// command line before it touches standard input.
func (in *planInput) load(env *Env, o planOptions, args []string) error {
	if o.recovery != "" {
		data, _, err := env.readInput(o.recovery, "the recovery bundle", maxRequestBytes)
		if err != nil {
			return err
		}
		in.bundle = data
		return nil
	}
	if len(args) == 1 {
		data, _, err := env.readInput(args[0], "the LDIF document", maxLDIFBytes)
		if err != nil {
			return err
		}
		text := string(data)
		in.ldif = &text
		in.request = api.PlanRequest{Ldif: &text, Mode: &in.mode, Reconcile: ptr(false)}
		return nil
	}
	data, name, err := env.readInput(o.changes, "the change requests", maxRequestBytes)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// A field the client does not know is refused, not dropped: a change with
	// part of it silently ignored is a different change.
	dec.DisallowUnknownFields()
	var changes []api.ChangeRequest
	if err := dec.Decode(&changes); err != nil {
		return failf("changes_invalid", "%s is not a JSON array of change requests: %v", name, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return failf("changes_invalid", "%s holds more than one JSON document", name)
	}
	if len(changes) == 0 {
		return failf("changes_invalid", "%s holds no change requests", name)
	}
	in.staged = changes
	in.request = api.PlanRequest{Changes: &changes, Reconcile: ptr(false)}
	return nil
}

// resolve turns a recovery bundle into the changes to plan. It reports false
// when the bundle has nothing that can be compensated, after saying so.
func (in *planInput) resolve(ctx context.Context, r *remote, w io.Writer) (bool, error) {
	if in.bundle == nil {
		return true, nil
	}
	inspection, err := inspectRecovery(ctx, r, in.bundle)
	if err != nil {
		return false, err
	}
	in.inspection = &inspection
	renderRecovery(w, inspection)
	if len(inspection.Changes) == 0 {
		writeln(w, "Nothing in this bundle can be compensated.")
		return false, nil
	}
	changes := inspection.Changes
	in.staged = changes
	in.request = api.PlanRequest{Changes: &changes, Reconcile: ptr(false)}
	return true, nil
}

func runPlan(ctx context.Context, env *Env, conn *connection, o planOptions, flags *pflag.FlagSet, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	in, err := o.input(env, flags, args)
	if err != nil {
		return err
	}
	if err := conn.check(in.readsStdin); err != nil {
		return err
	}
	if err := in.load(env, o, args); err != nil {
		return err
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	header := env.Stdout
	if o.json {
		header = env.Stderr
	}
	proceed, err := in.resolve(ctx, r, header)
	if err != nil {
		return err
	}
	if !proceed {
		if o.json {
			empty, _ := json.Marshal(api.Plan{Items: []api.PlanItem{}})
			if err := writeDocument(env.Stdout, empty); err != nil {
				return failf("output", "cannot write to standard output: %v", err)
			}
		}
		return nil
	}

	p, raw, err := requestPlan(ctx, r, in.request)
	if err != nil {
		return err
	}
	if o.json {
		if err := writeDocument(env.Stdout, raw); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
	} else {
		renderPlan(env.Stdout, p, !o.summary)
	}
	if n := blocked(p); n > 0 {
		// The plan is the answer and is already written; this only sets the
		// exit status, so no second JSON document.
		writef(env.Stderr, "alder: %d change(s) cannot be applied as written\n", n)
		return &ExitError{Code: ExitNotApplicable}
	}
	return nil
}

func requestPlan(ctx context.Context, r *remote, req api.PlanRequest) (api.Plan, []byte, error) {
	res, err := r.api.PlanChangesWithResponse(ctx, req)
	if err != nil {
		return api.Plan{}, nil, transportFailure(ctx, "planning", err)
	}
	if res.StatusCode() != http.StatusOK {
		return api.Plan{}, nil, r.refusal(ctx, "planning", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return api.Plan{}, nil, failf("unexpected_response", "Alder answered the plan request with something that is not a plan")
	}
	return *res.JSON200, res.Body, nil
}

func blocked(p api.Plan) int {
	n := p.Counts.Conflict
	if p.Counts.Invalid != nil {
		n += *p.Counts.Invalid
	}
	return n
}

func applicable(p api.Plan) int {
	c := p.Counts
	return c.Add + c.Modify + c.Delete + c.Rename + c.SetPassword
}

// applyEnvelope is apply's JSON: the plan that was reviewed, and what applying
// it did or why it did not.
type applyEnvelope struct {
	Plan   json.RawMessage `json:"plan,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
	// RecoveryFile is where the recovery bundle was written, when it was.
	RecoveryFile string `json:"recoveryFile,omitempty"`
}

func runApply(ctx context.Context, env *Env, conn *connection, o planOptions, flags *pflag.FlagSet, args []string) (err error) {
	var envelope applyEnvelope
	defer func() {
		if !o.json {
			return
		}
		var exit *ExitError
		if errors.As(err, &exit) && (len(exit.Server) > 0 || exit.Local != "") {
			envelope.Error = exit.document()
		}
		doc, _ := json.Marshal(envelope)
		_ = writeDocument(env.Stdout, doc)
	}()

	in, err := o.input(env, flags, args)
	if err != nil {
		return err
	}
	switch {
	case o.recoveryOut == "-":
		return usagef("--recovery-out needs a file: standard output carries the plan and the result")
	case o.force && o.recoveryOut == "":
		return usagef("--force replaces the --recovery-out file, and none was given")
	case o.allowOriginMismatch && o.recovery == "":
		return usagef("--allow-origin-mismatch is about a --recovery bundle, and none was given")
	}
	if err := refuseExisting(o.recoveryOut, o.force); err != nil {
		return err
	}
	if err := conn.check(in.readsStdin); err != nil {
		return err
	}
	// A person can only be asked where standard input is theirs to answer on.
	canAsk := !in.readsStdin && !conn.bindPasswordStdin && env.Interactive()
	if !o.yes && !canAsk {
		return &ExitError{Code: ExitNotConfirmed, Local: "confirmation_required",
			Message: "not applied: there is no terminal to ask for confirmation, and --yes was not given. " +
				"Review with alder plan, then run apply with --yes"}
	}
	if err := in.load(env, o, args); err != nil {
		return err
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	// An exact change is applied from the client's own copy, because a plan
	// withholds sensitive values from its response. For LDIF that copy is the
	// document as Alder parses it -- by the same reader the plan uses, record for
	// record -- so no LDIF is interpreted here.
	if in.ldif != nil && in.mode == api.PlanLdifModeChanges {
		parsed, parseErr := r.api.ParseLdifWithResponse(ctx, api.ImportRequest{Ldif: *in.ldif, Reconcile: ptr(false)})
		if parseErr != nil {
			return transportFailure(ctx, "reading the LDIF", parseErr)
		}
		if parsed.StatusCode() != http.StatusOK {
			return r.refusal(ctx, "reading the LDIF", parsed.HTTPResponse, parsed.Body)
		}
		if parsed.JSON200 == nil || parsed.JSON200.Requests == nil {
			return failf("unexpected_response", "Alder returned no change requests for the LDIF")
		}
		in.staged = *parsed.JSON200.Requests
	}

	out := env.Stdout
	if o.json {
		out = env.Stderr
	}
	proceed, err := in.resolve(ctx, r, out)
	if err != nil {
		return err
	}
	if !proceed {
		return nil
	}
	if in.inspection != nil && !in.inspection.OriginMatches && !o.allowOriginMismatch {
		return &ExitError{Code: ExitNotConfirmed, Local: "origin_mismatch",
			Message: "not applied: the bundle was made against a directory that announces itself differently. " +
				"If this is the directory you mean to recover, pass --allow-origin-mismatch"}
	}

	p, raw, err := requestPlan(ctx, r, in.request)
	if err != nil {
		return err
	}
	envelope.Plan = compactJSON(raw)
	renderPlan(out, p, !o.summary)

	if n := blocked(p); n > 0 {
		return &ExitError{Code: ExitNotApplicable, Local: "plan_not_applicable",
			Message: fmt.Sprintf("not applied: %d change(s) cannot be applied as written, so nothing was written", n)}
	}
	if applicable(p) == 0 {
		writeln(out, "Nothing to apply: the directory already matches.")
		return nil
	}
	deletes := p.Counts.Delete
	if o.yes {
		if deletes > 0 && !o.allowDeletes {
			return &ExitError{Code: ExitNotConfirmed, Local: "deletes_not_allowed",
				Message: fmt.Sprintf("not applied: the plan deletes %s, and --yes does not permit a deletion without --allow-deletes", plural(deletes, "entry", "entries"))}
		}
	} else {
		question := fmt.Sprintf("Apply %s to %s?", plural(applicable(p), "change", "changes"), conn.label())
		if deletes > 0 {
			question = fmt.Sprintf("Apply %s to %s, deleting %s?", plural(applicable(p), "change", "changes"),
				conn.label(), plural(deletes, "entry", "entries"))
		}
		if !confirm(env, question) {
			return &ExitError{Code: ExitNotConfirmed, Local: "declined", Message: "not applied: nothing was written"}
		}
	}

	changes := changesFromPlan(p, in.staged)
	body := api.ApplyChangesetJSONRequestBody{Changes: changes}
	if o.recoveryOut != "" {
		body.Recovery = ptr(true)
	}
	res, err := r.api.ApplyChangesetWithResponse(ctx, body)
	if err != nil {
		why := transportFailure(ctx, "applying", err)
		return failf("outcome_unknown", "%s. The request was sent and no answer came back, so some changes may "+
			"have been written: plan again to see the directory as it is now", why.Message)
	}
	if res.StatusCode() != http.StatusOK {
		return r.refusal(ctx, "applying the plan", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return failf("outcome_unknown", "Alder's answer to the apply is not a result; plan again to see the directory as it is now")
	}
	envelope.Result = compactJSON(res.Body)
	result := *res.JSON200
	renderApplyResult(out, result, len(changes))
	if o.recoveryOut != "" {
		// Written before the exit status is decided, so a run that stopped
		// partway still leaves the bundle for what it did apply.
		if result.AppliedCount == 0 {
			writef(env.Stderr, "alder: no recovery bundle was written: nothing was applied\n")
		} else if werr := writeRecoveryBundle(o.recoveryOut, o.force, res.Body); werr != nil {
			var exit *ExitError
			message := werr.Error()
			if errors.As(werr, &exit) {
				message = exit.Message
			}
			return failf("recovery_not_written", "%s applied, but the recovery bundle was not written: %s",
				plural(result.AppliedCount, "change was", "changes were"), message)
		} else {
			envelope.RecoveryFile = o.recoveryOut
			writef(env.Stderr, "alder: recovery bundle for %s written to %s\n",
				plural(result.AppliedCount, "applied change", "applied changes"), safe(o.recoveryOut))
		}
	}
	if result.FailedIndex == nil {
		return nil
	}
	failed := *result.FailedIndex
	why, dnText := "", ""
	if failed >= 0 && failed < len(result.Outcomes) {
		dnText = result.Outcomes[failed].Dn
		if e := result.Outcomes[failed].Error; e != nil {
			why = fmt.Sprintf(" [%s] %s", e.Error, e.Message)
		}
	}
	if result.AppliedCount > 0 {
		return &ExitError{Code: ExitPartial, Local: "partially_applied",
			Message: fmt.Sprintf("stopped at change %d (%s) after applying %d of %d:%s. The first %d are in the directory",
				failed+1, safe(dnText), result.AppliedCount, len(changes), safe(why), result.AppliedCount)}
	}
	return &ExitError{Code: ExitFailed, Local: "apply_refused",
		Message: fmt.Sprintf("change %d (%s) was refused and nothing was written:%s", failed+1, safe(dnText), safe(why))}
}

// changesFromPlan is what applying a reviewed plan sends. It is the web
// interface's rule (web/src/lib/plan.ts), and must stay the same rule:
//
// Items that apply nothing are dropped. An exact change is sent from the
// client's own copy, because the plan withholds sensitive values; a
// desired-state change may have been rewritten by the planner, so the plan's
// record is sent. Each carries the baseline the plan issued, which binds the
// operation and the directory state it was planned against.
func changesFromPlan(p api.Plan, staged []api.ChangeRequest) []api.ChangeRequest {
	out := []api.ChangeRequest{}
	for _, item := range p.Items {
		if item.Record == nil || item.Baseline == nil {
			continue
		}
		exact := item.Intent == nil || *item.Intent != api.PlanIntentDesired
		var change api.ChangeRequest
		if exact && item.Index >= 0 && item.Index < len(staged) {
			change = staged[item.Index]
		} else {
			change = *item.Record
		}
		baseline := *item.Baseline
		change.Baseline = &baseline
		out = append(out, change)
	}
	return out
}

// confirm asks a yes-or-no question on standard error. Anything but yes is no,
// including end of input.
func confirm(env *Env, question string) bool {
	writef(env.Stderr, "%s [y/N] ", question)
	if err := env.claimStdin("the confirmation"); err != nil {
		writeln(env.Stderr)
		return false
	}
	line, err := bufio.NewReader(env.Stdin).ReadString('\n')
	if err != nil && line == "" {
		writeln(env.Stderr)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
