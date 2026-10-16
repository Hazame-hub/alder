//go:build conformance

package conformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// alder plan --out and alder apply --plan, against both real directories: an
// LDIF change set's plan, saved through one Alder and applied through
// another, applies exactly what was saved, a deletion included; overtaken by
// another administrator, on the modified entry or on the deleted one, it is
// refused whole and writes nothing.
func TestASavedChangeSetPlanOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		gone := cliDN("uid=cli-gone")
		setup := func(t *testing.T) (saved string, first string) {
			t.Helper()
			seedCLIOU(t, sess) // uid=cli-probe, with title Before
			mustApply(t, sess, person(t, gone, "cli-gone"))
			dir := t.TempDir()
			ldif := filepath.Join(dir, "change.ldif")
			text := "dn: " + cliDN("uid=cli-probe") + "\nchangetype: modify\nreplace: title\ntitle: After\n-\n\n" +
				"dn: " + gone + "\nchangetype: delete\n"
			if err := os.WriteFile(ldif, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			first = startAlder(t)
			saved = filepath.Join(dir, "change.plan.json")
			r := alderCLI(t, first, s, cliOpts{}, "plan", ldif, "--out", saved)
			wantExit(t, r, cli.ExitOK, "plan --out")
			if got := titleOf(t, sess); got != "Before" || !exists(t, sess, gone) {
				t.Fatalf("saving a plan wrote to the directory: title %q, deleted entry present %v", got, exists(t, sess, gone))
			}
			return saved, first
		}

		t.Run("applied through another Alder", func(t *testing.T) {
			saved, _ := setup(t)
			// Another process, with another baseline key.
			second := startAlder(t)
			r := alderCLI(t, second, s, cliOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
			wantExit(t, r, cli.ExitOK, "apply --plan")
			if got := titleOf(t, sess); got != "After" {
				t.Errorf("the probe's title is %q after applying the saved plan, want After", got)
			}
			if exists(t, sess, gone) {
				t.Error("the entry the saved plan deletes still exists")
			}
		})

		t.Run("the modified entry moved", func(t *testing.T) {
			saved, base := setup(t)
			setTitle(t, sess, "Elsewhere")
			r := alderCLI(t, base, s, cliOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
			wantExit(t, r, cli.ExitStale, "apply --plan after the modified entry moved")
			if got := titleOf(t, sess); got != "Elsewhere" || !exists(t, sess, gone) {
				t.Errorf("a saved plan that no longer holds wrote: title %q, deleted entry present %v", got, exists(t, sess, gone))
			}
		})

		t.Run("the deleted entry moved", func(t *testing.T) {
			saved, base := setup(t)
			// The reviewer approved deleting the entry as it was.
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, gone), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModAdd, Name: "description", Values: [][]byte{[]byte("added since")}}}})
			r := alderCLI(t, base, s, cliOpts{}, "apply", "--plan", saved, "--yes", "--allow-deletes")
			wantExit(t, r, cli.ExitStale, "apply --plan after the deleted entry moved")
			if got := titleOf(t, sess); got != "Before" || !exists(t, sess, gone) {
				t.Errorf("a saved plan that no longer holds wrote: title %q, deleted entry present %v", got, exists(t, sess, gone))
			}
		})
	})
}
