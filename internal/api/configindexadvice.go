package api

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/indexadvice"
)

// AdviseConfigIndexes reads a log the operator uploaded against this server's
// live index list, and says which indexes its searches lacked (1.41).
//
// The log is the only input (2026-10-22): nothing is fetched from the server
// but its configuration, which GET /config/indexes reads too. It is held in
// memory for the length of the request and dropped; Alder's own log gets one
// line with the counts, never a line of the upload.
func (s *Server) AdviseConfigIndexes(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var req IndexAdviceRequest
	if err := c.BodyParser(&req); err != nil {
		return badRequest(c, "The request body could not be read.", err.Error())
	}
	if strings.TrimSpace(req.Log) == "" {
		return badRequest(c, "The log is empty.", "Upload a 389 Directory Server access log, or an OpenLDAP log written with loglevel stats.")
	}
	opts := indexadvice.Options{}
	if req.SlowerThanMs != nil {
		if *req.SlowerThanMs < 1 {
			return badRequest(c, "slowerThanMs must be at least 1.", "")
		}
		opts.SlowerThan = time.Duration(*req.SlowerThanMs) * time.Millisecond
	}

	ctx, cancel := reqCtx(c)
	defer cancel()
	snap, err := config.Capture(ctx, sess.Conn, config.Options{})
	if err != nil {
		return configRefusal(c, s, err)
	}
	opts.Backends = indexadvice.FromConfig(snap)

	report, err := indexadvice.Read(strings.NewReader(req.Log), opts)
	if err != nil {
		return badRequest(c, "The log could not be read.", err.Error())
	}
	s.logger.InfoContext(c.UserContext(), "index advice read",
		"bytes", len(req.Log), "lines", report.Lines, "searches", report.Searches,
		"suggestions", len(report.Suggestions))
	return c.JSON(indexAdviceView(report))
}

func indexAdviceView(r *indexadvice.Report) IndexAdvice {
	out := IndexAdvice{
		Lines: r.Lines, Unrecognised: r.Unrecognised, Searches: r.Searches, Counted: r.Counted,
		WithoutResult: r.WithoutResult, Unattributed: r.Unattributed, Unparsed: r.Unparsed,
		Caveat:      indexadvice.Caveat,
		Suggestions: make([]IndexSuggestion, 0, len(r.Suggestions)),
	}
	if r.Format != "" {
		format := IndexAdviceFormat(r.Format)
		out.Format = &format
	}
	for _, sug := range r.Suggestions {
		view := IndexSuggestion{
			Backend: sug.Backend, Attribute: sug.Attribute, Type: sug.Type,
			Status:   IndexSuggestionStatus(sug.Status),
			Searches: sug.Searches, MarkedUnindexed: sug.MarkedUnindexed,
			ServerSaidNotIndexed: sug.ServerSaidNotIndexed,
			TotalSeconds:         sug.Total.Seconds(), WorstSeconds: sug.Worst.Seconds(),
			Examples: append([]string{}, sug.Examples...),
		}
		view.Reason = ptrIfSet(sug.Reason)
		view.First = timeIfSet(sug.First)
		view.Last = timeIfSet(sug.Last)
		out.Suggestions = append(out.Suggestions, view)
	}
	return out
}
