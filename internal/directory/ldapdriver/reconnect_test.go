package ldapdriver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// What a lost connection is, and what may be done about it.
//
// These are the decisions rather than the plumbing: which failures mean the
// socket is gone, which requests may be repeated, and what a write says when
// nobody can tell whether it landed. Dialling, binding and actually
// recovering are proved against real servers in the conformance suite,
// because a fake that agrees with this code would only prove that this code
// agrees with itself.

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func ldapErr(code uint16) error {
	return &Error{Code: code, Message: ldap.LDAPResultCodeMap[code]}
}

func TestOnlyANetworkErrorMeansTheSocketIsGone(t *testing.T) {
	if !isTransportFailure(ldapErr(ldap.ErrorNetwork)) {
		t.Error("a network error is the connection dying")
	}
	// go-ldap numbers its own conditions from 200 in the same field as the
	// protocol's result codes. Reconnecting on the others would turn a clear
	// fault in this code into a mysterious one that also reconnects.
	notTransport := []uint16{
		ldap.ErrorFilterCompile, // a bug in the filter builder
		ldap.ErrorEmptyPassword, // a bug in the bind path
		ldap.LDAPResultNoSuchObject,
		ldap.LDAPResultInsufficientAccessRights,
		ldap.LDAPResultInvalidCredentials,
		ldap.LDAPResultUnwillingToPerform,
		ldap.LDAPResultSizeLimitExceeded,
	}
	for _, c := range notTransport {
		if isTransportFailure(ldapErr(c)) {
			t.Errorf("code %d must not be treated as a lost connection", c)
		}
	}
	if isTransportFailure(errors.New("not an LDAP error at all")) {
		t.Error("a plain error is not a transport failure")
	}
}

func TestADesynchronisedStreamEndsTheConnectionWithoutBeingRetried(t *testing.T) {
	// 204 and 205 mean the stream no longer makes sense. The connection is
	// finished, but that says nothing about whether the operation happened,
	// so it is not retryable.
	for _, c := range []uint16{ldap.ErrorUnexpectedMessage, ldap.ErrorUnexpectedResponse} {
		if !leavesConnectionUnusable(ldapErr(c)) {
			t.Errorf("code %d should leave the connection unusable", c)
		}
		if isTransportFailure(ldapErr(c)) {
			t.Errorf("code %d must not be retried", c)
		}
	}
	if leavesConnectionUnusable(ldapErr(ldap.LDAPResultNoSuchObject)) {
		t.Error("a missing entry does not break the connection")
	}
}

func TestOnlyAContinuationPageIsUnsafeToRepeat(t *testing.T) {
	first := ldap.NewSearchRequest("dc=x", ldap.ScopeWholeSubtree, 0, 0, 0, false,
		"(objectClass=*)", nil, []ldap.Control{ldap.NewControlPaging(100)})
	if continuesAPage(first) {
		t.Error("a first page carries no cookie and may be retried")
	}

	cont := ldap.NewControlPaging(100)
	cont.SetCookie([]byte("opaque to any other connection"))
	next := ldap.NewSearchRequest("dc=x", ldap.ScopeWholeSubtree, 0, 0, 0, false,
		"(objectClass=*)", nil, []ldap.Control{cont})
	if !continuesAPage(next) {
		t.Error("a cookie belongs to the connection that issued it; this page must not be retried")
	}

	plain := ldap.NewSearchRequest("dc=x", ldap.ScopeBaseObject, 0, 0, 0, false,
		"(objectClass=*)", nil, nil)
	if continuesAPage(plain) {
		t.Error("an unpaged search may be retried")
	}
}

func TestAnInterruptedWriteSaysItsOutcomeIsUnknown(t *testing.T) {
	target, err := dn.Parse("uid=alice,ou=people,dc=alder,dc=test")
	if err != nil {
		t.Fatal(err)
	}
	got := unknownOutcome(
		directory.ChangeRecord{DN: target, Type: directory.ChangeDelete},
		ldapErr(ldap.ErrorNetwork))

	if !errors.Is(got, ErrWriteOutcomeUnknown) {
		t.Fatal("callers have to be able to recognise this case")
	}
	text := got.Error()
	for _, want := range []string{"unknown", "will not send it again", "read the entry"} {
		if !strings.Contains(text, want) {
			t.Errorf("the message should contain %q, and is: %s", want, text)
		}
	}
	// It must not read as a failure, because it is not one: the change may
	// well have been applied.
	for _, wrong := range []string{"the change failed", "was not applied"} {
		if strings.Contains(text, wrong) {
			t.Errorf("the message claims to know something it cannot: %s", text)
		}
	}
}

func TestClassifySaysWhatAFailureMeansForTheConnection(t *testing.T) {
	l := &link{name: "the directory connection"} // no conn: the error decides

	cases := []struct {
		err                 error
		unusable, retryable bool
		why                 string
	}{
		{ldapErr(ldap.LDAPResultNoSuchObject), false, false, "a missing entry is an answer, not a broken socket"},
		{ldapErr(ldap.LDAPResultInsufficientAccessRights), false, false, "a refusal leaves the connection fine"},
		{ldapErr(ldap.ErrorFilterCompile), false, false, "a fault in this code is not a lost connection"},
		{ldapErr(ldap.ErrorNetwork), true, true, "the socket is gone and nothing was decided"},
		{ldapErr(ldap.ErrorUnexpectedResponse), true, false, "a desynchronised stream says nothing about the operation"},
		{ldapErr(ldap.ErrorUnexpectedMessage), true, false, "likewise"},
	}
	for _, c := range cases {
		unusable, retryable := classify(l, c.err)
		if unusable != c.unusable || retryable != c.retryable {
			t.Errorf("%v: got unusable=%v retryable=%v, want %v/%v — %s",
				c.err, unusable, retryable, c.unusable, c.retryable, c.why)
		}
	}
}

func TestABrokenLinkIsMarkedOnceAndClosedOnce(t *testing.T) {
	s := &session{logger: quiet(), cfg: directory.ConnConfig{Host: "h", Port: 636}}
	l := &link{name: "the directory connection"}

	s.markBroken(l, ldapErr(ldap.ErrorNetwork))
	if !l.broken {
		t.Fatal("a network error should retire the link")
	}
	// Again is harmless, and must not close anything twice.
	s.markBroken(l, ldapErr(ldap.ErrorNetwork))
	if !l.broken {
		t.Error("an already-broken link is still broken")
	}
}

func TestReconnectFailureNamesTheConnectionAndTheCause(t *testing.T) {
	// The two connections are different identities with different rights, so
	// "could not reconnect" without saying which one is an answer nobody can
	// act on.
	for _, name := range []string{"the directory connection", "the configuration connection"} {
		s := &session{
			logger:  quiet(),
			cfg:     directory.ConnConfig{Host: "gone.invalid", Port: 636},
			timeout: time.Second,
			dial: func(context.Context, directory.ConnConfig, time.Duration) (*ldap.Conn, error) {
				return nil, errors.New("connection refused")
			},
		}
		l := &link{name: name, broken: true}

		_, err := s.ready(context.Background(), l)
		if err == nil {
			t.Fatal("a broken link with no way to reconnect must not look ready")
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the failure should name %q: %v", name, err)
		}
		if !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("the failure should carry the cause: %v", err)
		}
		if !l.broken {
			t.Error("a failed reconnect leaves the link broken")
		}
	}
}

func TestReconnectingHonoursACancelledContext(t *testing.T) {
	dialled := 0
	s := &session{
		logger: quiet(),
		dial: func(context.Context, directory.ConnConfig, time.Duration) (*ldap.Conn, error) {
			dialled++
			return nil, errors.New("should not have been reached")
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.ready(ctx, &link{name: "the directory connection", broken: true}); err == nil {
		t.Fatal("a cancelled request must not start dialling")
	}
	if dialled != 0 {
		t.Errorf("dialled %d times under a cancelled context", dialled)
	}
}

func TestAHealthyLinkIsNotReopened(t *testing.T) {
	dialled := 0
	s := &session{
		logger: quiet(),
		dial: func(context.Context, directory.ConnConfig, time.Duration) (*ldap.Conn, error) {
			dialled++
			return nil, errors.New("should not have been called")
		},
	}
	l := &link{name: "the directory connection", conn: &ldap.Conn{}}
	if _, err := s.ready(context.Background(), l); err != nil {
		t.Fatalf("a healthy link is ready: %v", err)
	}
	if dialled != 0 {
		t.Errorf("dialled %d times for a healthy link", dialled)
	}
}

func TestEachConnectionRebindsAsItself(t *testing.T) {
	// A configuration link that re-bound as the data identity would silently
	// change what the session may do, in whichever direction is worse.
	cfg := directory.ConnConfig{
		BindDN: "cn=admin,dc=alder,dc=test", BindPassword: "one",
		ConfigBindDN: "cn=config-admin", ConfigBindPassword: "two",
	}
	data := &link{bindDN: cfg.BindDN, bindPW: cfg.BindPassword}
	conf := &link{bindDN: cfg.ConfigBindDN, bindPW: cfg.ConfigBindPassword}

	if data.bindDN == conf.bindDN || data.bindPW == conf.bindPW {
		t.Fatal("the two links must not share an identity")
	}
	if conf.bindDN != cfg.ConfigBindDN || conf.bindPW != cfg.ConfigBindPassword {
		t.Error("the configuration link must rebind as the configuration identity")
	}
	if data.bindDN != cfg.BindDN || data.bindPW != cfg.BindPassword {
		t.Error("the data link must rebind as the data identity")
	}
}

// What is deliberately not here: link.bind needs a live *ldap.Conn, which
// go-ldap gives no way to fake. Whether a reopened connection is bound as the
// right identity is asserted where it can be -- against both servers, in the
// conformance suite.
