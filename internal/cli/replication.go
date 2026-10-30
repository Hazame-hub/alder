package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/apiclient"
	"github.com/hazame-hub/alder/internal/envflags"
	"github.com/hazame-hub/alder/internal/replication"
)

// replicationFormat is the document `alder replication` writes and `alder
// replication compare` reads (1.41). A replication report is not
// configuration -- its running state was ruled out of config snapshots on
// 2026-09-27 -- so it is a document kind of its own.
const replicationFormat = "alder-replication"

// replicationDocument is one server's own record of its replication, as read
// at one moment. The report is the API's shape, verbatim.
type replicationDocument struct {
	Format  string                      `json:"format"`
	Version int                         `json:"version"`
	Server  string                      `json:"server"`
	ReadAt  time.Time                   `json:"readAt"`
	Report  apiclient.ReplicationReport `json:"report"`
}

// maxReplicationBytes bounds a replication document. A report is a handful of
// values per suffix; a megabyte is far beyond any real topology.
const maxReplicationBytes = 1 << 20

type replicationOptions struct {
	output string
	force  bool
}

func replicationCmd(env *Env) *cobra.Command {
	var conn connection
	var o replicationOptions
	cmd := command(env, &cobra.Command{
		Use:   "replication --output FILE",
		Short: "Write what one server records about its own replication",
		Long: "Reads what the server records about its own replication -- its role, its links,\n" +
			"and for each origin the latest change it holds -- through a running Alder server,\n" +
			"and writes it as an alder-replication version 1 document. Alder does not contact\n" +
			"the other servers named in it.\n\n" +
			"Read two servers this way, from wherever each is reachable and with each one's\n" +
			"own credentials, and compare the two files with alder replication compare.\n\n" +
			"--output - writes the document to standard output. A file is written to a\n" +
			"temporary name and renamed when complete, and an existing file is never replaced\n" +
			"without --force.",
		Args: argsBetween(0, 0, "no arguments"),
	}, func(ctx context.Context, _ []string) error {
		return runReplication(ctx, env, &conn, o)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	f := cmd.Flags()
	f.StringVarP(&o.output, "output", "o", "", "the file to write, or - for standard output")
	f.BoolVar(&o.force, "force", false, "replace --output if it already exists")
	envflags.Exclude(f, "output", "force")
	cmd.AddCommand(replicationCompareCmd(env))
	return cmd
}

func runReplication(ctx context.Context, env *Env, conn *connection, o replicationOptions) error {
	if o.output == "" {
		return usagef("--output is required: a file to write, or - for standard output")
	}
	if err := conn.check(false); err != nil {
		return err
	}
	if err := refuseExisting(o.output, o.force); err != nil {
		return err
	}

	ctx, cancel := conn.bound(ctx)
	defer cancel()
	r, err := conn.open(ctx, env)
	if err != nil {
		return err
	}
	defer r.close()

	res, err := r.api.GetReplicationWithResponse(ctx, &apiclient.GetReplicationParams{})
	if err != nil {
		return transportFailure(ctx, "reading replication", err)
	}
	if res.StatusCode() != http.StatusOK {
		return r.refusal(ctx, "replication", res.HTTPResponse, res.Body)
	}
	if res.JSON200 == nil {
		return failf("unexpected_response", "Alder answered with something that is not a replication report")
	}
	doc := replicationDocument{
		Format: replicationFormat, Version: 1,
		Server: net.JoinHostPort(conn.host, strconv.Itoa(conn.effectivePort())),
		ReadAt: time.Now().UTC().Truncate(time.Second),
		Report: *res.JSON200,
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return failf("output", "cannot encode the document: %v", err)
	}
	if o.output == "-" {
		return writeDocument(env.Stdout, body)
	}
	if err := writeFile(o.output, o.force, func(w io.Writer) error {
		return writeDocument(w, body)
	}, func(*os.File) error { return nil }); err != nil {
		return err
	}
	writef(env.Stderr, "Read the replication of %s (%s, %d suffixes) to %s\n",
		safe(doc.Server), safe(string(doc.Report.Role)), len(doc.Report.Suffixes), o.output)
	return nil
}

type replicationCompareOptions struct {
	json     bool
	tolerate time.Duration
}

func replicationCompareCmd(env *Env) *cobra.Command {
	var o replicationCompareOptions
	cmd := command(env, &cobra.Command{
		Use:   "compare A B",
		Short: "Compare two servers' replication records, per suffix and per origin",
		Long: "Compares two alder-replication documents, as alder replication writes them.\n" +
			"Nothing is contacted: not an Alder server, not a directory.\n\n" +
			"For each suffix and each origin, the two servers' latest changes from that\n" +
			"origin are compared by change sequence. When one is behind, the span is\n" +
			"measured by that origin's own clock, so it does not depend on the two servers'\n" +
			"clocks agreeing. It is never a count of changes: a change sequence says when a\n" +
			"change was made, not how many lie between two of them.\n\n" +
			"In step means both hold the same latest change from each origin. If an origin\n" +
			"has made no change since, a link that stopped working looks the same, so the age\n" +
			"of the latest change is printed beside it.\n\n" +
			"--tolerate counts an origin behind by no more than that as in step, for a\n" +
			"monitoring job that should not page on a change in flight.\n\n" +
			"Exit status: 0 in step, 1 one behind or each holding changes the other lacks,\n" +
			"2 incomplete or not comparable (this wins over 1), 7 usage, 8 failure.",
		Args: argsBetween(2, 2, "two alder-replication documents, A and B (one may be - for standard input)"),
	}, func(_ context.Context, args []string) error {
		return runReplicationCompare(env, o, args)
	})
	f := cmd.Flags()
	f.BoolVar(&o.json, "json", false, "write the comparison as JSON to standard output")
	f.DurationVar(&o.tolerate, "tolerate", 0, "count an origin behind by no more than this as in step (default exact)")
	envflags.Exclude(f, "json", "tolerate")
	return cmd
}

func runReplicationCompare(env *Env, o replicationCompareOptions, args []string) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	if o.tolerate < 0 {
		return usagef("--tolerate cannot be negative")
	}
	if args[0] == "-" && args[1] == "-" {
		return usagef("standard input can be read once: only one of A and B can be -")
	}
	var docs [2]replicationDocument
	for i, arg := range args {
		data, name, readErr := env.readInput(arg, "a replication document", maxReplicationBytes)
		if readErr != nil {
			return readErr
		}
		doc, decodeErr := decodeReplicationDocument(data)
		if decodeErr != nil {
			return failf("invalid_document", "%s: %v", name, decodeErr)
		}
		docs[i] = doc
	}

	cmp := replication.Compare(reportFromDocument(docs[0].Report), reportFromDocument(docs[1].Report),
		replication.CompareOptions{Tolerate: o.tolerate})

	if o.json {
		if err := writeJSON(env, replicationComparisonJSON(cmp, docs)); err != nil {
			return err
		}
	} else {
		renderReplicationComparison(env.Stdout, cmp, docs, time.Now())
	}
	return replicationOutcome(cmp)
}

// decodeReplicationDocument reads a document as strictly as every other
// document Alder reads: one JSON object, no unknown field, this format and a
// version this client knows.
func decodeReplicationDocument(data []byte) (replicationDocument, error) {
	var doc replicationDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return doc, fmt.Errorf("not an alder-replication document: %w", err)
	}
	if dec.More() {
		return doc, fmt.Errorf("more than one JSON document")
	}
	if doc.Format != replicationFormat {
		return doc, fmt.Errorf("its format is %q, not %s", doc.Format, replicationFormat)
	}
	if doc.Version != 1 {
		return doc, fmt.Errorf("version %d is not one this Alder reads (1)", doc.Version)
	}
	return doc, nil
}

// reportFromDocument turns the API's shape back into the one Compare reads.
// Only what a comparison uses is carried: the links are for a person.
func reportFromDocument(in apiclient.ReplicationReport) replication.Report {
	out := replication.Report{Role: string(in.Role), Why: in.Why}
	if in.Provider != nil {
		out.Provider = *in.Provider
	}
	for _, s := range in.Suffixes {
		suffix := replication.Suffix{DN: s.Dn, Role: string(s.Role)}
		if s.ServerId != nil {
			suffix.ServerID = *s.ServerId
		}
		if s.Generation != nil {
			suffix.Generation = *s.Generation
		}
		for _, c := range s.Cursors {
			cursor := replication.Cursor{Origin: c.Origin, At: c.At, Raw: c.Raw}
			if c.Csn != nil {
				cursor.CSN = *c.Csn
			}
			suffix.Cursors = append(suffix.Cursors, cursor)
		}
		out.Suffixes = append(out.Suffixes, suffix)
	}
	if in.Unread != nil {
		for _, u := range *in.Unread {
			out.Unread = append(out.Unread, replication.Unread{Where: u.Where, Reason: u.Reason})
		}
	}
	return out
}

// replicationOutcome is the exit status, following alder diff: incomplete
// (and not comparable) wins over a difference.
func replicationOutcome(cmp replication.Comparison) error {
	switch cmp.Verdict {
	case replication.VerdictInStep:
		return nil
	case replication.VerdictNotComparable, replication.VerdictIncomplete:
		return &ExitError{Code: ExitIncomplete}
	}
	return &ExitError{Code: ExitDifferences}
}

type replicationComparisonOut struct {
	A        replicationSideOut     `json:"a"`
	B        replicationSideOut     `json:"b"`
	Verdict  string                 `json:"verdict"`
	Why      string                 `json:"why"`
	Suffixes []replicationSuffixOut `json:"suffixes"`
	Caveat   string                 `json:"caveat"`
}

type replicationSideOut struct {
	Server string    `json:"server"`
	ReadAt time.Time `json:"readAt"`
}

type replicationSuffixOut struct {
	DN      string                 `json:"dn"`
	Verdict string                 `json:"verdict"`
	Why     string                 `json:"why,omitempty"`
	Origins []replicationOriginOut `json:"origins"`
}

type replicationOriginOut struct {
	Origin      string              `json:"origin"`
	Verdict     string              `json:"verdict"`
	A           *replication.Cursor `json:"a,omitempty"`
	B           *replication.Cursor `json:"b,omitempty"`
	SpanSeconds float64             `json:"spanSeconds"`
	Tolerated   bool                `json:"tolerated,omitempty"`
}

func replicationComparisonJSON(cmp replication.Comparison, docs [2]replicationDocument) replicationComparisonOut {
	out := replicationComparisonOut{
		A:       replicationSideOut{Server: docs[0].Server, ReadAt: docs[0].ReadAt},
		B:       replicationSideOut{Server: docs[1].Server, ReadAt: docs[1].ReadAt},
		Verdict: cmp.Verdict, Why: cmp.Why, Caveat: cmp.Caveat,
		Suffixes: []replicationSuffixOut{},
	}
	for _, s := range cmp.Suffixes {
		view := replicationSuffixOut{DN: s.DN, Verdict: s.Verdict, Why: s.Why, Origins: []replicationOriginOut{}}
		for _, o := range s.Origins {
			view.Origins = append(view.Origins, replicationOriginOut{
				Origin: o.Origin, Verdict: o.Verdict, A: o.A, B: o.B,
				SpanSeconds: o.Span.Seconds(), Tolerated: o.Tolerated,
			})
		}
		out.Suffixes = append(out.Suffixes, view)
	}
	return out
}

func renderReplicationComparison(w io.Writer, cmp replication.Comparison, docs [2]replicationDocument, now time.Time) {
	writef(w, "A: %s, read %s\n", safe(docs[0].Server), docs[0].ReadAt.Format(time.RFC3339))
	writef(w, "B: %s, read %s\n", safe(docs[1].Server), docs[1].ReadAt.Format(time.RFC3339))
	if apart := docs[0].ReadAt.Sub(docs[1].ReadAt).Abs(); apart > time.Minute {
		writef(w, "The two were read %s apart: a change made in between is on one and not the other.\n",
			apart.Round(time.Second))
	}
	writef(w, "\n%s: %s\n", verdictWords(cmp.Verdict), safe(cmp.Why))
	for _, s := range cmp.Suffixes {
		writef(w, "\n%s  %s\n", safe(s.DN), verdictWords(s.Verdict))
		if s.Why != "" {
			writef(w, "  %s\n", safe(s.Why))
		}
		for _, o := range s.Origins {
			writef(w, "  %s\n", originLine(o, now))
		}
	}
	if cmp.Verdict == replication.VerdictInStep {
		writef(w, "\n%s\n", cmp.Caveat)
	}
}

func verdictWords(verdict string) string {
	switch verdict {
	case replication.VerdictInStep:
		return "in step"
	case replication.VerdictABehind:
		return "A is behind"
	case replication.VerdictBBehind:
		return "B is behind"
	case replication.VerdictDiverged:
		return "each holds changes the other lacks"
	case replication.VerdictNotComparable:
		return "not comparable"
	}
	return "incomplete"
}

func originLine(o replication.OriginComparison, now time.Time) string {
	who := "server " + safe(o.Origin)
	switch o.Verdict {
	case replication.VerdictInStep:
		at := o.A
		if at == nil {
			at = o.B
		}
		age := ""
		if at != nil {
			age = fmt.Sprintf(" (latest change %s ago)", now.Sub(at.At).Round(time.Second))
		}
		if o.Tolerated {
			return fmt.Sprintf("%s: within tolerance, %s apart%s", who, o.Span, age)
		}
		return who + ": in step" + age
	case replication.VerdictABehind:
		if o.A == nil {
			return who + ": A holds nothing from it"
		}
		return fmt.Sprintf("%s: A lacks its changes made in the %s before %s", who, spanWords(o.Span),
			o.B.At.Format(time.RFC3339))
	case replication.VerdictBBehind:
		if o.B == nil {
			return who + ": B holds nothing from it"
		}
		return fmt.Sprintf("%s: B lacks its changes made in the %s before %s", who, spanWords(o.Span),
			o.A.At.Format(time.RFC3339))
	}
	return who + ": " + verdictWords(o.Verdict)
}

// spanWords says a span without inventing precision. Two changes in the same
// second on a server that stamps whole seconds are ordered by sequence and
// have no span at all.
func spanWords(d time.Duration) string {
	if d < time.Second {
		return "less than a second"
	}
	return d.Round(time.Second).String()
}
