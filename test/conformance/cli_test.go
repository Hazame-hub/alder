//go:build conformance

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// 1.8: the command-line client, against the real API server and both real
// directories.
//
// internal/cli's own tests hold the client to its contract against a stub. These
// hold it to the thing that matters: that a person at a shell, driving the real
// server, gets the same Snapshot, Diff, Plan and Apply as the web interface --
// and that where the client and the API are asked the same question, they give
// the same answer.

const cliOU = "ou=alder-cli," + suffix

func cliDN(rdn string) string { return rdn + "," + cliOU }

func clearCLIOU(t *testing.T, sess directory.Session) {
	t.Helper()
	for _, rdn := range []string{"uid=cli-probe", "uid=cli-extra", "uid=cli-new"} {
		_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, cliDN(rdn)), Type: directory.ChangeDelete})
	}
	_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, cliOU), Type: directory.ChangeDelete})
}

func seedCLIOU(t *testing.T, sess directory.Session) {
	t.Helper()
	clearCLIOU(t, sess)
	t.Cleanup(func() { clearCLIOU(t, sess) })
	mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, cliOU), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
		{Name: "ou", Values: [][]byte{[]byte("alder-cli")}},
	}})
	mustApply(t, sess, person(t, cliDN("uid=cli-probe"), "cli-probe", [2]string{"title", "Before"}))
}

func setTitle(t *testing.T, sess directory.Session, title string) {
	t.Helper()
	mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, cliDN("uid=cli-probe")), Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: "title", Values: [][]byte{[]byte(title)}}}})
}

func titleOf(t *testing.T, sess directory.Session) string {
	t.Helper()
	e, err := sess.Read(ctx(t), mustDN(t, cliDN("uid=cli-probe")), []string{"title"})
	if err != nil {
		t.Fatalf("reading the probe: %v", err)
	}
	v := e.Get("title")
	if len(v) != 1 {
		return fmt.Sprintf("%d titles", len(v))
	}
	return string(v[0])
}

func exists(t *testing.T, sess directory.Session, dnText string) bool {
	t.Helper()
	_, err := sess.Read(ctx(t), mustDN(t, dnText), []string{"objectClass"})
	return err == nil
}

type cliOpts struct {
	stdin       io.Reader
	interactive bool
}

type cliResult struct {
	code           int
	stdout, stderr string
}

// alderCLI runs one client command the way a person types it, against the real
// server at base, connected to s.
func alderCLI(t *testing.T, base string, s server, o cliOpts, args ...string) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	stdin := o.stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	env := &cli.Env{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Getenv: func(k string) (string, bool) {
			if k == "ALDER_BIND_PASSWORD" {
				return s.bindPW, true
			}
			return "", false
		},
		Interactive: func() bool { return o.interactive },
		Version:     "conformance",
	}
	root := &cobra.Command{Use: "alder"}
	root.AddCommand(cli.Commands(env)...)
	connection := []string{"--api-url", base, "--host", s.host, "--port", strconv.Itoa(s.port),
		"--ca-file", filepath.Join("..", "compose", "certs", "ca.crt"), "--server-name", "localhost", "--bind-dn", s.bindDN}
	full := append([]string{args[0]}, append(connection, args[1:]...)...)
	code := cli.Execute(t.Context(), root, env, full)
	r := cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
	if strings.Contains(r.stdout, s.bindPW) || strings.Contains(r.stderr, s.bindPW) {
		t.Errorf("the bind password was printed")
	}
	return r
}

func wantExit(t *testing.T, r cliResult, want int, what string) {
	t.Helper()
	if r.code != want {
		t.Fatalf("%s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", what, r.code, want, r.stdout, r.stderr)
	}
}

func decodeMap(t *testing.T, doc string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, doc)
	}
	return m
}

// oneShotReader runs fn the first time it is read: the moment a person reads
// the plan and starts to type an answer.
type oneShotReader struct {
	once sync.Once
	fn   func()
	r    io.Reader
}

func (o *oneShotReader) Read(p []byte) (int, error) {
	o.once.Do(o.fn)
	return o.r.Read(p)
}

func TestCLISnapshotDiffPlanApply(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := startAlder(t)
		seedCLIOU(t, sess)
		dir := t.TempDir()
		run := func(o cliOpts, args ...string) cliResult { return alderCLI(t, base, s, o, args...) }

		// 1. A snapshot to a file.
		baseline := filepath.Join(dir, "baseline.json")
		r := run(cliOpts{}, "snapshot", "--base", cliOU, "--output", baseline)
		wantExit(t, r, cli.ExitOK, "snapshot")
		data, err := os.ReadFile(baseline)
		if err != nil {
			t.Fatal(err)
		}
		snap := decodeMap(t, string(data))
		if snap["format"] != "alder-snapshot" || snap["version"] != float64(1) || snap["kind"] != "data" ||
			snap["completeness"] != "complete" || snap["entryCount"] != float64(2) {
			t.Fatalf("the file is not a complete version 1 snapshot of the two entries:\n%s", data)
		}

		// 2. The same state compares equal.
		wantExit(t, run(cliOpts{}, "diff", baseline, "@live"), cli.ExitOK, "diff of an unchanged directory")

		// 3-4. The directory drifts, and the comparison says so.
		setTitle(t, sess, "After")
		r = run(cliOpts{}, "diff", "@live", baseline, "--json")
		wantExit(t, r, cli.ExitDifferences, "diff after the drift")
		d := decodeMap(t, r.stdout)
		if c := d["counts"].(map[string]any); c["modified"] != float64(1) || c["added"] != float64(0) || c["removed"] != float64(0) {
			t.Fatalf("counts = %v", c)
		}

		// 5. LDIF that restores it, written with Windows line endings.
		restore := filepath.Join(dir, "restore.ldif")
		ldif := "dn: " + cliDN("uid=cli-probe") + "\r\nchangetype: modify\r\nreplace: title\r\ntitle: Before\r\n-\r\n"
		if err := os.WriteFile(restore, []byte(ldif), 0o600); err != nil {
			t.Fatal(err)
		}

		// 6. The plan shows the modification.
		r = run(cliOpts{}, "plan", restore, "--json")
		wantExit(t, r, cli.ExitOK, "plan")
		p := decodeMap(t, r.stdout)
		items := p["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["action"] != "modify" {
			t.Fatalf("plan = %s", r.stdout)
		}

		// 7. Without a terminal and without --yes, nothing is written.
		wantExit(t, run(cliOpts{}, "apply", restore), cli.ExitNotConfirmed, "apply without confirmation")
		if got := titleOf(t, sess); got != "After" {
			t.Fatalf("an unconfirmed apply wrote: title is %q", got)
		}

		// 8. With --yes, the plan is applied.
		r = run(cliOpts{}, "apply", restore, "--yes")
		wantExit(t, r, cli.ExitOK, "apply --yes")
		if got := titleOf(t, sess); got != "Before" {
			t.Fatalf("title is %q after the apply", got)
		}

		// 9. The directory matches the baseline again.
		wantExit(t, run(cliOpts{}, "diff", "@live", baseline), cli.ExitOK, "diff after the restore")

		// A plan the directory moves away from while a person reads it is
		// refused, and nothing is written.
		setTitle(t, sess, "Drifted")
		moving := &oneShotReader{fn: func() { setTitle(t, sess, "Moved") }, r: strings.NewReader("y\n")}
		r = run(cliOpts{stdin: moving, interactive: true}, "apply", restore)
		wantExit(t, r, cli.ExitStale, "apply of a plan that went stale")
		if got := titleOf(t, sess); got != "Moved" {
			t.Fatalf("a stale plan wrote: title is %q", got)
		}
		if !strings.Contains(r.stderr, "stale") {
			t.Errorf("stderr = %s", r.stderr)
		}
		setTitle(t, sess, "Before")

		// A file that is not a snapshot, and one edited after capture.
		notJSON := filepath.Join(dir, "broken.json")
		_ = os.WriteFile(notJSON, []byte("dn: "+cliOU+"\n"), 0o600)
		wantExit(t, run(cliOpts{}, "diff", notJSON, "@live"), cli.ExitFailed, "diff of a file that is not JSON")
		tampered := filepath.Join(dir, "tampered.json")
		_ = os.WriteFile(tampered, bytes.Replace(data, []byte(`"Before"`), []byte(`"Tampered"`), 1), 0o600)
		r = run(cliOpts{}, "diff", tampered, "@live", "--json")
		wantExit(t, r, cli.ExitFailed, "diff of an edited snapshot")
		if e := decodeMap(t, r.stdout)["error"].(map[string]any); e["error"] != "snapshot_checksum_mismatch" {
			t.Fatalf("stdout = %s", r.stdout)
		}

		// A deletion goes from a comparison to the directory only when it is
		// named as a deletion, twice: selected with --stage-deletion, and
		// permitted with --allow-deletes.
		extra := cliDN("uid=cli-extra")
		mustApply(t, sess, person(t, extra, "cli-extra"))
		asChange := filepath.Join(dir, "as-change.json")
		wantExit(t, run(cliOpts{}, "diff", "@live", baseline, "--stage", extra, "--changes-out", asChange),
			cli.ExitNotApplicable, "a deletion selected with --stage")
		if _, err := os.Stat(asChange); err == nil {
			t.Fatal("a deletion selected as a change was written")
		}
		deletion := filepath.Join(dir, "deletion.json")
		wantExit(t, run(cliOpts{}, "diff", "@live", baseline, "--stage-deletion", extra, "--changes-out", deletion),
			cli.ExitDifferences, "a deletion selected with --stage-deletion")
		wantExit(t, run(cliOpts{}, "apply", "--changes", deletion, "--yes"), cli.ExitNotConfirmed, "a deletion without --allow-deletes")
		if !exists(t, sess, extra) {
			t.Fatal("deleted without --allow-deletes")
		}
		wantExit(t, run(cliOpts{}, "apply", "--changes", deletion, "--yes", "--allow-deletes"), cli.ExitOK, "the deletion")
		if exists(t, sess, extra) {
			t.Fatal("the deletion was not applied")
		}
		wantExit(t, run(cliOpts{}, "diff", "@live", baseline), cli.ExitOK, "diff after the deletion")

		// A comparison that cannot see everything says so in its exit status.
		r = run(cliOpts{}, "diff", baseline, "@live", "--scope", "one", "--json")
		wantExit(t, r, cli.ExitIncomplete, "diff of a subtree snapshot with a one-level live read")
		if decodeMap(t, r.stdout)["complete"] != false {
			t.Fatalf("stdout = %s", r.stdout)
		}
	})
}

// TestCLIAndAPIAgree asks the client and the API the same questions. The client
// passes Alder's answers through; this is what proves it, against the real
// server, rather than against a stub that agrees with it.
func TestCLIAndAPIAgree(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		seedCLIOU(t, sess)
		client, apiBase := alder(t, s)
		cliBase := startAlder(t)
		dir := t.TempDir()
		run := func(args ...string) cliResult { return alderCLI(t, cliBase, s, cliOpts{}, args...) }

		// A snapshot: identical apart from when it was taken.
		r := run("snapshot", "--base", cliOU, "--output", "-")
		wantExit(t, r, cli.ExitOK, "snapshot")
		captured := post(t, client, apiBase+"/snapshots/capture", fmt.Sprintf(`{"base":%q}`, cliOU))
		if captured.status != http.StatusOK {
			t.Fatalf("capture: %d %s", captured.status, captured.body)
		}
		viaCLI, viaAPI := decodeMap(t, r.stdout), decodeMap(t, captured.body)
		delete(viaCLI, "createdAt")
		delete(viaAPI, "createdAt")
		if !reflect.DeepEqual(viaCLI, viaAPI) {
			t.Fatalf("the snapshots differ:\nCLI: %s\nAPI: %s", r.stdout, captured.body)
		}
		snapFile := filepath.Join(dir, "s.json")
		cliSnapshot := r.stdout
		_ = os.WriteFile(snapFile, []byte(cliSnapshot), 0o600)

		// A comparison with something to find. Both are given the same snapshot
		// document: two captures a moment apart differ in createdAt, which the
		// comparison reports, and that would be the captures disagreeing, not the
		// client and the API.
		setTitle(t, sess, "Compared")
		r = run("diff", "@live", snapFile, "--json")
		wantExit(t, r, cli.ExitDifferences, "diff")
		diffed := post(t, client, apiBase+"/diff", `{"source":{"live":{}},"target":{"snapshot":`+cliSnapshot+`}}`)
		if diffed.status != http.StatusOK {
			t.Fatalf("diff: %d %s", diffed.status, diffed.body)
		}
		if a, b := decodeMap(t, r.stdout), decodeMap(t, diffed.body); !reflect.DeepEqual(a, b) {
			t.Fatalf("the comparisons differ:\nCLI: %s\nAPI: %s", r.stdout, diffed.body)
		}

		// A plan, including a password. Baselines are keyed to the process and
		// session that issued them, so they are the one field that must differ.
		const secret = "cli-conformance-not-a-real-password"
		ldif := "dn: " + cliDN("uid=cli-probe") + "\nchangetype: modify\nreplace: title\ntitle: Planned\n-\n\n" +
			"dn: " + cliDN("uid=cli-new") + "\nobjectClass: top\nobjectClass: person\nobjectClass: organizationalPerson\n" +
			"objectClass: inetOrgPerson\nuid: cli-new\ncn: CLI New\nsn: New\nuserPassword: " + secret + "\n"
		ldifFile := filepath.Join(dir, "p.ldif")
		_ = os.WriteFile(ldifFile, []byte(ldif), 0o600)
		r = run("plan", ldifFile, "--json")
		wantExit(t, r, cli.ExitOK, "plan")
		if strings.Contains(r.stdout, secret) || strings.Contains(r.stderr, secret) {
			t.Fatal("the password in the LDIF was printed")
		}
		body, _ := json.Marshal(map[string]any{"ldif": ldif, "mode": "changes", "reconcile": false})
		planned := post(t, client, apiBase+"/plan", string(body))
		if planned.status != http.StatusOK {
			t.Fatalf("plan: %d %s", planned.status, planned.body)
		}
		a, b := decodeMap(t, r.stdout), decodeMap(t, planned.body)
		withoutBaselines(a)
		withoutBaselines(b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("the plans differ:\nCLI: %s\nAPI: %s", r.stdout, planned.body)
		}
		if a["counts"].(map[string]any)["add"] != float64(1) || a["counts"].(map[string]any)["modify"] != float64(1) {
			t.Fatalf("plan counts = %v", a["counts"])
		}
	})
}

func withoutBaselines(v any) {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "baseline")
		for _, child := range x {
			withoutBaselines(child)
		}
	case []any:
		for _, child := range x {
			withoutBaselines(child)
		}
	}
}
