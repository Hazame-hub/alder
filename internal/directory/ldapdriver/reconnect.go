package ldapdriver

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
)

// Surviving the loss of a connection.
//
// A UI audit watched every directory call fail with 502 while the session
// endpoint kept answering 200 and the overview kept reporting a healthy bind.
// Disconnect and reconnect cured it completely. The trigger was never
// reproduced, and this does not claim to have found it -- what it does is
// remove the reason a lost connection has to be the operator's problem: the
// session dials again and carries on.
//
// Three rules hold the whole thing up.
//
// A read may be repeated. A search has no effect on the directory, so running
// it twice is running it once. A *continuation* page may not: see
// searchLocked.
//
// A write may not, ever. If the connection dies after the request went out,
// the server may have applied it, and nothing the client can see says which.
// Repeating it would be a second change nobody confirmed -- in a product whose
// central promise is that no modification reaches a directory without being
// previewed and confirmed exactly once. So a write interrupted in flight is
// reported as what it is: an unknown outcome, for a person to look at.
//
// The two connections recover separately. The data identity and the
// configuration identity are different rights on purpose, and a reconnect that
// re-bound one as the other would silently change what the session may do.
// Each link carries the identity that binds it.

// link is one bound connection, and what it takes to build another like it.
type link struct {
	conn *ldap.Conn
	// name appears in failure messages: "the directory connection" reads very
	// differently from "the configuration connection" to somebody deciding
	// whether their change went in.
	name string
	// bindDN and bindPW are this connection's own identity. Held in memory for
	// the life of the session and nowhere else, exactly as the first bind was
	// -- reconnecting reuses what is already here and persists nothing new.
	bindDN string
	bindPW string
	// broken means the socket failed and this link must be rebuilt before it
	// is used again.
	broken bool
}

// bind authenticates a freshly dialled connection as this link's identity.
// An empty DN is an anonymous session, which is a legitimate way to browse a
// directory that permits it.
func (l *link) bind(conn *ldap.Conn) error {
	if l.bindDN == "" {
		if err := conn.UnauthenticatedBind(""); err != nil {
			return fmt.Errorf("anonymous bind: %w", cleanLDAPError(err))
		}
		return nil
	}
	if err := conn.Bind(l.bindDN, l.bindPW); err != nil {
		// Without the password and without the server's diagnostic, the same
		// way the first bind reports a failure.
		return fmt.Errorf("binding as %s: %w", l.bindDN, cleanLDAPError(err))
	}
	return nil
}

// isTransportFailure reports that the connection itself failed, as opposed to
// the directory answering "no".
//
// Deliberately narrower than Error.IsClientSide. go-ldap numbers its own
// conditions from 200, and only 200 -- Network Error -- means the socket is
// gone. 201 is a filter this code failed to compile and 206 is an empty
// password it refused to send: both are bugs here, and reconnecting would
// turn a clear client-side fault into a mysterious one that also reconnects.
//
// 204 and 205 -- Unexpected Message, Unexpected Response -- mean the stream is
// out of step with itself. The connection is not usable after that, so it is
// marked broken, but it is not treated as retryable: a desynchronised stream
// is not a fact about whether the operation happened.
func isTransportFailure(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == ldap.ErrorNetwork
}

// leavesConnectionUnusable reports a failure after which this connection
// cannot be trusted for anything further, whatever it says about the
// operation.
func leavesConnectionUnusable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == ldap.ErrorNetwork ||
		e.Code == ldap.ErrorUnexpectedMessage ||
		e.Code == ldap.ErrorUnexpectedResponse
}

// classify says what a failed operation means for the connection it ran on:
// whether the connection is finished, and whether the operation may be tried
// again on a new one.
//
// It asks the connection as well as the error, because the error alone is
// not enough. When go-ldap's reader goroutine loses the socket it stores
// fmt.Errorf("unable to read LDAP response packet: %s", err) and hands that
// to the waiting request -- a plain error, formatted with %s, so there is no
// result code to read and no wrapped cause to unwrap. Only the connection
// knows, and it does: IsClosing is set by the time that error is delivered.
// Both servers produce the code-200 form as well, and OpenLDAP produced the
// plain one in the conformance suite the first time this was run, which is
// how the gap was found rather than shipped.
//
// It must be called before the link is marked broken. Closing a connection
// makes it report itself closing, so afterwards every failure would look
// like a lost socket, including a refusal that was nothing of the kind.
func classify(l *link, err error) (unusable, retryable bool) {
	if (l.conn != nil && l.conn.IsClosing()) || isTransportFailure(err) {
		// The socket is gone. Nothing was decided by the directory, so a
		// read may be asked again -- and a write may not, which is Apply's
		// business rather than this function's.
		return true, true
	}
	if leavesConnectionUnusable(err) {
		// The stream is out of step with itself. The connection is finished,
		// but that is not a statement about whether the operation happened.
		return true, false
	}
	return false, false
}

// revive dials and binds a replacement connection for a broken link.
//
// The caller holds s.mu, so no operation can be using the old connection
// while it is swapped out.
func (s *session) revive(ctx context.Context, l *link) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := s.dial(ctx, s.cfg, s.timeout)
	if err != nil {
		return fmt.Errorf("directory: %s was lost and could not be reopened: %w", l.name, err)
	}
	if err := l.bind(conn); err != nil {
		_ = conn.Close()
		// A reconnect that dials but cannot bind is worth saying out loud:
		// the usual cause is a password changed underneath a live session,
		// and "could not be reopened" alone would send somebody to the
		// network.
		return fmt.Errorf("directory: %s was reopened but %w", l.name, err)
	}
	if l.conn != nil {
		// Best effort: the old one is already broken by definition.
		_ = l.conn.Close()
	}
	l.conn = conn
	l.broken = false
	s.logger.Info("reopened a directory connection",
		"connection", l.name, "address", s.cfg.Address(), "bind_dn", l.bindDN)
	return nil
}

// ready returns the link's connection, rebuilding it first if it is known to
// be broken.
//
// This is what makes a write safe to attempt after a failure: the connection
// is replaced *before* the request goes out, so the change is sent once, on a
// connection that works, and is never a repeat of one that may already have
// landed.
func (s *session) ready(ctx context.Context, l *link) (*ldap.Conn, error) {
	if l.broken || l.conn == nil {
		if err := s.revive(ctx, l); err != nil {
			return nil, err
		}
	}
	return l.conn, nil
}

// markBroken retires a link so the next operation rebuilds it.
func (s *session) markBroken(l *link, err error) {
	if l.broken {
		return
	}
	l.broken = true
	if l.conn != nil {
		_ = l.conn.Close()
	}
	s.logger.Warn("a directory connection failed and will be reopened on the next operation",
		"connection", l.name, "address", s.cfg.Address(), "error", err)
}

// ErrWriteOutcomeUnknown is returned when the connection failed while a
// change was in flight.
//
// It is not "the change failed". The request may have reached the server and
// been applied before the socket went; the client cannot tell, and neither
// can the person, which is why this says so instead of guessing. Read the
// entry and decide.
var ErrWriteOutcomeUnknown = errors.New(
	"directory: the connection was lost while the change was being sent, so whether " +
		"the directory applied it is unknown — Alder will not send it again by itself; " +
		"read the entry and, if the change is not there, apply it once more")

// unknownOutcome wraps a lost-connection write failure.
func unknownOutcome(ch directory.ChangeRecord, err error) error {
	// Both are wrapped: callers match on the sentinel, and the result code
	// underneath stays inspectable for anyone who needs it.
	return fmt.Errorf("%w (%s: %w)", ErrWriteOutcomeUnknown, ch.Summary(), err)
}
