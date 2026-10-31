// Package reqlog puts the value of one request header the operator names on
// every log line written while Alder answers that request, so that Alder's
// lines can be matched to the access log of the proxy in front of it
// (docs/proposals/request-header-logging.md).
//
// The value is a correlation key and nothing more. Nothing in Alder reads it
// back, decides anything on it, or sends it anywhere but the log.
package reqlog

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// Attribute is the name every log line carries the value under. It is fixed,
// not the header's own name, so that a log query reads the same whatever each
// deployment calls its header.
const Attribute = "request_header"

// MaxLen bounds what a client can make Alder write per line.
const MaxLen = 128

type key struct{}

// With returns ctx carrying value, cleaned. A value that is empty once
// cleaned leaves ctx as it was.
func With(ctx context.Context, value string) context.Context {
	if v := Clean(value); v != "" {
		return context.WithValue(ctx, key{}, v)
	}
	return ctx
}

// From returns the value ctx carries, if any.
func From(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	v, ok := ctx.Value(key{}).(string)
	return v, ok
}

// Clean keeps printable ASCII and nothing else, then cuts the result at
// MaxLen bytes. slog already quotes values in both formats; this keeps one
// line one line, and a hostile client from making the log large.
func Clean(value string) string {
	var b strings.Builder
	for i := 0; i < len(value) && b.Len() < MaxLen; i++ {
		if c := value[i]; c >= 0x20 && c < 0x7f {
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String())
}

// Headers that carry credentials or sessions, and words that mark one.
// Logging any of them would break hard rule 6 by configuration, so the list
// is not configurable, like rule 6's own.
var (
	refusedNames = []string{"authorization", "proxy-authorization", "cookie", "set-cookie"}
	// The words reach the names deployments give their own credential
	// headers: X-Authorization and X-Session-Id carry a bearer value or a
	// session as surely as Authorization and Cookie do; Cf-Access-Jwt-Assertion
	// and X-Amzn-Oidc-Data are signed identity assertions an application
	// behind the proxy accepts as a login. COMPATIBILITY.md 1.40 lets a later
	// release refuse more names, never fewer.
	refusedWords = []string{
		"token", "secret", "password", "passwd", "key",
		"authorization", "cookie", "session", "credential", "jwt", "assertion", "oidc", "saml",
	}
)

// CheckName refuses a header name that is not a valid field name, or that
// carries credentials.
func CheckName(name string) error {
	if name == "" {
		return fmt.Errorf("--log-request-header: the header name is empty")
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return fmt.Errorf("--log-request-header: %q is not a header name", name)
		}
	}
	lower := strings.ToLower(name)
	for _, n := range refusedNames {
		if lower == n {
			return fmt.Errorf("--log-request-header: %s carries credentials and is never logged", name)
		}
	}
	for _, w := range refusedWords {
		if strings.Contains(lower, w) {
			return fmt.Errorf("--log-request-header: a header whose name contains %q may carry a credential and is never logged", w)
		}
	}
	return nil
}

// isTokenChar reports whether c may appear in an HTTP field name (RFC 9110
// section 5.6.2).
func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
}

// Middleware puts the named header's value, cleaned, in the request's
// context, where Handler finds it for every line logged with that context
// (c.UserContext(), and every context derived from it).
func Middleware(name string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if v := c.Get(name); v != "" {
			c.SetUserContext(With(c.UserContext(), v))
		}
		return c.Next()
	}
}

// Handler adds the request's value, when its context carries one, to every
// record h handles.
func Handler(h slog.Handler) slog.Handler { return handler{h} }

type handler struct{ slog.Handler }

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if v, ok := From(ctx); ok {
		r = r.Clone()
		r.AddAttrs(slog.String(Attribute, v))
	}
	return h.Handler.Handle(ctx, r)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler{h.Handler.WithAttrs(attrs)}
}

func (h handler) WithGroup(name string) slog.Handler {
	return handler{h.Handler.WithGroup(name)}
}
