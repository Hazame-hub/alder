package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
)

// changesFromPlan is web/src/lib/plan.ts in Go. These are that file's cases.

func item(index int, intent api.PlanIntent, record *api.ChangeRequest, baseline *string) api.PlanItem {
	return api.PlanItem{Index: index, Dn: aliceDN, Action: api.PlanActionModify, Intent: &intent, Record: record, Baseline: baseline}
}

func TestChangesFromPlanIsTheWebInterfacesRule(t *testing.T) {
	staged := api.ChangeRequest{Dn: aliceDN, Type: api.ChangeRequestTypeModify, NewRdn: ptr("the staged copy")}
	withheld := api.ChangeRequest{Dn: aliceDN, Type: api.ChangeRequestTypeModify, NewRdn: ptr("the plan's record")}

	t.Run("an exact item is sent from the staged copy with the plan's baseline", func(t *testing.T) {
		got := changesFromPlan(api.Plan{Items: []api.PlanItem{item(0, api.PlanIntentExact, &withheld, ptr("b0"))}}, []api.ChangeRequest{staged})
		if len(got) != 1 || *got[0].NewRdn != "the staged copy" || *got[0].Baseline != "b0" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("items that apply nothing are dropped", func(t *testing.T) {
		got := changesFromPlan(api.Plan{Items: []api.PlanItem{
			item(0, api.PlanIntentExact, nil, nil),
			item(1, api.PlanIntentExact, &staged, ptr("b1")),
		}}, []api.ChangeRequest{staged, staged})
		if len(got) != 1 || *got[0].Baseline != "b1" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a record with no baseline is never sent", func(t *testing.T) {
		if got := changesFromPlan(api.Plan{Items: []api.PlanItem{item(0, api.PlanIntentExact, &staged, nil)}}, []api.ChangeRequest{staged}); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a desired-state item the planner rewrote is sent from the plan's record", func(t *testing.T) {
		got := changesFromPlan(api.Plan{Items: []api.PlanItem{item(0, api.PlanIntentDesired, &withheld, ptr("b"))}}, []api.ChangeRequest{staged})
		if len(got) != 1 || *got[0].NewRdn != "the plan's record" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("the staged copy is not modified", func(t *testing.T) {
		copies := []api.ChangeRequest{staged}
		_ = changesFromPlan(api.Plan{Items: []api.PlanItem{item(0, api.PlanIntentExact, &staged, ptr("b0"))}}, copies)
		if copies[0].Baseline != nil {
			t.Error("the baseline was written into the caller's copy")
		}
	})
}

func TestAFileIsAbsentOrWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub dir ünïcode", "out.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	write := func(content string) func(io.Writer) error {
		return func(w io.Writer) error { _, err := io.WriteString(w, content); return err }
	}

	if err := writeFile(path, false, write("one"), nil); err != nil {
		t.Fatal(err)
	}
	err := writeFile(path, false, write("two"), nil)
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Local != "output_exists" {
		t.Fatalf("replaced an existing file without force: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "one" {
		t.Fatalf("content = %q", got)
	}
	if err := writeFile(path, true, write("three"), nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "three" {
		t.Fatalf("content = %q", got)
	}

	failing := filepath.Join(filepath.Dir(path), "failing.json")
	if err := writeFile(failing, false, func(w io.Writer) error {
		_, _ = io.WriteString(w, "half")
		return errors.New("the source failed")
	}, nil); err == nil {
		t.Fatal("a failed write reported success")
	}
	checked := filepath.Join(filepath.Dir(path), "checked.json")
	if err := writeFile(checked, false, write("{"), func(*os.File) error { return errors.New("not whole") }); err == nil {
		t.Fatal("a failed check reported success")
	}
	assertAbsent(t, failing)
	assertAbsent(t, checked)
	noPartials(t, filepath.Dir(path))
}

func TestDirectoryTextIsMadeSafeForATerminal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Zo\u00eb \u00c5ngstr\u00f6m \u65e5\u672c\u8a9e", "Zo\u00eb \u00c5ngstr\u00f6m \u65e5\u672c\u8a9e"},
		{"a\x1b[2Jb", `a\x1b[2Jb`},
		{"bell\x07", `bell\x07`},
		{"new\nline", `new\x0aline`},
		{"c1\u009b31m", `c1\u009b31m`},
		{"\u202eexe.txt", `\u202eexe.txt`},
		{"a\u200eb\u200fc", `a\u200eb\u200fc`},
		{"\u061cx", `\u061cx`},
		{"isolate\u2066x\u2069", `isolate\u2066x\u2069`},
		{"uid=alice,ou=people,dc=a,dc=b", "uid=alice,ou=people,dc=a,dc=b"},
	}
	for _, tc := range cases {
		if got := safe(tc.in); got != tc.want {
			t.Errorf("safe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if strings.ContainsAny(safe("\x00\x1f\x7f"), "\x00\x1f\x7f") {
		t.Error("a control character survived")
	}
}
