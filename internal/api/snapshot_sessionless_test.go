package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Comparing two snapshots reads no directory, so it needs no session and opens
// none. These run against a server whose directory driver counts every attempt
// to connect and connects to nothing, with no session in its store: a request
// that needed a directory could only fail, and one that asked for a session
// would be counted.

type connectCounter struct{ connects atomic.Int32 }

func (d *connectCounter) Connect(context.Context, directory.ConnConfig) (directory.Session, error) {
	d.connects.Add(1)
	return nil, errors.New("this test has no directory")
}

// directorylessServer is a server with no session and no reachable directory,
// behind the same body limit alder serve sets.
func directorylessServer(t *testing.T) (*fiber.App, *connectCounter) {
	t.Helper()
	s := NewServer(slog.New(slog.DiscardHandler), Config{IdleTimeout: time.Minute, MaxLifetime: time.Hour})
	t.Cleanup(s.Close)
	driver := &connectCounter{}
	s.driver = driver
	app := fiber.New(fiber.Config{DisableStartupMessage: true, BodyLimit: 16 << 20})
	s.Register(app)
	return app, driver
}

func postDiffWithoutSession(t *testing.T, app *fiber.App, body string) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/diff", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("POST /diff: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	return response{Status: res.StatusCode, Body: string(raw), Header: res.Header}
}

func snapshotWithTitle(t *testing.T, title string) string {
	t.Helper()
	s, err := snapshot.Build(snapshot.Capture{
		Base: mustParse(t, "ou=people,dc=alder,dc=test"), Scope: "sub", Filter: "(objectClass=*)",
		Vendor: "389 Project", CreatedAt: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
	}, testSchema(t), []*directory.Entry{
		personAt(t, "uid=alice,ou=people,dc=alder,dc=test", []string{"cn", "Alice"}, []string{"sn", "A"}, []string{"title", title}),
		personAt(t, "uid=bob,ou=people,dc=alder,dc=test", []string{"cn", "Bob"}, []string{"sn", "B"}),
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

func diffOfTwoSnapshots(before, after string) string {
	return `{"source":{"snapshot":` + before + `},"target":{"snapshot":` + after + `}}`
}

func TestTwoSnapshotsCompareWithoutADirectorySession(t *testing.T) {
	app, driver := directorylessServer(t)
	before, after := snapshotWithTitle(t, "Engineer"), snapshotWithTitle(t, "Senior Engineer")

	res := postDiffWithoutSession(t, app, diffOfTwoSnapshots(before, after))
	if res.Status != http.StatusOK {
		t.Fatalf("two snapshots without a session: status %d\n%s", res.Status, res.Body)
	}
	d := decode[Diff](t, res)
	if !d.Complete || d.Counts.Modified != 1 || d.Counts.Unchanged != 1 {
		t.Fatalf("complete = %v, counts = %+v", d.Complete, d.Counts)
	}
	if n := driver.connects.Load(); n != 0 {
		t.Fatalf("comparing two snapshots tried to connect to a directory %d time(s)", n)
	}
	if cookie := res.Header.Get("Set-Cookie"); cookie != "" {
		t.Errorf("comparing two snapshots issued a session: %s", cookie)
	}

	// The same request from a connected client gets the same answer, byte for
	// byte: there is one comparison, whether or not a session is present.
	rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
	withSession := rig.do(t, http.MethodPost, "/api/v1/diff", strings.NewReader(diffOfTwoSnapshots(before, after)))
	if withSession.Status != http.StatusOK || withSession.Body != res.Body {
		t.Fatalf("the comparison differs with a session:\nwithout: %s\nwith:    %s", res.Body, withSession.Body)
	}
	if len(rig.fake.readDNs) != 0 || rig.fake.lastSearch != nil || len(rig.fake.visibilityAsked) != 0 {
		t.Error("comparing two snapshots read the directory of the session that happened to be there")
	}
}

func TestASessionlessComparisonIsStillUntrustedInput(t *testing.T) {
	app, driver := directorylessServer(t)
	before, after := snapshotWithTitle(t, "Engineer"), snapshotWithTitle(t, "Senior Engineer")
	cases := []struct {
		name string
		body string
		want ErrorError
	}{
		{"not JSON", `{"source":`, ErrorErrorBadRequest},
		{"a side missing", `{"source":{"snapshot":` + before + `}}`, ErrorErrorBadRequest},
		{"an unknown field", diffOfTwoSnapshots(strings.Replace(before, `"kind"`, `"future":true,"kind"`, 1), after), ErrorErrorSnapshotInvalid},
		{"edited after capture", diffOfTwoSnapshots(strings.Replace(before, `"Engineer"`, `"Architect"`, 1), after), ErrorErrorSnapshotChecksumMismatch},
		{"a future version", diffOfTwoSnapshots(strings.Replace(before, `"version": 1`, `"version": 2`, 1), after), ErrorErrorSnapshotUnsupportedVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := postDiffWithoutSession(t, app, tc.body)
			if res.Status != http.StatusBadRequest || errorCode(t, res) != tc.want {
				t.Fatalf("status %d, body %s; want 400 %s", res.Status, res.Body, tc.want)
			}
		})
	}

	t.Run("larger than the request limit", func(t *testing.T) {
		padded := diffOfTwoSnapshots(before, after)[:len(diffOfTwoSnapshots(before, after))-1] + `,"pad":"` + strings.Repeat("x", 17<<20) + `"}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/diff", strings.NewReader(padded))
		req.Header.Set("Content-Type", "application/json")
		// The server refuses the body before routing it, which the in-memory
		// transport reports as an error rather than as a 413 response.
		res, err := app.Test(req, -1)
		switch {
		case err != nil && strings.Contains(err.Error(), "body size exceeds"):
		case err == nil && res.StatusCode == http.StatusRequestEntityTooLarge:
			_ = res.Body.Close()
		case err == nil:
			_ = res.Body.Close()
			t.Fatalf("status %d for a request over 16 MB", res.StatusCode)
		default:
			t.Fatalf("a request over 16 MB: %v", err)
		}
	})
	if n := driver.connects.Load(); n != 0 {
		t.Fatalf("refusing input tried to connect to a directory %d time(s)", n)
	}
}

func TestAComparisonWithTheLiveDirectoryStillNeedsASession(t *testing.T) {
	app, driver := directorylessServer(t)
	snap := snapshotWithTitle(t, "Engineer")
	for name, body := range map[string]string{
		"live source": `{"source":{"live":{}},"target":{"snapshot":` + snap + `}}`,
		"live target": `{"source":{"snapshot":` + snap + `},"target":{"live":{"scope":"one"}}}`,
		// Refused as unauthorised before the snapshot is read, not as invalid.
		"live with a broken snapshot": `{"source":{"live":{}},"target":{"snapshot":{"format":"not a snapshot"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			res := postDiffWithoutSession(t, app, body)
			if res.Status != http.StatusUnauthorized || errorCode(t, res) != ErrorErrorUnauthorized {
				t.Fatalf("status %d, body %s; want 401 unauthorized", res.Status, res.Body)
			}
		})
	}
	if n := driver.connects.Load(); n != 0 {
		t.Fatalf("an unauthenticated live comparison tried to connect %d time(s)", n)
	}
}
