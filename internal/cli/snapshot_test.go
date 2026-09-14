package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestASnapshotIsWrittenExactlyAsAlderSentIt(t *testing.T) {
	s := newStub(t)
	s.on("POST /api/v1/snapshots/capture", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="alder-snapshot.json"`)
		_, _ = io.WriteString(w, snapshotDoc)
	})
	dir := t.TempDir()
	out := filepath.Join(dir, "baseline with space.json")
	r := s.run(t, runOpts{}, "snapshot", "--base", "ou=people,dc=example,dc=test", "--scope", "one",
		"--filter", "(uid=a*)", "--operational", "--output", out)
	expectCode(t, r, ExitOK)
	got, err := os.ReadFile(out)
	if err != nil || string(got) != snapshotDoc {
		t.Fatalf("the file is not the snapshot Alder sent: %v\n%s", err, got)
	}
	if !strings.Contains(r.stderr, "Captured 1 entries") || r.stdout != "" {
		t.Errorf("stdout = %q, stderr = %q", r.stdout, r.stderr)
	}
	var req map[string]any
	_ = json.Unmarshal(s.called("POST /api/v1/snapshots/capture")[0].Body, &req)
	if req["base"] != "ou=people,dc=example,dc=test" || req["scope"] != "one" || req["filter"] != "(uid=a*)" || req["operationalAttributes"] != true {
		t.Errorf("capture request = %v", req)
	}
	noPartials(t, dir)
	if len(s.called("DELETE /api/v1/session")) != 1 {
		t.Error("the session was not closed")
	}
}

func TestASnapshotToStandardOutputIsTheSnapshotAndNothingElse(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/snapshots/capture", http.StatusOK, snapshotDoc)
	r := s.run(t, runOpts{}, "snapshot", "--base", "ou=people,dc=example,dc=test", "--output", "-")
	expectCode(t, r, ExitOK)
	if r.stdout != snapshotDoc {
		t.Fatalf("stdout is not the snapshot:\n%s", r.stdout)
	}
}

func TestAnExistingFileIsNeverReplacedWithoutForce(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/snapshots/capture", http.StatusOK, snapshotDoc)
	dir := t.TempDir()
	out := writeTemp(t, dir, "baseline.json", "the previous snapshot")

	r := s.run(t, runOpts{}, "snapshot", "--base", "dc=example,dc=test", "--output", out)
	expectCode(t, r, ExitUsage)
	if got, _ := os.ReadFile(out); string(got) != "the previous snapshot" {
		t.Fatal("the file was replaced")
	}
	if len(s.called("POST /api/v1/session")) != 0 {
		t.Error("connected before refusing")
	}

	r = s.run(t, runOpts{}, "snapshot", "--base", "dc=example,dc=test", "--output", out, "--force")
	expectCode(t, r, ExitOK)
	if got, _ := os.ReadFile(out); string(got) != snapshotDoc {
		t.Fatal("--force did not replace the file")
	}
	noPartials(t, dir)
}

func TestARefusedCaptureWritesNothing(t *testing.T) {
	s := newStub(t)
	s.reply("POST /api/v1/snapshots/capture", http.StatusBadRequest,
		apiErrorJSON("snapshot_scope_unsupported", "The base is in the schema tree.", nil))
	dir := t.TempDir()
	out := filepath.Join(dir, "s.json")
	r := s.run(t, runOpts{}, "snapshot", "--base", "cn=schema", "--output", out)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "snapshot_scope_unsupported") {
		t.Errorf("stderr = %s", r.stderr)
	}
	assertAbsent(t, out)
	noPartials(t, dir)
}

func TestASnapshotCutShortNeverGetsItsName(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		// The connection drops partway through the body.
		"connection dropped": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "100000")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, snapshotDoc[:120])
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		},
		// A complete response that is not a complete document.
		"document incomplete": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, snapshotDoc[:200])
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStub(t)
			s.on("POST /api/v1/snapshots/capture", h)
			dir := t.TempDir()
			out := filepath.Join(dir, "s.json")
			r := s.run(t, runOpts{}, "snapshot", "--base", "dc=example,dc=test", "--output", out)
			expectCode(t, r, ExitFailed)
			assertAbsent(t, out)
			noPartials(t, dir)
		})
	}
}

func TestInterruptingACaptureLeavesNoFileAndClosesTheSession(t *testing.T) {
	s := newStub(t)
	arrived := make(chan struct{})
	s.on("POST /api/v1/snapshots/capture", func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
	})
	dir := t.TempDir()
	out := filepath.Join(dir, "s.json")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
		}
		cancel()
	}()
	r := s.run(t, runOpts{ctx: ctx}, "snapshot", "--base", "dc=example,dc=test", "--output", out)
	expectCode(t, r, ExitFailed)
	if !strings.Contains(r.stderr, "interrupted") {
		t.Errorf("stderr = %s", r.stderr)
	}
	assertAbsent(t, out)
	noPartials(t, dir)
	if len(s.called("DELETE /api/v1/session")) != 1 {
		t.Error("an interrupted command left its session open")
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists", path)
	}
}

func noPartials(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}
