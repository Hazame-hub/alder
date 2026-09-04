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
		conn:       conn,
		cfg:        cfg,
		logger:     d.Logger,
		timeout:    timeout,
		schemaOnce: new(sync.Once),
	}
	caps, err := s.readRootDSE(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	s.caps = caps

	d.Logger.Info("connected to directory",
		"address", cfg.Address(),
		"tls", string(cfg.TLS),
		"verified", !cfg.InsecureSkipVerify,
		"bind_dn", cfg.BindDN,
		"vendor", caps.VendorName,
		"naming_contexts", caps.NamingContexts,
		"subschema", caps.SubschemaSubentry,
		"paging", caps.Paging)
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
	conn    *ldap.Conn
	cfg     directory.ConnConfig
	caps    directory.Capabilities
	logger  *slog.Logger
	timeout time.Duration
	closed  bool

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

func (s *session) Capabilities() directory.Capabilities { return s.caps }

func (s *session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.conn.Close()
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
	caps.SchemaWrite = s.findSchemaTargets(ctx, caps)

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

// searchLocked runs a search. The caller must not hold mu; this takes it.
func (s *session) searchLocked(ctx context.Context, req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("directory: the session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res, err := s.conn.Search(req)
	if err != nil {
		return nil, cleanLDAPError(err)
	}
	return res, nil
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
	if e.Message == "" {
		return fmt.Sprintf("LDAP result code %d", e.Code)
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
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
