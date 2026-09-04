// Package directory defines the interface Alder talks to a directory through,
// and the ChangeRecord every modification is expressed as.
//
// There is exactly one driver behind this interface in v1, the LDAP one. The
// interface exists so that a future FreeIPA driver (LDAP to read, the FreeIPA
// API to write) or an Entra ID driver (Microsoft Graph, not LDAP at all) can be
// added without restructuring the application. It is not an invitation to start
// either of them.
package directory

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

// Driver opens sessions against a directory.
type Driver interface {
	Connect(ctx context.Context, cfg ConnConfig) (Session, error)
}

// Session is an authenticated connection to a directory.
//
// A Session is used concurrently by HTTP handlers, so implementations must be
// safe for concurrent use. An LDAP connection is not, which is why the driver
// serialises operations rather than leaving each handler to remember.
type Session interface {
	// Capabilities reports what the server announced at connect time.
	Capabilities() Capabilities
	// Schema returns the parsed subschema, reading it once and caching it. A
	// schema is a few hundred kilobytes and changes about once a year.
	Schema(ctx context.Context) (*schema.Schema, error)
	// Search runs a paged search. There is no unbounded search.
	Search(ctx context.Context, req SearchRequest) (*SearchResult, error)
	// Read returns a single entry by DN.
	Read(ctx context.Context, target dn.DN, attrs []string) (*Entry, error)
	// SchemaDefinitions returns the definitions one schema entry holds, exactly
	// as the server stores them -- ordering prefixes, server-added extensions
	// and all. A change that removes or replaces a definition has to send back
	// the value the server matched on, and this is where that value comes from.
	SchemaDefinitions(ctx context.Context, targetDN string, kind SchemaDefKind) ([]string, error)
	// Apply performs a change. It is the only method that writes.
	Apply(ctx context.Context, ch ChangeRecord) error
	// Close releases the connection.
	Close() error
}

// TLSMode selects how the connection is secured.
type TLSMode string

// The TLS modes. Plaintext is refused unless the operator has explicitly opted
// out of transport security, per rule 7 of the project charter.
const (
	TLSModeLDAPS     TLSMode = "ldaps"
	TLSModeStartTLS  TLSMode = "starttls"
	TLSModePlaintext TLSMode = "plaintext"
)

// ConnConfig is everything needed to open a session.
//
// BindPassword is the one field that must never be logged, serialised to disk,
// or returned by the API. It lives in an in-memory session and nowhere else.
type ConnConfig struct {
	Host string
	Port int

	TLS TLSMode
	// InsecureSkipVerify disables certificate verification. It exists because
	// self-signed certificates on internal directories are the norm, and a tool
	// that cannot connect to them will simply be replaced with one that can.
	// It is per-connection, never a default, and the UI marks a session using
	// it as unverified.
	InsecureSkipVerify bool
	// CACertificates, when non-nil, replaces the system roots for this
	// connection. This is the supported way to reach a directory behind a
	// private CA without turning verification off.
	CACertificates *x509.CertPool
	// ServerName overrides the name checked against the certificate, for the
	// case where the directory is reached by IP but certified by name.
	ServerName string

	BindDN       string
	BindPassword string

	// ConfigBindDN and ConfigBindPassword are an optional second identity, used
	// only for the server's own configuration tree.
	//
	// A directory keeps its configuration beside its data, and the account that
	// administers a suffix normally has no rights there — they are separate
	// administrative domains on purpose. Without this, reaching the
	// configuration means connecting as the configuration administrator and
	// losing access to the data, which on a server that stores its schema in
	// its configuration makes schema editing and entry browsing mutually
	// exclusive. Operations are routed by DN, so one session does both.
	//
	// These live in memory for the life of the session, exactly like the bind
	// password, and are never written down or logged.
	ConfigBindDN       string
	ConfigBindPassword string

	// Timeout bounds a single operation. Zero means DefaultTimeout.
	Timeout time.Duration
}

// DefaultTimeout bounds any single directory operation.
const DefaultTimeout = 30 * time.Second

// Secure reports whether the connection encrypts its traffic.
func (c ConnConfig) Secure() bool { return c.TLS != TLSModePlaintext }

// Address renders "host:port" for dialling.
func (c ConnConfig) Address() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }

// Validate checks a configuration before anything is dialled.
func (c ConnConfig) Validate() error {
	if c.Host == "" {
		return errors.New("directory: host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("directory: port %d is out of range", c.Port)
	}
	switch c.TLS {
	case TLSModeLDAPS, TLSModeStartTLS, TLSModePlaintext:
	default:
		return fmt.Errorf("directory: unknown TLS mode %q", c.TLS)
	}
	// An anonymous bind is legitimate: many directories allow a read-only
	// anonymous browse, and refusing it would make Alder useless against them.
	// A password without a DN is not, since it would be silently discarded.
	if c.BindDN == "" && c.BindPassword != "" {
		return errors.New("directory: a bind password was given without a bind DN")
	}
	if c.BindDN != "" {
		if _, err := dn.Parse(c.BindDN); err != nil {
			return fmt.Errorf("directory: bind DN: %w", err)
		}
	}
	return nil
}

// Capabilities is what the RootDSE said about the server.
//
// Alder branches on these, never on the vendor. The vendor fields exist for
// display: telling the user which server they are looking at is useful, and
// deciding behaviour from it is how a tool ends up working against one vendor's
// quirks rather than the protocol.
type Capabilities struct {
	// NamingContexts are the suffixes the server holds. The tree browser roots
	// itself at these.
	NamingContexts []string `json:"namingContexts"`
	// SubschemaSubentry is the DN of the schema entry, as the server reported
	// it. This is why nothing in Alder hardcodes "cn=subschema" or "cn=schema".
	SubschemaSubentry string `json:"subschemaSubentry"`
	// ConfigContext is the DN the server *announces* for its own configuration
	// tree, and only that.
	//
	// The announcement is the load-bearing part. A server that publishes a
	// configContext is declaring that its configuration is part of its protocol
	// surface, which is also what makes its subschema subentry a generated,
	// read-only view of configuration entries rather than the schema itself.
	// That is why schema editing branches on this and not on Config.DN below:
	// a tree merely being reachable at the conventional DN says nothing about
	// where the schema is kept.
	ConfigContext string `json:"configContext,omitempty"`

	SupportedControls    []string `json:"supportedControls"`
	SupportedExtensions  []string `json:"supportedExtensions"`
	SupportedSASLMechs   []string `json:"supportedSASLMechanisms"`
	SupportedLDAPVersion []string `json:"supportedLDAPVersion"`

	// VendorName and VendorVersion are for display only.
	VendorName    string `json:"vendorName,omitempty"`
	VendorVersion string `json:"vendorVersion,omitempty"`

	// Derived flags, computed once from the lists above so callers do not each
	// re-scan them.
	Paging       bool `json:"paging"`
	ServerSort   bool `json:"serverSort"`
	StartTLS     bool `json:"startTLS"`
	AllOperional bool `json:"allOperationalAttributes"`
	WhoAmI       bool `json:"whoAmI"`
	// PasswordModify reports RFC 3062. Where a server offers it, Alder sets
	// passwords with it rather than writing a hash into userPassword, so the
	// server applies its own policy and chooses its own scheme.
	PasswordModify bool `json:"passwordModify"`

	// SchemaWrite says where a schema definition would have to be written, and
	// whether this session can write it.
	SchemaWrite SchemaWrite `json:"schemaWrite"`

	// Config says whether this session can reach the server's own
	// configuration tree, and how.
	Config ConfigAccess `json:"config"`
}

// ConfigAccess describes this session's reach into the configuration tree.
type ConfigAccess struct {
	// DN is the configuration tree's root as far as browsing is concerned:
	// what the server announced, or the conventional location if the server
	// answered there. Empty when neither found anything.
	DN string `json:"dn,omitempty"`
	// Readable reports whether this session can actually read it. A server may
	// announce a tree that the bound identity has no rights in.
	Readable bool `json:"readable"`
	// SeparateBind reports that a second identity is being used for it.
	SeparateBind bool `json:"separateBind"`
	// BoundAs is the identity the configuration tree is read as.
	BoundAs string `json:"boundAs,omitempty"`
	// Reason explains, in a sentence meant to be read, why it is unreachable.
	Reason string `json:"reason,omitempty"`
}

// SchemaStyle is how a server stores the schema it can be told to change.
//
// There are two, and which one a server uses is announced rather than assumed:
// a server that publishes a configContext generates its subschema subentry from
// configuration entries, and a server that does not lets the subschema subentry
// be modified directly.
type SchemaStyle string

const (
	// SchemaStyleNone means no writable location was found.
	SchemaStyleNone SchemaStyle = "none"
	// SchemaStyleSubschema means definitions are added to and removed from the
	// subschema subentry itself, with an ordinary modify.
	SchemaStyleSubschema SchemaStyle = "subschema"
	// SchemaStyleConfig means definitions live in configuration entries under
	// the config context, each holding part of the schema, and the subschema
	// subentry is a read-only view generated from them.
	SchemaStyleConfig SchemaStyle = "config"
)

// SchemaWrite describes the writable schema location for a session.
type SchemaWrite struct {
	Style SchemaStyle `json:"style"`
	// Targets are the entries a definition may be written to. There is exactly
	// one under SchemaStyleSubschema. Under SchemaStyleConfig a server holds
	// several, each a separate collection of definitions, and which one to add
	// to is a choice only the person making the change can make.
	Targets []SchemaTarget `json:"targets,omitempty"`
	// ObjectClassAttr and AttributeTypeAttr name the attributes that carry
	// definitions at these targets.
	ObjectClassAttr   string `json:"objectClassAttr,omitempty"`
	AttributeTypeAttr string `json:"attributeTypeAttr,omitempty"`
	// Unavailable explains, in a sentence meant for the person reading it, why
	// there is nothing to write to. Empty when Style is not None.
	Unavailable string `json:"unavailable,omitempty"`
}

// SchemaTarget is one entry that holds schema definitions.
type SchemaTarget struct {
	DN   string `json:"dn"`
	Name string `json:"name"`
	// ObjectClasses and AttributeTypes count what this target already holds, so
	// the UI can say which collection a definition would join.
	ObjectClasses  int `json:"objectClasses"`
	AttributeTypes int `json:"attributeTypes"`
}

// Editable reports whether a definition can be written at all.
func (w SchemaWrite) Editable() bool { return w.Style != SchemaStyleNone && len(w.Targets) > 0 }

// Target returns the target with the given DN, matched case-insensitively as
// DNs compare, and reports whether it was found. A schema write names its
// target explicitly; accepting an unlisted one would let a caller aim an
// ordinary modify at any entry through the schema endpoint.
func (w SchemaWrite) Target(dn string) (SchemaTarget, bool) {
	for _, t := range w.Targets {
		if strings.EqualFold(t.DN, dn) {
			return t, true
		}
	}
	return SchemaTarget{}, false
}

// OIDs of the controls and extensions Alder looks for. Named here so the
// capability detection reads as prose rather than as a wall of digits.
const (
	OIDPagedResults    = "1.2.840.113556.1.4.319"
	OIDServerSort      = "1.2.840.113556.1.4.473"
	OIDMatchedValues   = "1.2.826.0.1.3344810.2.3"
	OIDStartTLS        = "1.3.6.1.4.1.1466.20037"
	OIDWhoAmI          = "1.3.6.1.4.1.4203.1.11.3"
	OIDPasswordModify  = "1.3.6.1.4.1.4203.1.11.1"
	OIDAllOpAttributes = "1.3.6.1.4.1.4203.1.5.1"
)

// Has reports whether a string appears in a capability list, case-insensitively
// on the OID, which is always ASCII.
func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// Derive fills the computed flags from the announced lists. It is called once,
// by the driver, after reading the RootDSE.
func (c *Capabilities) Derive() {
	c.Paging = has(c.SupportedControls, OIDPagedResults)
	c.ServerSort = has(c.SupportedControls, OIDServerSort)
	c.StartTLS = has(c.SupportedExtensions, OIDStartTLS)
	c.WhoAmI = has(c.SupportedExtensions, OIDWhoAmI)
	c.AllOperional = has(c.SupportedExtensions, OIDAllOpAttributes)
	c.PasswordModify = has(c.SupportedExtensions, OIDPasswordModify)
}

// Scope is the search scope of RFC 4511 section 4.5.1.2.
type Scope int

// The search scopes.
const (
	ScopeBase Scope = iota
	ScopeOneLevel
	ScopeSubtree
)

func (s Scope) String() string {
	switch s {
	case ScopeBase:
		return "base"
	case ScopeOneLevel:
		return "one"
	default:
		return "sub"
	}
}

// ParseScope parses the scope names the API uses.
func ParseScope(s string) (Scope, error) {
	switch s {
	case "base":
		return ScopeBase, nil
	case "one", "onelevel":
		return ScopeOneLevel, nil
	case "sub", "subtree":
		return ScopeSubtree, nil
	default:
		return 0, fmt.Errorf("directory: unknown scope %q", s)
	}
}

// Search limits. These are hard caps, not defaults to be raised by a config
// key: an unbounded search against a directory with a million entries is a way
// to take down the directory, not a feature.
const (
	// DefaultPageSize is the page requested from the server when the caller
	// does not choose.
	DefaultPageSize = 100
	// MaxPageSize bounds one page.
	MaxPageSize = 1000
	// MaxResults bounds the total returned to a caller by one search.
	MaxResults = 10000
)

// SearchRequest describes a paged search.
//
// Filter is a filter.Filter, not a string, so there is no path by which
// user-supplied text reaches the server without going through RFC 4515
// escaping. A raw filter typed by the user is parsed by filter.Parse first,
// which either produces a tree or an error.
type SearchRequest struct {
	BaseDN dn.DN
	Scope  Scope
	Filter filter.Filter

	// Attributes to return. Empty means the server's default, which is all
	// user attributes. Include "+" for operational attributes.
	Attributes []string

	// PageSize is the page requested from the server; it is clamped to
	// MaxPageSize. Limit bounds the total returned and is clamped to
	// MaxResults.
	PageSize int
	Limit    int

	// Cookie continues a previous paged search. It is opaque to the caller.
	Cookie []byte

	// TypesOnly asks the server for attribute names without values, which the
	// tree browser uses to test whether a node has children cheaply.
	TypesOnly bool
}

// Normalise clamps the request to the hard limits and fills in defaults.
func (r *SearchRequest) Normalise() {
	if r.PageSize <= 0 {
		r.PageSize = DefaultPageSize
	}
	if r.PageSize > MaxPageSize {
		r.PageSize = MaxPageSize
	}
	if r.Limit <= 0 || r.Limit > MaxResults {
		r.Limit = MaxResults
	}
	if r.PageSize > r.Limit {
		r.PageSize = r.Limit
	}
}

// SearchResult is one page of results.
type SearchResult struct {
	Entries []*Entry
	// Cookie is non-empty when the server has more results to give. Passing it
	// back in the next SearchRequest continues from here.
	Cookie []byte
	// Truncated reports that Alder stopped short of the server's full result
	// set because Limit was reached. It is surfaced in the UI: a silently
	// truncated search is worse than no search.
	Truncated bool
	// Referrals the server returned instead of entries. Alder does not chase
	// them; it reports them, because following a referral means opening a new
	// connection to a host the user did not choose.
	Referrals []string
}

// Entry is a directory entry.
//
// Values are [][]byte for the same reason as in the LDIF package: an attribute
// value is an octet string, and a jpegPhoto is not text.
type Entry struct {
	DN         dn.DN
	Attributes map[string][][]byte
	// Order preserves the attribute order the server returned, so a re-export
	// produces a stable diff.
	Order []string
}

// NewEntry returns an empty entry for the given DN.
func NewEntry(d dn.DN) *Entry {
	return &Entry{DN: d, Attributes: map[string][][]byte{}}
}

// Set replaces the values of an attribute, recording its position on first use.
func (e *Entry) Set(name string, values [][]byte) {
	if _, seen := e.Attributes[name]; !seen {
		e.Order = append(e.Order, name)
	}
	e.Attributes[name] = values
}

// Get returns the values of an attribute, matching case-insensitively.
func (e *Entry) Get(name string) [][]byte {
	if v, ok := e.Attributes[name]; ok {
		return v
	}
	for k, v := range e.Attributes {
		if equalFoldASCII(k, name) {
			return v
		}
	}
	return nil
}

// GetStrings is Get with the values as strings, for attributes known to be
// text. It must not be used for binary attributes.
func (e *Entry) GetStrings(name string) []string {
	vals := e.Get(name)
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// GetOne returns the first value of an attribute as a string, or "".
func (e *Entry) GetOne(name string) string {
	vals := e.Get(name)
	if len(vals) == 0 {
		return ""
	}
	return string(vals[0])
}

// ObjectClasses returns the entry's objectClass values.
func (e *Entry) ObjectClasses() []string { return e.GetStrings("objectClass") }

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
