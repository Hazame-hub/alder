package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every command that writes a file takes --output (-o). plan and project plan
// spelled it --out until 1.43; that spelling still works and is not shown.
func TestPlanTakesOutputAndStillAcceptsOut(t *testing.T) {
	e := &Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Getenv: func(string) (string, bool) { return "", false }}
	root := &cobra.Command{Use: "alder"}
	root.AddCommand(Commands(e)...)
	for _, path := range [][]string{{"plan"}, {"project", "plan"}} {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.Join(path, " ")
		output := cmd.Flags().Lookup("output")
		if output == nil || output.Shorthand != "o" {
			t.Fatalf("%s: no --output/-o flag", name)
		}
		out := cmd.Flags().Lookup("out")
		if out == nil || !out.Hidden {
			t.Fatalf("%s: --out is missing or shown", name)
		}
		if err := cmd.Flags().Set("out", "saved.json"); err != nil {
			t.Fatal(err)
		}
		if output.Value.String() != "saved.json" {
			t.Errorf("%s: --out does not set what --output reads", name)
		}
	}
}

// cobra's "Did you mean" is indented, not printed with a literal \x09, under
// the same prefix as every other error.
func TestAMistypedCommandIsSuggestedReadably(t *testing.T) {
	r := newStub(t).run(t, runOpts{noConnection: true}, "pln")
	expectCode(t, r, ExitUsage)
	if strings.Contains(r.stderr, `\x09`) || !strings.Contains(r.stderr, "\n  plan") {
		t.Errorf("stderr:\n%s", r.stderr)
	}
	if !strings.HasPrefix(r.stderr, "alder: ") {
		t.Errorf("stderr does not start with alder: \n%s", r.stderr)
	}
}
