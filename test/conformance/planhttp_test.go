//go:build conformance

package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// The plan, over HTTP, against the real servers.
//
// plan_test.go next door drives the planner directly, which covers the
// classification and the drift check but not the layer above them: the request
// body, the conversion into records, the JSON coming back, and the apply
// endpoint reading the baseline out of what the plan returned. Those were
// covered only against a fake, and a fake is a thing that agrees with whatever
// it was written beside.
//
// So this runs Alder itself -- the real API server, the real LDAP driver -- and
// talks to it the way the browser does.

// alder starts an API server against one harness directory and returns a client
// already holding a session.
func alder(t *testing.T, s server) (*http.Client, string) {
	t.Helper()

	// No AllowPlaintextLDAP: this connects over LDAPS with the harness CA, the
	// same way every other case in this suite does, so the TLS path is under
	// test here too.
	srv := api.NewServer(slog.New(slog.DiscardHandler), api.Config{})
	t.Cleanup(srv.Close)

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	srv.Register(app)

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building a cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}
	base := "http://" + ln.Addr().String() + "/api/v1"

	// Connect the way the connection screen does. Everything after this rides
	// on the session cookie the jar picked up.
	caPEM, err := os.ReadFile(filepath.Join("..", "compose", "certs", "ca.crt"))
	if err != nil {
		t.Fatalf("reading the harness CA: %v (run \"task compose:up\" first)", err)
	}
	connect, err := json.Marshal(map[string]any{
		"host":          s.host,
		"port":          s.port,
		"tls":           "ldaps",
		"caCertificate": string(caPEM),
		"serverName":    "localhost",
		"bindDn":        s.bindDN,
		"bindPassword":  s.bindPW,
	})
	if err != nil {
		t.Fatalf("encoding the connect request: %v", err)
	}
	res := post(t, client, base+"/session", string(connect))
	if res.status != http.StatusCreated {
		t.Fatalf("connecting to %s: status %d\n%s", s.name, res.status, res.body)
	}
	return client, base
}

type httpResult struct {
	status int
	body   string
}

func post(t *testing.T, client *http.Client, url, body string) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url,
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("building a request for %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("posting to %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the response from %s: %v", url, err)
	}
	return httpResult{status: res.StatusCode, body: string(raw)}
}

func decodeInto[T any](t *testing.T, res httpResult) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatalf("decoding: %v\nbody: %s", err, res.body)
	}
	return out
}

// planRequest is the body the plan endpoint takes. Written out rather than
// imported, so that a change to the generated types that broke the wire format
// shows up here as a failing test rather than as a compile error that is fixed
// by changing the test.
func planRequest(reconcile bool, changes ...string) string {
	return fmt.Sprintf(`{"reconcile":%t,"changes":[%s]}`,
		reconcile, strings.Join(changes, ","))
}

func modifyChange(target, attr, value string) string {
	return fmt.Sprintf(
		`{"dn":%q,"type":"modify","mods":[{"op":"replace","name":%q,"values":[{"text":%q}]}]}`,
		target, attr, value)
}

// TestPlanOverHTTPThenApplyWhatItReturned is the whole loop: plan against a
// real directory, hand the plan's own records back to the apply endpoint, and
// check the directory holds what the plan said it would.
func TestPlanOverHTTPThenApplyWhatItReturned(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		target := "uid=user0004,ou=people," + suffix
		restoreDescription(t, sess, target)

		res := post(t, client, base+"/plan",
			planRequest(false, modifyChange(target, "description", "set by the plan test")))
		if res.status != http.StatusOK {
			t.Fatalf("planning: status %d\n%s", res.status, res.body)
		}
		plan := decodeInto[api.Plan](t, res)

		if plan.Counts.Examined != 1 || plan.Counts.Modify != 1 {
			t.Fatalf("counts = %+v, want one examined and one modification", plan.Counts)
		}
		item := plan.Items[0]
		if item.Action != api.PlanActionModify {
			t.Fatalf("action = %q, want modify", item.Action)
		}
		if item.Record == nil || item.Baseline == nil {
			t.Fatal("the plan returned nothing to apply")
		}
		if item.Preview == nil || !strings.Contains(item.Preview.Ldif, "description") {
			t.Errorf("the preview does not describe the change: %+v", item.Preview)
		}

		// Nothing has been written yet.
		if got := readOne(t, sess, target, "description"); got == "set by the plan test" {
			t.Fatal("planning wrote to the directory")
		}

		// Hand back exactly what the plan returned, baseline included.
		applied := post(t, client, base+"/changeset/apply", applyBody(t, item))
		if applied.status != http.StatusOK {
			t.Fatalf("applying: status %d\n%s", applied.status, applied.body)
		}
		if got := readOne(t, sess, target, "description"); got != "set by the plan test" {
			t.Errorf("description = %q after applying the plan", got)
		}
	})
}

// The drift check, through the endpoints rather than through the planner: plan,
// let somebody else write, and the apply is refused.
func TestApplyOverHTTPRefusesAPlanTheDirectoryHasOutgrown(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		target := "uid=user0005,ou=people," + suffix
		restoreDescription(t, sess, target)

		res := post(t, client, base+"/plan",
			planRequest(false, modifyChange(target, "description", "what the plan intended")))
		if res.status != http.StatusOK {
			t.Fatalf("planning: status %d\n%s", res.status, res.body)
		}
		item := decodeInto[api.Plan](t, res).Items[0]
		if item.Baseline == nil {
			t.Fatal("the plan carried no baseline")
		}

		// The other administrator, on the same attribute.
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN: mustDN(t, target), Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description",
				Values: [][]byte{[]byte("somebody else got here first")}}},
		}); err != nil {
			t.Fatalf("the other administrator's write failed: %v", err)
		}

		refused := post(t, client, base+"/changeset/apply", applyBody(t, item))
		if refused.status != http.StatusConflict {
			t.Fatalf("applying a stale plan: status %d, want 409\n%s", refused.status, refused.body)
		}
		// And it really did not apply: the other administrator's value stands.
		if got := readOne(t, sess, target, "description"); got != "somebody else got here first" {
			t.Errorf("description = %q; the refused change was applied anyway", got)
		}
	})
}

// An unedited round trip through the endpoint plans to nothing on both servers.
// The same claim plan_test.go makes of the planner, made of the API.
func TestPlanOverHTTPFindsNothingToDoInAnUneditedRoundTrip(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		target := "uid=user0006,ou=people," + suffix

		live, err := sess.Read(ctx(t), mustDN(t, target), []string{"*"})
		if err != nil {
			t.Fatalf("reading: %v", err)
		}

		// The entry offered back as an add, which is what re-importing an
		// unedited export amounts to.
		var attrs []string
		for _, name := range live.Order {
			var values []string
			for _, v := range live.Get(name) {
				encoded, err := json.Marshal(string(v))
				if err != nil {
					t.Fatalf("encoding a value of %s: %v", name, err)
				}
				values = append(values, fmt.Sprintf(`{"text":%s}`, encoded))
			}
			attrs = append(attrs, fmt.Sprintf(`{"name":%q,"values":[%s]}`,
				name, strings.Join(values, ",")))
		}
		change := fmt.Sprintf(`{"dn":%q,"type":"add","attributes":[%s]}`,
			target, strings.Join(attrs, ","))

		res := post(t, client, base+"/plan", planRequest(true, change))
		if res.status != http.StatusOK {
			t.Fatalf("planning: status %d\n%s", res.status, res.body)
		}
		plan := decodeInto[api.Plan](t, res)
		if plan.Counts.Unchanged != 1 {
			t.Errorf("an unedited round trip planned as %+v, want one unchanged\nitem: %+v",
				plan.Counts, plan.Items[0])
		}
	})
}

// applyBody wraps one planned item as the changeset the apply endpoint takes,
// carrying the baseline the plan issued.
func applyBody(t *testing.T, item api.PlanItem) string {
	t.Helper()
	record := *item.Record
	record.Baseline = item.Baseline
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encoding the planned record: %v", err)
	}
	var buf bytes.Buffer
	buf.WriteString(`{"changes":[`)
	buf.Write(encoded)
	buf.WriteString(`]}`)
	return buf.String()
}

func readOne(t *testing.T, sess directory.Session, target, attr string) string {
	t.Helper()
	e, err := sess.Read(ctx(t), mustDN(t, target), []string{attr})
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	return e.GetOne(attr)
}

// restoreDescription puts an entry's description back at the end of a test, so
// the harness is the same afterwards as it was before.
func restoreDescription(t *testing.T, sess directory.Session, target string) {
	t.Helper()
	d := mustDN(t, target)
	before, err := sess.Read(ctx(t), d, []string{"description"})
	if err != nil {
		t.Fatalf("reading %s: %v", target, err)
	}
	original := before.Get("description")
	t.Cleanup(func() {
		restore := directory.ChangeRecord{DN: d, Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "description", Values: original}}}
		if len(original) == 0 {
			restore.Mods[0].Op = directory.ModDelete
			restore.Mods[0].Values = nil
		}
		if err := sess.Apply(ctx(t), restore); err != nil {
			t.Logf("restoring the description of %s: %v", target, err)
		}
	})
}
