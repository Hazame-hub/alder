// Package ldapdriver implements directory.Driver over the LDAP protocol.
//
// It is the only driver in v1. Everything vendor-specific it does is decided
// from the RootDSE at connect time and recorded in a Capabilities value; the
// vendor name is read for display and never branched on.
package ldapdriver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Driver opens LDAP sessions.
type Driver struct {
	// Logger receives connection-level events. It never receives credentials
	// or attribute values.
	Logger *slog.Logger
	// AllowPlaintext permits a plaintext connection. It is false unless the
	// operator passed --i-know-this-is-insecure, per rule 7 of the charter.
	AllowPlaintext bool
}

// New returns a Driver.
func New(logger *slog.Logger, allowPlaintext bool) *Driver {
	if logger == nil {
		logger = slog.Default()
	}
	return &Driver{Logger: logger, AllowPlaintext: allowPlaintext}
}

// Connect dials the server, secures the connection, binds, and reads the
// RootDSE.
//
// The RootDSE read happens before the bind as well as after: the pre-bind read
// is what tells us whether StartTLS is available, and the post-bind read is
// what sees the naming contexts a server only reveals to an authenticated
// client. Servers differ on which attributes they expose anonymously, and this
// is cheaper than guessing.
func (d *Driver) Connect(ctx context.Context, cfg directory.ConnConfig) (directory.Session, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.TLS == directory.TLSModePlaintext && !d.AllowPlaintext {
		return nil, errors.New("directory: refusing a plaintext LDAP connection; " +
			"use ldaps or StartTLS, or start alder with --i-know-this-is-insecure")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = directory.DefaultTimeout
	}

	conn, err := d.dial(ctx, cfg, timeout)
	if err != nil {
		return nil, err
	}

	if err := bind(conn, cfg); err != nil {
		_ = conn.Close()
		return nil, err
	}

	s := &session{
		data:       &link{conn: conn, name: "the directory connection", bindDN: cfg.BindDN, bindPW: cfg.BindPassword},
		cfg:        cfg,
		logger:     d.Logger,
		timeout:    timeout,
		dial:       d.dial,
		schemaOnce: new(sync.Once),
	}
	// Closed through the session from here on, not through `conn`. Reading
	// the RootDSE goes through searchLocked, which may replace a connection
	// that fails underneath it -- so by this line the session may be holding
	// a different one, and closing the local variable would leak it.
	caps, err := s.readRootDSE(ctx)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	s.caps = caps

	// The second identity, when there is one. It is dialled separately rather
	// than re-binding the first: re-binding would change who the session is for
	// every later operation, which is the opposite of what routing by DN is
	// for.
	if cfg.ConfigBindDN != "" {
		configConn, cfgErr := d.dial(ctx, cfg, timeout)
		if cfgErr != nil {
			_ = s.Close()
			return nil, fmt.Errorf("directory: connecting for the configuration tree: %w", cfgErr)
		}
		if bindErr := configConn.Bind(cfg.ConfigBindDN, cfg.ConfigBindPassword); bindErr != nil {
			_ = configConn.Close()
			_ = s.Close()
			// Named, because the alternative is a bind failure the person reads
			// as their main credentials being wrong.
			return nil, fmt.Errorf("directory: binding as the configuration identity %q: %w",
				cfg.ConfigBindDN, cleanLDAPError(bindErr))
		}
		s.config = &link{conn: configConn, name: "the configuration connection",
			bindDN: cfg.ConfigBindDN, bindPW: cfg.ConfigBindPassword}
	}

	// Resolved after the second bind, because the second identity is often the
	// only one that can see the tree at all. The announced value is left alone:
	// what is browsable and what the schema architecture is are two different
	// questions, and only the announcement answers the second.
	s.caps.Config = s.configAccess(ctx, s.resolveConfigContext(ctx, caps.ConfigContext))
	// The schema targets are found last: on a server that keeps its schema in
	// its configuration, whether there is anywhere to write depends on
	// everything above.
	s.caps.SchemaWrite = s.findSchemaTargets(ctx, s.caps)
	s.caps.Monitor = s.resolveMonitor(ctx)

	d.Logger.Info("connected to directory",
		"address", cfg.Address(),
		"tls", string(cfg.TLS),
		"verified", !cfg.InsecureSkipVerify,
		"bind_dn", cfg.BindDN,
		"vendor", caps.VendorName,
		"naming_contexts", caps.NamingContexts,
		"subschema", caps.SubschemaSubentry,
		"paging", caps.Paging,
		"config_context", s.caps.Config.DN,
		"config_readable", s.caps.Config.Readable,
		"schema_writable", s.caps.SchemaWrite.Editable())
	return s, nil
}

func (d *Driver) dial(ctx context.Context, cfg directory.ConnConfig, timeout time.Duration) (*ldap.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tlsCfg := &tls.Config{
		// #nosec G402 -- InsecureSkipVerify is opt-in per connection, never a
		// default, and the UI marks a session that uses it as unverified. A
		// directory tool that cannot reach a self-signed internal server is a
		// directory tool nobody uses.
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		RootCAs:            cfg.CACertificates,
		ServerName:         cfg.ServerName,
		MinVersion:         tls.VersionTLS12,
	}
	if tlsCfg.ServerName == "" {
		tlsCfg.ServerName = cfg.Host
	}

	scheme := "ldap"
	var opts []ldap.DialOpt
	switch cfg.TLS {
	case directory.TLSModeLDAPS:
		scheme = "ldaps"
		opts = append(opts, ldap.DialWithTLSConfig(tlsCfg))
	case directory.TLSModeStartTLS, directory.TLSModePlaintext:
	}
	opts = append(opts, ldap.DialWithDialer(&net.Dialer{Timeout: timeout}))

	url := fmt.Sprintf("%s://%s", scheme, cfg.Address())
	conn, err := ldap.DialURL(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("directory: connecting to %s: %w", url, err)
	}
	conn.SetTimeout(timeout)

	if cfg.TLS == directory.TLSModeStartTLS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("directory: StartTLS on %s: %w", cfg.Address(), err)
		}
	}
	// The context governs the dial; ldap.Conn has no context-aware dial of its
	// own, so an already-cancelled context is checked explicitly rather than
	// leaving a connection open behind a cancelled request.
	if err := dialCtx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func bind(conn *ldap.Conn, cfg directory.ConnConfig) error {
	if cfg.BindDN == "" {
		if err := conn.UnauthenticatedBind(""); err != nil {
			return fmt.Errorf("directory: anonymous bind: %w", err)
		}
		return nil
	}
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		// The error is wrapped without the password, obviously, and without the
		// server's diagnostic message, which on some servers distinguishes "no
		// such user" from "wrong password".
		return fmt.Errorf("directory: bind as %s failed: %w", cfg.BindDN, cleanLDAPError(err))
	}
	return nil
}

// session is one bound connection.
//
// An *ldap.Conn is not safe for concurrent use: two goroutines writing requests
// interleave their messages on the wire. HTTP handlers are concurrent, so every
// operation takes mu. This serialises a user's requests against their own
// session, which is the correct trade: a directory session belongs to one
// person clicking around a UI, and correctness beats throughput here.
type session struct {
	mu      sync.Mutex
	data    *link
	cfg     directory.ConnConfig
	caps    directory.Capabilities
	logger  *slog.Logger
	timeout time.Duration
	closed  bool

	// dial opens a new connection the same way the first one was opened. It
	// is what lets a session rebuild a link that failed without holding a
	// reference to the whole Driver.
	dial func(ctx context.Context, cfg directory.ConnConfig, timeout time.Duration) (*ldap.Conn, error)

	// config is the connection bound as the configuration identity, when one
	// was supplied, and nil otherwise. It is guarded by the same mutex as
	// data: the two are never used concurrently, and one lock keeps that
	// obviously true. They fail and recover independently -- see
	// reconnect.go.
	config *link
	// configTreeDN is the configuration tree this session can actually reach,
	// which is what operations are routed by. Distinct from
	// Capabilities.ConfigContext, which is only ever what the server announced.
	configTreeDN string

	// The parsed schema is cached because it is a few hundred kilobytes and
	// normally changes about once a year. Editing it is the exception, so the
	// cache is a resettable pointer rather than a sync.Once: a change applied
	// through this session has to be visible to the next read through it.
	schemaMu   sync.Mutex
	schemaOnce *sync.Once
	schema     *schema.Schema
	schemaErr  error
}

// invalidateSchema drops the cached schema so the next read fetches it again.
func (s *session) invalidateSchema() {
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	s.schemaOnce = new(sync.Once)
	s.schema, s.schemaErr = nil, nil
}

// RefreshSchema drops the cached schema and reads it again.
func (s *session) RefreshSchema(ctx context.Context) (*schema.Schema, error) {
	s.invalidateSchema()
	return s.Schema(ctx)
}

func (s *session) Capabilities() directory.Capabilities { return s.caps }

func (s *session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.config != nil && s.config.conn != nil {
		_ = s.config.conn.Close()
	}
	if s.data.conn == nil {
		return nil
	}
	return s.data.conn.Close()
}

// readRootDSE reads the empty-DN base entry and derives the capabilities.
func (s *session) readRootDSE(ctx context.Context) (directory.Capabilities, error) {
	req := ldap.NewSearchRequest(
		"", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, int(s.timeout.Seconds()), false,
		"(objectClass=*)",
		[]string{
			"namingContexts", "subschemaSubentry", "supportedControl",
			"supportedExtension", "supportedSASLMechanisms", "supportedLDAPVersion",
			"vendorName", "vendorVersion", "configContext",
			// 389 DS reports its version here and not in vendorVersion.
			"dataversion",
		},
		nil,
	)
	res, err := s.searchLocked(ctx, req)
	if err != nil {
		return directory.Capabilities{}, fmt.Errorf("directory: reading the RootDSE: %w", err)
	}
	if len(res.Entries) == 0 {
		return directory.Capabilities{}, errors.New("directory: the server returned no RootDSE")
	}
	e := res.Entries[0]

	caps := directory.Capabilities{
		NamingContexts:       e.GetAttributeValues("namingContexts"),
		SubschemaSubentry:    e.GetAttributeValue("subschemaSubentry"),
		SupportedControls:    e.GetAttributeValues("supportedControl"),
		SupportedExtensions:  e.GetAttributeValues("supportedExtension"),
		SupportedSASLMechs:   e.GetAttributeValues("supportedSASLMechanisms"),
		SupportedLDAPVersion: e.GetAttributeValues("supportedLDAPVersion"),
		VendorName:           e.GetAttributeValue("vendorName"),
		VendorVersion:        e.GetAttributeValue("vendorVersion"),
		ConfigContext:        e.GetAttributeValue("configContext"),
	}
	caps.Derive()

	if caps.SubschemaSubentry == "" {
		// Every server Alder targets publishes this. A server that does not is
		// one whose schema cannot be located without guessing, and guessing
		// "cn=subschema" then "cn=schema" is exactly the vendor branching the
		// charter forbids. Say so instead.
		s.logger.Warn("the server published no subschemaSubentry; the schema browser will be empty",
			"address", s.cfg.Address())
	}
	return caps, nil
}

// searchLocked runs a search, reopening the connection if it has failed. The
// caller must not hold mu; this takes it.
//
// A search changes nothing, so running it again after the connection is
// rebuilt gives the same answer -- with one exception, which is why this
// checks for a cookie. A paged search's cookie belongs to the connection that
// issued it. Presenting it to a new connection asks the server for a page it
// never handed out: the honest outcomes are an error, and the dangerous one
// is a different page, which would leave the caller assembling a result set
// with a gap or a repeat in the middle and no way to tell. So the first page
// of a search may be retried and a continuation may not.
func (s *session) searchLocked(ctx context.Context, req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("directory: the session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	l := s.connFor(req.BaseDN)
	conn, err := s.ready(ctx, l)
	if err != nil {
		return nil, err
	}

	res, searchErr := conn.Search(req)
	if searchErr == nil {
		return res, nil
	}
	cleaned := cleanLDAPError(searchErr)
	// Asked before anything is closed: a closed connection reports itself
	// closing whatever went wrong.
	unusable, retryable := classify(l, cleaned)
	if !unusable {
		// The directory answered, and what it answered was no.
		return nil, cleaned
	}
	s.markBroken(l, cleaned)
	if continuesAPage(req) {
		return nil, fmt.Errorf("directory: the connection was lost partway through a paged "+
			"search and the page cannot be resumed on a new one; run the search again: %w", cleaned)
	}
	if !retryable {
		return nil, cleaned
	}

	// Once. A second failure is the answer.
	conn, readyErr := s.ready(ctx, l)
	if readyErr != nil {
		return nil, readyErr
	}
	res, searchErr = conn.Search(req)
	if searchErr != nil {
		cleaned := cleanLDAPError(searchErr)
		if again, _ := classify(l, cleaned); again {
			s.markBroken(l, cleaned)
		}
		return nil, cleaned
	}
	s.logger.Info("a search completed after the connection was reopened",
		"connection", l.name, "base", req.BaseDN)
	return res, nil
}

// continuesAPage reports that this request carries a paging cookie, and so
// asks the server to continue a search that a previous connection began.
func continuesAPage(req *ldap.SearchRequest) bool {
	for _, c := range req.Controls {
		if p, ok := c.(*ldap.ControlPaging); ok && len(p.Cookie) > 0 {
			return true
		}
	}
	return false
}

// cleanLDAPError keeps the LDAP result code and drops the server's free-text
// diagnostic.
//
// Diagnostics are useful and occasionally dangerous: some servers name the
// entry that failed, some distinguish "no such object" from "insufficient
// access" in the text while returning the same code, and some echo part of the
// request. The code and its standard description are enough to act on.
func cleanLDAPError(err error) error {
	var le *ldap.Error
	if !errors.As(err, &le) {
		return err
	}
	return &Error{
		Code:    le.ResultCode,
		Message: ldap.LDAPResultCodeMap[le.ResultCode],
	}
}

// Error is an LDAP result code with its standard description.
type Error struct {
	Code    uint16
	Message string
}

func (e *Error) Error() string {
	// The number, only when it is one the directory sent.
	//
	// "(code 200)" was printed on a failure where nothing had answered at
	// all, and a UI audit read it as an HTTP 200 in a message about a
	// failure -- which is a fair reading, because 200 there means nothing an
	// operator has ever seen in a directory log either.
	if e.IsClientSide() {
		if e.Message == "" {
			return "the directory did not answer"
		}
		return e.Message
	}
	if e.Message == "" {
		return fmt.Sprintf("LDAP result code %d", e.Code)
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

// IsClientSide reports a failure that carries no LDAP result code, because no
// result ever arrived.
//
// go-ldap numbers its own conditions from 200 -- a dead socket is 200
// "Network Error", an unparseable reply is 205 -- in the same uint16 as the
// protocol's result codes, which stop at 123 (and 4096 for sync refresh
// required). They are not result codes: the directory did not send them, and
// 200 in particular is not "success" in any protocol Alder speaks.
//
// The library's own numbers exactly, rather than everything above 199. A
// wider range would quietly swallow any future result code allocated in
// between, and swallowing a code the directory really sent is the more
// expensive mistake: the hint and the remedy are built from it.
func (e *Error) IsClientSide() bool {
	return e.Code >= ldap.ErrorNetwork && e.Code <= ldap.ErrorEmptyPassword
}

// IsNoSuchObject reports the result code for an entry that does not exist, so
// the API can answer 404 rather than 500.
func (e *Error) IsNoSuchObject() bool { return e.Code == ldap.LDAPResultNoSuchObject }

// IsInsufficientAccess reports the result code for a denied operation, so the
// API can answer 403 and the UI can say "you are not allowed to do that"
// rather than "something went wrong".
func (e *Error) IsInsufficientAccess() bool {
	return e.Code == ldap.LDAPResultInsufficientAccessRights
}

// IsAuth reports the codes that mean the bind is no longer good.
func (e *Error) IsAuth() bool {
	switch e.Code {
	case ldap.LDAPResultInvalidCredentials,
		ldap.LDAPResultInappropriateAuthentication,
		ldap.LDAPResultStrongAuthRequired:
		return true
	}
	return false
}

// IsConstraintViolation reports a value the server rejected on schema or policy
// grounds, which is a 422 rather than a 500.
func (e *Error) IsConstraintViolation() bool {
	switch e.Code {
	case ldap.LDAPResultConstraintViolation,
		ldap.LDAPResultInvalidAttributeSyntax,
		ldap.LDAPResultObjectClassViolation,
		ldap.LDAPResultNotAllowedOnRDN,
		ldap.LDAPResultObjectClassModsProhibited,
		ldap.LDAPResultUndefinedAttributeType,
		ldap.LDAPResultNamingViolation,
		ldap.LDAPResultEntryAlreadyExists,
		ldap.LDAPResultAttributeOrValueExists,
		ldap.LDAPResultNoSuchAttribute:
		return true
	}
	return false
}

// parseDN converts a DN string the server returned. Servers return DNs they
// consider well-formed, so a failure here is worth reporting rather than
// papering over: it means the DN parser disagrees with a real directory.
func parseDN(s string) (dn.DN, error) {
	d, err := dn.ParseAllowEmpty(s)
	if err != nil {
		return nil, fmt.Errorf("directory: the server returned a DN this parser rejects: %q: %w", s, err)
	}
	return d, nil
}

// convertEntry maps a go-ldap entry onto a directory.Entry, keeping byte values
// rather than the string ones, so binary attributes survive.
func convertEntry(e *ldap.Entry) (*directory.Entry, error) {
	d, err := parseDN(e.DN)
	if err != nil {
		return nil, err
	}
	out := directory.NewEntry(d)
	for _, a := range e.Attributes {
		out.Set(a.Name, a.ByteValues)
	}
	return out, nil
}
