package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hazame-hub/alder/internal/session"
)

// The client's tests run it against a stub Alder: an HTTP server whose answers
// each test decides, and which records every request. That is the right layer
// for what these test -- the command line, exit codes, standard streams, files,
// confirmation, and what is sent back to the server -- and the wrong one for
// what the server does with it. That is proven against the real server, and the
// real directories, in test/conformance/cli_test.go.

// testSecret stands in for a bind password. Every run checks that it appears in
// neither output stream.
const testSecret = "not-a-real-password-4f7c1d"

type recorded struct {
	Method, Path string
	Cookie       string
	Body         []byte
}

type stub struct {
	t      testing.TB
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []recorded
	routes map[string]http.HandlerFunc
}

func newStub(t testing.TB) *stub {
	t.Helper()
	s := &stub{t: t, routes: map[string]http.HandlerFunc{}}
	s.on("POST /api/v1/session", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: session.CookieNameInsecure, Value: "stub-session", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"connected":true}`)
	})
	s.on("DELETE /api/v1/session", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	s.reply("GET /api/v1/source", http.StatusOK,
		`{"license":"AGPL-3.0-only","sourceUrl":"https://example.test/src","version":"9.9.9","notice":"n"}`)
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	cookie := ""
	if c, err := r.Cookie(session.CookieNameInsecure); err == nil {
		cookie = c.Value
	}
	s.mu.Lock()
	s.calls = append(s.calls, recorded{Method: r.Method, Path: r.URL.Path, Cookie: cookie, Body: body})
	h := s.routes[r.Method+" "+r.URL.Path]
	s.mu.Unlock()
	if h == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	h(w, r)
}

func (s *stub) on(route string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[route] = h
}

// reply answers a route with a fixed JSON body.
func (s *stub) reply(route string, status int, body string) {
	s.on(route, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// called returns the recorded requests to a route.
func (s *stub) called(route string) []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []recorded
	for _, c := range s.calls {
		if c.Method+" "+c.Path == route {
			out = append(out, c)
		}
	}
	return out
}

type runOpts struct {
	stdin       string
	interactive bool
	env         map[string]string
	ctx         context.Context
	// noConnection leaves out the connection flags run otherwise adds.
	noConnection bool
}

type result struct {
	code           int
	stdout, stderr string
}

// run executes one client command against the stub, as a user would type it:
// the command name first, then the connection flags, then args.
func (s *stub) run(t testing.TB, o runOpts, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	env := map[string]string{"ALDER_BIND_PASSWORD": testSecret}
	for k, v := range o.env {
		env[k] = v
	}
	e := &Env{
		Stdin:  strings.NewReader(o.stdin),
		Stdout: &stdout,
		Stderr: &stderr,
		Getenv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
		Interactive: func() bool { return o.interactive },
		Version:     "1.8.0-test",
	}
	root := &cobra.Command{Use: "alder"}
	root.AddCommand(Commands(e)...)
	full := args
	if !o.noConnection && len(args) > 0 {
		full = append([]string{args[0]}, append(s.connection(), args[1:]...)...)
	}
	ctx := o.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	code := Execute(ctx, root, e, full)
	r := result{code: code, stdout: stdout.String(), stderr: stderr.String()}
	if strings.Contains(r.stdout, testSecret) || strings.Contains(r.stderr, testSecret) {
		t.Errorf("a password was printed\nstdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
	}
	// Every request but opening a session and asking for the version rides on
	// the session, as the browser's do.
	s.mu.Lock()
	for _, c := range s.calls {
		if c.Path != "/api/v1/session" && c.Path != "/api/v1/source" && c.Cookie != "stub-session" && !snapshotsOnlyDiff(c) {
			t.Errorf("%s %s was sent without the session cookie", c.Method, c.Path)
		}
	}
	s.mu.Unlock()
	return r
}

// snapshotsOnlyDiff is a comparison of two snapshots, which needs no session.
func snapshotsOnlyDiff(c recorded) bool {
	return c.Path == "/api/v1/diff" && !bytes.Contains(c.Body, []byte(`"live"`))
}

func (s *stub) connection() []string {
	return []string{"--api-url", s.srv.URL, "--host", "ldap.example.test", "--bind-dn", "cn=admin,dc=example,dc=test"}
}

func expectCode(t *testing.T, r result, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, want, r.stdout, r.stderr)
	}
}

// oneJSONDocument fails unless out holds exactly one JSON value and nothing else.
func oneJSONDocument(t *testing.T, out string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("standard output does not start with a JSON document: %v\n%s", err, out)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("standard output holds more than one JSON document:\n%s", out)
	}
	return doc
}
