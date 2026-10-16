package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/envflags"
	"github.com/hazame-hub/alder/internal/project"
)

func projectCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Check, plan, apply and format an Alder project: a directory's intended state, kept in a repository",
		Long: "An Alder project is an alder.yaml naming the subtrees a repository is\n" +
			"responsible for, the LDIF files that describe them, and the environments the\n" +
			"project is planned against. See docs/PROJECT.md.\n\n" +
			"A project describes state. An entry its files do not mention is never\n" +
			"deleted, and nothing about a project is stored anywhere: every plan is made\n" +
			"against the directory as it is.",
	}
	cmd.AddCommand(projectValidateCmd(env), projectPlanCmd(env), projectApplyCmd(env), projectFmtCmd(env))
	return cmd
}

type projectOptions struct {
	path             string
	envName          string
	json             bool
	summary          bool
	detailedExitcode bool
	timeout          time.Duration

	// plan only
	out string

	// apply only
	yes         bool
	recoveryOut string
	plan        string

	// force replaces an existing --out (plan) or --recovery-out (apply).
	force bool
}

func (o *projectOptions) registerPath(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.path, "project", "",
		"the project: its directory or its alder.yaml (default: the current directory)")
}

func projectValidateCmd(env *Env) *cobra.Command {
	var o projectOptions
	cmd := command(env, &cobra.Command{
		Use:   "validate",
		Short: "Check a project without connecting to anything",
		Long: "Reads alder.yaml and every file it names, and checks them: the file is\n" +
			"version 1 with no unknown keys, every glob matches, every file is LDIF of\n" +
			"content records, every entry lies in its managed subtree, subtrees do not\n" +
			"overlap, no entry appears twice, no file carries a secret, and every\n" +
			"environment names where its password comes from and never the password.\n" +
			"With a suffix, every managed base lies below it, and an environment's own\n" +
			"suffix is a DN that replaces it; each such environment is listed.\n\n" +
			"Every problem is listed, not only the first. The schema is not checked: it\n" +
			"belongs to a directory, which is what alder project plan is for.\n\n" +
			"Exit status: 0 valid, 3 not valid, 7 usage, 8 a file could not be read.",
		Args: argsBetween(0, 0, "no arguments; name the project with --project"),
	}, func(_ context.Context, _ []string) error {
		p, err := loadProject(env, o.path)
		if err != nil {
			return err
		}
		writef(env.Stdout, "%s: valid. %s in %s; environments: %s\n",
			safe(p.File), plural(p.Entries(), "entry", "entries"),
			plural(len(p.Managed), "managed subtree", "managed subtrees"),
			safe(strings.Join(p.EnvironmentNames(), ", ")))
		// Said here too, so a review of the project sees which environments
		// will not hold the names the files are written with.
		for _, name := range p.EnvironmentNames() {
			if from, to, ok := p.Rebasing(name); ok {
				writef(env.Stdout, "  environment %s: suffix %s -> %s\n", safe(name), safe(from.String()), safe(to.String()))
			}
		}
		return nil
	})
	o.registerPath(cmd)
	envflags.Exclude(cmd.Flags(), "project")
	return cmd
}

func projectPlanCmd(env *Env) *cobra.Command {
	var o projectOptions
	cmd := command(env, &cobra.Command{
		Use:   "plan --env NAME",
		Short: "Show what it would take to make an environment match the project",
		Long: "Validates the project, connects to the named environment, and plans the\n" +
			"project's entries as desired state: what each would need to become as the\n" +
			"project describes it. It is the plan alder plan --mode desired makes, and\n" +
			"nothing is written.\n\n" +
			"Every environment manages the same entries under the same names, so the\n" +
			"environment and the directory it was planned against are printed above the\n" +
			"plan, and given beside it with --json.\n\n" +
			"An environment that names a suffix of its own holds the entries under it:\n" +
			"every entry name, managed base and value of a DN-syntax attribute type\n" +
			"below the project's suffix is moved onto the environment's, as the\n" +
			"directory's schema says which types those are. The plan then shows those\n" +
			"names, and the line above it says \"Suffix FROM -> TO\" (rebase, with --json).\n\n" +
			"--env is read from the command line only, never from ALDER_ENV: which\n" +
			"directory a command acts on is not something an inherited environment\n" +
			"decides.\n\n" +
			"--detailed-exitcode answers \"does this environment match the project?\" in\n" +
			"the exit status: 0 nothing to change, 2 changes pending, 3 some change\n" +
			"cannot be applied as written (it wins over 2). Without it, 0 means planned.\n\n" +
			"--out FILE saves the plan, so that alder project apply --plan FILE applies\n" +
			"exactly it later, or refuses. Each change is saved with the state it expects\n" +
			"to find, and the file is written only once the saved changes have been\n" +
			"planned again and every one applies. A plan with a change that cannot be\n" +
			"applied as written is not saved. The exit status is the plan's.\n\n" +
			"Exit status: 0 planned, 3 the project is not valid or some change cannot be\n" +
			"applied as written, 4 the directory changed while the plan was being saved,\n" +
			"7 usage, 8 failure.",
		Args: argsBetween(0, 0, "no arguments; name the project with --project and the environment with --env"),
	}, func(ctx context.Context, _ []string) error {
		return runProjectPlan(ctx, env, o)
	})
	o.registerPath(cmd)
	f := cmd.Flags()
	f.StringVar(&o.envName, "env", "", "the environment, as alder.yaml names it (required)")
	f.BoolVar(&o.json, "json", false, "write JSON to standard output")
	f.BoolVar(&o.summary, "summary", false, "print the counts and not each change")
	f.StringVar(&o.out, "out", "", "save the plan to this file, for alder project apply --plan")
	f.BoolVar(&o.force, "force", false, "replace --out if it already exists")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "give up on the whole command after this long; 0 waits indefinitely")
	envflags.Exclude(f, "project", "env", "json", "summary", "out", "force")
	registerDetailedExitcode(cmd, &o.detailedExitcode)
	return cmd
}

func projectApplyCmd(env *Env) *cobra.Command {
	var o projectOptions
	cmd := command(env, &cobra.Command{
		Use:   "apply --env NAME",
		Short: "Make an environment match the project: plan, show, confirm, apply",
		Long: "Validates the project, connects to the named environment, plans the project\n" +
			"as desired state exactly as alder project plan does, shows the plan under the\n" +
			"environment and the directory it was made against, and asks before applying\n" +
			"it. What is applied is the plan that was shown, with the baselines Alder\n" +
			"issued for it: if the directory has moved since, Alder refuses the whole set,\n" +
			"nothing is written, and the plan is never made again and applied unseen.\n\n" +
			"The plan is made and applied in one run, against one environment, so a plan\n" +
			"made for another environment cannot reach it. The question names the\n" +
			"environment and the directory. Anywhere a terminal cannot be asked, nothing is\n" +
			"applied without --yes. A project never deletes an entry it does not mention.\n\n" +
			"--recovery-out FILE asks Alder for a recovery bundle for what was applied.\n\n" +
			"--plan FILE applies a plan alder project plan --out saved, instead of\n" +
			"planning the project: exactly the changes in it, each only if the directory\n" +
			"still holds what it held when the plan was made. The file must have been\n" +
			"made for this environment, its directory and binds, and from the project\n" +
			"files as they are now; anything else is refused before connecting (3). A\n" +
			"saved plan that no longer holds is refused whole (4): plan again and review.\n\n" +
			"--env, --project, --plan and --yes are read from the command line only,\n" +
			"never from ALDER_*.\n\n" +
			"Exit status: 0 applied (or nothing to apply), 3 the project is not valid or\n" +
			"some change cannot be applied as written, 4 the plan went stale, 5 not\n" +
			"confirmed, 6 stopped partway, 7 usage, 8 failure.",
		Args: argsBetween(0, 0, "no arguments; name the project with --project and the environment with --env"),
	}, func(ctx context.Context, _ []string) error {
		return runProjectApply(ctx, env, o)
	})
	o.registerPath(cmd)
	f := cmd.Flags()
	f.StringVar(&o.envName, "env", "", "the environment, as alder.yaml names it (required)")
	f.BoolVar(&o.yes, "yes", false, "apply without asking; required when no terminal can be asked")
	f.StringVar(&o.recoveryOut, "recovery-out", "", "write a recovery bundle for the changes that were applied to this file")
	f.BoolVar(&o.force, "force", false, "replace --recovery-out if it already exists")
	f.StringVar(&o.plan, "plan", "", "apply this saved plan (from alder project plan --out) instead of planning the project")
	f.BoolVar(&o.json, "json", false, "write JSON to standard output")
	f.BoolVar(&o.summary, "summary", false, "print the counts and not each change")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "give up on the whole command after this long; 0 waits indefinitely")
	envflags.Exclude(f, "project", "env", "yes", "recovery-out", "force", "plan", "json", "summary")
	return cmd
}

func runProjectApply(ctx context.Context, env *Env, o projectOptions) (err error) {
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

	switch {
	case o.envName == "":
		return usagef("--env is required: the environment to apply to, as alder.yaml names it")
	case o.recoveryOut == "-":
		return usagef("--recovery-out needs a file: standard output carries the plan and the result")
	case o.force && o.recoveryOut == "":
		return usagef("--force replaces the --recovery-out file, and none was given")
	case o.plan == "-":
		return usagef("--plan needs a file: standard input may be needed to confirm")
	}
	if err := refuseExisting(o.recoveryOut, o.force); err != nil {
		return err
	}
	p, err := loadProject(env, o.path)
	if err != nil {
		return err
	}
	e, ok := p.Environments[o.envName]
	if !ok {
		return usagef("%s has no environment %q; it has %s", safe(p.File), o.envName, safe(strings.Join(p.EnvironmentNames(), ", ")))
	}
	conn := connectionFor(p, e, o.timeout)
	if err := conn.check(false); err != nil {
		return err
	}
	envelope.Environment, envelope.Host = o.envName, conn.label()
	// Refused before connecting: a run that will not be allowed to apply has
	// no business opening a session on production to find that out.
	if !o.yes && !env.Interactive() {
		return &ExitError{Code: ExitNotConfirmed, Local: "confirmation_required",
			Message: "not applied: there is no terminal to ask for confirmation, and --yes was not given. " +
				"Review with alder project plan, then run apply with --yes"}
	}
	if o.plan != "" {
		return applySaved(ctx, env, p, o, conn, &envelope)
	}
	// Rendered before connecting, so a project too large to plan is said
	// without a session; an environment that rebases it renders it again.
	if _, err := projectDocument(p); err != nil {
		return err
	}

	writef(env.Stderr, "alder: applying %s to environment %s: %s through %s\n",
		safe(p.File), safe(o.envName), safe(conn.label()), safe(conn.apiURL))
	announceRebase(env.Stderr, p, o.envName)

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	p, rebase, err := forEnvironment(ctx, r, p, o.envName)
	if err != nil {
		return err
	}
	envelope.Rebase = rebase
	document, err := projectDocument(p)
	if err != nil {
		return err
	}

	mode := apiclient.PlanLdifModeDesired
	plan, raw, err := requestPlan(ctx, r, apiclient.PlanRequest{Ldif: &document, Mode: &mode, Reconcile: ptr(false)})
	if err != nil {
		return err
	}
	out := env.Stdout
	if o.json {
		out = env.Stderr
	}
	renderEnvironment(out, env.Stderr, o.envName, conn.label(), rebase)
	// Desired state only: every item is the plan's own record, so there is
	// no client copy of an exact change to send instead.
	return applyReviewed(ctx, env, r, plan, raw, nil, applySettings{
		target:      fmt.Sprintf("environment %s (%s)", safe(o.envName), safe(conn.label())),
		summary:     o.summary,
		yes:         o.yes,
		recoveryOut: o.recoveryOut,
		force:       o.force,
	}, out, &envelope)
}

// projectPlanDocument is project plan's JSON: the plan, with the environment
// and directory it was made for beside it, because two environments' plans
// otherwise read identically.
type projectPlanDocument struct {
	Environment string `json:"environment"`
	Host        string `json:"host"`
	// Rebase is present when the environment names a suffix of its own:
	// the plan is then under its names, not the files' own.
	Rebase *rebaseReport   `json:"rebase,omitempty"`
	Plan   json.RawMessage `json:"plan"`
	// Unmanaged is, for each managed subtree, what the directory holds there
	// that no project file describes. Reported, never planned.
	Unmanaged []unmanagedSubtree `json:"unmanaged"`
	// Saved is the file the plan was saved to, with --out.
	Saved string `json:"saved,omitempty"`
}

func runProjectPlan(ctx context.Context, env *Env, o projectOptions) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	switch {
	case o.envName == "":
		return usagef("--env is required: the environment to plan against, as alder.yaml names it")
	case o.out == "-":
		return usagef("--out needs a file: standard output carries the plan")
	case o.force && o.out == "":
		return usagef("--force replaces the --out file, and none was given")
	}
	if err := refuseExisting(o.out, o.force); err != nil {
		return err
	}
	p, err := loadProject(env, o.path)
	if err != nil {
		return err
	}
	e, ok := p.Environments[o.envName]
	if !ok {
		return usagef("%s has no environment %q; it has %s", safe(p.File), o.envName, safe(strings.Join(p.EnvironmentNames(), ", ")))
	}
	conn := connectionFor(p, e, o.timeout)
	if err := conn.check(false); err != nil {
		return err
	}
	// Rendered before connecting, so a project too large to plan is said
	// without a session; an environment that rebases it renders it again.
	if _, err := projectDocument(p); err != nil {
		return err
	}

	// Said before anything is sent, so a person sees which directory this is
	// about before reading a word of the plan.
	writef(env.Stderr, "alder: planning %s for environment %s: %s through %s\n",
		safe(p.File), safe(o.envName), safe(conn.label()), safe(conn.apiURL))
	announceRebase(env.Stderr, p, o.envName)

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	// From here on the project is the one this environment holds: the plan,
	// and the unmanaged entries beside it, are under its names. A saved plan
	// binds the files as they are written, which is what an apply recomputes
	// before connecting.
	files := p
	p, rebase, err := forEnvironment(ctx, r, p, o.envName)
	if err != nil {
		return err
	}
	document, err := projectDocument(p)
	if err != nil {
		return err
	}

	mode := apiclient.PlanLdifModeDesired
	plan, raw, err := requestPlan(ctx, r, apiclient.PlanRequest{Ldif: &document, Mode: &mode, Reconcile: ptr(false)})
	if err != nil {
		return err
	}
	unmanaged := listUnmanaged(ctx, r, p)
	// A plan with a change that cannot be applied as written is shown and
	// not saved: there is nothing an apply could do with it. Otherwise, with
	// --json, the plan is saved before the document is written, so that
	// standard output holds exactly one document: the plan, saying where it
	// was saved, or why it was not.
	saving := o.out != "" && blocked(plan) == 0
	saved := ""
	if saving && o.json {
		if err := savePlan(ctx, env, r, files, o.envName, conn, plan, o.out, o.force); err != nil {
			return err
		}
		saved = o.out
	}
	if o.json {
		if rebase != nil {
			reportLeftAsWritten(env.Stderr, rebase)
		}
		reportUnreadable(env.Stderr, unmanaged)
		doc, marshalErr := json.Marshal(projectPlanDocument{Environment: o.envName, Host: conn.label(), Rebase: rebase,
			Plan: raw, Unmanaged: unmanaged, Saved: saved})
		if marshalErr != nil {
			return failf("output", "cannot encode the plan: %v", marshalErr)
		}
		if err := writeDocument(env.Stdout, doc); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
	} else {
		renderEnvironment(env.Stdout, env.Stderr, o.envName, conn.label(), rebase)
		renderPlan(env.Stdout, plan, !o.summary)
		renderUnmanaged(env.Stdout, env.Stderr, unmanaged, !o.summary)
	}
	switch {
	case saving && !o.json:
		if err := savePlan(ctx, env, r, files, o.envName, conn, plan, o.out, o.force); err != nil {
			return err
		}
	case o.out != "" && !saving:
		writef(env.Stderr, "alder: the plan was not saved to %s: a change that cannot be applied as written is never saved\n",
			safe(o.out))
	}
	return planExit(env, plan, o.detailedExitcode)
}

// connectionFor turns an environment into the connection the other client
// commands build from their flags. Relative paths in alder.yaml are relative
// to the project, not to wherever the command was run.
func connectionFor(p *project.Project, e project.Environment, timeout time.Duration) *connection {
	tlsMode := e.TLS
	if tlsMode == "" {
		tlsMode = "ldaps"
	}
	return &connection{
		apiURL:    e.APIURL,
		apiCAFile: p.Path(e.APICAFile),
		timeout:   timeout,

		host:       e.Host,
		port:       e.Port,
		tlsMode:    tlsMode,
		caFile:     p.Path(e.CAFile),
		serverName: e.ServerName,

		bindDN:              e.BindDN,
		bindPasswordFile:    p.Path(e.BindPasswordFile),
		bindPasswordEnvName: e.BindPasswordEnv,

		configBindDN:              e.ConfigBindDN,
		configBindPasswordFile:    p.Path(e.ConfigBindPasswordFile),
		configBindPasswordEnvName: e.ConfigBindPasswordEnv,
	}
}

// loadProject finds, reads and checks a project, and turns what can go wrong
// into the client's exit statuses: a project that is not valid is 3, like a
// change that cannot be applied as written; one that cannot be read is 8.
func loadProject(env *Env, path string) (*project.Project, error) {
	file, err := project.Locate(path)
	if err != nil {
		return nil, failf("input", "cannot find the project: %s", safe(err.Error()))
	}
	p, err := project.Load(file)
	var invalid *project.Invalid
	switch {
	case errors.As(err, &invalid):
		// Every problem quotes the repository: a file name, a key, a DN, a
		// value. In CI that is whatever the pull request under review wrote,
		// and printed as it was, an escape sequence in it is acted on by the
		// terminal -- or by the CI log, where a line of its own can be a
		// workflow command. So the whole line goes through safe, newlines
		// included.
		for _, problem := range invalid.Problems {
			writef(env.Stderr, "%s\n", safe(problem.String()))
		}
		return nil, &ExitError{
			Code:    ExitNotApplicable,
			Local:   "project_invalid",
			Message: safe(invalid.Error()),
		}
	case err != nil:
		return nil, failf("input", "cannot read the project: %s", safe(err.Error()))
	}
	return p, nil
}

// applySaved is project apply --plan: the saved plan's changes, planned as
// they are written, shown, confirmed and applied with that plan's baselines.
func applySaved(ctx context.Context, env *Env, p *project.Project, o projectOptions, conn *connection,
	envelope *applyEnvelope) error {
	saved, err := readSavedPlan(env, o.plan)
	if err != nil {
		return err
	}
	if err := matchSaved(o.plan, saved, p, o.envName, conn); err != nil {
		return err
	}

	writef(env.Stderr, "alder: applying the plan saved in %s at %s to environment %s: %s through %s\n",
		safe(o.plan), saved.CreatedAt.Format(time.RFC3339), safe(o.envName), safe(conn.label()), safe(conn.apiURL))
	out := env.Stdout
	if o.json {
		out = env.Stderr
	}
	if len(saved.Changes) == 0 {
		renderEnvironment(out, env.Stderr, o.envName, conn.label(), nil)
		writeln(out, "Nothing to apply: the saved plan holds no change.")
		return nil
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	plan, raw, err := planSaved(ctx, r, saved.Changes)
	if err != nil {
		return err
	}
	renderEnvironment(out, env.Stderr, o.envName, conn.label(), nil)
	if why := holds(plan, len(saved.Changes)); why != nil {
		// Shown, so the changes that moved are named, and never applied:
		// what was reviewed is not what would happen.
		envelope.Plan = compactJSON(raw)
		renderPlan(out, plan, !o.summary)
		return &ExitError{Code: ExitStale, Local: "saved_plan_stale",
			Message: fmt.Sprintf("not applied: the plan saved in %s no longer holds: %s. Nothing was written; plan again and review",
				safe(o.plan), why)}
	}
	return applyReviewed(ctx, env, r, plan, raw, saved.Changes, applySettings{
		target:      fmt.Sprintf("environment %s (%s)", safe(o.envName), safe(conn.label())),
		summary:     o.summary,
		yes:         o.yes,
		recoveryOut: o.recoveryOut,
		force:       o.force,
	}, out, envelope)
}
