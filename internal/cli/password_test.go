package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

// asker is a terminal that answers every question with answer, and remembers
// what it was asked.
type asker struct {
	answer string
	err    error
	asked  []string
}

func (a *asker) ask(_ context.Context, prompt string) (string, error) {
	a.asked = append(a.asked, prompt)
	return a.answer, a.err
}

func sessionRequest(t *testing.T, s *stub) map[string]any {
	t.Helper()
	calls := s.called("POST /api/v1/session")
	if len(calls) != 1 {
		t.Fatalf("%d session requests", len(calls))
	}
	var connect map[string]any
	if err := json.Unmarshal(calls[0].Body, &connect); err != nil {
		t.Fatal(err)
	}
	return connect
}

func TestAPasswordGivenNowhereIsAskedForAtATerminal(t *testing.T) {
	ldifFile := writeTemp(t, t.TempDir(), "c.ldif", restoreLDIF)
	noVariable := map[string]string{"ALDER_BIND_PASSWORD": ""}
	newPlanStub := func(t *testing.T) *stub {
		s := newStub(t)
		s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
		return s
	}

	t.Run("asked, with the DN, and sent", func(t *testing.T) {
		s := newPlanStub(t)
		a := &asker{answer: testSecret}
		expectCode(t, s.run(t, runOpts{interactive: true, env: noVariable, ask: a.ask}, "plan", ldifFile), ExitOK)
		if len(a.asked) != 1 || a.asked[0] != "Password for cn=admin,dc=example,dc=test: " {
			t.Errorf("asked %q", a.asked)
		}
		if pw, _ := sessionRequest(t, s)["bindPassword"].(string); pw != testSecret {
			t.Errorf("sent %q", pw)
		}
	})

	t.Run("the configuration identity is asked for with its own DN", func(t *testing.T) {
		s := newPlanStub(t)
		a := &asker{answer: testSecret}
		r := s.run(t, runOpts{interactive: true, env: noVariable, ask: a.ask},
			"plan", ldifFile, "--config-bind-dn", "cn=Directory Manager")
		expectCode(t, r, ExitOK)
		if len(a.asked) != 2 || a.asked[1] != "Password for cn=Directory Manager: " {
			t.Errorf("asked %q", a.asked)
		}
		if pw, _ := sessionRequest(t, s)["configBindPassword"].(string); pw != testSecret {
			t.Errorf("sent %q for the configuration identity", pw)
		}
	})

	// The DN is the user's own flag, but it is printed on a terminal all
	// the same, and a terminal acts on escapes.
	t.Run("the DN in the question is escaped", func(t *testing.T) {
		s := newPlanStub(t)
		a := &asker{answer: testSecret}
		r := s.run(t, runOpts{interactive: true, env: noVariable, ask: a.ask, noConnection: true},
			"plan", "--api-url", s.srv.URL, "--host", "ldap.example.test", "--bind-dn", "cn=a\x1b[2J", ldifFile)
		expectCode(t, r, ExitOK)
		if len(a.asked) != 1 || a.asked[0] != `Password for cn=a\x1b[2J: ` {
			t.Errorf("asked %q", a.asked)
		}
	})

	// apply asks its confirmation on the same terminal afterwards, so the
	// password must not have claimed standard input for itself.
	t.Run("apply still confirms on the terminal", func(t *testing.T) {
		s := applyStub(t, planJSON(t, counts{modify: 1}, exactModify("b0")))
		a := &asker{answer: testSecret}
		r := s.run(t, runOpts{interactive: true, stdin: "y\n", env: noVariable, ask: a.ask}, "apply", ldifFile)
		expectCode(t, r, ExitOK)
		if len(a.asked) != 1 || len(s.called("POST /api/v1/changeset/apply")) != 1 {
			t.Errorf("asked %d times, applied %d times", len(a.asked), len(s.called("POST /api/v1/changeset/apply")))
		}
	})

	notAsked := []struct {
		name string
		opts runOpts
		args []string
		code int
		says string
	}{
		{"the variable is set", runOpts{interactive: true}, []string{"plan", ldifFile}, ExitOK, ""},
		{"a file is named", runOpts{interactive: true, env: noVariable},
			[]string{"plan", ldifFile, "--bind-password-file", writeTemp(t, t.TempDir(), "pw", testSecret)}, ExitOK, ""},
		{"standard input is not a terminal", runOpts{env: noVariable}, []string{"plan", ldifFile},
			ExitUsage, "asked for at the terminal only when standard input is one"},
		{"standard input is the document", runOpts{interactive: true, stdin: restoreLDIF, env: noVariable}, []string{"plan", "-"},
			ExitUsage, "because standard input is used for the LDIF document"},
	}
	for _, tc := range notAsked {
		t.Run(tc.name, func(t *testing.T) {
			s := newPlanStub(t)
			a := &asker{answer: "the wrong password"}
			tc.opts.ask = a.ask
			r := s.run(t, tc.opts, tc.args...)
			expectCode(t, r, tc.code)
			if len(a.asked) != 0 {
				t.Errorf("asked %q", a.asked)
			}
			if !strings.Contains(r.stderr, tc.says) {
				t.Errorf("stderr does not say %q:\n%s", tc.says, r.stderr)
			}
		})
	}

	refused := []struct {
		name string
		a    *asker
		code int
		says string
	}{
		{"nothing typed", &asker{answer: ""}, ExitUsage, "alder: the password typed at the terminal is empty\n"},
		{"a terminal that cannot hide what is typed", &asker{err: errCannotAsk}, ExitUsage,
			"this terminal cannot read a password without showing it"},
		{"interrupted", &asker{err: failf("interrupted", "interrupted while asking for the password")}, ExitFailed,
			"interrupted while asking for the password"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			s := newPlanStub(t)
			r := s.run(t, runOpts{interactive: true, env: noVariable, ask: tc.a.ask}, "plan", ldifFile)
			expectCode(t, r, tc.code)
			if !strings.Contains(r.stderr, tc.says) {
				t.Errorf("stderr does not say %q:\n%s", tc.says, r.stderr)
			}
			if len(s.called("POST /api/v1/session")) != 0 {
				t.Error("connected without a usable password")
			}
		})
	}
}

// A project names the variable each environment's password is read from, so
// that one shell cannot hand the development password to production. Asking
// at the terminal keeps that: the question names the environment's own bind
// DN, and the person answering has just been told which environment it is.
func TestAProjectAsksForThePasswordItsEnvironmentNames(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/plan", http.StatusOK, planJSON(t, counts{modify: 1}, exactModify("b0")))
	dir := writeProject(t, s, nil)
	a := &asker{answer: testSecret}
	// The generic variable is set and must not be used.
	opts := runOpts{noConnection: true, interactive: true, ask: a.ask,
		env: map[string]string{"ALDER_BIND_PASSWORD": "the-wrong-one"}}
	expectCode(t, s.run(t, opts, "project", "plan", "--project", dir, "--env", "dev"), ExitOK)
	if len(a.asked) != 1 || a.asked[0] != "Password for cn=alder,ou=services,dc=alder,dc=test: " {
		t.Errorf("asked %q", a.asked)
	}
	if pw, _ := sessionRequest(t, s)["bindPassword"].(string); pw != testSecret {
		t.Errorf("sent %q", pw)
	}

	// Without a terminal, the error names the environment's variable.
	s = newStub(t)
	opts.interactive = false
	r := s.run(t, opts, "project", "plan", "--project", dir, "--env", "dev")
	expectCode(t, r, ExitUsage)
	if !strings.Contains(r.stderr, "ALDER_DEV_BIND_PASSWORD") || len(s.called("POST /api/v1/session")) != 0 {
		t.Errorf("stderr:\n%s", r.stderr)
	}
}

func TestReadPasswordLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"secret\n", "secret"},
		{"secret\r\n", "secret"},
		{"secret", "secret"},
		{"", ""},
		{"secrex\bt\n", "secret"},
		{"\b\x7fsecret\n", "secret"},
		{"pässé\x7f\x7f\n", "päs"},
		{"first\nsecond\n", "first"},
	}
	for _, tc := range cases {
		got, err := readPasswordLine(iotest.OneByteReader(strings.NewReader(tc.in)))
		if err != nil || string(got) != tc.want {
			t.Errorf("readPasswordLine(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}

	// It reads no further than the line, so what follows is left for the
	// next question.
	r := strings.NewReader("secret\ny\n")
	if _, err := readPasswordLine(r); err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "y\n" {
		t.Errorf("left %q", rest)
	}

	if _, err := readPasswordLine(strings.NewReader(strings.Repeat("x", maxSecretBytes+1))); err == nil {
		t.Error("an endless line was read")
	}
	boom := errors.New("boom")
	if _, err := readPasswordLine(iotest.ErrReader(boom)); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
