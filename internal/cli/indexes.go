package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/envflags"
	"github.com/hazame-hub/alder/internal/indexadvice"
	"github.com/hazame-hub/alder/internal/signing"
	"github.com/hazame-hub/alder/internal/snapshot"
)

func indexesCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "indexes",
		Short: "Work out which indexes a directory's searches are missing",
		Long: "alder indexes advise reads a server log you supply, and says which indexes\n" +
			"its searches lacked. It never adds one: an index is staged with alder plan and\n" +
			"alder apply, or from the web interface's indexes panel, like any other change.",
	}
	cmd.AddCommand(indexesAdviseCmd(env))
	return cmd
}

type indexesAdviseOptions struct {
	log, config string
	slowerThan  time.Duration
	json        bool
}

func indexesAdviseCmd(env *Env) *cobra.Command {
	var o indexesAdviseOptions
	cmd := command(env, &cobra.Command{
		Use:   "advise --log FILE --config SNAPSHOT",
		Short: "Read a server log and say which indexes its searches lacked",
		Long: "Reads a directory server's log -- 389 Directory Server's access log, or OpenLDAP's\n" +
			"written with loglevel stats -- against the indexes a configuration snapshot holds\n" +
			"(alder snapshot --kind config), and lists the indexes its searches were missing,\n" +
			"ranked by the time those searches took.\n\n" +
			"Nothing is contacted: not an Alder server, not a directory. The log is read as a\n" +
			"stream, so it can be any size, and it never leaves this machine. --log - reads it\n" +
			"from standard input.\n\n" +
			"A search counts when the server marked it unindexed (389 DS's notes=U or\n" +
			"notes=A) or took at least --slower-than. Nothing from the log is printed but\n" +
			"attribute names: example filters have every value replaced by \"…\".\n\n" +
			"Adding an index is not building it: run a reindex task on 389 DS, or slapindex\n" +
			"on OpenLDAP, after adding one. Stage a suggestion with alder plan and alder apply\n" +
			"like any other change, or from the indexes panel in the web interface.\n\n" +
			"Exit status: 0 nothing suggested, 1 at least one suggestion, 2 some counted\n" +
			"searches were under no backend in the snapshot (this wins over 1), 7 usage,\n" +
			"8 failure.",
		Args: argsBetween(0, 0, "no arguments: the log is --log, the snapshot --config"),
	}, func(_ context.Context, _ []string) error {
		return runIndexesAdvise(env, o)
	})
	f := cmd.Flags()
	f.StringVar(&o.log, "log", "", "the server log to read, or - for standard input")
	f.StringVar(&o.config, "config", "", "a configuration snapshot of the same server (alder snapshot --kind config)")
	f.DurationVar(&o.slowerThan, "slower-than", indexadvice.DefaultSlowerThan, "count any search that took at least this long")
	f.BoolVar(&o.json, "json", false, "write the report as JSON to standard output")
	envflags.Exclude(f, "log", "config", "slower-than", "json")
	return cmd
}

func runIndexesAdvise(env *Env, o indexesAdviseOptions) (err error) {
	defer func() { err = asJSON(env, o.json, err) }()
	if o.log == "" {
		return usagef("--log is required: a 389 DS access log or an OpenLDAP stats log, or - for standard input")
	}
	if o.config == "" {
		return usagef("--config is required: a configuration snapshot of the same server, from alder snapshot --kind config")
	}
	if o.slowerThan <= 0 {
		return usagef("--slower-than must be more than zero")
	}

	data, name, err := env.readInput(o.config, "the configuration snapshot", maxRequestBytes)
	if err != nil {
		return err
	}
	document := bytes.TrimSpace(data)
	if signing.IsEnvelope(document) {
		// The index list is all that is read from it, and a signature says
		// nothing about which indexes a log needed; the envelope is opened
		// without checking it, and the command says so.
		envelope, derr := signing.Decode(document)
		if derr != nil {
			return failf("signature_invalid", "%s: %v", name, derr)
		}
		document = envelope.Payload
		writef(env.Stderr, "%s is signed; its signature was not checked, only its index list read.\n", name)
	}
	snap, _, err := snapshot.DecodeConfig(document)
	if err != nil {
		return failf("invalid_document", "%s is not a configuration snapshot: %v", name, err)
	}
	backends := indexadvice.FromConfig(snap)
	if len(backends) == 0 {
		return failf("invalid_document", "%s holds no backend with an index, so there is nothing to compare a log with", name)
	}

	var in io.Reader
	logName := o.log
	if o.log == "-" {
		if err := env.claimStdin("the log"); err != nil {
			return err
		}
		in, logName = env.Stdin, "standard input"
	} else {
		// #nosec G304 -- a file the user named on the command line.
		f, openErr := os.Open(o.log)
		if openErr != nil {
			return failf("input", "cannot read the log: %v", openErr)
		}
		defer func() { _ = f.Close() }()
		in = f
	}
	report, err := indexadvice.Read(in, indexadvice.Options{Backends: backends, SlowerThan: o.slowerThan})
	if err != nil {
		return failf("input", "cannot read the log %s: %v", logName, err)
	}

	if o.json {
		if err := writeJSON(env, indexAdviceJSON(report)); err != nil {
			return err
		}
	} else {
		renderIndexAdvice(env.Stdout, report, o.slowerThan)
	}
	switch {
	case report.Unattributed > 0:
		return &ExitError{Code: ExitIncomplete}
	case len(report.Suggestions) > 0:
		return &ExitError{Code: ExitDifferences}
	}
	return nil
}

type indexAdviceOut struct {
	Format        string               `json:"format,omitempty"`
	Lines         int                  `json:"lines"`
	Unrecognised  int                  `json:"unrecognised"`
	Searches      int                  `json:"searches"`
	Counted       int                  `json:"counted"`
	WithoutResult int                  `json:"withoutResult"`
	Unattributed  int                  `json:"unattributed"`
	Unparsed      int                  `json:"unparsed"`
	Suggestions   []indexSuggestionOut `json:"suggestions"`
	Caveat        string               `json:"caveat"`
}

type indexSuggestionOut struct {
	Backend              string     `json:"backend"`
	Attribute            string     `json:"attribute"`
	Type                 string     `json:"type"`
	Status               string     `json:"status"`
	Reason               string     `json:"reason,omitempty"`
	Searches             int        `json:"searches"`
	MarkedUnindexed      int        `json:"markedUnindexed"`
	ServerSaidNotIndexed int        `json:"serverSaidNotIndexed"`
	TotalSeconds         float64    `json:"totalSeconds"`
	WorstSeconds         float64    `json:"worstSeconds"`
	First                *time.Time `json:"first,omitempty"`
	Last                 *time.Time `json:"last,omitempty"`
	Examples             []string   `json:"examples"`
}

func indexAdviceJSON(r *indexadvice.Report) indexAdviceOut {
	out := indexAdviceOut{
		Format: r.Format, Lines: r.Lines, Unrecognised: r.Unrecognised, Searches: r.Searches,
		Counted: r.Counted, WithoutResult: r.WithoutResult, Unattributed: r.Unattributed,
		Unparsed: r.Unparsed, Caveat: indexadvice.Caveat, Suggestions: []indexSuggestionOut{},
	}
	for _, s := range r.Suggestions {
		view := indexSuggestionOut{
			Backend: s.Backend, Attribute: s.Attribute, Type: s.Type, Status: s.Status, Reason: s.Reason,
			Searches: s.Searches, MarkedUnindexed: s.MarkedUnindexed, ServerSaidNotIndexed: s.ServerSaidNotIndexed,
			TotalSeconds: s.Total.Seconds(), WorstSeconds: s.Worst.Seconds(),
			Examples: append([]string{}, s.Examples...),
		}
		if !s.First.IsZero() {
			first, last := s.First, s.Last
			view.First, view.Last = &first, &last
		}
		out.Suggestions = append(out.Suggestions, view)
	}
	return out
}

func renderIndexAdvice(w io.Writer, r *indexadvice.Report, slowerThan time.Duration) {
	format := r.Format
	if format == "" {
		format = "no recognised format"
	}
	writef(w, "Read %d lines (%s): %d searches, %d counted (marked unindexed, or %s or slower).\n",
		r.Lines, format, r.Searches, r.Counted, slowerThan)
	if r.WithoutResult > 0 {
		writef(w, "%d searches had no result line in the log, so their duration is unknown.\n", r.WithoutResult)
	}
	if r.Unattributed > 0 {
		writef(w, "%d counted searches were under no backend in the snapshot.\n", r.Unattributed)
	}
	if r.Unparsed > 0 {
		writef(w, "%d counted searches had a filter that is not RFC 4515.\n", r.Unparsed)
	}
	if len(r.Suggestions) == 0 {
		writef(w, "\nNothing to suggest.\n")
		return
	}
	for _, s := range r.Suggestions {
		writef(w, "\n%s %s on %s  %s\n", safe(s.Attribute), safe(s.Type), safe(s.Backend), suggestionWords(s.Status))
		writef(w, "  %d searches, %s in all, worst %s", s.Searches, s.Total.Round(time.Millisecond), s.Worst.Round(time.Millisecond))
		if s.MarkedUnindexed > 0 {
			writef(w, "; %d marked unindexed by the server", s.MarkedUnindexed)
		}
		if s.ServerSaidNotIndexed > 0 {
			writef(w, "; %d \"not indexed\" lines", s.ServerSaidNotIndexed)
		}
		writef(w, "\n")
		if !s.First.IsZero() {
			writef(w, "  seen %s to %s\n", s.First.Format(time.RFC3339), s.Last.Format(time.RFC3339))
		}
		if s.Reason != "" {
			writef(w, "  %s\n", safe(s.Reason))
		}
		for _, e := range s.Examples {
			writef(w, "  e.g. %s\n", safe(e))
		}
	}
	writef(w, "\n%s\n", indexadvice.Caveat)
}

func suggestionWords(status string) string {
	switch status {
	case indexadvice.StatusCandidate:
		return "can be added"
	case indexadvice.StatusTypeMissing:
		return "indexed for other matches only"
	case indexadvice.StatusBlocked:
		return "cannot be added by Alder"
	}
	return status
}
