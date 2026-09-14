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

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/envflags"
)

type snapshotOptions struct {
	base, scope, filter string
	operational         bool
	output              string
	force               bool
}

func snapshotCmd(env *Env) *cobra.Command {
	var conn connection
	var o snapshotOptions
	cmd := command(env, &cobra.Command{
		Use:   "snapshot --base DN --output FILE",
		Short: "Capture a subtree as an Alder snapshot",
		Long: "Captures a subtree through a running Alder server and writes the snapshot\n" +
			"exactly as Alder produced it: an alder-snapshot version 1 document, the same\n" +
			"file the web interface downloads. Sensitive attributes are a count of values,\n" +
			"never a value. A capture that cannot read the whole subtree fails; it never\n" +
			"writes part of one.\n\n" +
			"--output - writes the snapshot to standard output and nothing else. A file is\n" +
			"written to a temporary name and renamed when complete, and an existing file is\n" +
			"never replaced without --force.",
		Args: argsBetween(0, 0, "no arguments"),
	}, func(ctx context.Context, _ []string) error {
		return runSnapshot(ctx, env, &conn, o)
	})
	conn.registerAPI(cmd)
	conn.registerDirectory(cmd)
	f := cmd.Flags()
	f.StringVar(&o.base, "base", "", "the DN of the subtree to capture")
	f.StringVar(&o.scope, "scope", "sub", "sub, one or base")
	f.StringVar(&o.filter, "filter", "", "an RFC 4515 filter; the default captures every entry in scope")
	f.BoolVar(&o.operational, "operational", false, "also capture operational attributes")
	f.StringVarP(&o.output, "output", "o", "", "the file to write, or - for standard output")
	f.BoolVar(&o.force, "force", false, "replace --output if it already exists")
	envflags.Exclude(f, "base", "scope", "filter", "operational", "output", "force")
	return cmd
}

func runSnapshot(ctx context.Context, env *Env, conn *connection, o snapshotOptions) error {
	if o.base == "" {
		return usagef("--base is required: the DN of the subtree to capture")
	}
	if o.output == "" {
		return usagef("--output is required: a file to write, or - for standard output")
	}
	switch o.scope {
	case "sub", "one", "base":
	default:
		return usagef("--scope must be sub, one or base, not %q", o.scope)
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

	scope := api.SnapshotScope(o.scope)
	req := api.SnapshotCaptureRequest{Base: o.base, Scope: &scope}
	if o.filter != "" {
		req.Filter = &o.filter
	}
	if o.operational {
		req.OperationalAttributes = ptr(true)
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
	EntryCount int    `json:"entryCount"`
	Checksum   string `json:"checksum"`
	Source     struct {
		Base  string `json:"base"`
		Scope string `json:"scope"`
	} `json:"source"`
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
