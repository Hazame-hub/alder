package api

import (
	"mime"

	"github.com/gofiber/fiber/v2"
)

// sameOriginWrites refuses a state-changing request that a page on another
// origin could have made with the session cookie attached.
//
// The cookie is SameSite=Strict, which keeps it off requests from another
// *site*. It does nothing about another origin on the same site: a page on
// wiki.example.com is same-site with alder.example.com, and on a laptop every
// http://localhost:<port> is same-site with Alder on another port. Such a page
// can submit a form to /api/v1/changes/apply with the cookie, and Fiber's
// BodyParser reads a form body as readily as JSON, so it would have been
// decoded into a change and applied.
//
// Two checks close that, neither of which a client the API has ever had
// trips over:
//
//   - **Every request body is JSON.** Every operation in api/openapi.yaml that
//     takes a body takes application/json, and both of Alder's clients send
//     it. A browser will not send JSON to another origin without a CORS
//     preflight, which Alder never answers, so a cross-origin page is left
//     with the "simple" types -- form encoding, multipart, text/plain -- and
//     those are refused before a handler reads them.
//   - **A browser that says where a request came from is believed.** Every
//     current browser sends Sec-Fetch-Site; a write it labels same-site or
//     cross-site is refused whatever its body. "none" is a request the user
//     made directly, and a client that is not a browser sends nothing.
//
// The Host and Origin headers are deliberately not compared: behind a reverse
// proxy the Host Alder sees is often not the one the browser used, and the
// check would refuse the proxy's own users. Sec-Fetch-Site is computed by the
// browser and survives any proxy.
func sameOriginWrites(c *fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return c.Next()
	}
	switch c.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return writeError(c, fiber.StatusForbidden, ErrorErrorForbidden,
			"Alder refuses a change requested by a page on another origin.",
			"The browser reported this request as "+c.Get("Sec-Fetch-Site")+
				"; Alder accepts writes only from its own pages and from clients that are not browsers.")
	}
	if len(c.Body()) > 0 || c.Get(fiber.HeaderContentType) != "" {
		media, _, err := mime.ParseMediaType(c.Get(fiber.HeaderContentType))
		if err != nil || media != fiber.MIMEApplicationJSON {
			return writeError(c, fiber.StatusUnsupportedMediaType, ErrorErrorBadRequest,
				"Alder's API takes request bodies as application/json only.", "")
		}
	}
	return c.Next()
}
