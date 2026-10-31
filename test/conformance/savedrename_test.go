//go:build conformance

package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// A saved plan that renames or moves, against both real directories
// (docs/proposals/saved-renames.md): bound to the entry's identity
// (entryUUID on OpenLDAP, nsUniqueId on 389 DS) and to how many entries are
// below it, never to what the rest of the entry holds.

const moveOU = "ou=alder-move," + suffix

func moveDN(rdns ...string) string {
	if len(rdns) == 0 {
		return moveOU
	}
	return strings.Join(rdns, ",") + "," + moveOU
}

// clearMoveOU deletes everything these cases can leave, deepest first.
func clearMoveOU(t *testing.T, sess directory.Session) {
	t.Helper()
	for _, d := range []string{
		moveDN("uid=mv-new", "ou=mv-team", "ou=mv-to"), moveDN("uid=mv-b", "ou=mv-team", "ou=mv-to"),
		moveDN("uid=mv-a", "ou=mv-team", "ou=mv-to"), moveDN("ou=mv-team", "ou=mv-to"),
		moveDN("uid=mv-new", "ou=mv-team"), moveDN("uid=mv-b", "ou=mv-team"), moveDN("uid=mv-a", "ou=mv-team"),
		moveDN("uid=mv-leaf", "ou=mv-to"), moveDN("uid=mv-leaf"),
		moveDN("ou=mv-team"), moveDN("ou=mv-to"), moveOU,
	} {
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, d), Type: directory.ChangeDelete})
	}
}

func orgUnit(t *testing.T, dnText, ou string) directory.ChangeRecord {
	t.Helper()
	return directory.ChangeRecord{DN: mustDN(t, dnText), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
		{Name: "ou", Values: [][]byte{[]byte(ou)}},
	}}
}

// seedMoveOU builds:
//
//	ou=alder-move
//	  uid=mv-leaf
//	  ou=mv-to
//	  ou=mv-team
//	    uid=mv-a
//	    uid=mv-b
func seedMoveOU(t *testing.T, sess directory.Session) {
	t.Helper()
	clearMoveOU(t, sess)
	t.Cleanup(func() { clearMoveOU(t, sess) })
	mustApply(t, sess, orgUnit(t, moveOU, "alder-move"))
	mustApply(t, sess, orgUnit(t, moveDN("ou=mv-to"), "mv-to"))
	mustApply(t, sess, orgUnit(t, moveDN("ou=mv-team"), "mv-team"))
	mustApply(t, sess, person(t, moveDN("uid=mv-leaf"), "mv-leaf"))
	mustApply(t, sess, person(t, moveDN("uid=mv-a", "ou=mv-team"), "mv-a"))
	mustApply(t, sess, person(t, moveDN("uid=mv-b", "ou=mv-team"), "mv-b"))
}

// saveMove writes an LDIF of one move and saves its plan through a fresh
// Alder, returning the plan file and that Alder.
func saveMove(t *testing.T, s server, from, newRDN, newSuperior string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	ldif := filepath.Join(dir, "move.ldif")
	text := "dn: " + from + "\nchangetype: modrdn\nnewrdn: " + newRDN + "\ndeleteoldrdn: 1\nnewsuperior: " + newSuperior + "\n"
	if err := os.WriteFile(ldif, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	base := startAlder(t)
	saved := filepath.Join(dir, "move.plan.json")
	r := alderCLI(t, base, s, cliOpts{}, "plan", ldif, "--out", saved)
	wantExit(t, r, cli.ExitOK, "plan --out")
	return saved, base
}

func TestASavedRenameOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		leaf, leafMoved := moveDN("uid=mv-leaf"), moveDN("uid=mv-leaf", "ou=mv-to")
		apply := func(t *testing.T, base, saved string) cliResult {
			t.Helper()
			return alderCLI(t, base, s, cliOpts{}, "apply", "--plan", saved, "--yes")
		}

		t.Run("a leaf move, applied through another Alder", func(t *testing.T) {
			seedMoveOU(t, sess)
			saved, _ := saveMove(t, s, leaf, "uid=mv-leaf", moveDN("ou=mv-to"))
			text, err := os.ReadFile(saved)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ToLower(string(text)), strings.ToLower(s.identityAttribute())) {
				t.Errorf("the saved rename does not expect %s:\n%s", s.identityAttribute(), text)
			}
			if !exists(t, sess, leaf) || exists(t, sess, leafMoved) {
				t.Fatal("saving the plan moved the entry")
			}
			wantExit(t, apply(t, startAlder(t), saved), cli.ExitOK, "apply --plan")
			if exists(t, sess, leaf) || !exists(t, sess, leafMoved) {
				t.Error("the saved move did not move the entry")
			}
		})

		t.Run("an unrelated edit since does not stop it", func(t *testing.T) {
			seedMoveOU(t, sess)
			saved, base := saveMove(t, s, leaf, "uid=mv-leaf", moveDN("ou=mv-to"))
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, leaf), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: "title", Values: [][]byte{[]byte("edited since")}}}})
			wantExit(t, apply(t, base, saved), cli.ExitOK, "apply --plan after an unrelated edit")
			if !exists(t, sess, leafMoved) {
				t.Error("the saved move did not move the entry")
			}
		})

		t.Run("another entry given its name since", func(t *testing.T) {
			seedMoveOU(t, sess)
			saved, base := saveMove(t, s, leaf, "uid=mv-leaf", moveDN("ou=mv-to"))
			// The same DN and the same content, and not the entry reviewed.
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, leaf), Type: directory.ChangeDelete})
			mustApply(t, sess, person(t, leaf, "mv-leaf"))
			wantExit(t, apply(t, base, saved), cli.ExitStale, "apply --plan after the entry was replaced")
			if !exists(t, sess, leaf) || exists(t, sess, leafMoved) {
				t.Error("a saved move that no longer holds moved an entry")
			}
		})

		t.Run("the target taken since", func(t *testing.T) {
			seedMoveOU(t, sess)
			saved, base := saveMove(t, s, leaf, "uid=mv-leaf", moveDN("ou=mv-to"))
			mustApply(t, sess, person(t, leafMoved, "mv-leaf"))
			wantExit(t, apply(t, base, saved), cli.ExitStale, "apply --plan after the target was taken")
			if !exists(t, sess, leaf) {
				t.Error("a saved move that no longer holds moved the entry")
			}
		})

		t.Run("the new parent removed since", func(t *testing.T) {
			seedMoveOU(t, sess)
			saved, base := saveMove(t, s, leaf, "uid=mv-leaf", moveDN("ou=mv-to"))
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, moveDN("ou=mv-to")), Type: directory.ChangeDelete})
			// Refused before anything is written (4), not partway through (6).
			r := apply(t, base, saved)
			wantExit(t, r, cli.ExitStale, "apply --plan after the new parent was removed")
			if !strings.Contains(r.stdout+r.stderr, "parent_missing") {
				t.Errorf("the refusal does not name parent_missing:\n%s\n%s", r.stdout, r.stderr)
			}
			if !exists(t, sess, leaf) {
				t.Error("the entry moved")
			}
		})

		team, teamMoved := moveDN("ou=mv-team"), moveDN("ou=mv-team", "ou=mv-to")

		t.Run("a branch move, its entries with it", func(t *testing.T) {
			seedMoveOU(t, sess)
			saved, base := saveMove(t, s, team, "ou=mv-team", moveDN("ou=mv-to"))
			text, err := os.ReadFile(saved)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(text), `"subordinates": 2`) {
				t.Errorf("the saved move does not expect the 2 entries below it:\n%s", text)
			}
			wantExit(t, apply(t, base, saved), cli.ExitOK, "apply --plan of a branch")
			for _, d := range []string{teamMoved, moveDN("uid=mv-a", "ou=mv-team", "ou=mv-to"), moveDN("uid=mv-b", "ou=mv-team", "ou=mv-to")} {
				if !exists(t, sess, d) {
					t.Errorf("%s is not there after the branch moved", d)
				}
			}
		})

		t.Run("an entry added below the branch since", func(t *testing.T) {
			seedMoveOU(t, sess)
			saved, base := saveMove(t, s, team, "ou=mv-team", moveDN("ou=mv-to"))
			mustApply(t, sess, person(t, moveDN("uid=mv-new", "ou=mv-team"), "mv-new"))
			wantExit(t, apply(t, base, saved), cli.ExitStale, "apply --plan after an entry was added below")
			if !exists(t, sess, team) || exists(t, sess, teamMoved) {
				t.Error("a saved move that no longer holds moved the branch")
			}
		})
	})
}

// identityAttribute is the attribute the server keeps an entry's identity in.
func (s server) identityAttribute() string {
	if strings.Contains(strings.ToLower(s.name), "389") {
		return "nsUniqueId"
	}
	return "entryUUID"
}
