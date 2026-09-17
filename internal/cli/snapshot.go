package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/envflags"
)

type snapshotOptions struct {
	kind                string
	base, scope, filter string
	operational         bool
	output              string
	force               bool
}

func snapshotCmd(env *Env) *cobra.Command {
	var conn connection
	var o snapshotOptions
	var cmd *cobra.Command
	cmd = command(env, &cobra.Command{
		Use:   "snapshot (--base DN | --kind schema) --output FILE",
		Short: "Capture a subtree or the schema as an Alder snapshot",
		Long: "Captures a subtree through a running Alder server and writes the snapshot\n" +
			"exactly as Alder produced it: an alder-snapshot version 1 document, the same\n" +
			"file the web interface downloads. Sensitive attributes are a count of values,\n" +
			"never a value. A capture that cannot read the whole subtree fails; it never\n" +
			"writes part of one.\n\n" +
			"--kind schema captures the schema the server publishes instead: attribute\n" +
			"types and object classes for comparison, and syntaxes, matching rules, matching\n" +
			"rule uses, DIT content rules and name forms as context. It takes no --base,\n" +
			"--scope, --filter or --operational, and never captures server configuration.\n\n" +
			"--output - writes the snapshot to standard output and nothing else. A file is\n" +
			"written to a temporary name and renamed when complete, and an existing file is\n" +
			"never replaced without --force.",
		Args: argsBetween(0, 0, "no arguments"),
	}, func(ctx context.Context, _ []string) error {
		return runSnapshot(ctx, env, &conn, o, cmd.Flags())
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	f := cmd.Flags()
	f.StringVar(&o.kind, "kind", "data", "data, or schema for the published schema")
	f.StringVar(&o.base, "base", "", "the DN of the subtree to capture")
	f.StringVar(&o.scope, "scope", "sub", "sub, one or base")
	f.StringVar(&o.filter, "filter", "", "an RFC 4515 filter; the default captures every entry in scope")
	f.BoolVar(&o.operational, "operational", false, "also capture operational attributes")
	f.StringVarP(&o.output, "output", "o", "", "the file to write, or - for standard output")
	f.BoolVar(&o.force, "force", false, "replace --output if it already exists")
	envflags.Exclude(f, "kind", "base", "scope", "filter", "operational", "output", "force")
	return cmd
}

func runSnapshot(ctx context.Context, env *Env, conn *connection, o snapshotOptions, flags *pflag.FlagSet) error {
	switch o.kind {
	case "data":
		if o.base == "" {
			return usagef("--base is required: the DN of the subtree to capture")
		}
		switch o.scope {
		case "sub", "one", "base":
		default:
			return usagef("--scope must be sub, one or base, not %q", o.scope)
		}
	case "schema":
		if flags.Changed("base") || flags.Changed("scope") || flags.Changed("filter") || flags.Changed("operational") {
			return usagef("--kind schema captures the whole published schema: --base, --scope, --filter and --operational do not apply")
		}
	case "config":
		if flags.Changed("base") || flags.Changed("scope") || flags.Changed("filter") || flags.Changed("operational") {
			return usagef("--kind config captures the server's whole configuration: --base, --scope, --filter and --operational do not apply")
		}
	default:
		return usagef("--kind must be data, schema or config, not %q", o.kind)
	}
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

	var req api.SnapshotCaptureRequest
	switch o.kind {
	case "schema":
		req.Kind = ptr(api.StateKindSchema)
	case "config":
		req.Kind = ptr(api.StateKindConfig)
	default:
		scope := api.SnapshotScope(o.scope)
		req = api.SnapshotCaptureRequest{Base: &o.base, Scope: &scope}
		if o.filter != "" {
			req.Filter = &o.filter
		}
		if o.operational {
			req.OperationalAttributes = ptr(true)
		}
	}
	// The raw call, not the one that reads the body into memory: a snapshot
	// can be tens of megabytes, and it goes straight from the socket to disk.
	res, err := r.api.CaptureSnapshot(ctx, req)
	if err != nil {
		return transportFailure(ctx, "capturing the snapshot", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return r.refusal(ctx, "capturing the snapshot", res, body)
	}

	if o.output == "-" {
		if _, err := io.Copy(env.Stdout, res.Body); err != nil {
			return copyFailure(ctx, "standard output", err)
		}
		return nil
	}

	var head snapshotHead
	err = writeFile(o.output, o.force, func(w io.Writer) error {
		if _, err := io.Copy(w, res.Body); err != nil {
			return copyFailure(ctx, o.output, err)
		}
		return nil
	}, func(f *os.File) error {
		h, headErr := readSnapshotHead(f)
		if headErr != nil {
			return failf("incomplete_snapshot",
				"what Alder sent is not a complete snapshot, so %s was not written: %v", o.output, headErr)
		}
		head = h
		return nil
	})
	if err != nil {
		return err
	}
	if head.Kind == "config" {
		// A configuration is one server's own, so the line says whose it is,
		// and says plainly that the secrets in it were not read.
		writef(env.Stderr, "Captured the %s configuration at %s (%d settings in %d resources, %d withheld; %s) to %s, %s\n",
			safe(head.Source.Provider), safe(head.Source.Root), head.Counts.Settings, head.Counts.Resources,
			head.Counts.Withheld, safe(head.Completeness), o.output, safe(head.Checksum))
		return nil
	}
	if head.Kind == "schema" {
		writef(env.Stderr, "Captured the schema at %s (%d attribute types, %d object classes, %d unparsed; %s) to %s, %s\n",
			safe(head.Source.SubschemaEntry), head.Counts.AttributeTypes, head.Counts.ObjectClasses, head.Counts.Unparsed,
			safe(head.Completeness), o.output, safe(head.Checksum))
		return nil
	}
	writef(env.Stderr, "Captured %d entries (%s of %s) to %s, %s\n",
		head.EntryCount, safe(head.Source.Scope), safe(head.Source.Base), o.output, safe(head.Checksum))
	return nil
}

func copyFailure(ctx context.Context, dest string, err error) *ExitError {
	if ctx.Err() != nil {
		return transportFailure(ctx, "writing the snapshot to "+dest, err)
	}
	return failf("incomplete_snapshot", "the snapshot did not reach %s whole: %v", dest, err)
}

// snapshotHead is what a finished snapshot says about itself, read without
// holding its entries in memory.
type snapshotHead struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	Kind       string `json:"kind"`
	EntryCount int    `json:"entryCount"`
	Checksum   string `json:"checksum"`
	Source     struct {
		Base           string `json:"base"`
		Scope          string `json:"scope"`
		SubschemaEntry string `json:"subschemaEntry"`
		// A configuration snapshot's (1.13).
		Provider string `json:"provider"`
		Root     string `json:"root"`
	} `json:"source"`
	// A schema snapshot's (1.10).
	Completeness string `json:"completeness"`
	Counts       struct {
		AttributeTypes int `json:"attributeTypes"`
		ObjectClasses  int `json:"objectClasses"`
		Unparsed       int `json:"unparsed"`
		// A configuration snapshot's (1.13).
		Settings  int `json:"settings"`
		Resources int `json:"resources"`
		Withheld  int `json:"withheld"`
	} `json:"counts"`
}

// readSnapshotHead walks a whole document, which is also the proof that it is
// one: a response cut short fails here, before the file gets its name. It does
// not validate the snapshot -- that is the server's to do, and it already did.
func readSnapshotHead(r io.Reader) (snapshotHead, error) {
	var head snapshotHead
	dec := json.NewDecoder(bufio.NewReaderSize(r, 1<<16))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return head, errors.New("it is not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return head, err
		}
		key, _ := tok.(string)
		switch key {
		case "format":
			err = dec.Decode(&head.Format)
		case "version":
			err = dec.Decode(&head.Version)
		case "kind":
			err = dec.Decode(&head.Kind)
		case "completeness":
			err = dec.Decode(&head.Completeness)
		case "counts":
			err = dec.Decode(&head.Counts)
		case "entryCount":
			err = dec.Decode(&head.EntryCount)
		case "checksum":
			err = dec.Decode(&head.Checksum)
		case "source":
			err = dec.Decode(&head.Source)
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return head, err
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return head, errors.New("the document ends early")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return head, errors.New("there is more after the document")
	}
	if head.Format != "alder-snapshot" {
		return head, fmt.Errorf("its format is %q, not alder-snapshot", head.Format)
	}
	return head, nil
}

// skipValue reads past one JSON value token by token, so a large one is never
// held whole.
func skipValue(dec *json.Decoder) error {
	depth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}
