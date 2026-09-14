package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Two snapshot files are compared by the Alder server with no directory session.
// The first test runs the real API server, with its real LDAP driver and no
// directory anywhere, and a client with no directory flags and no password: the
// comparison can only succeed if nothing on either side needs a directory.

func realSnapshot(t *testing.T, title string) string {
	t.Helper()
	sch := schema.Load("cn=schema", map[string][]string{"attributeTypes": {
		"( 2.5.4.0 NAME 'objectClass' EQUALITY objectIdentifierMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
		"( 2.5.4.3 NAME 'cn' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.4 NAME 'sn' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.12 NAME 'title' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	}})
	entry := func(d string, pairs ...[]string) *directory.Entry {
		e := directory.NewEntry(dn.MustParse(d))
		for _, p := range pairs {
			values := make([][]byte, 0, len(p)-1)
			for _, v := range p[1:] {
				values = append(values, []byte(v))
			}
			e.Set(p[0], values)
		}
		return e
	}
	s, err := snapshot.Build(snapshot.Capture{
		Base: dn.MustParse("ou=people,dc=example,dc=test"), Scope: "sub", Filter: "(objectClass=*)",
		Vendor: "389 Project", CreatedAt: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
	}, sch, []*directory.Entry{
		entry(aliceDN, []string{"objectClass", "top", "person"}, []string{"cn", "Alice"}, []string{"sn", "A"}, []string{"title", title}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestTwoSnapshotFilesNeedNoDirectoryAtAll(t *testing.T) {
	srv := api.NewServer(slog.New(slog.DiscardHandler), api.Config{})
	t.Cleanup(srv.Close)
	app := fiber.New(fiber.Config{DisableStartupMessage: true, BodyLimit: 16 << 20})
	srv.Register(app)
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	base := "http://" + ln.Addr().String()

	dir := t.TempDir()
	before, after := realSnapshot(t, "Engineer"), realSnapshot(t, "Senior Engineer")
	beforeFile := writeTemp(t, dir, "before.json", before)
	afterFile := writeTemp(t, dir, "after.json", after)

	// What the API says to a client with no session at all.
	res, err := postJSON(t, base+"/api/v1/diff", "application/json",
		strings.NewReader(`{"source":{"snapshot":`+before+`},"target":{"snapshot":`+after+`},"includeUnchanged":false}`))
	if err != nil {
		t.Fatal(err)
	}
	direct, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the API refused two snapshots without a session: %d %s", res.StatusCode, direct)
	}

	run := func(stdin string, args ...string) result {
		var stdout, stderr bytes.Buffer
		env := &Env{
			Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
			// No ALDER_BIND_PASSWORD, no ALDER_HOST, nothing.
			Getenv:      func(string) (string, bool) { return "", false },
			Interactive: func() bool { return false },
			Version:     "test",
		}
		root := &cobra.Command{Use: "alder"}
		root.AddCommand(Commands(env)...)
		code := Execute(context.Background(), root, env, append(args, "--api-url", base))
		return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}

	r := run("", "diff", beforeFile, afterFile, "--json")
	expectCode(t, r, ExitDifferences)
	if strings.TrimSpace(r.stdout) != strings.TrimSpace(string(direct)) {
		t.Fatalf("the client's JSON is not the API's:\nclient: %s\napi:    %s", r.stdout, direct)
	}

	r = run(before, "diff", "-", afterFile, "--json")
	expectCode(t, r, ExitDifferences)
	if strings.TrimSpace(r.stdout) != strings.TrimSpace(string(direct)) {
		t.Fatalf("with a snapshot on standard input the JSON differs:\n%s", r.stdout)
	}

	expectCode(t, run("", "diff", beforeFile, beforeFile), ExitOK)

	// A comparison with the live directory still needs one.
	r = run("", "diff", beforeFile, "@live")
	expectCode(t, r, ExitUsage)
	if !strings.Contains(r.stderr, "--host") {
		t.Errorf("stderr = %s", r.stderr)
	}
	live, err := postJSON(t, base+"/api/v1/diff", "application/json",
		strings.NewReader(`{"source":{"live":{}},"target":{"snapshot":`+before+`}}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = live.Body.Close()
	if live.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a live comparison without a session: status %d, want 401", live.StatusCode)
	}
}

func TestTwoSnapshotsOpenNoSessionAndSendNoCredentials(t *testing.T) {
	body := diffJSON(t, true, diffCounts{compared: 1, modified: 1}, modifiedAlice(nil))
	dir := t.TempDir()
	a, b := writeTemp(t, dir, "a.json", snapshotDoc), writeTemp(t, dir, "b.json", snapshotDoc)

	// With only the Alder address, and with every directory flag given anyway:
	// neither opens a session, and no password is read or sent.
	for name, opts := range map[string]runOpts{
		"only --api-url":          {noConnection: true},
		"directory flags ignored": {},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			s.reply("POST /api/v1/diff", http.StatusOK, body)
			args := []string{"diff", a, b, "--json"}
			if opts.noConnection {
				args = append(args, "--api-url", s.srv.URL)
			}
			r := s.run(t, opts, args...)
			expectCode(t, r, ExitDifferences)
			if strings.TrimSpace(r.stdout) != body {
				t.Errorf("stdout is not the comparison: %s", r.stdout)
			}
			if n := len(s.called("POST /api/v1/session")) + len(s.called("DELETE /api/v1/session")); n != 0 {
				t.Fatalf("comparing two snapshots touched a session (%d requests)", n)
			}
			for _, c := range s.called("POST /api/v1/diff") {
				if c.Cookie != "" || bytes.Contains(c.Body, []byte(testSecret)) {
					t.Fatalf("the comparison carried a session or a password: %s", c.Body)
				}
			}
		})
	}
}

func TestALiveComparisonStillNeedsTheDirectoryFlags(t *testing.T) {
	s := newStub(t)
	snap := writeTemp(t, t.TempDir(), "s.json", snapshotDoc)
	for _, args := range [][]string{{"diff", snap, "@live"}, {"diff", "@live", snap}} {
		r := s.run(t, runOpts{noConnection: true}, append(args, "--api-url", s.srv.URL)...)
		expectCode(t, r, ExitUsage)
	}
	if len(s.calls) != 0 {
		t.Fatalf("a live comparison without directory flags sent %d request(s)", len(s.calls))
	}
}

func TestAServerThatNeedsASessionToCompareSnapshotsIsGivenOne(t *testing.T) {
	body := diffJSON(t, true, diffCounts{compared: 1, unchanged: 1})
	olderServer := func(t *testing.T) *stub {
		s := newStub(t)
		s.on("POST /api/v1/diff", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if _, err := r.Cookie("alder-session"); err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, apiErrorJSON("unauthorized", "Not connected to a directory. Connect first.", nil))
				return
			}
			_, _ = io.WriteString(w, body)
		})
		return s
	}
	dir := t.TempDir()
	a, b := writeTemp(t, dir, "a.json", snapshotDoc), writeTemp(t, dir, "b.json", snapshotDoc)

	s := olderServer(t)
	r := s.run(t, runOpts{}, "diff", a, b)
	expectCode(t, r, ExitOK)
	if len(s.called("POST /api/v1/session")) != 1 || len(s.called("POST /api/v1/diff")) != 2 {
		t.Fatalf("expected one refused comparison, a session, and the comparison again")
	}

	s = olderServer(t)
	r = s.run(t, runOpts{noConnection: true}, "diff", a, b, "--api-url", s.srv.URL)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "servers before 1.8") {
		t.Errorf("the refusal does not say what to add: %s", r.stderr)
	}
}

var _ = filepath.Join
var _ = os.Remove
var _ = json.Valid

// postJSON is http.Post with the test's context, so a stuck server fails the
// test instead of hanging it.
func postJSON(t *testing.T, url, contentType string, body io.Reader) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return http.DefaultClient.Do(req)
}
