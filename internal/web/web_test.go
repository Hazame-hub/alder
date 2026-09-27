package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// What the static handler must get right, which is three different answers to
// three paths that all look like "not a route I know".

// answer is what the handler said, with the body already closed: the response
// itself never leaves this helper, so no caller can leak one.
type answer struct {
	status      int
	contentType string
}

func serve(t *testing.T, path string) answer {
	t.Helper()
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	// An API route, registered first the way the real server does it, so the
	// assertion below about /api/ falling through is about the real ordering
	// rather than about an empty app.
	app.Get("/api/v1/session", func(c *fiber.Ctx) error {
		return c.SendString("api")
	})
	Register(app)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	res, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	return answer{status: res.StatusCode, contentType: res.Header.Get("Content-Type")}
}

func TestAMissingAssetIsNotThePage(t *testing.T) {
	if !Built() {
		t.Skip("no SPA is embedded in this build; run \"task web\" first")
	}
	res := serve(t, "/assets/index-DOESNOTEXIST.js")
	if res.status != http.StatusNotFound {
		t.Errorf("a missing asset answered %d, want 404", res.status)
	}
	// The status is only half of it. A browser that asked for JavaScript and
	// was handed an HTML document reports a syntax error in a file that is
	// perfectly valid, which is the failure this exists to prevent.
	if got := res.contentType; strings.Contains(got, "text/html") {
		t.Errorf("a missing asset answered with %q", got)
	}
}

func TestAnUnrecognisedRouteStillReturnsTheApplication(t *testing.T) {
	if !Built() {
		t.Skip("no SPA is embedded in this build; run \"task web\" first")
	}
	// A hard refresh on a client-side route. This is the behaviour the
	// /assets/ rule above must not have broken: every path that is not an
	// asset and not the API still gets the page.
	for _, path := range []string{"/", "/?view=schema", "/somewhere/deep"} {
		res := serve(t, path)
		if res.status != http.StatusOK {
			t.Errorf("GET %s answered %d, want the application", path, res.status)
		}
		if got := res.contentType; !strings.Contains(got, "text/html") {
			t.Errorf("GET %s answered with %q, want the page", path, got)
		}
	}
}

func TestTheApiIsNeverAnsweredByTheStaticHandler(t *testing.T) {
	if !Built() {
		t.Skip("no SPA is embedded in this build; run \"task web\" first")
	}
	res := serve(t, "/api/v1/session")
	if res.status != http.StatusOK {
		t.Fatalf("the API route answered %d", res.status)
	}
	if got := res.contentType; strings.Contains(got, "text/html") {
		t.Errorf("the API route was answered by the SPA handler: %q", got)
	}
	// And an API path with no route behind it must still be the API's own 404,
	// not the page: a client that asked for JSON and got HTML with a 200 is
	// the same class of bug as the asset case above.
	missing := serve(t, "/api/v1/nothing-here")
	if missing.status == http.StatusOK {
		t.Error("an unknown API path was answered with the application")
	}
}

func TestTheRealAssetIsStillServed(t *testing.T) {
	if !Built() {
		t.Skip("no SPA is embedded in this build; run \"task web\" first")
	}
	// The rule is a guard, not a wall: whatever the build actually emitted has
	// to keep being served, or the application never loads at all.
	name := oneAsset(t)
	res := serve(t, "/assets/"+name)
	if res.status != http.StatusOK {
		t.Fatalf("GET /assets/%s answered %d", name, res.status)
	}
}

// oneAsset names a file the build really emitted.
func oneAsset(t *testing.T) string {
	t.Helper()
	entries, err := dist.ReadDir("dist/assets")
	if err != nil || len(entries) == 0 {
		t.Skip("this build embeds no assets directory")
	}
	return entries[0].Name()
}
