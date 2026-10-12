//go:build conformance

package conformance

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/cli"
	"github.com/hazame-hub/alder/internal/directory"
)

// An Alder project, planned against both real directories through the real
// server: the first slice of docs/PROJECT.md, end to end. internal/cli's tests
// hold the command to its contract against a stub; this holds it to what the
// directory says.
func TestAProjectPlansAsDesiredStateOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := startAlder(t)
		seedCLIOU(t, sess) // uid=cli-probe, with title Before
		mustApply(t, sess, person(t, cliDN("uid=cli-extra"), "cli-extra"))

		dir := writeHarnessProject(t, s, base)

		r := runProject(t, s, "project", "validate", "--project", dir)
		wantExit(t, r, cli.ExitOK, "validate")

		r = runProject(t, s, "project", "plan", "--project", dir, "--env", "harness", "--json")
		wantExit(t, r, cli.ExitOK, "plan")
		doc := decodeMap(t, r.stdout)
		if doc["environment"] != "harness" || doc["host"] != s.host+":"+strconv.Itoa(s.port) {
			t.Errorf("environment = %v, host = %v", doc["environment"], doc["host"])
		}
		plan, _ := doc["plan"].(map[string]any)
		actions := map[string]string{}
		for _, raw := range plan["items"].([]any) {
			item := raw.(map[string]any)
			actions[strings.ToLower(item["dn"].(string))] = item["action"].(string)
		}
		want := map[string]string{
			strings.ToLower(cliDN("uid=cli-probe")): "modify",
			strings.ToLower(cliDN("uid=cli-new")):   "add",
		}
		for d, action := range want {
			if actions[d] != action {
				t.Errorf("%s: planned %q, want %q (plan: %v)", d, actions[d], action, actions)
			}
		}
		if len(actions) != len(want) {
			t.Errorf("planned %d items, want %d: %v", len(actions), len(want), actions)
		}

		// The OU and an entry no file mentions are reported as unmanaged,
		// the probe the project describes is not, and none of them is
		// planned: a project never deletes what it does not mention.
		unmanaged, _ := doc["unmanaged"].([]any)
		if len(unmanaged) != 1 {
			t.Fatalf("unmanaged = %v", doc["unmanaged"])
		}
		sub := unmanaged[0].(map[string]any)
		var got []string
		for _, d := range sub["entries"].([]any) {
			got = append(got, strings.ToLower(d.(string)))
		}
		wantUnmanaged := []string{strings.ToLower(cliOU), strings.ToLower(cliDN("uid=cli-extra"))}
		if sub["error"] != nil || strings.Join(got, "|") != strings.Join(wantUnmanaged, "|") {
			t.Errorf("unmanaged entries = %v (error %v), want %v", got, sub["error"], wantUnmanaged)
		}

		// A plan writes nothing.
		if got := titleOf(t, sess); got != "Before" {
			t.Errorf("the probe's title is %q after planning; a plan must not write", got)
		}
		if exists(t, sess, cliDN("uid=cli-new")) {
			t.Error("the new entry exists after planning; a plan must not write")
		}
	})
}

// alder project apply, against both real directories: the project lands, the
// directory says so, and planning again with --detailed-exitcode says there
// is nothing left to change -- the environment matches the repository.
func TestAProjectAppliesAndThenMatchesOnBothServers(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := startAlder(t)
		seedCLIOU(t, sess) // uid=cli-probe, with title Before
		dir := writeHarnessProject(t, s, base)

		r := runProject(t, s, "project", "apply", "--project", dir, "--env", "harness", "--yes", "--json")
		wantExit(t, r, cli.ExitOK, "apply")
		doc := decodeMap(t, r.stdout)
		if doc["environment"] != "harness" || doc["result"] == nil {
			t.Errorf("apply JSON = %v", doc)
		}

		// The directory, not Alder, says it landed.
		if got := titleOf(t, sess); got != "After" {
			t.Errorf("the probe's title is %q after apply, want After", got)
		}
		if !exists(t, sess, cliDN("uid=cli-new")) {
			t.Error("the new entry does not exist after apply")
		}

		// And the environment now matches the project.
		r = runProject(t, s, "project", "plan", "--project", dir, "--env", "harness", "--detailed-exitcode", "--summary")
		wantExit(t, r, cli.ExitOK, "plan after apply")

		// Applying again is a no-op, not a second change.
		r = runProject(t, s, "project", "apply", "--project", dir, "--env", "harness", "--yes")
		wantExit(t, r, cli.ExitOK, "second apply")
		if !strings.Contains(r.stdout, "Nothing to apply") {
			t.Errorf("a second apply should have nothing to do:\n%s", r.stdout)
		}
	})
}

// writeHarnessProject lays out a project against the harness server s,
// through the Alder at base: the probe as seeded but for its title, and an
// entry the directory does not have.
func writeHarnessProject(t *testing.T, s server, base string) string {
	t.Helper()
	ca, err := filepath.Abs(filepath.Join("..", "compose", "certs", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, dir, "alder.yaml", `version: 1
managed:
  - base: `+cliOU+`
    files: [entries/*.ldif]
environments:
  harness:
    api-url: `+base+`
    host: `+s.host+`
    port: `+strconv.Itoa(s.port)+`
    ca-file: `+filepath.ToSlash(ca)+`
    server-name: localhost
    bind-dn: `+s.bindDN+`
    bind-password-env: ALDER_HARNESS_BIND_PASSWORD
`)
	// The probe as the project wants it, and an entry the directory does
	// not have yet. Nothing about the OU itself is said, so nothing is
	// planned for it.
	writeFile(t, dir, "entries/a-probe.ldif", "dn: "+cliDN("uid=cli-probe")+`
objectClass: top
objectClass: person
objectClass: organizationalPerson
objectClass: inetOrgPerson
uid: cli-probe
cn: Snapshot cli-probe
sn: Snapshot
title: After
`)
	writeFile(t, dir, "entries/b-new.ldif", "dn: "+cliDN("uid=cli-new")+`
objectClass: top
objectClass: person
objectClass: organizationalPerson
objectClass: inetOrgPerson
uid: cli-new
cn: CLI New
sn: New
`)
	return dir
}

// runProject runs a project command as a person types it. Unlike alderCLI it
// adds no connection flags: a project's connection comes from its alder.yaml.
func runProject(t *testing.T, s server, args ...string) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := &cli.Env{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(k string) (string, bool) {
			if k == "ALDER_HARNESS_BIND_PASSWORD" {
				return s.bindPW, true
			}
			return "", false
		},
		Interactive: func() bool { return false },
		Version:     "conformance",
	}
	root := &cobra.Command{Use: "alder"}
	root.AddCommand(cli.Commands(env)...)
	code := cli.Execute(t.Context(), root, env, args)
	r := cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
	if strings.Contains(r.stdout, s.bindPW) || strings.Contains(r.stderr, s.bindPW) {
		t.Errorf("the bind password was printed")
	}
	return r
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
