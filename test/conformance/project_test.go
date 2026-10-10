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

		// A plan writes nothing.
		if got := titleOf(t, sess); got != "Before" {
			t.Errorf("the probe's title is %q after planning; a plan must not write", got)
		}
		if exists(t, sess, cliDN("uid=cli-new")) {
			t.Error("the new entry exists after planning; a plan must not write")
		}
	})
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
