// Package web embeds the built single-page application and serves it.
//
// The SPA is embedded rather than served from disk so that "alder" is one
// binary with no runtime layout to get wrong. When the build has not run, the
// binary still starts and serves a page saying so, because a server that
// refuses to start because the frontend is missing is useless for working on
// the backend.
package web

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

// dist holds the Vite build output. The all: prefix is required or files
// beginning with "_" and "." are skipped, and Vite emits an "assets" directory
// whose contents are hashed, not underscored, but the placeholder and any
// future dotfile would be silently dropped without it.
//
//go:embed all:dist
var dist embed.FS

// placeholder is served when the SPA has not been built into the binary.
const placeholder = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Alder</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.6 ui-sans-serif, system-ui, sans-serif; margin: 0;
         display: grid; place-items: center; min-height: 100vh; }
  main { max-width: 34rem; padding: 2rem; }
  code { background: rgba(127,127,127,.18); padding: .15em .4em; border-radius: 4px; }
  h1 { font-size: 1.4rem; margin: 0 0 .5rem; }
  p { opacity: .85; }
</style></head>
<body><main>
  <h1>Alder is running, without its interface</h1>
  <p>The single-page application was not built into this binary. The API is
     live at <code>/api/v1</code>.</p>
  <p>Build the frontend with <code>task web</code>, or run
     <code>npm run dev</code> in <code>web/</code> for a dev server that proxies
     the API.</p>
</main></body></html>`

// Built reports whether a real SPA build is embedded.
func Built() bool {
	f, err := dist.Open("dist/index.html")
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	return true
}

// Register mounts the SPA at the root of the app.
//
// It must be registered after the API routes: this handler answers every path
// it does not recognise with index.html, which is what makes client-side
// routing work on a hard refresh, and would otherwise swallow the API's 404s.
func Register(app *fiber.App) {
	if !Built() {
		app.Get("/*", func(c *fiber.Ctx) error {
			// A missing build is not an error for an API path; those routes are
			// registered first and never reach here.
			c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
			return c.SendString(placeholder)
		})
		return
	}

	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("web: the embedded dist directory is malformed: " + err.Error())
	}

	app.Use("/", filesystem.New(filesystem.Config{
		Root:               http.FS(sub),
		Index:              "index.html",
		NotFoundFile:       "index.html",
		ContentTypeCharset: "utf-8",
		// Hashed asset filenames are immutable; index.html must not be, or a
		// deploy leaves browsers pinned to the previous build's asset names.
		MaxAge: 0,
		Next: func(c *fiber.Ctx) bool {
			return strings.HasPrefix(c.Path(), "/api/")
		},
	}))
}

// ErrNotBuilt reports a missing SPA build to callers that care, such as the
// startup log.
var ErrNotBuilt = errors.New("web: the SPA has not been built into this binary")
