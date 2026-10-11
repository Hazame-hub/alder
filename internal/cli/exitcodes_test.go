package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/apiclient"
)

func planWith(add, unchanged, conflict int) apiclient.Plan {
	return apiclient.Plan{Counts: apiclient.PlanCounts{Add: add, Unchanged: unchanged, Conflict: conflict}}
}

// Every exit path of planExit, with and without --detailed-exitcode.
func TestPlanExit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		plan     apiclient.Plan
		detailed bool
		want     int
	}{
		{"nothing to change", planWith(0, 1, 0), false, ExitOK},
		{"changes pending, without the flag", planWith(1, 0, 0), false, ExitOK},
		{"a conflict, without the flag", planWith(0, 0, 1), false, ExitNotApplicable},
		{"nothing to change, with the flag", planWith(0, 1, 0), true, ExitOK},
		{"an empty plan, with the flag", planWith(0, 0, 0), true, ExitOK},
		{"changes pending, with the flag", planWith(1, 0, 0), true, ExitPending},
		{"a conflict, with the flag", planWith(0, 0, 1), true, ExitNotApplicable},
		{"a conflict beside pending changes wins", planWith(1, 0, 1), true, ExitNotApplicable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := planExit(&Env{Stderr: &stderr}, tc.plan, tc.detailed)
			got := ExitOK
			var exit *ExitError
			if errors.As(err, &exit) {
				got = exit.Code
			} else if err != nil {
				t.Fatalf("planExit returned a non-exit error: %v", err)
			}
			if got != tc.want {
				t.Errorf("exit %d, want %d", got, tc.want)
			}
		})
	}
	if ExitPending != 2 {
		t.Errorf("ExitPending = %d; with --detailed-exitcode, changes pending is 2, as in terraform plan", ExitPending)
	}
}

// execute runs a root built for the test, with one command that does what it
// is told, through Execute.
func execute(t *testing.T, run func() error, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &Env{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(string) (string, bool) { return "", false }, Interactive: func() bool { return false }}
	root := &cobra.Command{Use: "alder"}
	root.AddCommand(&cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return run() }})
	return Execute(context.Background(), root, env, args), stderr.String()
}

func TestAnUnknownCommandExitsSeven(t *testing.T) {
	code, stderr := execute(t, func() error { return nil }, "nosuchcommand")
	if code != ExitUsage {
		t.Errorf("exit %d, want %d (usage)\n%s", code, ExitUsage, stderr)
	}
}

func TestAnUnknownRootFlagExitsSeven(t *testing.T) {
	code, stderr := execute(t, func() error { return nil }, "--nosuchflag")
	if code != ExitUsage {
		t.Errorf("exit %d, want %d (usage)\n%s", code, ExitUsage, stderr)
	}
}

func TestAPlainErrorExitsSeven(t *testing.T) {
	code, _ := execute(t, func() error { return errors.New("refused") }, "probe")
	if code != ExitUsage {
		t.Errorf("exit %d, want %d", code, ExitUsage)
	}
}

// A panic is an internal error, exit 8, never the runtime's exit 2, which
// would read as "changes pending".
func TestAPanicExitsEightAndSaysSo(t *testing.T) {
	code, stderr := execute(t, func() error { panic("probe went wrong") }, "probe")
	if code != ExitFailed {
		t.Errorf("exit %d, want %d", code, ExitFailed)
	}
	if !strings.Contains(stderr, "alder: internal error: probe went wrong") || !strings.Contains(stderr, "goroutine") {
		t.Errorf("stderr does not carry the error and the stack:\n%s", stderr)
	}
}

func TestSuccessStillExitsZero(t *testing.T) {
	if code, stderr := execute(t, func() error { return nil }, "probe"); code != ExitOK {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}
