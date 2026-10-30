package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/reqlog"
	"github.com/hazame-hub/alder/internal/session"
)

// With --log-request-header, the line an apply writes carries the header's
// value, so it can be matched to the proxy's access log; without the header,
// it carries nothing. A line break in the value never splits the line.
func TestApplyLinesCarryTheRequestHeader(t *testing.T) {
	alice := personAt(t, planAlice, []string{"cn", "Alice"}, []string{"sn", "L"})
	rig := planRig(t, alice)
	var buf bytes.Buffer
	rig.server.logger = slog.New(reqlog.Handler(slog.NewJSONHandler(&buf, nil)))

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(reqlog.Middleware("X-Request-Id"))
	rig.server.Register(app)
	rig.app = app

	apply := func(id string) {
		body := fmt.Sprintf(`{"changes":[{"dn":%q,"type":"modify","mods":[{"op":"replace","name":"sn","values":[{"text":"X"}]}]}]}`, planAlice)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/changeset/apply", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if id != "" {
			req.Header.Set("X-Request-Id", id)
		}
		req.AddCookie(&http.Cookie{Name: session.CookieNameInsecure, Value: rig.cookie})
		if res := rig.send(t, req); res.Status != http.StatusOK {
			t.Fatalf("status = %d\n%s", res.Status, res.Body)
		}
	}
	apply("req-7\nforged")
	apply("")

	var applied []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "changeset applied") {
			applied = append(applied, l)
		}
	}
	if len(applied) != 2 {
		t.Fatalf("got %d apply lines, want 2:\n%s", len(applied), buf.String())
	}
	if !strings.Contains(applied[0], `"request_header":"req-7 forged"`) {
		t.Errorf("the first apply line lacks the cleaned header: %s", applied[0])
	}
	if strings.Contains(applied[1], reqlog.Attribute) {
		t.Errorf("an apply without the header carries %s: %s", reqlog.Attribute, applied[1])
	}
}
