package cli

import (
	"context"
	"encoding/json"
	"errors"
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
		Short: "Check and plan an Alder project: a directory's intended state, kept in a repository",
		Long: "An Alder project is an alder.yaml naming the subtrees a repository is\n" +
			"responsible for, the LDIF files that describe them, and the environments the\n" +
			"project is planned against. See docs/PROJECT.md.\n\n" +
			"A project describes state. An entry its files do not mention is never\n" +
			"deleted, and nothing about a project is stored anywhere: every plan is made\n" +
			"against the directory as it is.",
	}
	cmd.AddCommand(projectValidateCmd(env), projectPlanCmd(env))
	return cmd
}

type projectOptions struct {
	path    string
	envName string
	json    bool
	summary bool
	timeout time.Duration
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
			p.File, plural(p.Entries(), "entry", "entries"),
			plural(len(p.Managed), "managed subtree", "managed subtrees"),
			strings.Join(p.EnvironmentNames(), ", "))
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
	return cmd
}

// projectPlanDocument is project plan's JSON: the plan, with the environment
// and directory it was made for beside it, because two environments' plans
// otherwise read identically.
type projectPlanDocument struct {
	Environment string          `json:"environment"`
	Host        string          `json:"host"`
	Plan        json.RawMessage `json:"plan"`
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
		return usagef("%s has no environment %q; it has %s", p.File, o.envName, strings.Join(p.EnvironmentNames(), ", "))
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
		p.File, o.envName, conn.label(), conn.apiURL)

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
	if o.json {
		doc, marshalErr := json.Marshal(projectPlanDocument{Environment: o.envName, Host: conn.label(), Plan: raw})
		if marshalErr != nil {
			return failf("output", "cannot encode the plan: %v", marshalErr)
		}
		if err := writeDocument(env.Stdout, doc); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
	} else {
		writef(env.Stdout, "Environment %s (%s)\n\n", o.envName, conn.label())
		renderPlan(env.Stdout, plan, !o.summary)
	}
	if n := blocked(plan); n > 0 {
		writef(env.Stderr, "alder: %d change(s) cannot be applied as written\n", n)
		return &ExitError{Code: ExitNotApplicable}
	}
	return nil
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
		return nil, failf("input", "cannot find the project: %v", err)
	}
	p, err := project.Load(file)
	var invalid *project.Invalid
	switch {
	case errors.As(err, &invalid):
		for _, problem := range invalid.Problems {
			writef(env.Stderr, "%s\n", problem)
		}
		return nil, &ExitError{
			Code:    ExitNotApplicable,
			Local:   "project_invalid",
			Message: invalid.Error(),
		}
	case err != nil:
		return nil, failf("input", "cannot read the project: %v", err)
	}
	return p, nil
}
