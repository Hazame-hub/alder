package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestAWrongCommandLineExitsSevenAndSendsNothing(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"diff needs two sides", []string{"diff", "a.json"}},
		{"only one side is live", []string{"diff", "@live", "@live"}},
		{"live flags need a live side", []string{"diff", "a.json", "b.json", "--scope", "one"}},
		{"snapshot needs an output", []string{"snapshot", "--base", "dc=example,dc=test"}},
		{"snapshot needs a base", []string{"snapshot", "--output", "-"}},
		{"snapshot scope", []string{"snapshot", "--base", "dc=example,dc=test", "--output", "-", "--scope", "everything"}},
		{"plan needs an input", []string{"plan"}},
		{"plan takes one input", []string{"plan", "a.ldif", "--changes", "c.json"}},
		{"mode reads LDIF only", []string{"plan", "--changes", "c.json", "--mode", "desired"}},
		{"mode has two values", []string{"plan", "a.ldif", "--mode", "guess"}},
		{"no password flag", []string{"plan", "a.ldif", "--password", "x"}},
		{"stage needs changes-out", []string{"diff", "@live", "s.json", "--stage", "uid=a"}},
		{"stage needs a live source", []string{"diff", "s.json", "@live", "--stage", "uid=a", "--changes-out", "c.json"}},
		{"changes-out needs a selection", []string{"diff", "@live", "s.json", "--changes-out", "c.json"}},
		{"changes-out is a file", []string{"diff", "@live", "s.json", "--stage", "uid=a", "--changes-out", "-"}},
		{"two readers of stdin", []string{"diff", "-", "-"}},
		{"tls mode", []string{"plan", "a.ldif", "--tls", "maybe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStub(t)
			r := s.run(t, runOpts{}, tc.args...)
			expectCode(t, r, ExitUsage)
			if n := len(s.called("POST /api/v1/session")); n != 0 {
				t.Errorf("a session was opened for a command line that was wrong")
			}
			if !strings.Contains(r.stderr, "alder: ") {
				t.Errorf("no explanation on standard error:\n%s", r.stderr)
			}
		})
	}
}

func TestTheAlderAddressIsRequiredAndCarriesNoCredential(t *testing.T) {
	s := newStub(t)
	r := s.run(t, runOpts{noConnection: true}, "plan", "a.ldif", "--host", "h", "--bind-dn", "cn=a")
	expectCode(t, r, ExitUsage)
	if !strings.Contains(r.stderr, "--api-url") {
		t.Errorf("stderr = %s", r.stderr)
	}
	r = s.run(t, runOpts{noConnection: true}, "plan", "a.ldif", "--api-url", "https://admin:pw@alder.example.test", "--host", "h")
	expectCode(t, r, ExitUsage)
}

func TestTheAlderAddressMayNameTheAPIOrNot(t *testing.T) {
	for _, suffix := range []string{"", "/", "/api/v1", "/api/v1/"} {
		t.Run(suffix, func(t *testing.T) {
			s := newStub(t)
			s.reply("GET /api/v1/source", http.StatusOK, `{"license":"l","sourceUrl":"u","version":"9.9.9","notice":"n"}`)
			r := s.run(t, runOpts{noConnection: true}, "version", "--api-url", s.srv.URL+suffix)
			expectCode(t, r, ExitOK)
			if !strings.Contains(r.stdout, "server: alder 9.9.9") {
				t.Errorf("stdout = %s", r.stdout)
			}
		})
	}
}

func TestNoFlagCarriesASecret(t *testing.T) {
	env := &Env{Getenv: func(string) (string, bool) { return "", false }}
	for _, cmd := range Commands(env) {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			name := strings.ToLower(f.Name)
			for _, word := range []string{"password", "secret", "token", "credential"} {
				if strings.Contains(name, word) && !strings.HasSuffix(name, "-file") && !strings.HasSuffix(name, "-stdin") {
					t.Errorf("alder %s --%s takes a secret as its value; secrets come from a file, standard input or the environment",
						cmd.Name(), f.Name)
				}
			}
		})
	}
}

func TestTheEnvironmentFillsInConnectionFlagsAndTheFlagWins(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	dir := t.TempDir()
	ldifFile := writeTemp(t, dir, "c.ldif", restoreLDIF)

	// ALDER_API_URL and ALDER_HOST are enough.
	r := s.run(t, runOpts{noConnection: true, env: map[string]string{
		"ALDER_API_URL": s.srv.URL, "ALDER_HOST": "ldap.example.test", "ALDER_BIND_DN": "cn=admin",
	}}, "plan", ldifFile)
	expectCode(t, r, ExitOK)

	// A flag beats a variable naming somewhere that does not answer.
	r = s.run(t, runOpts{noConnection: true, env: map[string]string{"ALDER_API_URL": "http://127.0.0.1:1"}},
		"plan", ldifFile, "--api-url", s.srv.URL, "--host", "ldap.example.test")
	expectCode(t, r, ExitOK)

	var connect map[string]any
	calls := s.called("POST /api/v1/session")
	_ = json.Unmarshal(calls[0].Body, &connect)
	if connect["bindDn"] != "cn=admin" || connect["bindPassword"] != testSecret || connect["port"] != float64(636) || connect["tls"] != "ldaps" {
		t.Errorf("connect request = %v", connect)
	}
}

func TestSafetyFlagsIgnoreTheEnvironment(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/import/ldif", http.StatusOK, importJSON(t))
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	dir := t.TempDir()
	ldifFile := writeTemp(t, dir, "c.ldif", restoreLDIF)
	r := s.run(t, runOpts{env: map[string]string{"ALDER_YES": "true", "ALDER_ALLOW_DELETES": "true"}}, "apply", ldifFile)
	expectCode(t, r, ExitNotConfirmed)
	if len(s.called("POST /api/v1/changeset/apply")) != 0 {
		t.Fatal("an environment variable confirmed an apply")
	}

	existing := writeTemp(t, dir, "out.json", "keep")
	s.reply("POST /api/v1/snapshots/capture", http.StatusOK, snapshotDoc)
	r = s.run(t, runOpts{env: map[string]string{"ALDER_FORCE": "true"}}, "snapshot", "--base", "dc=example,dc=test", "--output", existing)
	expectCode(t, r, ExitUsage)
	if got, _ := os.ReadFile(existing); string(got) != "keep" {
		t.Fatal("an environment variable replaced a file")
	}
}

func TestPasswordsComeFromTheEnvironmentAFileOrStandardInput(t *testing.T) {
	dir := t.TempDir()
	ldifFile := writeTemp(t, dir, "c.ldif", restoreLDIF)
	sent := func(t *testing.T, s *stub) string {
		t.Helper()
		calls := s.called("POST /api/v1/session")
		if len(calls) != 1 {
			t.Fatalf("%d session requests", len(calls))
		}
		var connect map[string]any
		_ = json.Unmarshal(calls[0].Body, &connect)
		pw, _ := connect["bindPassword"].(string)
		return pw
	}
	newPlanStub := func(t *testing.T) *stub {
		s := newStub(t)
		s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
		return s
	}

	t.Run("environment", func(t *testing.T) {
		s := newPlanStub(t)
		expectCode(t, s.run(t, runOpts{}, "plan", ldifFile), ExitOK)
		if sent(t, s) != testSecret {
			t.Error("the password from ALDER_BIND_PASSWORD was not sent")
		}
	})
	t.Run("file written on Windows", func(t *testing.T) {
		s := newPlanStub(t)
		file := writeTemp(t, dir, "pw.txt", testSecret+"\r\n")
		expectCode(t, s.run(t, runOpts{env: map[string]string{"ALDER_BIND_PASSWORD": ""}}, "plan", ldifFile, "--bind-password-file", file), ExitOK)
		if sent(t, s) != testSecret {
			t.Errorf("sent %q", sent(t, s))
		}
	})
	t.Run("standard input", func(t *testing.T) {
		s := newPlanStub(t)
		expectCode(t, s.run(t, runOpts{stdin: testSecret + "\n", env: map[string]string{"ALDER_BIND_PASSWORD": ""}},
			"plan", ldifFile, "--bind-password-stdin"), ExitOK)
		if sent(t, s) != testSecret {
			t.Error("the password from standard input was not sent")
		}
	})
	refused := []struct {
		name string
		opts runOpts
		args []string
	}{
		{"two sources", runOpts{stdin: "x\n"}, []string{"plan", ldifFile, "--bind-password-stdin", "--bind-password-file", ldifFile}},
		{"stdin is the document", runOpts{stdin: restoreLDIF}, []string{"plan", "-", "--bind-password-stdin"}},
		{"no password at all", runOpts{env: map[string]string{"ALDER_BIND_PASSWORD": ""}}, []string{"plan", ldifFile}},
		{"an empty password file", runOpts{env: map[string]string{"ALDER_BIND_PASSWORD": ""}}, []string{"plan", ldifFile, "--bind-password-file", writeTemp(t, dir, "empty.txt", "\n")}},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			s := newPlanStub(t)
			expectCode(t, s.run(t, tc.opts, tc.args...), ExitUsage)
			if len(s.called("POST /api/v1/plan")) != 0 {
				t.Error("planned without a usable password")
			}
		})
	}
}

func TestHelpAndVersion(t *testing.T) {
	s := newStub(t)
	r := s.run(t, runOpts{noConnection: true}, "snapshot", "--help")
	expectCode(t, r, ExitOK)
	if !strings.Contains(r.stdout, "--output") {
		t.Errorf("help = %s", r.stdout)
	}
	r = s.run(t, runOpts{noConnection: true}, "version")
	expectCode(t, r, ExitOK)
	if strings.TrimSpace(r.stdout) != "alder 1.8.0-test" {
		t.Errorf("version = %q", r.stdout)
	}
	r = s.run(t, runOpts{noConnection: true}, "version", "--json", "--api-url", s.srv.URL)
	expectCode(t, r, ExitOK)
	doc := oneJSONDocument(t, r.stdout)
	if doc["client"].(map[string]any)["version"] != "1.8.0-test" || doc["server"].(map[string]any)["version"] != "9.9.9" {
		t.Errorf("version JSON = %v", doc)
	}
}

func TestAServerWithoutTheEndpointIsNamedWithItsVersion(t *testing.T) {
	s := newStub(t) // no /snapshots/capture: an older Alder
	r := s.run(t, runOpts{}, "snapshot", "--base", "dc=example,dc=test", "--output", "-")
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "version 9.9.9") || !strings.Contains(r.stderr, "1.7") {
		t.Errorf("stderr = %s", r.stderr)
	}
	if len(s.called("DELETE /api/v1/session")) != 1 {
		t.Error("the session was not closed")
	}
}

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
