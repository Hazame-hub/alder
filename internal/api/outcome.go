package api

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/outcome"
)

// Finding out what became of a change nobody can account for.
//
// The driver refuses to repeat a write whose connection died in flight, and
// refuses to say whether it landed, because it does not know. That leaves a
// question, and this is where the question gets answered: by reading the
// directory, which is the only thing that can settle it.
//
// What this is not is the other half of a retry. There is no path from here
// to applying anything. A change the directory does not hold is made again
// the ordinary way -- planned against the directory as it is now, rendered
// as LDIF, confirmed -- because the review that preceded the interruption
// was a review of a directory that may since have moved.

// sessionReader lets internal/outcome ask a live session the one question it
// needs, and nothing else.
//
// The narrowness is the point. internal/outcome is handed something that can
// only read, so no amount of future editing in that package can make it
// write; the compiler enforces what the comments promise.
type sessionReader struct {
	ctx  context.Context
	sess directory.Session
}

func (r sessionReader) Read(target string, attributes []string) (*directory.Entry, error) {
	parsed, err := dn.Parse(target)
	if err != nil {
		return nil, err
	}
	entry, err := r.sess.Read(r.ctx, parsed, attributes)
	if err != nil {
		if isNoSuchObject(err) {
			// Absent is an answer, and a useful one: it is what proves a
			// delete landed and an add did not. Only a failure to ask is an
			// error.
			return nil, nil
		}
		return nil, err
	}
	return entry, nil
}

// DetermineChangeOutcome reports what reading the directory establishes about
// a change whose outcome was never confirmed.
func (s *Server) DetermineChangeOutcome(c *fiber.Ctx) error {
	// Deliberately not behind the read-only check. This reads, and the
	// moment an operator most needs to know whether a change landed is a
	// bad moment to tell them to reconnect as somebody who can write.
	sess := s.require(c)
	if sess == nil {
		return nil
	}

	var body ChangeRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	record, err := changeRecord(body)
	if err != nil {
		return badRequest(c, "The change could not be read.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	// The schema, so values are compared the way the directory compares
	// them. Without it the comparison falls back to exact bytes, which is
	// stricter than the server: it would call "Platform" and "platform" a
	// difference and send somebody to look at a change that was applied
	// exactly as they asked. A schema that cannot be read is not worth
	// failing the whole answer over, so the stricter comparison stands and
	// the verdict is still returned.
	sch, _ := sess.Conn.Schema(ctx)

	result := outcome.Determine(sessionReader{ctx: ctx, sess: sess.Conn}, record, sch)

	// Only "not applied" can be put right, and even then not from here.
	resolvable := result.Verdict == outcome.NotApplied

	s.logger.Info("judged a change whose outcome was unknown",
		"change", record.Summary(), "verdict", string(result.Verdict))

	out := ChangeOutcome{
		Verdict: ChangeOutcomeVerdict(result.Verdict),
		Reason:  result.Reason,
	}
	if result.Attribute != "" {
		out.Attribute = ptr(result.Attribute)
	}
	if resolvable {
		out.Resolvable = ptr(true)
	}
	return c.JSON(out)
}
