package api

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/replication"
)

// GetReplication reports what this server records about its own replication.
//
// The third of the read-only views that answer "why is the directory like
// this", after access (1.19) and password policy (1.21), and built to the
// same rule: it says what this server holds and where, and it never contacts
// anybody else. A report that dialled the host named in a configuration value
// would be a report that can be pointed at anything.
func (s *Server) GetReplication(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	ctx, cancel := reqCtx(c)
	defer cancel()

	report, err := replication.For(ctx, sess.Conn, replication.Options{})
	if err != nil {
		return s.fail(c, err)
	}

	out := ReplicationReport{
		Role:       ReplicationRole(report.Role),
		Why:        report.Why,
		Disclaimer: report.Disclaimer,
		Suffixes:   make([]ReplicatedSuffix, 0, len(report.Suffixes)),
	}
	out.Provider = ptrIfSet(report.Provider)
	for _, suffix := range report.Suffixes {
		view := ReplicatedSuffix{
			Dn: suffix.DN, Role: ReplicationRole(suffix.Role),
			Cursors: make([]ReplicationCursor, 0, len(suffix.Cursors)),
			Links:   make([]ReplicationLink, 0, len(suffix.Links)),
		}
		view.ServerId = ptrIfSet(suffix.ServerID)
		for _, cursor := range suffix.Cursors {
			view.Cursors = append(view.Cursors, ReplicationCursor{
				Origin: cursor.Origin, At: cursor.At, Raw: cursor.Raw,
			})
		}
		for _, link := range suffix.Links {
			view.Links = append(view.Links, replicationLink(link))
		}
		view.Notes = ptrIfAny(suffix.Notes)
		out.Suffixes = append(out.Suffixes, view)
	}
	if len(report.Unread) > 0 {
		unread := make([]AccessUnread, 0, len(report.Unread))
		for _, u := range report.Unread {
			unread = append(unread, AccessUnread{Where: u.Where, Reason: u.Reason})
		}
		out.Unread = &unread
	}
	return c.JSON(out)
}

func replicationLink(link replication.Link) ReplicationLink {
	out := ReplicationLink{
		Name:      link.Name,
		Direction: ReplicationLinkDirection(link.Direction),
		State:     ReplicationLinkState(link.State),
	}
	out.Peer = ptrIfSet(link.Peer)
	out.BindDn = ptrIfSet(link.BindDN)
	out.Transport = ptrIfSet(link.Transport)
	out.Status = ptrIfSet(link.Status)
	out.Dn = ptrIfSet(link.DN)
	out.LastUpdate = timeIfSet(link.LastUpdate)
	out.LastInit = timeIfSet(link.LastInit)
	if link.InProgress {
		out.InProgress = ptr(true)
	}
	out.Notes = ptrIfAny(link.Notes)
	return out
}

// timeIfSet leaves a timestamp out rather than sending the zero time, which a
// client would render as the first second of the year one.
func timeIfSet(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	out := at
	return &out
}
