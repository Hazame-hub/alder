package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/envflags"
)

// alder preflight.
//
// One command for three artifacts, because the artifact says what it is: a
// change package, a schema snapshot or a data snapshot is recognised by the
// server from its own format and kind, never by this client and never by a
// file name. The client reads the file, sends it as it is, and prints the
// report. There is no compatibility logic here.

type preflightOptions struct {
	json         bool
	all          bool
	schemaTarget string
}

func preflightCmd(env *Env) *cobra.Command {
	var conn connection
	var o preflightOptions
	cmd := command(env, &cobra.Command{
		Use:   "preflight ARTIFACT_FILE | -",
		Short: "Report what of a package or snapshot would carry across to a directory",
		Long: "Reads a change package, a schema snapshot, a data snapshot or a\n" +
			"configuration snapshot against the directory and reports, finding by\n" +
			"finding, what it already holds, what it could take, what needs something\n" +
			"done first, what contradicts it, what it cannot represent, and what could\n" +
			"not be seen.\n\n" +
			"A preflight writes nothing, changes nothing in the artifact, rewrites no DN\n" +
			"and maps no attribute or object class. Access control is not evaluated.\n" +
			"Server configuration is evaluated only for a configuration snapshot, and\n" +
			"only against a directory of the same server software.\n\n" +
			"Exit status: 0 compatible, 1 compatible once the prerequisites it lists are\n" +
			"done, 2 incomplete (something could not be decided), 3 incompatible,\n" +
			"7 usage, 8 failure.",
		Args: argsBetween(1, 1, "one artifact file, or - for standard input"),
	}, func(ctx context.Context, args []string) error {
		return runPreflight(ctx, env, &conn, o, args)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "write the report as JSON to standard output")
	f.BoolVar(&o.all, "all", false, "list every finding, including what is portable or already satisfied")
	f.StringVar(&o.schemaTarget, "schema-target", "",
		"for a package, the schema entry an added definition would be written to, where the server keeps schema in several")
	envflags.Exclude(f, "json", "all", "schema-target")
	return cmd
}

func runPreflight(ctx context.Context, env *Env, conn *connection, o preflightOptions, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	if err := conn.check(args[0] == "-"); err != nil {
		return err
	}
	data, name, err := env.readInput(args[0], "the artifact", maxRequestBytes)
	if err != nil {
		return err
	}
	document := bytes.TrimSpace(data)
	if len(document) == 0 || document[0] != '{' || !json.Valid(document) {
		return failf("artifact_not_json", "%s is not a JSON document, so it is not an Alder change package or snapshot", name)
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	report, raw, err := preflightArtifact(ctx, r, document, o.schemaTarget)
	if err != nil {
		return err
	}
	if o.json {
		if err := writeDocument(env.Stdout, raw); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
	} else {
		renderPreflight(env.Stdout, report, o.all)
	}
	return preflightOutcome(report)
}

// preflightArtifact sends the artifact as the bytes it is, so the server
// decides what it is and whether it is valid.
func preflightArtifact(ctx context.Context, r *remote, document []byte, schemaTarget string) (api.PreflightReport, []byte, error) {
	var body bytes.Buffer
	body.WriteString(`{"artifact":`)
	body.Write(document)
	if schemaTarget != "" {
		target, err := json.Marshal(schemaTarget)
		if err != nil {
			return api.PreflightReport{}, nil, failf("output", "cannot encode --schema-target: %v", err)
		}
		body.WriteString(`,"schemaTarget":`)
		body.Write(target)
	}
	body.WriteString("}")

	res, err := r.api.PreflightWithBodyWithResponse(ctx, "application/json", bytes.NewReader(body.Bytes()))
	if err != nil {
		return api.PreflightReport{}, nil, transportFailure(ctx, "running the preflight", err)
	}
	if res.StatusCode() == http.StatusNotFound {
		return api.PreflightReport{}, nil, r.unsupported(ctx, "a preflight")
	}
	if res.StatusCode() != http.StatusOK {
		return api.PreflightReport{}, nil, r.refusal(ctx, "the artifact", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return api.PreflightReport{}, nil, failf("unexpected_response", "Alder answered with something that is not a preflight report")
	}
	return *res.JSON200, res.Body, nil
}

// preflightOutcome is the exit status of a finished preflight, in the client's
// existing codes: work to do is a difference, something undecided is
// incomplete, and something that cannot carry across is not applicable.
func preflightOutcome(r api.PreflightReport) error {
	switch r.Overall {
	case api.PreflightCompatible:
		return nil
	case api.PreflightCompatibleWithPrerequisites:
		return &ExitError{Code: ExitDifferences}
	case api.PreflightNotCompatible:
		return &ExitError{Code: ExitNotApplicable}
	}
	return &ExitError{Code: ExitIncomplete}
}

var preflightOverallText = map[api.PreflightOverall]string{
	api.PreflightCompatible:                  "Compatible",
	api.PreflightCompatibleWithPrerequisites: "Compatible with prerequisites",
	api.PreflightNotCompatible:               "Incompatible",
	api.PreflightIncomplete:                  "Incomplete",
}

var preflightSourceText = map[api.PreflightSourceType]string{
	api.PreflightSourceChangePackage:  "change package",
	api.PreflightSourceSchemaSnapshot: "schema snapshot",
	api.PreflightSourceDataSnapshot:   "data snapshot",
	api.PreflightSourceConfigSnapshot: "configuration snapshot",
}

func renderPreflight(w io.Writer, r api.PreflightReport, all bool) {
	source := preflightSourceText[r.Source.Type]
	if vendor := safe(text(r.Source.Vendor)); vendor != "" {
		source += " from " + vendor
	}
	target := strings.Join(r.Target.NamingContexts, ", ")
	if vendor := safe(text(r.Target.Vendor)); vendor != "" {
		target += " (" + vendor + ")"
	}
	writef(w, "Migration preflight: %s -> %s\n", source, safe(target))
	if title := safe(text(r.Source.Title)); title != "" {
		writef(w, "  %s\n", title)
	}
	writef(w, "  integrity %s, %s\n", safe(r.Source.Integrity), plural(r.Source.Objects, "object", "objects"))
	writeln(w)
	writef(w, "Overall: %s\n", preflightOverallText[r.Overall])
	if !r.Complete && len(r.Reasons) > 0 {
		writef(w, "  not complete: %s\n", safe(strings.Join(r.Reasons, ", ")))
	}
	writeln(w)

	for _, s := range r.Sections {
		c := s.Counts
		writef(w, "%s\n", sectionTitle(s.Category))
		for _, row := range []struct {
			n    int
			name string
		}{
			{c.Portable, "portable"}, {c.AlreadySatisfied, "already present"}, {c.PrerequisiteRequired, "need prerequisites"},
			{c.Incompatible, "incompatible"}, {c.Unsupported, "unsupported"}, {c.Unknown, "unknown"}, {c.Excluded, "not migrated"},
		} {
			if row.n > 0 {
				writef(w, "  %5d %s\n", row.n, row.name)
			}
		}
	}
	if len(r.Capabilities) > 0 {
		writeln(w)
		writeln(w, "Capabilities this source needs")
		for _, c := range r.Capabilities {
			state := "available"
			if !c.Available {
				state = "NOT available"
			}
			writef(w, "  %-18s %s  (%s)\n", safe(c.Capability), state, safe(c.RequiredBy))
		}
	}

	writeln(w)
	shown := 0
	for _, f := range r.Findings {
		if !all && (f.Classification == api.PreflightPortable || f.Classification == api.PreflightAlreadySatisfied) {
			continue
		}
		shown++
		writef(w, "%-4s %-22s %-33s %s\n", f.Id, f.Classification, f.Code, findingSubject(f))
		writef(w, "       %s\n", safe(f.Explanation))
		if f.Causes != nil && len(*f.Causes) > 0 {
			writef(w, "       because of %s\n", strings.Join(*f.Causes, ", "))
		}
		if f.Prerequisites != nil {
			for _, p := range *f.Prerequisites {
				writef(w, "       needs %s\n", prerequisiteText(p))
			}
		}
	}
	if shown == 0 && !all {
		writeln(w, "Nothing needs attention. --all lists every finding.")
	}

	writeln(w)
	writeln(w, "Not evaluated")
	for _, n := range r.NotEvaluated {
		writef(w, "  %s\n", safe(strings.ReplaceAll(n.Area, "_", " ")))
	}
	writeln(w)
	writeln(w, "Nothing has been written, and the artifact is unchanged. A preflight is not a")
	writeln(w, "plan: changes still go through alder plan and alder apply.")
}

func sectionTitle(c api.PreflightCategory) string {
	switch c {
	case api.PreflightCategoryArtifact:
		return "Artifact"
	case api.PreflightCategorySchema:
		return "Schema"
	case api.PreflightCategoryNaming:
		return "Naming"
	case api.PreflightCategoryEntries:
		return "Entries"
	case api.PreflightCategoryReferences:
		return "References"
	case api.PreflightCategoryConfiguration:
		return "Configuration"
	case api.PreflightCategoryCapabilities:
		return "Capabilities"
	case api.PreflightCategoryOperational:
		return "Operational attributes"
	case api.PreflightCategorySensitive:
		return "Sensitive data"
	}
	return string(c)
}

func findingSubject(f api.PreflightFinding) string {
	s := f.Source
	var parts []string
	if item := safe(text(s.Item)); item != "" {
		parts = append(parts, item)
	}
	for _, v := range []*string{s.Oid, s.Name, s.Setting, s.Resource, s.Dn, s.Attribute, s.Value} {
		if value := safe(text(v)); value != "" {
			parts = append(parts, value)
		}
	}
	if f.Count != nil && *f.Count > 1 {
		parts = append(parts, "x"+strconv.Itoa(*f.Count))
	}
	return strings.Join(parts, " ")
}

func prerequisiteText(p api.PreflightPrerequisite) string {
	parts := []string{safe(p.Type)}
	for _, v := range []*string{p.Element, p.Oid, p.Name, p.Dn, p.Capability, p.Setting, p.Resource} {
		if value := safe(text(v)); value != "" {
			parts = append(parts, value)
		}
	}
	if by := text(p.ProvidedBy); by != "" {
		parts = append(parts, "(supplied by "+safe(by)+")")
	}
	return strings.Join(parts, " ")
}
