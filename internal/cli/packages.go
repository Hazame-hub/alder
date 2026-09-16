package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/envflags"
)

// Change packages on the command line.
//
// Three verbs, because a package only does three things before it becomes an
// ordinary plan: it is created from changes, read on its own, and checked
// against a directory. Planning and applying one are inputs to the commands
// that already do that -- alder plan --package, alder apply --package -- so a
// package goes through exactly the same review as everything else, and there is
// no way to apply one without seeing a plan first.

type packageOptions struct {
	changes      string
	title        string
	description  string
	assume       []string
	recordSource bool
	output       string
	force        bool
	json         bool
	schemaTarget string
}

func packageCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "package",
		Short: "Create, read and validate change packages",
		Long: "A change package is a portable description of intended directory and schema\n" +
			"changes: what to change, not the operations one environment produced. The same\n" +
			"package is carried to each target, validated against what is there now, and\n" +
			"planned separately in each.\n\n" +
			"A package never holds a plan token, a baseline, a credential or a password, and\n" +
			"it is never applied directly: alder plan --package and alder apply --package\n" +
			"validate it, plan the changes that are ready, and apply exactly that plan.",
	}
	cmd.AddCommand(packageCreateCmd(env), packageInspectCmd(env), packageValidateCmd(env))
	return cmd
}

func packageCreateCmd(env *Env) *cobra.Command {
	var conn connection
	var o packageOptions
	cmd := command(env, &cobra.Command{
		Use:   "create --changes FILE --output FILE",
		Short: "Turn change requests into a change package",
		Long: "Reads a JSON array of change requests -- what alder diff --changes-out writes,\n" +
			"or what the web interface exports from a changeset -- and writes a change\n" +
			"package.\n\n" +
			"A change to this server's schema entry is recorded as the schema intent it\n" +
			"expresses, so it can be carried to a server that keeps its schema elsewhere.\n" +
			"A baseline or an expectation on a change is dropped: both describe the\n" +
			"environment the change was planned in. A password change is refused and\n" +
			"recorded as an omission with its reason, because a package never carries a\n" +
			"secret.\n\n" +
			"Dependencies are worked out from the changes themselves and written into the\n" +
			"package, so the order they arrived in stops mattering.",
		Args: argsBetween(0, 0, "no arguments"),
	}, func(ctx context.Context, _ []string) error {
		return runPackageCreate(ctx, env, &conn, o)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	f := cmd.Flags()
	f.StringVar(&o.changes, "changes", "", "a JSON array of change requests, or - for standard input")
	f.StringVar(&o.title, "title", "", "a short name for the package")
	f.StringVar(&o.description, "description", "", "what this package is for")
	f.StringArrayVar(&o.assume, "assume-naming-context", nil,
		"a naming context the target must hold for this package to make sense (repeatable)")
	f.BoolVar(&o.recordSource, "record-source", false,
		"record this directory's vendor and naming contexts in the package as provenance")
	f.StringVarP(&o.output, "output", "o", "", "the file to write, or - for standard output")
	f.BoolVar(&o.force, "force", false, "replace --output if it already exists")
	envflags.Exclude(f, "changes", "title", "description", "assume-naming-context", "record-source", "output", "force")
	return cmd
}

func packageInspectCmd(env *Env) *cobra.Command {
	var conn connection
	var o packageOptions
	cmd := command(env, &cobra.Command{
		Use:   "inspect PACKAGE_FILE | -",
		Short: "Read a change package and check everything that needs no directory",
		Long: "Reads a package and reports what it holds: its identity, where it came from,\n" +
			"what it assumes of a target, its changes and the order they must be applied in.\n\n" +
			"Everything that can be checked without a directory is checked: the format and\n" +
			"version, unknown fields, duplicate identifiers, missing dependencies, cycles,\n" +
			"malformed DNs, schema definitions that do not parse, secrets, and the checksum.\n" +
			"No directory session is needed, so this is the check a pipeline runs before it\n" +
			"has credentials.\n\n" +
			"Exit status: 0 the package is readable, 8 it is not.",
		Args: argsBetween(1, 1, "one package file, or - for standard input"),
	}, func(ctx context.Context, args []string) error {
		return runPackageInspect(ctx, env, &conn, o, args)
	})
	conn.registerAPI(cmd)
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "write Alder's answer as JSON to standard output")
	envflags.Exclude(f, "json")
	return cmd
}

func packageValidateCmd(env *Env) *cobra.Command {
	var conn connection
	var o packageOptions
	cmd := command(env, &cobra.Command{
		Use:   "validate PACKAGE_FILE | -",
		Short: "Check a change package against the directory as it is now",
		Long: "Asks the directory what this package's intent would mean here: which changes\n" +
			"are ready, which it already satisfies, which conflict with what is there now,\n" +
			"which name something missing, and which this server cannot do at all.\n\n" +
			"Validation is not a plan and is not kept. The plan reads the directory again\n" +
			"and remains the only thing that decides what is written: alder plan --package\n" +
			"and alder apply --package do that.\n\n" +
			"Exit status: 0 the package is applicable here or has nothing left to do,\n" +
			"2 something could not be decided, 3 some change cannot be applied here,\n" +
			"7 usage, 8 failure.",
		Args: argsBetween(1, 1, "one package file, or - for standard input"),
	}, func(ctx context.Context, args []string) error {
		return runPackageValidate(ctx, env, &conn, o, args)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "write Alder's answer as JSON to standard output")
	f.StringVar(&o.schemaTarget, "schema-target", "",
		"the schema entry an added definition is written to, where the server keeps schema in several")
	envflags.Exclude(f, "json", "schema-target")
	return cmd
}

func runPackageCreate(ctx context.Context, env *Env, conn *connection, o packageOptions) error {
	if o.changes == "" {
		return usagef("--changes is required: a JSON array of change requests, or - for standard input")
	}
	if o.output == "" {
		return usagef("--output is required: a file to write, or - for standard output")
	}
	if err := conn.check(o.changes == "-"); err != nil {
		return err
	}
	if err := refuseExisting(o.output, o.force); err != nil {
		return err
	}
	data, name, err := env.readInput(o.changes, "the change requests", maxRequestBytes)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var changes []api.ChangeRequest
	if err := dec.Decode(&changes); err != nil {
		return failf("changes_invalid", "%s is not a JSON array of change requests: %v", name, err)
	}
	if _, err := dec.Token(); err == nil {
		return failf("changes_invalid", "%s holds more than one JSON document", name)
	}
	if len(changes) == 0 {
		return failf("changes_invalid", "%s holds no change requests", name)
	}

	req := api.PackageBuildRequest{Changes: changes}
	if o.title != "" {
		req.Title = &o.title
	}
	if o.description != "" {
		req.Description = &o.description
	}
	if len(o.assume) > 0 {
		req.Assumptions = &api.PackageAssumptions{NamingContexts: &o.assume}
	}
	if o.recordSource {
		req.RecordSource = ptr(true)
	}
	method := api.PackageBuildCLI
	req.Method = &method

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	res, err := r.api.BuildPackage(ctx, req)
	if err != nil {
		return transportFailure(ctx, "building the package", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return r.refusal(ctx, "building the package", res, body)
	}
	document, err := io.ReadAll(io.LimitReader(res.Body, maxRequestBytes))
	if err != nil {
		return copyFailure(ctx, o.output, err)
	}

	if o.output == "-" {
		return writeDocument(env.Stdout, document)
	}
	if err := writeFile(o.output, o.force, func(w io.Writer) error {
		_, werr := w.Write(document)
		return werr
	}, nil); err != nil {
		return err
	}
	var head packageHead
	if err := json.Unmarshal(document, &head); err != nil {
		return failf("unexpected_response", "Alder answered with something that is not a package")
	}
	writef(env.Stderr, "Wrote %s: %s, %s.\n", o.output, safe(head.ID), packageSummary(head))
	for _, omitted := range head.Omitted {
		writef(env.Stderr, "  left out: %s %s (%s)\n", safe(omitted.Kind), safe(omitted.Subject), safe(omitted.Reason))
	}
	return nil
}

// packageHead is what the client reads out of a package for its own messages.
// Alder is what validates one.
type packageHead struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Counts struct {
		Changes     int `json:"changes"`
		Data        int `json:"data"`
		Schema      int `json:"schema"`
		Destructive int `json:"destructive"`
		Omitted     int `json:"omitted"`
	} `json:"counts"`
	Omitted []struct {
		Subject string `json:"subject"`
		Kind    string `json:"kind"`
		Reason  string `json:"reason"`
	} `json:"omitted"`
}

func packageSummary(head packageHead) string {
	parts := []string{plural(head.Counts.Changes, "change", "changes")}
	parts = append(parts, fmt.Sprintf("%d schema", head.Counts.Schema), fmt.Sprintf("%d data", head.Counts.Data))
	if head.Counts.Destructive > 0 {
		parts = append(parts, fmt.Sprintf("%d destructive", head.Counts.Destructive))
	}
	if head.Counts.Omitted > 0 {
		parts = append(parts, fmt.Sprintf("%d left out", head.Counts.Omitted))
	}
	return strings.Join(parts, ", ")
}

func runPackageInspect(ctx context.Context, env *Env, conn *connection, o packageOptions, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	if err := conn.checkAPI(); err != nil {
		return err
	}
	document, err := readPackage(env, args[0])
	if err != nil {
		return err
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.client(env)
	if err != nil {
		return err
	}
	defer r.close()

	res, err := r.api.InspectPackageWithBodyWithResponse(ctx, "application/json", bytes.NewReader(document))
	if err != nil {
		return transportFailure(ctx, "reading the package", err)
	}
	if res.StatusCode() != http.StatusOK {
		return r.refusal(ctx, "the package", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return failf("unexpected_response", "Alder answered with something that is not a package")
	}
	if o.json {
		return writeDocument(env.Stdout, res.Body)
	}
	renderPackageInspection(env.Stdout, *res.JSON200)
	return nil
}

func runPackageValidate(ctx context.Context, env *Env, conn *connection, o packageOptions, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	if err := conn.check(args[0] == "-"); err != nil {
		return err
	}
	document, err := readPackage(env, args[0])
	if err != nil {
		return err
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	validation, raw, err := validatePackage(ctx, r, document, o.schemaTarget)
	if err != nil {
		return err
	}
	if o.json {
		if err := writeDocument(env.Stdout, raw); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
	} else {
		renderPackageValidation(env.Stdout, validation)
	}
	return validationOutcome(validation)
}

// readPackage reads a package file, refusing what is not one JSON object
// before it is sent, exactly as a snapshot is.
func readPackage(env *Env, path string) ([]byte, error) {
	data, name, err := env.readInput(path, "the change package", maxRequestBytes)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, failf("package_not_json", "%s is not a JSON document, so it is not an Alder change package", name)
	}
	return trimmed, nil
}

// validatePackage sends the package as the bytes it is, so the server decides
// whether it is a valid package rather than this client.
func validatePackage(ctx context.Context, r *remote, document []byte, schemaTarget string) (api.PackageValidation, []byte, error) {
	var body bytes.Buffer
	body.WriteString(`{"package":`)
	body.Write(document)
	if schemaTarget != "" {
		target, err := json.Marshal(schemaTarget)
		if err != nil {
			return api.PackageValidation{}, nil, failf("output", "cannot encode --schema-target: %v", err)
		}
		body.WriteString(`,"schemaTarget":`)
		body.Write(target)
	}
	body.WriteString("}")

	res, err := r.api.ValidatePackageWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body.Bytes()))
	if err != nil {
		return api.PackageValidation{}, nil, transportFailure(ctx, "validating the package", err)
	}
	if res.StatusCode() != http.StatusOK {
		return api.PackageValidation{}, nil, r.refusal(ctx, "the package", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return api.PackageValidation{}, nil, failf("unexpected_response", "Alder answered with something that is not a validation")
	}
	return *res.JSON200, res.Body, nil
}

// validationOutcome is the exit status of a finished validation. It reuses the
// client's own codes: something that cannot be applied is "not applicable", and
// something that could not be decided is "incomplete", as everywhere else.
func validationOutcome(v api.PackageValidation) error {
	c := v.Counts
	if c.Unknown > 0 {
		return &ExitError{Code: ExitIncomplete}
	}
	if c.Conflict+c.DependencyMissing+c.Unsupported+c.TargetIncompatible > 0 {
		return &ExitError{Code: ExitNotApplicable}
	}
	return nil
}

// readyChanges are the prepared changes of the ready items, in the order the
// validation gave.
func readyChanges(v api.PackageValidation) []api.ChangeRequest {
	byID := map[string]api.PackageValidationItem{}
	for _, item := range v.Items {
		byID[item.Id] = item
	}
	var out []api.ChangeRequest
	for _, id := range v.Order {
		item, ok := byID[id]
		if !ok || item.Status != api.PackageStatusReady || item.Changes == nil {
			continue
		}
		out = append(out, *item.Changes...)
	}
	return out
}
