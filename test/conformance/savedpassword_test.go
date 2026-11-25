//go:build conformance

package conformance

import (
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// A saved plan that changes a password, against both real directories
// (docs/proposals/saved-password-changes.md): the file holds the intent and
// the entry's identity, never the password, which alder apply --plan takes
// from a file of its own.

// startAlderLogged is startAlder, with everything the server logs, at debug,
// kept for the test to read.
func startAlderLogged(t *testing.T) (string, *lockedBuffer) {
	t.Helper()
	logs := &lockedBuffer{}
	srv := api.NewServer(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), api.Config{})
	t.Cleanup(srv.Close)
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	srv.Register(app)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return "http://" + ln.Addr().String() + "/api/v1", logs
}

func TestASavedPasswordChangeOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		probe := cliDN("uid=cli-probe")
		const first, next = "Saved-Plan-First-1!", "Saved-Plan-Next-2!"

		setup := func(t *testing.T) (saved, dir string) {
			t.Helper()
			seedCLIOU(t, sess)
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, probe), Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: "userPassword", Values: bs(first)}}})
			dir = t.TempDir()
			changes := filepath.Join(dir, "reset.json")
			if err := os.WriteFile(changes, []byte(`[{"dn":"`+probe+`","type":"setpassword"}]`), 0o600); err != nil {
				t.Fatal(err)
			}
			saved = filepath.Join(dir, "reset.plan.json")
			r := alderCLI(t, startAlder(t), s, cliOpts{}, "plan", "--changes", changes, "--out", saved)
			wantExit(t, r, cli.ExitOK, "plan --out")
			text, err := os.ReadFile(saved)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(text), "newPassword") {
				t.Fatalf("the saved plan holds a password field:\n%s", text)
			}
			if !strings.Contains(string(text), `"name": "entryUUID"`) && !strings.Contains(string(text), `"name": "nsUniqueId"`) {
				t.Errorf("the saved password change does not expect the entry's identity:\n%s", text)
			}
			if bindAs(t, s, probe, first) != nil {
				t.Fatal("saving the plan changed the password")
			}
			return saved, dir
		}
		secrets := func(t *testing.T, dir, content string) string {
			t.Helper()
			path := filepath.Join(dir, "secrets")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}

		t.Run("applied through another Alder, from a file", func(t *testing.T) {
			saved, dir := setup(t)
			base, logs := startAlderLogged(t)
			r := alderCLI(t, base, s, cliOpts{}, "apply", "--plan", saved, "--new-password-file", secrets(t, dir, next+"\n"),
				"--yes", "--json")
			wantExit(t, r, cli.ExitOK, "apply --plan")
			if err := bindAs(t, s, probe, next); err != nil {
				t.Errorf("the new password does not authenticate: %v", err)
			}
			if bindAs(t, s, probe, first) == nil {
				t.Error("the old password still authenticates")
			}
			text, _ := os.ReadFile(saved)
			for what, body := range map[string]string{"the plan file": string(text), "the output": r.stdout + r.stderr,
				"the server log": logs.String()} {
				if strings.Contains(body, next) {
					t.Errorf("%s holds the password", what)
				}
			}
		})

		t.Run("the entry deleted and re-created since", func(t *testing.T) {
			saved, dir := setup(t)
			mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, probe), Type: directory.ChangeDelete})
			mustApply(t, sess, person(t, probe, "cli-probe", [2]string{"userPassword", first}))
			r := alderCLI(t, startAlder(t), s, cliOpts{}, "apply", "--plan", saved, "--new-password-file", secrets(t, dir, next+"\n"), "--yes")
			wantExit(t, r, cli.ExitStale, "apply --plan after the entry was replaced")
			if bindAs(t, s, probe, first) != nil {
				t.Error("a saved password change that no longer holds changed the password")
			}
		})

		t.Run("one password too many", func(t *testing.T) {
			saved, dir := setup(t)
			r := alderCLI(t, startAlder(t), s, cliOpts{}, "apply", "--plan", saved,
				"--new-password-file", secrets(t, dir, next+"\nanother-one\n"), "--yes")
			wantExit(t, r, cli.ExitUsage, "apply --plan with one password too many")
			if bindAs(t, s, probe, first) != nil {
				t.Error("the password changed")
			}
		})
	})
}
