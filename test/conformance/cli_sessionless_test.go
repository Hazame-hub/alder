//go:build conformance

package conformance

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// Two snapshots of a real directory, compared through the real server by a
// client that has only the server's address: no directory flags, no password,
// no session.

func alderCLIWithoutDirectory(t *testing.T, base string, stdin string, args ...string) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &cli.Env{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		Getenv:      func(string) (string, bool) { return "", false },
		Interactive: func() bool { return false },
		Version:     "conformance",
	}
	root := &cobra.Command{Use: "alder"}
	root.AddCommand(cli.Commands(env)...)
	code := cli.Execute(t.Context(), root, env, append(args, "--api-url", base))
	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestCLIComparesTwoSnapshotsWithoutADirectory(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := startAlder(t)
		seedCLIOU(t, sess)
		dir := t.TempDir()

		before, after := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")
		wantExit(t, alderCLI(t, base, s, cliOpts{}, "snapshot", "--base", cliOU, "--output", before), cli.ExitOK, "snapshot before")
		setTitle(t, sess, "After")
		wantExit(t, alderCLI(t, base, s, cliOpts{}, "snapshot", "--base", cliOU, "--output", after), cli.ExitOK, "snapshot after")
		beforeDoc, _ := os.ReadFile(before)
		afterDoc, _ := os.ReadFile(after)

		r := alderCLIWithoutDirectory(t, base, "", "diff", before, after, "--json")
		wantExit(t, r, cli.ExitDifferences, "diff of two snapshots with no directory flags")

		// The API, asked by a client that never connected.
		res, err := (&http.Client{}).Post(base+"/diff", "application/json",
			strings.NewReader(`{"source":{"snapshot":`+string(beforeDoc)+`},"target":{"snapshot":`+string(afterDoc)+`}}`))
		if err != nil {
			t.Fatal(err)
		}
		direct, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("the API refused two snapshots without a session: %d %s", res.StatusCode, direct)
		}
		if a, b := decodeMap(t, r.stdout), decodeMap(t, string(direct)); !reflect.DeepEqual(a, b) {
			t.Fatalf("the client's comparison is not the API's:\nclient: %s\napi:    %s", r.stdout, direct)
		}
		if c := decodeMap(t, r.stdout)["counts"].(map[string]any); c["modified"] != float64(1) {
			t.Fatalf("counts = %v", c)
		}

		r = alderCLIWithoutDirectory(t, base, string(beforeDoc), "diff", "-", after, "--json")
		wantExit(t, r, cli.ExitDifferences, "diff with a snapshot on standard input and no directory flags")

		// Anything involving the live directory still needs the session.
		r = alderCLIWithoutDirectory(t, base, "", "diff", before, "@live")
		wantExit(t, r, cli.ExitUsage, "diff with @live and no directory flags")
		live, err := (&http.Client{}).Post(base+"/diff", "application/json",
			strings.NewReader(`{"source":{"live":{}},"target":{"snapshot":`+string(beforeDoc)+`}}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = live.Body.Close()
		if live.StatusCode != http.StatusUnauthorized {
			t.Fatalf("a live comparison without a session: %d, want 401", live.StatusCode)
		}
	})
}
