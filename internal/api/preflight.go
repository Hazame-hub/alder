package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/preflight"
	"github.com/hazame-hub/alder/internal/session"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Migration preflight.
//
// A preflight reads an artifact the caller holds against the directory the
// session is bound to, and answers with a report. It is the one handler in this
// package that takes an artifact from anywhere -- another server, another
// environment, another team -- and it is analysis only: it passes the session to
// preflight as a type with no write method, it never rewrites what it was
// given, and nothing in its answer is something a plan or an apply reads.

type preflightBody struct {
	Artifact     json.RawMessage `json:"artifact"`
	SchemaTarget *string         `json:"schemaTarget"`
}

// Preflight reports what of an artifact would carry across to this directory.
func (s *Server) Preflight(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body preflightBody
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	artifact, signature, ok := s.openDocument(c, bytes.TrimSpace(body.Artifact))
	if !ok {
		return nil
	}
	artifact = bytes.TrimSpace(artifact)
	if len(artifact) == 0 || artifact[0] != '{' {
		return badRequest(c, "The request carries no artifact.", "artifact must be a change package, a schema snapshot, a data snapshot or a configuration snapshot.")
	}
	opts := preflight.Options{NotFound: isNoSuchObject}
	if body.SchemaTarget != nil {
		if target := strings.TrimSpace(*body.SchemaTarget); target != "" {
			if _, err := dn.Parse(target); err != nil {
				return badRequest(c, "schemaTarget is not a DN.", err.Error())
			}
			opts.SchemaEntry = target
		}
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	opts.Capture = func(ctx context.Context, base dn.DN, scope, rawFilter string) (*snapshot.Snapshot, bool, error) {
		return s.preflightCapture(ctx, sess, base, scope, rawFilter)
	}
	opts.CaptureConfig = func(ctx context.Context) (*snapshot.ConfigSnapshot, error) {
		return config.Capture(ctx, sess.Conn, config.Options{})
	}
	report, err := preflight.Run(ctx, artifact, readOnly{sess.Conn}, opts)
	if err != nil {
		return preflightRefusal(c, s, err)
	}
	// The report carries what the artifact's signature amounted to, so a
	// person reading the report knows whose artifact it was about.
	return c.JSON(withSignature(report, signature))
}

// readOnly is the session as a preflight may see it: every read, and no Apply.
type readOnly struct{ preflight.Target }

// preflightCapture reads the target's entries under a data snapshot's base, as
// a snapshot capture would, refusing the trees a data snapshot never covers.
func (s *Server) preflightCapture(ctx context.Context, sess *session.Session, base dn.DN, scope, rawFilter string) (*snapshot.Snapshot, bool, error) {
	if kind := targetKind(sess.Conn.Capabilities(), base); kind != PlanTargetData {
		return nil, false, errors.New("the base is not directory data")
	}
	if strings.TrimSpace(rawFilter) == "" {
		rawFilter = "(objectClass=*)"
	}
	parsed, err := filter.Parse(rawFilter)
	if err != nil {
		return nil, false, err
	}
	if scope == "" {
		scope = "sub"
	}
	return s.captureLive(ctx, sess, liveRequest{base: base, scope: scope, rawFilter: rawFilter, parsed: parsed})
}

// preflightRefusal answers an artifact that could not be preflighted, with the
// code its own endpoints would use.
func preflightRefusal(c *fiber.Ctx, s *Server, err error) error {
	var pe *changepkg.Error
	var se *snapshot.Error
	switch {
	case errors.Is(err, preflight.ErrNotArtifact):
		return writeError(c, fiber.StatusBadRequest, ErrorErrorPreflightArtifactUnsupported,
			"The document is not an artifact a preflight reads.",
			"A preflight reads an Alder change package, a schema snapshot, a data snapshot or a configuration snapshot, recognised by their format field.")
	case errors.Is(err, preflight.ErrUnsupportedKind):
		return writeError(c, fiber.StatusBadRequest, ErrorErrorPreflightArtifactUnsupported,
			"The snapshot is of a kind a preflight does not read.", "A preflight reads data and schema snapshots.")
	case errors.As(err, &pe):
		return packageRefusal(c, err)
	case errors.As(err, &se):
		return snapshotRefusal(c, "", err)
	}
	return s.fail(c, err)
}
