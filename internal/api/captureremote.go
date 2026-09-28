package api

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/allowlist"
	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Capturing the server you are not connected to.
//
// A configuration snapshot exists to be compared against another server, and
// until now Alder could only capture the one it was connected to. So the
// comparison an operator actually wants -- this replica against the primary
// -- meant capturing one, disconnecting, connecting to the other, and
// finding the capture gone. A UI audit measured forty-six interactions
// before the drift appeared on screen, and lost its first capture entirely.
// (1.31 stopped the bench being cleared, which removed the loss; this
// removes the second connection.)
//
// The shape of the thing is deliberately small. It reads one tree and closes
// the connection. It never writes, it never becomes a session, and the
// credentials exist for the length of one request.

// CaptureFromAnotherServer captures the configuration of a directory other
// than this session's own.
func (s *Server) CaptureFromAnotherServer(c *fiber.Ctx) error {
	// A session is still required. Not because this reads the session's
	// directory -- it does not -- but because the endpoint dials outward on
	// the operator's behalf, and an unauthenticated caller must not be able
	// to make this Alder open connections to hosts it names.
	if sess := s.require(c); sess == nil {
		return nil
	}

	var body RemoteCaptureRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	if body.Kind != nil && *body.Kind != snapshot.KindConfig {
		return badRequest(c, "Only a configuration snapshot can be captured from another server.",
			"A schema or data snapshot of somewhere else is a bigger promise than this endpoint makes.")
	}

	cfg, ok := connConfigFrom(c, body.From)
	if !ok {
		return nil
	}

	// The same check the connection screen goes through, on the server,
	// before anything is dialled. Without it this endpoint is a way around
	// the allowlist: "capture from" would reach hosts "connect to" cannot.
	if err := s.cfg.AllowedTargets.Check(cfg.Host, cfg.Port); err != nil {
		if errors.Is(err, allowlist.ErrNotAllowed) {
			// The target and nothing else: the body it came in carries a
			// bind password.
			s.logger.Info("remote capture refused by the target allowlist",
				"host", cfg.Host, "port", cfg.Port)
			return writeError(c, fiber.StatusForbidden, ErrorErrorTargetNotAllowed,
				"This Alder is not permitted to connect to that directory.",
				"The operator has restricted which directories this instance may reach. "+
					"Permitted: "+strings.Join(s.cfg.AllowedTargets.Endpoints(), ", "))
		}
		return badRequest(c, "The connection settings are not usable.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	conn, err := s.driver.Connect(ctx, cfg)
	if err != nil {
		s.logger.Info("remote capture could not connect",
			"host", cfg.Host, "port", cfg.Port, "tls", cfg.TLS, "error", err)
		return writeError(c, fiber.StatusBadGateway, ErrorErrorUpstream,
			"Could not connect to that directory.", err.Error())
	}
	// Closed however this ends. The connection is this request's, and
	// nothing outlives it -- no session, no cookie, no stored credential.
	defer func() { _ = conn.Close() }()

	snap, err := config.Capture(ctx, conn, config.Options{})
	if err != nil {
		return configRefusal(c, s, err)
	}

	var buf bytes.Buffer
	if err := snapshot.EncodeConfig(&buf, snap); err != nil {
		return s.fail(c, err)
	}
	name := fmt.Sprintf("alder-config-snapshot-%s-%s.json", snap.Source.Provider,
		time.Now().UTC().Format("2006-01-02T150405Z"))
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", name))
	return c.Send(buf.Bytes())
}

// connConfigFrom reads connection settings out of a request body, the same
// way a connection does.
//
// Shared with CreateSession deliberately: two readings of the same settings
// is two places for the TLS rules to drift apart, and the rule that matters
// -- a plaintext target is refused unless this Alder was started to permit
// it -- lives in ConnConfig.Validate, which both go through.
func connConfigFrom(c *fiber.Ctx, body ConnectRequest) (directory.ConnConfig, bool) {
	cfg := directory.ConnConfig{
		Host:         strings.TrimSpace(body.Host),
		Port:         body.Port,
		TLS:          directory.TLSMode(body.Tls),
		BindDN:       strings.TrimSpace(deref(body.BindDn)),
		ServerName:   strings.TrimSpace(deref(body.ServerName)),
		Timeout:      requestTimeout,
		ConfigBindDN: strings.TrimSpace(deref(body.ConfigBindDn)),
	}
	if body.BindPassword != nil {
		cfg.BindPassword = *body.BindPassword
	}
	if body.ConfigBindPassword != nil {
		cfg.ConfigBindPassword = *body.ConfigBindPassword
	}
	if body.InsecureSkipVerify != nil {
		cfg.InsecureSkipVerify = *body.InsecureSkipVerify
	}
	if body.CaCertificate != nil && strings.TrimSpace(*body.CaCertificate) != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(*body.CaCertificate)) {
			_ = badRequest(c, "The CA certificate is not a PEM bundle.", "")
			return directory.ConnConfig{}, false
		}
		cfg.CACertificates = pool
	}
	if err := cfg.Validate(); err != nil {
		_ = badRequest(c, "The connection settings are not usable.", err.Error())
		return directory.ConnConfig{}, false
	}
	return cfg, true
}
