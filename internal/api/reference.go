package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/allowlist"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/session"
)

// A second server inside one session (B6, docs/proposals/second-server.md).
//
// The comparisons Alder is best at are between two servers, and a session is
// one directory. A session can now hold one more connection, the reference,
// opened exactly as a session is -- the same settings, the same TLS rules,
// the same allowlist check before anything is dialled -- and held read-only
// by type: the session keeps it as a directory.Reader, which has no Apply, so
// there is no handler that could write to it by mistake.
//
// Reads that take a side name it. Writes do not: nothing in a /plan,
// /changes or /changeset body can name the reference, so every write goes to
// the session's own server, and the question "which server does this go to"
// has one answer.

// AttachReference connects to a second, read-only server for comparison.
func (s *Server) AttachReference(c *fiber.Ctx) error {
	// A session first: attaching dials outward on the operator's behalf, and
	// an unauthenticated caller must not be able to make this Alder open
	// connections to hosts it names.
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body ConnectRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	cfg, ok := connConfigFrom(c, body)
	if !ok {
		return nil
	}
	// The same check as a connection, on the server, before dialling.
	// Without it attaching is a way around the allowlist.
	if err := s.cfg.AllowedTargets.Check(cfg.Host, cfg.Port); err != nil {
		if errors.Is(err, allowlist.ErrNotAllowed) {
			s.logger.Info("reference refused by the target allowlist",
				"host", cfg.Host, "port", cfg.Port)
			return writeError(c, fiber.StatusForbidden, ErrorErrorTargetNotAllowed,
				"This Alder is not permitted to connect to that directory.",
				"The operator has restricted which directories this instance may reach. "+
					"Permitted: "+strings.Join(s.cfg.AllowedTargets.Endpoints(), ", "))
		}
		return badRequest(c, "The connection settings are not usable.", err.Error())
	}
	// Refused before dialling, so a second attach does not open a connection
	// only to close it. AttachReference asks again once it has one.
	if sess.Reference() != nil {
		return referenceAttached(c)
	}
	if s.sessions.Full() {
		return s.sessionsFull(c)
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	conn, err := s.driver.Connect(ctx, cfg)
	if err != nil {
		s.logger.Info("reference connection refused",
			"host", cfg.Host, "port", cfg.Port, "tls", cfg.TLS, "error", err)
		return writeError(c, fiber.StatusBadGateway, ErrorErrorUpstream,
			"Could not connect to that directory.", s.connectFailureDetail(cfg, err))
	}
	// Read now for the same reason a session does: the schema is how Alder
	// learns which of this directory's own attribute types are passwords by
	// inheritance, and the reference's entries are rendered by the same code.
	_, _ = conn.Schema(ctx)

	ref, err := s.sessions.AttachReference(sess, conn, cfg)
	if err != nil {
		_ = conn.Close()
		switch {
		case errors.Is(err, session.ErrReferenceAttached):
			return referenceAttached(c)
		case errors.Is(err, session.ErrFull):
			return s.sessionsFull(c)
		case errors.Is(err, session.ErrNotFound):
			return writeError(c, fiber.StatusUnauthorized, ErrorErrorUnauthorized,
				"Not connected.", "The session closed while the reference was being connected.")
		}
		return s.fail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(referenceInfo(ref))
}

// GetReference describes the reference server.
func (s *Server) GetReference(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ref := sess.Reference()
	if ref == nil {
		return writeError(c, fiber.StatusNotFound, ErrorErrorNotFound,
			"No reference server is attached to this session.", "")
	}
	return c.JSON(referenceInfo(ref))
}

// DetachReference closes the reference server. The session is untouched.
func (s *Server) DetachReference(c *fiber.Ctx) error {
	if sess, err := s.current(c); err == nil {
		s.sessions.DetachReference(sess)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func referenceAttached(c *fiber.Ctx) error {
	return writeError(c, fiber.StatusConflict, ErrorErrorConflict,
		"A reference server is already attached to this session.",
		"A session holds one reference. Detach it before attaching another.")
}

// referenceInfo describes a reference the way a session is described. It is
// read-only whatever the instance's --read-only says, because nothing can
// write to it.
func referenceInfo(ref *session.Reference) SessionInfo {
	caps := ref.Conn.Capabilities()
	info := SessionInfo{
		Connected:    true,
		Role:         ptr(ServerSideReference),
		Host:         ptr(ref.Host()),
		Port:         ptr(ref.Port()),
		Tls:          ptr(ref.TLS()),
		Verified:     ptr(ref.Verified()),
		BindDn:       ptr(ref.BindDN()),
		ReadOnly:     ptr(true),
		Capabilities: ptr(capabilitiesView(caps)),
	}
	if caps.VendorName != "" {
		info.VendorName = ptr(caps.VendorName)
	}
	if caps.VendorVersion != "" {
		info.VendorVersion = ptr(caps.VendorVersion)
	}
	return info
}

// onServer is the directory a read names: the session's own, or its
// reference. It answers the request itself, with a 409, when the reference
// is named and none is attached. The second result is the server's address,
// for a response that says which server it read.
//
// It returns a directory.Reader whichever is named, so a handler that takes a
// side cannot write through it.
func onServer(c *fiber.Ctx, sess *session.Session, on *ServerSide) (directory.Reader, string, bool) {
	if on == nil || *on == ServerSideSession {
		return sess.Conn, sess.Config.Address(), true
	}
	if *on != ServerSideReference {
		return nil, "", fail(badRequest(c, "on must be session or reference.", ""))
	}
	ref := sess.Reference()
	if ref == nil {
		return nil, "", fail(noReference(c))
	}
	return ref.Conn, ref.Address(), true
}

func noReference(c *fiber.Ctx) error {
	return writeError(c, fiber.StatusConflict, ErrorErrorConflict,
		"No reference server is attached to this session.",
		"Attach one from the header, or read the session's own server.")
}
