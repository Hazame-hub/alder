package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/envflags"
)

// liveSide names the live directory as one side of a comparison. It is spelled
// so that no ordinary file name is mistaken for it; a file really called @live
// is ./@live.
const liveSide = "@live"

type diffOptions struct {
	json, summary, includeUnchanged bool

	base, scope, filter string
	operational         bool

	stage, stageDeletion []string
	changesOut           string
	force                bool
}

func diffCmd(env *Env) *cobra.Command {
	var conn connection
	var o diffOptions
	var cmd *cobra.Command
	cmd = command(env, &cobra.Command{
		Use:   "diff SOURCE TARGET",
		Short: "Compare two states: two snapshots, or a snapshot and the live directory",
		Long: "Compares SOURCE with TARGET through a running Alder server, with the same\n" +
			"comparison the web interface uses. Each side is a snapshot file, - for a\n" +
			"snapshot on standard input, or @live for the directory as it is now.\n\n" +
			"Two snapshots need only --api-url: the server compares them without a\n" +
			"directory session. A comparison with @live needs the directory flags.\n\n" +
			"The direction is always SOURCE to TARGET: added means in TARGET and not in\n" +
			"SOURCE, removed means in SOURCE and not in TARGET. To see what would bring the\n" +
			"directory back to a snapshot, compare @live with the snapshot.\n\n" +
			"Exit status: 0 complete and no differences, 1 complete with differences,\n" +
			"2 incomplete (something could not be seen), 7 usage, 8 failure.\n\n" +
			"With @live as SOURCE, --stage and --stage-deletion write the change requests\n" +
			"Alder derived for the named differences to --changes-out, for alder plan and\n" +
			"alder apply --changes. Nothing is selected by default, and a deletion is only\n" +
			"ever selected by --stage-deletion.",
		Args: argsBetween(2, 2, "SOURCE and TARGET: snapshot files, - for standard input, or @live"),
	}, func(ctx context.Context, args []string) error {
		return runDiff(ctx, env, &conn, o, cmd.Flags(), args)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "write Alder's comparison as JSON to standard output")
	f.BoolVar(&o.summary, "summary", false, "print the counts and not the differences")
	f.BoolVar(&o.includeUnchanged, "include-unchanged", false, "list unchanged entries too")
	f.StringVar(&o.base, "base", "", "the live side's base DN (default: the snapshot's)")
	f.StringVar(&o.scope, "scope", "", "the live side's scope: sub, one or base (default: the snapshot's)")
	f.StringVar(&o.filter, "filter", "", "the live side's filter (default: the snapshot's)")
	f.BoolVar(&o.operational, "operational", false, "capture operational attributes on the live side (default: as the snapshot did)")
	f.StringArrayVar(&o.stage, "stage", nil, "select the change Alder derived for this DN (repeatable)")
	f.StringArrayVar(&o.stageDeletion, "stage-deletion", nil, "select the deletion Alder derived for this DN (repeatable)")
	f.StringVar(&o.changesOut, "changes-out", "", "the file to write selected change requests to")
	f.BoolVar(&o.force, "force", false, "replace --changes-out if it already exists")
	envflags.Exclude(f, "json", "summary", "include-unchanged", "base", "scope", "filter", "operational",
		"stage", "stage-deletion", "changes-out", "force")
	return cmd
}

type diffSide struct {
	live bool
	name string
	raw  []byte
}

func runDiff(ctx context.Context, env *Env, conn *connection, o diffOptions, flags *pflag.FlagSet, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()

	srcArg, tgtArg := args[0], args[1]
	if srcArg == liveSide && tgtArg == liveSide {
		return usagef("only one side can be %s: the directory compared with itself says nothing", liveSide)
	}
	if srcArg == "-" && tgtArg == "-" {
		return usagef("only one side can be read from standard input")
	}
	live := srcArg == liveSide || tgtArg == liveSide
	liveFlags := flags.Changed("base") || flags.Changed("scope") || flags.Changed("filter") || flags.Changed("operational")
	if liveFlags && !live {
		return usagef("--base, --scope, --filter and --operational describe the %s side, and neither side is %s", liveSide, liveSide)
	}
	if flags.Changed("scope") {
		switch o.scope {
		case "sub", "one", "base":
		default:
			return usagef("--scope must be sub, one or base, not %q", o.scope)
		}
	}
	selecting := len(o.stage)+len(o.stageDeletion) > 0
	switch {
	case selecting && srcArg != liveSide:
		return usagef("--stage and --stage-deletion need %s as SOURCE: the changes they select move the live directory toward TARGET", liveSide)
	case selecting && o.changesOut == "":
		return usagef("--stage and --stage-deletion write change requests, so --changes-out is required")
	case !selecting && o.changesOut != "":
		return usagef("--changes-out needs --stage or --stage-deletion: nothing is selected by default")
	case o.changesOut == "-":
		return usagef("--changes-out needs a file: standard output carries the comparison")
	}
	if err := refuseExisting(o.changesOut, o.force || o.changesOut == ""); err != nil {
		return err
	}
	// Only a live side reads the directory. Two snapshots need nothing but the
	// Alder server: no directory target, no bind, no password, no session.
	if live {
		if err := conn.check(srcArg == "-" || tgtArg == "-"); err != nil {
			return err
		}
	} else if err := conn.checkAPI(); err != nil {
		return err
	}

	// Read and check both snapshots before connecting, so a file that is not
	// one fails without a session.
	var sides [2]diffSide
	for i, arg := range args {
		if arg == liveSide {
			sides[i] = diffSide{live: true, name: "the live directory"}
			continue
		}
		data, name, readErr := env.readInput(arg, "a snapshot", maxRequestBytes)
		if readErr != nil {
			return readErr
		}
		// The snapshot is sent as the bytes it is, so that Alder -- not this
		// client -- decides whether it is a valid snapshot: re-encoding it
		// would drop exactly the unknown fields a version 1 reader must refuse.
		// Sending bytes as they are needs them to be one JSON object, or a
		// crafted file could close the object early and write the other side
		// of the request itself.
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
			return failf("snapshot_not_json", "%s is not a JSON document, so it is not an Alder snapshot", name)
		}
		sides[i] = diffSide{name: name, raw: trimmed}
	}

	var parts [][]byte
	for i, label := range []string{`{"source":`, `,"target":`} {
		parts = append(parts, []byte(label))
		if sides[i].live {
			doc, _ := json.Marshal(liveRequest(o, flags))
			parts = append(parts, []byte(`{"live":`), doc, []byte(`}`))
			continue
		}
		parts = append(parts, []byte(`{"snapshot":`), sides[i].raw, []byte(`}`))
	}
	parts = append(parts, []byte(fmt.Sprintf(`,"includeUnchanged":%t}`, o.includeUnchanged)))
	var total int64
	for _, p := range parts {
		total += int64(len(p))
	}
	if total > maxRequestBytes {
		return failf("request_too_large", "the comparison request would be %.1f MB, and Alder reads at most %d MB in one request",
			float64(total)/(1<<20), maxRequestBytes>>20)
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	var r *remote
	if live {
		r, err = conn.open(ctx, env)
	} else {
		r, err = conn.client(env)
	}
	if err != nil {
		return err
	}
	defer r.close()

	send := func() (*api.DiffStatesReply, error) {
		readers := make([]io.Reader, 0, len(parts))
		for _, p := range parts {
			readers = append(readers, bytes.NewReader(p))
		}
		setLength := func(_ context.Context, req *http.Request) error {
			req.ContentLength = total
			return nil
		}
		return r.api.DiffStatesWithBodyWithResponse(ctx, "application/json", io.MultiReader(readers...), setLength)
	}
	res, err := send()
	if err != nil {
		return transportFailure(ctx, "comparing", err)
	}
	// A server before 1.8 needs a session for every comparison. With the
	// directory flags given, the comparison is made again with one; without
	// them, the refusal says what to add.
	if !live && res.StatusCode() == http.StatusUnauthorized {
		if conn.host == "" {
			refused := r.refusal(ctx, "the comparison", res.HTTPResponse, res.Body)
			refused.Message += "\nThis Alder server needs a directory session even to compare two snapshots, " +
				"as servers before 1.8 do: give the directory flags (--host, --bind-dn and a password) to compare with one"
			return refused
		}
		if err := conn.check(srcArg == "-" || tgtArg == "-"); err != nil {
			return err
		}
		if r, err = conn.open(ctx, env); err != nil {
			return err
		}
		defer r.close()
		if res, err = send(); err != nil {
			return transportFailure(ctx, "comparing", err)
		}
	}
	if res.StatusCode() != http.StatusOK {
		return r.refusal(ctx, "the comparison", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return failf("unexpected_response", "Alder answered the comparison with something that is not one")
	}
	d := *res.JSON200

	if o.json {
		if err := writeDocument(env.Stdout, res.Body); err != nil {
			return failf("output", "cannot write to standard output: %v", err)
		}
	} else {
		renderDiff(env.Stdout, d, sides[0].name, sides[1].name, o.summary, srcArg == liveSide)
	}
	if selecting {
		if err := stageSelected(env, d, res.Body, o); err != nil {
			// The comparison is already on standard output; the refusal
			// goes to standard error only.
			var exit *ExitError
			if errors.As(err, &exit) && o.json {
				writef(env.Stderr, "alder: %s\n", exit.Message)
				return &ExitError{Code: exit.Code}
			}
			return err
		}
	}
	return diffOutcome(d)
}

func liveRequest(o diffOptions, flags *pflag.FlagSet) api.DiffLiveSide {
	var side api.DiffLiveSide
	if flags.Changed("base") {
		side.Base = &o.base
	}
	if flags.Changed("scope") {
		scope := api.SnapshotScope(o.scope)
		side.Scope = &scope
	}
	if flags.Changed("filter") {
		side.Filter = &o.filter
	}
	if flags.Changed("operational") {
		side.OperationalAttributes = &o.operational
	}
	return side
}

// diffOutcome is the exit status of a finished comparison. Incomplete wins over
// differences: a partial answer that found differences has still not said
// what the rest holds.
func diffOutcome(d api.Diff) error {
	if !d.Complete || d.Counts.Unknown > 0 {
		return &ExitError{Code: ExitIncomplete}
	}
	if d.Counts.Added+d.Counts.Removed+d.Counts.Modified+d.Counts.Renamed > 0 {
		return &ExitError{Code: ExitDifferences}
	}
	return nil
}

// stageSelected writes the change requests Alder derived for the differences
// named on the command line. It chooses nothing itself: every DN is named, a
// deletion is named as one, and a difference Alder offered no change for is
// refused rather than skipped.
func stageSelected(env *Env, d api.Diff, body []byte, o diffOptions) error {
	var rawItems struct {
		Items []struct {
			Candidate *struct {
				Changes []json.RawMessage `json:"changes"`
			} `json:"candidate"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &rawItems); err != nil || len(rawItems.Items) != len(d.Items) {
		return failf("unexpected_response", "cannot read the change requests in Alder's comparison")
	}

	find := func(dnText string) (int, bool) {
		want := strings.TrimSpace(dnText)
		for i, it := range d.Items {
			if (it.SourceDn != nil && strings.EqualFold(*it.SourceDn, want)) ||
				(it.TargetDn != nil && strings.EqualFold(*it.TargetDn, want)) {
				return i, true
			}
		}
		return 0, false
	}
	selected := map[int]bool{}
	pick := func(dnText string, deletion bool) error {
		i, ok := find(dnText)
		if !ok {
			return notApplicable("selection_not_found", "no difference is about %s; name a DN as diff prints it", safe(dnText))
		}
		it := d.Items[i]
		c := it.Candidate
		switch {
		case c == nil:
			return notApplicable("selection_not_applicable", "%s is %s, and Alder offers no change for it", safe(dnText), it.Kind)
		case c.Blocked != nil:
			return notApplicable("selection_blocked", "%s offers no change: %s", safe(dnText), *c.Blocked)
		case c.Destructive && !deletion:
			return notApplicable("deletion_not_selected", "the change for %s deletes the entry; a deletion is selected with --stage-deletion, never with --stage", safe(dnText))
		case !c.Destructive && deletion:
			return notApplicable("not_a_deletion", "the change for %s is not a deletion; select it with --stage", safe(dnText))
		}
		selected[i] = true
		return nil
	}
	for _, dnText := range o.stage {
		if err := pick(dnText, false); err != nil {
			return err
		}
	}
	for _, dnText := range o.stageDeletion {
		if err := pick(dnText, true); err != nil {
			return err
		}
	}

	indexes := make([]int, 0, len(selected))
	for i := range selected {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	changes := []json.RawMessage{}
	for _, i := range indexes {
		changes = append(changes, rawItems.Items[i].Candidate.Changes...)
	}
	doc, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return failf("output", "cannot encode the selected change requests: %v", err)
	}
	if err := writeFile(o.changesOut, o.force, func(w io.Writer) error {
		_, werr := w.Write(append(doc, '\n'))
		return werr
	}, nil); err != nil {
		return err
	}
	writef(env.Stderr, "Wrote %d change request(s) for %d selected difference(s) to %s.\n"+
		"Review them with: alder plan --changes %s\n", len(changes), len(indexes), o.changesOut, o.changesOut)
	return nil
}

func notApplicable(local, format string, a ...any) *ExitError {
	return &ExitError{Code: ExitNotApplicable, Local: local, Message: fmt.Sprintf(format, a...)}
}

// asJSON writes a failure's JSON document to standard output when the command
// was asked for JSON, so that standard output always holds exactly one
// document: the answer, or why there is none.
func asJSON(env *Env, jsonMode bool, err error) error {
	if !jsonMode || err == nil {
		return err
	}
	var exit *ExitError
	if errors.As(err, &exit) && (len(exit.Server) > 0 || exit.Local != "") {
		_ = writeDocument(env.Stdout, []byte(`{"error":`+string(exit.document())+`}`))
	}
	return err
}
