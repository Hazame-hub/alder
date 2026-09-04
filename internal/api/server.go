package api

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/session"
)

// Config is what the server needs from the command line.
type Config struct {
	// Secure marks the deployment as HTTPS, which decides the cookie name and
	// whether the Secure attribute is set. It is true unless the operator asked
	// for plain HTTP.
	Secure bool
	// AllowPlaintextLDAP permits connecting to a directory without TLS.
	AllowPlaintextLDAP bool
	// ReadOnly refuses every write at the API layer, whatever the directory
	// would have allowed. It is a seatbelt for demos and for a bind that has
	// more rights than the session needs.
	ReadOnly bool

	IdleTimeout time.Duration
	MaxLifetime time.Duration

	// SourceURL is where this build's source can be obtained, served at
	// /api/v1/source to satisfy AGPL-3.0 section 13. An operator running a
	// modified Alder is required to point it at their own source.
	SourceURL string
	// Version is reported alongside the source offer, so someone can tell which
	// build they are being offered the source of.
	Version string
}

// Server implements the generated ServerInterface.
type Server struct {
	driver   directory.Driver
	sessions *session.Store
	logger   *slog.Logger
	cfg      Config
}

// NewServer returns a Server. The caller owns the session store's lifetime and
// should call Close on shutdown.
func NewServer(logger *slog.Logger, cfg Config) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		driver:   ldapdriver.New(logger, cfg.AllowPlaintextLDAP),
		sessions: session.NewStore(logger, cfg.IdleTimeout, cfg.MaxLifetime),
		logger:   logger,
		cfg:      cfg,
	}
}

// Close releases every directory connection.
func (s *Server) Close() { s.sessions.Close() }

// Register mounts the API on a Fiber router under /api/v1.
func (s *Server) Register(app *fiber.App) {
	RegisterHandlersWithOptions(app, s, FiberServerOptions{BaseURL: "/api/v1"})
	s.registerSourceOffer(app.Group("/api/v1"))
}

// cookieName is the session cookie for this deployment.
func (s *Server) cookieName() string {
	if s.cfg.Secure {
		return session.CookieName
	}
	return session.CookieNameInsecure
}

// setSessionCookie issues the session cookie.
//
// httpOnly keeps it away from any script on the page, Secure keeps it off
// plaintext connections, and SameSite=Strict means a request originating from
// another site never carries it: with no CSRF token in the design, SameSite is
// the whole defence, so it is Strict rather than Lax.
func (s *Server) setSessionCookie(c *fiber.Ctx, id string) {
	c.Cookie(&fiber.Cookie{
		Name:     s.cookieName(),
		Value:    id,
		Path:     "/",
		HTTPOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     s.cookieName(),
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: fiber.CookieSameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// current resolves the session from the request cookie.
func (s *Server) current(c *fiber.Ctx) (*session.Session, error) {
	return s.sessions.Get(c.Cookies(s.cookieName()))
}

// require resolves the session or writes a 401 and returns nil.
func (s *Server) require(c *fiber.Ctx) *session.Session {
	sess, err := s.current(c)
	if err != nil {
		_ = writeError(c, fiber.StatusUnauthorized, ErrorErrorUnauthorized,
			"Not connected to a directory. Connect first.", "")
		return nil
	}
	return sess
}

// requireWritable resolves the session and refuses writes in read-only mode.
func (s *Server) requireWritable(c *fiber.Ctx) *session.Session {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	if sess.ReadOnly {
		_ = writeError(c, fiber.StatusForbidden, ErrorErrorForbidden,
			"This Alder instance is running read-only, so it will not write to the directory.", "")
		return nil
	}
	return sess
}

// --- error mapping ----------------------------------------------------------

// writeError renders an Error body with the given status.
func writeError(c *fiber.Ctx, status int, code ErrorError, message, detail string) error {
	body := Error{Error: code, Message: message}
	if detail != "" {
		body.Detail = &detail
	}
	return c.Status(status).JSON(body)
}

// fail turns a domain error into the right status and body.
//
// The mapping matters for the UI more than it looks: "you are not allowed to do
// that", "that entry does not exist" and "the server rejected this value" are
// three different things a user can act on, and collapsing them into 500 turns
// every one of them into a support ticket.
func (s *Server) fail(c *fiber.Ctx, err error) error {
	var ldapErr *ldapdriver.Error
	if errors.As(err, &ldapErr) {
		code := ldapErr.Code
		switch {
		case ldapErr.IsNoSuchObject():
			return writeErrorWithLDAP(c, fiber.StatusNotFound, ErrorErrorNotFound,
				"No such entry.", ldapErr.Error(), code)
		case ldapErr.IsInsufficientAccess():
			return writeErrorWithLDAP(c, fiber.StatusForbidden, ErrorErrorForbidden,
				"The directory refused this operation for the account you are bound as.",
				ldapErr.Error(), code)
		case ldapErr.IsAuth():
			return writeErrorWithLDAP(c, fiber.StatusUnauthorized, ErrorErrorUnauthorized,
				"The directory rejected the bind.", ldapErr.Error(), code)
		case ldapErr.IsConstraintViolation():
			return writeErrorWithLDAP(c, fiber.StatusUnprocessableEntity, ErrorErrorConstraintViolation,
				"The directory rejected the change.", ldapErr.Error(), code)
		default:
			return writeErrorWithLDAP(c, fiber.StatusBadGateway, ErrorErrorUpstream,
				"The directory returned an error.", ldapErr.Error(), code)
		}
	}

	var syntaxErr *ldif.SyntaxError
	if errors.As(err, &syntaxErr) {
		return writeError(c, fiber.StatusBadRequest, ErrorErrorBadRequest,
			"The LDIF could not be parsed.", syntaxErr.Error())
	}
	var urlErr *ldif.ErrURLReference
	if errors.As(err, &urlErr) {
		return writeError(c, fiber.StatusBadRequest, ErrorErrorBadRequest,
			"The LDIF references a URL, which Alder does not fetch.", urlErr.Error())
	}
	if errors.Is(err, directory.ErrEmptyChange) {
		return writeError(c, fiber.StatusBadRequest, ErrorErrorBadRequest,
			"Nothing changed, so there is nothing to apply.", "")
	}
	if errors.Is(err, session.ErrNotFound) {
		return writeError(c, fiber.StatusUnauthorized, ErrorErrorUnauthorized,
			"The session has expired. Connect again.", "")
	}

	// Anything unrecognised is logged in full and reported in outline. The
	// server's internal detail is not the browser's business.
	s.logger.Error("request failed", "path", c.Path(), "error", err)
	return writeError(c, fiber.StatusInternalServerError, ErrorErrorInternal,
		"Something went wrong.", "")
}

// failChange is fail() for a change that the directory refused.
//
// A result code on its own rarely tells an operator what to do; knowing what was
// being attempted usually does. Everything else about the response is identical,
// so a caller that ignores the hint sees no difference.
func (s *Server) failChange(
	c *fiber.Ctx, err error, record directory.ChangeRecord, caps directory.Capabilities,
) error {
	var ldapErr *ldapdriver.Error
	if errors.As(err, &ldapErr) {
		if hint := ldapHint(ldapErr.Code, record, caps); hint != "" {
			c.Locals(hintLocal, hint)
		}
	}
	return s.fail(c, err)
}

// hintLocal carries the explanation from failChange to the writer below without
// threading it through every error path that has nothing to explain.
const hintLocal = "alder.ldap.hint"

func writeErrorWithLDAP(c *fiber.Ctx, status int, code ErrorError, message, detail string, ldapCode uint16) error {
	n := int(ldapCode)
	body := Error{Error: code, Message: message, Detail: &detail, LdapCode: &n}
	if hint, ok := c.Locals(hintLocal).(string); ok && hint != "" {
		body.Hint = &hint
	}
	return c.Status(status).JSON(body)
}

// badRequest is the common "the client sent something unusable" reply.
func badRequest(c *fiber.Ctx, message, detail string) error {
	return writeError(c, fiber.StatusBadRequest, ErrorErrorBadRequest, message, detail)
}

// parseDN parses a user-supplied DN and writes a 400 if it does not parse.
//
// Every DN entering the API goes through this. There is no path where a string
// from a query parameter is concatenated into a DN, which is rule 2 of the
// charter enforced at the boundary.
func parseDNParam(c *fiber.Ctx, raw string) (dn.DN, bool) {
	d, err := dn.Parse(raw)
	if err != nil {
		_ = badRequest(c, "That is not a valid distinguished name.", err.Error())
		return nil, false
	}
	return d, true
}
