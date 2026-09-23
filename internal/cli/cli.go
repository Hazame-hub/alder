// Package cli is the command-line client in the alder binary: snapshot, diff,
// plan, apply and version.
//
// It is a client of Alder's HTTP API and nothing else. A command that reads or
// writes a directory opens a session on a running Alder the way the connection
// screen does, calls the endpoints the web interface calls, and closes the
// session when it is done; comparing two snapshots needs no session at all. It
// has no LDAP code: it parses no LDIF, compares no values, derives no change and
// writes to no directory. Those have one implementation, on the server, and the
// web interface and this client both ask it.
//
// What the client adds is what a terminal needs and a browser does not: reading
// files and standard input, writing files atomically, exit codes a script can
// branch on, and a stricter safety policy -- asking before a write, requiring
// --allow-deletes before --yes deletes, and refusing a plan that already holds a
// conflict. That policy decides whether a request is sent, never what it does. JSON output is the server's own
// response, passed through, so a script reads exactly what the web interface
// reads.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/envflags"
)

// Exit codes. They are part of the 1.x compatibility promise for the client
// commands, and a script is expected to switch on them.
const (
	// ExitOK: the command did what it was asked. For diff, the comparison was
	// complete and found no difference.
	ExitOK = 0
	// ExitDifferences: diff completed and found differences.
	ExitDifferences = 1
	// ExitIncomplete: diff could not see everything, so what it reports is not
	// the whole answer. It wins over ExitDifferences.
	ExitIncomplete = 2
	// ExitNotApplicable: the plan holds a change that cannot be applied as
	// written (a conflict or a schema violation), or a selected difference
	// offers no change. Nothing was written.
	ExitNotApplicable = 3
	// ExitStale: the directory, or the operation sent, no longer matches the
	// plan that was reviewed (plan_stale, plan_mismatch). Nothing was written.
	ExitStale = 4
	// ExitNotConfirmed: the apply was declined, or needed a confirmation that
	// was not given. Nothing was written.
	ExitNotConfirmed = 5
	// ExitPartial: an apply stopped after writing some of its changes.
	ExitPartial = 6
	// ExitUsage: the command line was wrong. Nothing was sent.
	ExitUsage = 7
	// ExitFailed: anything else -- Alder or the directory refused, could not be
	// reached, or a file could not be read or written.
	ExitFailed = 8
)

// ExitError is how a command reports an outcome other than success.
type ExitError struct {
	Code int
	// Message is for a person, printed to standard error. Empty prints nothing,
	// which is how "differences found" is reported.
	Message string
	// Server is the error object Alder returned, verbatim, when the refusal was
	// Alder's own.
	Server json.RawMessage
	// Local is a stable code for a refusal the client made itself.
	Local string
}

func (e *ExitError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// document is the JSON a --json command writes to standard output instead of
// its result. Alder's own error object is passed through as it came; a refusal
// the client made itself uses the same two fields, error and message, and says
// where it came from.
func (e *ExitError) document() json.RawMessage {
	if len(e.Server) > 0 {
		return e.Server
	}
	doc, _ := json.Marshal(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Origin  string `json:"origin"`
	}{Error: e.Local, Message: e.Message, Origin: "cli"})
	return doc
}

func usagef(format string, a ...any) *ExitError {
	return &ExitError{Code: ExitUsage, Local: "usage", Message: fmt.Sprintf(format, a...)}
}

func failf(local, format string, a ...any) *ExitError {
	return &ExitError{Code: ExitFailed, Local: local, Message: fmt.Sprintf(format, a...)}
}

// Env is everything a command reads from or writes to outside itself, so a test
// can supply its own.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Getenv reads the environment.
	Getenv func(string) (string, bool)
	// Interactive reports whether standard input is a terminal a person can
	// answer a question at.
	Interactive func() bool
	// Version is this client's version.
	Version string

	// stdinOwner is what standard input has been given to. It can be read once.
	stdinOwner string
}

// OSEnv is the process's own environment.
func OSEnv(version string) *Env {
	return &Env{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Getenv:      os.LookupEnv,
		Interactive: stdinIsTerminal,
		Version:     version,
	}
}

// claimStdin gives standard input to one purpose, and refuses a second.
func (e *Env) claimStdin(purpose string) error {
	if e.stdinOwner != "" {
		return usagef("standard input is already used for %s, so it cannot also be used for %s",
			e.stdinOwner, purpose)
	}
	e.stdinOwner = purpose
	return nil
}

// Commands are the client commands, for the root command to add.
func Commands(env *Env) []*cobra.Command {
	return []*cobra.Command{
		snapshotCmd(env),
		diffCmd(env),
		packageCmd(env),
		preflightCmd(env),
		keyCmd(env),
		signCmd(env),
		verifyCmd(env),
		planCmd(env),
		applyCmd(env),
		versionCmd(env),
	}
}

// Execute runs root with args and returns the process exit code, having printed
// whatever a failure has to say to standard error.
//
// Errors that are not ExitErrors -- an unknown command, a failure in alder
// serve -- are printed the way Cobra printed them before the client existed and
// exit 1, so nothing about the server's command line changes.
func Execute(ctx context.Context, root *cobra.Command, env *Env, args []string) int {
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.SilenceErrors = true
	root.SilenceUsage = true
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		if exit.Message != "" {
			writef(env.Stderr, "alder: %s\n", exit.Message)
		}
		return exit.Code
	}
	writeln(env.Stderr, "Error:", err.Error())
	return 1
}

// command is the shape every client command shares: a usage error for a bad
// flag, the environment read before running, and a context that Ctrl+C cancels.
func command(env *Env, cmd *cobra.Command, run func(ctx context.Context, args []string) error) *cobra.Command {
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usagef("%v", err)
	})
	cmd.PreRunE = func(c *cobra.Command, _ []string) error {
		if err := envflags.Apply(c.Flags(), env.Getenv); err != nil {
			return usagef("%v", err)
		}
		return nil
	}
	cmd.RunE = func(c *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
		defer stop()
		return run(ctx, args)
	}
	return cmd
}

// argsBetween is a positional-argument check that reports a usage error.
func argsBetween(minimum, maximum int, what string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < minimum || len(args) > maximum {
			return usagef("expected %s, got %d argument(s)", what, len(args))
		}
		return nil
	}
}

// writeDocument writes one JSON document and a newline.
func writeDocument(w io.Writer, doc []byte) error {
	for len(doc) > 0 && (doc[len(doc)-1] == '\n' || doc[len(doc)-1] == '\r' || doc[len(doc)-1] == ' ') {
		doc = doc[:len(doc)-1]
	}
	if _, err := w.Write(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// writef and writeln write text for a person. Human output that cannot be
// written is not worth stopping a command halfway through for; what a script
// depends on -- a JSON document, a snapshot -- is written with writeDocument or
// io.Copy, and checked.
func writef(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

func writeln(w io.Writer, a ...any) { _, _ = fmt.Fprintln(w, a...) }
