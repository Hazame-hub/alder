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

	// apply only
	yes         bool
	recoveryOut string
	force       bool
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
			"environment names where its password comes from and never the password.\n\n" +
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
			"--env is read from the command line only, never from ALDER_ENV: which\n" +
			"directory a command acts on is not something an inherited environment\n" +
			"decides.\n\n" +
			"--detailed-exitcode answers \"does this environment match the project?\" in\n" +
			"the exit status: 0 nothing to change, 2 changes pending, 3 some change\n" +
			"cannot be applied as written (it wins over 2). Without it, 0 means planned.\n\n" +
			"Exit status: 0 planned, 3 the project is not valid or some change cannot be\n" +
			"applied as written, 7 usage, 8 failure.",
		Args: argsBetween(0, 0, "no arguments; name the project with --project and the environment with --env"),
	}, func(ctx context.Context, _ []string) error {
		return runProjectPlan(ctx, env, o)
	})
	o.registerPath(cmd)
	f := cmd.Flags()
	f.StringVar(&o.envName, "env", "", "the environment, as alder.yaml names it (required)")
	f.BoolVar(&o.json, "json", false, "write JSON to standard output")
	f.BoolVar(&o.summary, "summary", false, "print the counts and not each change")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "give up on the whole command after this long; 0 waits indefinitely")
	envflags.Exclude(f, "project", "env", "json", "summary")
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
			"--env, --project and --yes are read from the command line only, never from\n" +
			"ALDER_*.\n\n" +
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
	f.BoolVar(&o.json, "json", false, "write JSON to standard output")
	f.BoolVar(&o.summary, "summary", false, "print the counts and not each change")
	f.DurationVar(&o.timeout, "timeout", 10*time.Minute, "give up on the whole command after this long; 0 waits indefinitely")
	envflags.Exclude(f, "project", "env", "yes", "recovery-out", "force", "json", "summary")
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
	document, err := p.Document()
	if err != nil {
		return failf("project", "cannot render the project as LDIF: %v", err)
	}
	if len(document) > maxLDIFBytes {
		return failf("project_too_large", "the project renders to %d bytes of LDIF, more than one plan request carries (%d)",
			len(document), maxLDIFBytes)
	}

	writef(env.Stderr, "alder: applying %s to environment %s: %s through %s\n",
		safe(p.File), safe(o.envName), safe(conn.label()), safe(conn.apiURL))

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	mode := apiclient.PlanLdifModeDesired
	plan, raw, err := requestPlan(ctx, r, apiclient.PlanRequest{Ldif: &document, Mode: &mode, Reconcile: ptr(false)})
	if err != nil {
		return err
	}
	out := env.Stdout
	if o.json {
		out = env.Stderr
	}
	writef(out, "Environment %s (%s)\n\n", safe(o.envName), safe(conn.label()))
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
	Environment string          `json:"environment"`
	Host        string          `json:"host"`
	Plan        json.RawMessage `json:"plan"`
	// Unmanaged is, for each managed subtree, what the directory holds there
	// that no project file describes. Reported, never planned.
	Unmanaged []unmanagedSubtree `json:"unmanaged"`
}

func runProjectPlan(ctx context.Context, env *Env, o projectOptions) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	if o.envName == "" {
		return usagef("--env is required: the environment to plan against, as alder.yaml names it")
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
	document, err := p.Document()
	if err != nil {
		return failf("project", "cannot render the project as LDIF: %v", err)
	}
	if len(document) > maxLDIFBytes {
		return failf("project_too_large", "the project renders to %d bytes of LDIF, more than one plan request carries (%d)",
			len(document), maxLDIFBytes)
	}

	// Said before anything is sent, so a person sees which directory this is
	// about before reading a word of the plan.
	writef(env.Stderr, "alder: planning %s for environment %s: %s through %s\n",
		safe(p.File), safe(o.envName), safe(conn.label()), safe(conn.apiURL))

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	mode := apiclient.PlanLdifModeDesired
	plan, raw, err := requestPlan(ctx, r, apiclient.PlanRequest{Ldif: &document, Mode: &mode, Reconcile: ptr(false)})
	if err != nil {
		return err
	}
	unmanaged := listUnmanaged(ctx, r, p)
	if o.json {
		reportUnreadable(env.Stderr, unmanaged)
		doc, marshalErr := json.Marshal(projectPlanDocument{Environment: o.envName, Host: conn.label(), Plan: raw, Unmanaged: unmanaged})
		if marshalErr != nil {
			return failf("output", "cannot encode the plan: %v", marshalErr)
		}
		if err := writeDocument(env.Stdout, doc); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
	} else {
		writef(env.Stdout, "Environment %s (%s)\n\n", safe(o.envName), safe(conn.label()))
		renderPlan(env.Stdout, plan, !o.summary)
		renderUnmanaged(env.Stdout, env.Stderr, unmanaged, !o.summary)
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
