//go:build conformance

// Package conformance runs one suite against both target directory servers and
// asserts they behave identically through the directory.Driver interface.
//
// It is guarded by a build tag because it needs the docker-compose harness
// running: "task test:conformance" brings the servers up and runs it, and a
// plain "go test ./..." skips it entirely.
//
// The rule this suite exists to enforce: every test runs against every server,
// from one table. There is no OpenLDAP test file and no 389 DS test file. If a
// behaviour genuinely differs, the difference belongs in a Capabilities field
// that the test reads, not in a branch on the server's name.
package conformance

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/ansible"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

// server describes one harness server. Only the connection differs between
// them; everything below this table is shared.
type server struct {
	name   string
	host   string
	port   int
	bindDN string
	bindPW string

	// schemaBindDN and schemaBindPW are the identity that can write schema on
	// this server, when it is not the one above.
	//
	// This is not a per-vendor exception dressed up as configuration. Where a
	// server keeps its schema in its configuration tree, the schema is
	// configuration, and the account that administers a suffix has no business
	// in it -- so editing it means binding as someone else. The suite says so
	// once, here, and every schema case below runs the same table against both
	// servers regardless.
	schemaBindDN string
	schemaBindPW string

	// restrictedDN and restrictedPW are a delegated account: it administers
	// people and groups and cannot see ou=services at all.
	//
	// Every other case in this file binds as the directory's own administrator,
	// which on OpenLDAP is the rootdn and bypasses access control entirely.
	// That makes the whole suite a test of what Alder does when it is allowed
	// to do everything -- which is not how anybody runs it. Where the rules
	// live differs by vendor (slapd.conf against an aci attribute); that the
	// account sees the same directory does not.
	restrictedDN string
	restrictedPW string

	// hiddenDN is a subtree the restricted account cannot read. It exists, and
	// asking about it is not an error -- the server simply behaves as though it
	// were not there, which is the case a tool has to get right.
	hiddenDN string

	// lockAttr and lockValue name an operational attribute this server keeps
	// and still lets an administrator set, and a value it accepts.
	//
	// Both servers have one and they are not the same one: 389 DS has
	// nsAccountLock natively, OpenLDAP gets pwdAccountLockedTime from the
	// ppolicy overlay the harness loads. Naming them per server is what lets
	// the case below be identical -- it asserts that the schema's own
	// declaration predicts what the server will accept, without knowing which
	// server it is talking to.
	lockAttr  string
	lockValue string

	// configWriteDN, configWriteAttr and configWriteValue name a harmless,
	// restorable setting in this server's configuration.
	//
	// Both are an idle timeout, which is the same idea on both servers and has
	// no effect on anything the suite does. Naming them per server is what lets
	// the case itself be identical: the assertions below never learn which
	// server they are talking to.
	configWriteDN    string
	configWriteAttr  string
	configWriteValue string
}

var servers = []server{
	{
		name:         "openldap",
		host:         "localhost",
		port:         10636,
		bindDN:       "cn=admin,dc=alder,dc=test",
		bindPW:       "alder-admin",
		schemaBindDN: "cn=admin,cn=config",
		schemaBindPW: "alder-config",

		restrictedDN: "cn=svc-alder,ou=services,dc=alder,dc=test",
		restrictedPW: "alder-service",
		hiddenDN:     "ou=services,dc=alder,dc=test",

		lockAttr:  "pwdAccountLockedTime",
		lockValue: "20260101000000Z",

		configWriteDN:    "cn=config",
		configWriteAttr:  "olcIdleTimeout",
		configWriteValue: "1800",
	},
	{
		name:   "389ds",
		host:   "localhost",
		port:   11636,
		bindDN: "cn=Directory Manager",
		bindPW: "alder-directory-manager",

		restrictedDN: "cn=svc-alder,ou=services,dc=alder,dc=test",
		restrictedPW: "alder-service",
		hiddenDN:     "ou=services,dc=alder,dc=test",

		lockAttr:  "nsAccountLock",
		lockValue: "true",

		configWriteDN:    "cn=config",
		configWriteAttr:  "nsslapd-idletimeout",
		configWriteValue: "1800",
	},
}

const suffix = "dc=alder,dc=test"

// caPool loads the harness CA. No client in this repository is allowed to skip
// certificate verification, so a missing CA is a hard failure rather than a
// reason to fall back.
func caPool(t *testing.T) *x509.CertPool {
	t.Helper()
	path := filepath.Join("..", "compose", "certs", "ca.crt")
	pem, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the harness CA at %s: %v\nrun \"task compose:up\" first", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatalf("%s is not a PEM certificate", path)
	}
	return pool
}

func connect(t *testing.T, s server, withConfig bool) directory.Session {
	t.Helper()
	// The driver logs at info; the suite discards it unless a test fails, and
	// a failing test's useful output is the assertion, not the connection log.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	drv := ldapdriver.New(logger, false)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := directory.ConnConfig{
		Host:           s.host,
		Port:           s.port,
		TLS:            directory.TLSModeLDAPS,
		CACertificates: caPool(t),
		ServerName:     "localhost",
		BindDN:         s.bindDN,
		BindPassword:   s.bindPW,
	}
	if withConfig {
		cfg.ConfigBindDN, cfg.ConfigBindPassword = s.schemaBindDN, s.schemaBindPW
	}
	sess, err := drv.Connect(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting to %s at %s:%d: %v\nis the harness up? run \"task compose:up\"",
			s.name, s.host, s.port, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// connectForSchema connects with the configuration identity as well, where the
// server needs one to reach its schema.
//
// It supplies it as a *second* identity rather than binding as it instead. That
// is the whole point of the feature: the session still browses data as the
// directory administrator, and only operations addressed into the configuration
// tree use the other account. Swapping the bind would test something nobody
// would want to do.
func connectForSchema(t *testing.T, s server) directory.Session {
	t.Helper()
	return connect(t, s, true)
}

// eachServerForSchema runs fn against every server, bound so that schema can be
// written. The assertions inside are the same for both, which is the point.
func eachServerForSchema(t *testing.T, fn func(t *testing.T, s server, sess directory.Session)) {
	t.Helper()
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			fn(t, s, connectForSchema(t, s))
		})
	}
}

// connectRestricted connects as the delegated account rather than the
// directory's administrator.
func connectRestricted(t *testing.T, s server) directory.Session {
	t.Helper()
	restricted := s
	restricted.bindDN, restricted.bindPW = s.restrictedDN, s.restrictedPW
	return connect(t, restricted, false)
}

// eachServerRestricted runs fn against every server, bound as the delegated
// account. The assertions inside are the same for both, as everywhere else.
func eachServerRestricted(t *testing.T, fn func(t *testing.T, s server, sess directory.Session)) {
	t.Helper()
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			fn(t, s, connectRestricted(t, s))
		})
	}
}

// eachServer runs fn against every server as a subtest.
func eachServer(t *testing.T, fn func(t *testing.T, s server, sess directory.Session)) {
	t.Helper()
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			fn(t, s, connect(t, s, false))
		})
	}
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

// --- capabilities -----------------------------------------------------------

func TestCapabilities(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		caps := sess.Capabilities()

		if !containsFold(caps.NamingContexts, suffix) {
			t.Errorf("namingContexts = %v, want it to include %q", caps.NamingContexts, suffix)
		}
		if caps.SubschemaSubentry == "" {
			t.Error("the server published no subschemaSubentry; nothing can locate its schema")
		}
		if !caps.Paging {
			t.Error("the server does not advertise the paged results control, which every search depends on")
		}
		if len(caps.SupportedControls) == 0 {
			t.Error("supportedControl is empty")
		}
		// Vendor identification is display-only, but if it is absent the
		// connection screen has nothing to show, so it is worth knowing.
		t.Logf("%s: vendor=%q version=%q subschema=%q",
			s.name, caps.VendorName, caps.VendorVersion, caps.SubschemaSubentry)
	})
}

// TestSubschemaDNDiffersButIsDiscovered is the point of capability detection,
// stated as a test: the two servers publish their schema at different DNs, and
// nothing in Alder knows which is which.
func TestSubschemaDNDiffersButIsDiscovered(t *testing.T) {
	seen := map[string]string{}
	for _, s := range servers {
		sess := connect(t, s, false)
		seen[s.name] = sess.Capabilities().SubschemaSubentry
	}
	if seen["openldap"] == seen["389ds"] {
		t.Logf("both servers happen to publish the schema at %q", seen["openldap"])
	} else {
		t.Logf("schema DNs differ as expected: openldap=%q 389ds=%q", seen["openldap"], seen["389ds"])
	}
	for name, subschema := range seen {
		if subschema == "" {
			t.Errorf("%s published no subschemaSubentry", name)
		}
	}
}

// --- schema -----------------------------------------------------------------

func TestSchemaLoads(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}
		counts := sch.Counts()
		t.Logf("%s: %+v", s.name, counts)

		if counts.ObjectClasses < 20 {
			t.Errorf("only %d object classes; a real directory publishes far more", counts.ObjectClasses)
		}
		if counts.AttributeTypes < 100 {
			t.Errorf("only %d attribute types; a real directory publishes far more", counts.AttributeTypes)
		}
		// A handful of failures is tolerable and expected; a schema that mostly
		// fails to parse means the parser is wrong.
		if counts.Errors > counts.ObjectClasses/10 {
			for _, e := range sch.Errors[:min(5, len(sch.Errors))] {
				t.Logf("parse error: %v", e)
			}
			t.Errorf("%d definitions failed to parse out of %d; the parser is not keeping up",
				counts.Errors, counts.ObjectClasses+counts.AttributeTypes)
		}
	})
}

// TestSchemaHasTheSameCoreDefinitions asserts both servers describe the
// standard schema the same way. Where they disagree, the entry editor would
// behave differently against each, which is exactly what this suite exists to
// prevent.
func TestSchemaHasTheSameCoreDefinitions(t *testing.T) {
	type view struct {
		mustCN      bool
		singleValue bool
		syntax      string
		kind        schema.Kind
	}
	views := map[string]view{}

	for _, s := range servers {
		sess := connect(t, s, false)
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("%s: Schema: %v", s.name, err)
		}
		person := sch.ObjectClass("person")
		if person == nil {
			t.Fatalf("%s: the schema has no \"person\" object class", s.name)
		}
		req := sch.Requirements([]string{"inetOrgPerson"})
		uid := sch.AttributeType("uid")
		if uid == nil {
			t.Fatalf("%s: the schema has no \"uid\" attribute type", s.name)
		}
		views[s.name] = view{
			mustCN:      containsFold(req.Must, "cn"),
			singleValue: sch.EffectiveSingleValue(sch.AttributeType("employeeNumber")),
			syntax:      sch.EffectiveSyntax(uid),
			kind:        person.Kind,
		}
	}

	a, b := views["openldap"], views["389ds"]
	if a != b {
		t.Errorf("the two servers describe the core schema differently:\n  openldap: %+v\n  389ds:    %+v", a, b)
	}
	if !a.mustCN {
		t.Error("inetOrgPerson does not require cn, which cannot be right")
	}
	if a.kind != schema.KindStructural {
		t.Errorf("person.Kind = %v, want structural", a.kind)
	}
}

// TestSchemaHasTheHarnessCustomClass proves a custom schema installed by the
// harness is visible through the same code path as the standard one.
func TestSchemaHasTheHarnessCustomClass(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}
		oc := sch.ObjectClass("alderEmployee")
		if oc == nil {
			t.Fatal("the custom class alderEmployee is missing from the schema")
		}
		if oc.Kind != schema.KindAuxiliary {
			t.Errorf("alderEmployee.Kind = %v, want auxiliary", oc.Kind)
		}
		at := sch.AttributeType("alderTeam")
		if at == nil {
			t.Fatal("the custom attribute alderTeam is missing from the schema")
		}
		if kind := sch.KindOf("alderOnCall").Kind; kind != schema.KindBoolean {
			t.Errorf("alderOnCall presents as %q, want boolean", kind)
		}
	})
}

// --- read -------------------------------------------------------------------

func TestReadEntry(t *testing.T) {
	target := dn.MustParse("cn=svc-alder,ou=services," + suffix)
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		e, err := sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if !e.DN.Equal(target) {
			t.Errorf("DN = %s, want %s", e.DN, target)
		}
		if got := e.GetOne("cn"); got != "svc-alder" {
			t.Errorf("cn = %q, want svc-alder", got)
		}
		if !containsFold(e.ObjectClasses(), "inetOrgPerson") {
			t.Errorf("objectClass = %v, want it to include inetOrgPerson", e.ObjectClasses())
		}
	})
}

// TestReadOperationalAttributes covers the divergence the charter calls out:
// the two servers name the entry's unique identifier differently, and a caller
// that asks for operational attributes gets whichever one this server has.
func TestReadOperationalAttributes(t *testing.T) {
	target := dn.MustParse("cn=svc-alder,ou=services," + suffix)
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		e, err := sess.Read(ctx(t), target, []string{"*", "+"})
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if e.GetOne("createTimestamp") == "" && e.GetOne("createtimestamp") == "" {
			t.Error("no createTimestamp; operational attributes were not returned")
		}
		uuid := e.GetOne("entryUUID")
		unique := e.GetOne("nsUniqueId")
		if uuid == "" && unique == "" {
			t.Error("the entry has neither entryUUID nor nsUniqueId")
		}
		t.Logf("%s: entryUUID=%q nsUniqueId=%q", s.name, uuid, unique)
	})
}

func TestReadNonASCIIDN(t *testing.T) {
	// From the harness seed data: an RDN that is not ASCII, which must survive
	// being parsed, re-rendered, and sent back to the server.
	target := dn.MustParse("ou=Zweigstelle München,ou=services," + suffix)
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		e, err := sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read(%s): %v", target, err)
		}
		if got := e.GetOne("ou"); got != "Zweigstelle München" {
			t.Errorf("ou = %q, want %q", got, "Zweigstelle München")
		}
		if !e.DN.Equal(target) {
			t.Errorf("the DN did not round-trip: got %s, want %s", e.DN, target)
		}
	})
}

func TestReadDNWithEscapedComma(t *testing.T) {
	target := dn.MustParse(`cn=Liddell\, Alice,ou=people,` + suffix)
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		e, err := sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read(%s): %v", target, err)
		}
		if got := e.GetOne("cn"); got != "Liddell, Alice" {
			t.Errorf("cn = %q, want %q", got, "Liddell, Alice")
		}
	})
}

func TestReadPreservesBinaryValues(t *testing.T) {
	target := dn.MustParse("cn=awkward-values,ou=people," + suffix)
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		e, err := sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		notes := e.Get("alderNote")
		if len(notes) == 0 {
			t.Fatal("alderNote has no values")
		}
		var sawLeadingSpace, sawNewline, sawNonASCII bool
		for _, v := range notes {
			switch {
			case len(v) > 0 && v[0] == ' ':
				sawLeadingSpace = true
			case containsByte(v, '\n'):
				sawNewline = true
			}
			for _, c := range v {
				if c >= 0x80 {
					sawNonASCII = true
				}
			}
		}
		if !sawLeadingSpace {
			t.Error("the value with a leading space did not survive the read")
		}
		if !sawNewline {
			t.Error("the value with an embedded newline did not survive the read")
		}
		if !sawNonASCII {
			t.Error("the value with non-ASCII bytes did not survive the read")
		}
	})
}

func TestReadMissingEntryIsNoSuchObject(t *testing.T) {
	target := dn.MustParse("cn=definitely-not-here,ou=people," + suffix)
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		_, err := sess.Read(ctx(t), target, nil)
		if err == nil {
			t.Fatal("Read of a missing entry succeeded")
		}
		var le *ldapdriver.Error
		if !errors.As(err, &le) {
			t.Fatalf("error = %v (%T), want an *ldapdriver.Error", err, err)
		}
		if !le.IsNoSuchObject() {
			t.Errorf("error = %v, want no such object so the API can answer 404", le)
		}
	})
}

// --- search -----------------------------------------------------------------

func TestSearchByFilter(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN: dn.MustParse(suffix),
			Scope:  directory.ScopeSubtree,
			Filter: filter.And(
				filter.Equal("objectClass", "inetOrgPerson"),
				filter.Equal("alderTeam", "platform"),
			),
			Attributes: []string{"cn", "alderTeam"},
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Entries) == 0 {
			t.Fatal("no entries matched, but the seed data has a platform team")
		}
		for _, e := range res.Entries {
			if got := e.GetOne("alderTeam"); got != "platform" {
				t.Errorf("%s: alderTeam = %q, want platform", e.DN, got)
			}
		}
		t.Logf("%s: %d platform entries", s.name, len(res.Entries))
	})
}

// TestSearchEscapesUserInput is the LDAP injection test. The value contains the
// characters that would close the filter and open another if it were
// interpolated; the filter builder escapes them, so the search matches nothing
// rather than everything.
func TestSearchEscapesUserInput(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN: dn.MustParse(suffix),
			Scope:  directory.ScopeSubtree,
			Filter: filter.Equal("cn", "*)(objectClass=*"),
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Entries) != 0 {
			t.Errorf("an injected filter matched %d entries; it must match none", len(res.Entries))
		}
	})
}

func TestSearchPagesBeyondOnePage(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		// The seed data holds 300 users, so a page size of 25 forces the driver
		// to fetch many pages to satisfy one request.
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN:     dn.MustParse("ou=people," + suffix),
			Scope:      directory.ScopeOneLevel,
			Filter:     filter.Equal("objectClass", "inetOrgPerson"),
			Attributes: []string{"cn"},
			PageSize:   25,
			Limit:      500,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Entries) < 250 {
			t.Errorf("got %d entries, want the whole seeded set; paging stopped early", len(res.Entries))
		}
		if res.Truncated {
			t.Error("Truncated is set, but the limit was above the result count")
		}
		seen := map[string]bool{}
		for _, e := range res.Entries {
			key := e.DN.String()
			if seen[key] {
				t.Errorf("%s appeared twice; a page boundary is being re-read", key)
			}
			seen[key] = true
		}
		t.Logf("%s: paged %d entries at 25 per page", s.name, len(res.Entries))
	})
}

func TestSearchRespectsTheLimitAndReportsTruncation(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN:     dn.MustParse(suffix),
			Scope:      directory.ScopeSubtree,
			Filter:     filter.Present("objectClass"),
			Attributes: []string{"cn"},
			PageSize:   10,
			Limit:      15,
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Entries) > 15 {
			t.Errorf("got %d entries, want at most the limit of 15", len(res.Entries))
		}
		if !res.Truncated {
			t.Error("Truncated is not set, but the directory holds far more than 15 entries")
		}
	})
}

func TestSearchBaseScopeReturnsOneEntry(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN: dn.MustParse(suffix),
			Scope:  directory.ScopeBase,
			Filter: filter.Present("objectClass"),
		})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(res.Entries) != 1 {
			t.Fatalf("base scope returned %d entries, want exactly 1", len(res.Entries))
		}
		if !res.Entries[0].DN.Equal(dn.MustParse(suffix)) {
			t.Errorf("DN = %s, want %s", res.Entries[0].DN, suffix)
		}
	})
}

func TestSearchNestedGroupsResolveIdentically(t *testing.T) {
	// cn=everyone nests two levels deep in the seed data. Both servers must
	// return the same member DNs for it.
	target := dn.MustParse("cn=everyone,ou=groups," + suffix)
	members := map[string][]string{}
	for _, s := range servers {
		sess := connect(t, s, false)
		e, err := sess.Read(ctx(t), target, []string{"member"})
		if err != nil {
			t.Fatalf("%s: Read(%s): %v", s.name, target, err)
		}
		got := e.GetStrings("member")
		normalised := make([]string, 0, len(got))
		for _, m := range got {
			parsed, parseErr := dn.Parse(m)
			if parseErr != nil {
				t.Errorf("%s: member %q does not parse: %v", s.name, m, parseErr)
				continue
			}
			normalised = append(normalised, strings.ToLower(parsed.String()))
		}
		members[s.name] = normalised
	}
	if !sameSet(members["openldap"], members["389ds"]) {
		t.Errorf("the two servers disagree about the members of %s:\n  openldap: %v\n  389ds:    %v",
			target, members["openldap"], members["389ds"])
	}
}

// --- tree browsing ----------------------------------------------------------

func TestChildrenAndHasChildren(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		type hasChildren interface {
			HasChildren(context.Context, dn.DN) (bool, error)
			Children(context.Context, dn.DN, []string, int, []byte) (*directory.SearchResult, error)
		}
		browser, ok := sess.(hasChildren)
		if !ok {
			t.Fatal("the session does not implement the tree browsing helpers")
		}

		got, err := browser.HasChildren(ctx(t), dn.MustParse(suffix))
		if err != nil {
			t.Fatalf("HasChildren: %v", err)
		}
		if !got {
			t.Error("the suffix reports no children, but it has three OUs")
		}

		leaf := dn.MustParse("cn=svc-alder,ou=services," + suffix)
		got, err = browser.HasChildren(ctx(t), leaf)
		if err != nil {
			t.Fatalf("HasChildren(leaf): %v", err)
		}
		if got {
			t.Error("a leaf entry reports children")
		}

		res, err := browser.Children(ctx(t), dn.MustParse(suffix), []string{"objectClass"}, 50, nil)
		if err != nil {
			t.Fatalf("Children: %v", err)
		}
		var names []string
		for _, e := range res.Entries {
			names = append(names, e.DN.RDN().String())
		}
		for _, want := range []string{"ou=people", "ou=groups", "ou=services"} {
			if !containsFold(names, want) {
				t.Errorf("children of the suffix = %v, want it to include %q", names, want)
			}
		}
	})
}

// --- write ------------------------------------------------------------------

// writeBase is where every test that modifies the directory works. It is
// created and removed per test, so a failed run cannot leave state that makes
// the next run pass or fail for the wrong reason.
func writeBase(t *testing.T, sess directory.Session, name string) dn.DN {
	t.Helper()
	base, err := dn.New("ou", name, "ou", "services", "dc", "alder", "dc", "test")
	if err != nil {
		t.Fatalf("building the test base DN: %v", err)
	}
	create := directory.ChangeRecord{
		DN:   base,
		Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{
			{Name: "objectClass", Values: bs("top", "organizationalUnit")},
			{Name: "ou", Values: bs(name)},
			{Name: "description", Values: bs("Alder conformance suite scratch space")},
		},
	}
	if err := sess.Apply(ctx(t), create); err != nil {
		t.Fatalf("creating the test base %s: %v", base, err)
	}
	t.Cleanup(func() {
		// Children first: a directory refuses to delete a non-leaf.
		res, listErr := sess.Search(context.Background(), directory.SearchRequest{
			BaseDN: base,
			Scope:  directory.ScopeOneLevel,
			Filter: filter.Present("objectClass"),
			Limit:  100,
		})
		if listErr == nil {
			for _, e := range res.Entries {
				_ = sess.Apply(context.Background(), directory.ChangeRecord{DN: e.DN, Type: directory.ChangeDelete})
			}
		}
		_ = sess.Apply(context.Background(), directory.ChangeRecord{DN: base, Type: directory.ChangeDelete})
	})
	return base
}

func TestApplyAddModifyDelete(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-crud-"+s.name)
		target, err := base.ChildAttr("cn", "test-person")
		if err != nil {
			t.Fatalf("building the target DN: %v", err)
		}

		// Add.
		add := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person", "organizationalPerson", "inetOrgPerson")},
				{Name: "cn", Values: bs("test-person")},
				{Name: "sn", Values: bs("Person")},
				{Name: "mail", Values: bs("first@alder.test", "second@alder.test")},
			},
		}
		if err := sess.Apply(ctx(t), add); err != nil {
			t.Fatalf("Apply(add): %v", err)
		}

		e, err := sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read after add: %v", err)
		}
		if len(e.Get("mail")) != 2 {
			t.Errorf("mail has %d values after add, want 2", len(e.Get("mail")))
		}

		// Modify: one of each operation in a single request, which is where an
		// implementation that reorders or merges modifications shows itself.
		modify := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{
				{Op: directory.ModAdd, Name: "description", Values: bs("added by the conformance suite")},
				{Op: directory.ModReplace, Name: "sn", Values: bs("Replaced")},
				{Op: directory.ModDelete, Name: "mail", Values: bs("first@alder.test")},
			},
		}
		if err := sess.Apply(ctx(t), modify); err != nil {
			t.Fatalf("Apply(modify): %v", err)
		}

		e, err = sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read after modify: %v", err)
		}
		if got := e.GetOne("sn"); got != "Replaced" {
			t.Errorf("sn = %q after replace, want Replaced", got)
		}
		if got := e.GetOne("description"); got != "added by the conformance suite" {
			t.Errorf("description = %q after add", got)
		}
		if got := e.GetStrings("mail"); len(got) != 1 || got[0] != "second@alder.test" {
			t.Errorf("mail = %v after deleting one value, want only second@alder.test", got)
		}

		// Delete the whole attribute, which is a delete with no values.
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModDelete, Name: "description"}},
		}); err != nil {
			t.Fatalf("Apply(delete attribute): %v", err)
		}
		e, err = sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read after attribute delete: %v", err)
		}
		if len(e.Get("description")) != 0 {
			t.Errorf("description survived a valueless delete: %v", e.GetStrings("description"))
		}

		// Delete the entry.
		if err := sess.Apply(ctx(t), directory.ChangeRecord{DN: target, Type: directory.ChangeDelete}); err != nil {
			t.Fatalf("Apply(delete): %v", err)
		}
		if _, err := sess.Read(ctx(t), target, nil); err == nil {
			t.Error("the entry is still readable after being deleted")
		}
	})
}

func TestApplyRename(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-rename-"+s.name)
		before, _ := base.ChildAttr("cn", "before")

		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   before,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person")},
				{Name: "cn", Values: bs("before")},
				{Name: "sn", Values: bs("Before")},
			},
		}); err != nil {
			t.Fatalf("Apply(add): %v", err)
		}

		rename := directory.ChangeRecord{
			DN:           before,
			Type:         directory.ChangeModRDN,
			NewRDN:       "cn=after",
			DeleteOldRDN: true,
		}
		if err := sess.Apply(ctx(t), rename); err != nil {
			t.Fatalf("Apply(modrdn): %v", err)
		}

		after, err := rename.Target()
		if err != nil {
			t.Fatalf("Target: %v", err)
		}
		e, err := sess.Read(ctx(t), after, nil)
		if err != nil {
			t.Fatalf("Read(%s) after rename: %v", after, err)
		}
		// deleteoldrdn was set, so the old cn value is gone and the new one is
		// present. Servers that ignore deleteoldrdn leave both.
		if got := e.GetStrings("cn"); len(got) != 1 || got[0] != "after" {
			t.Errorf("cn = %v after a rename with deleteoldrdn, want only [after]", got)
		}
		if _, err := sess.Read(ctx(t), before, nil); err == nil {
			t.Error("the entry is still readable at its old DN")
		}
	})
}

func TestApplyRoundTripsBinaryAndAwkwardValues(t *testing.T) {
	// Two sets, split by what each attribute's syntax actually permits.
	//
	// alderNote is a Directory String, so its values must be valid UTF-8; both
	// servers reject anything else, correctly. What makes these awkward is the
	// whitespace and the newline, all of which force base64 in LDIF and all of
	// which a careless round trip silently trims.
	awkwardText := [][]byte{
		[]byte(" leading space"),
		[]byte("trailing space "),
		[]byte("embedded\nnewline"),
		[]byte("nön-ASCII"),
		[]byte(":starts with a colon"),
	}
	// alderBadgePhoto has the JPEG syntax, which servers treat as opaque
	// octets. This is where arbitrary bytes belong, NUL included.
	binaryValue := []byte{0x00, 0x01, 0xff, 0xfe, 0x00, 0x7f, 0x80}

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-binary-"+s.name)
		target, _ := base.ChildAttr("cn", "awkward")

		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person", "organizationalPerson", "inetOrgPerson", "alderEmployee")},
				{Name: "cn", Values: bs("awkward")},
				{Name: "sn", Values: bs("Awkward")},
				{Name: "alderTeam", Values: bs("platform")},
				{Name: "alderNote", Values: awkwardText},
				{Name: "alderBadgePhoto", Values: [][]byte{binaryValue}},
			},
		}); err != nil {
			t.Fatalf("Apply(add): %v", err)
		}

		e, err := sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		got := e.Get("alderNote")
		if len(got) != len(awkwardText) {
			t.Fatalf("alderNote has %d values, want %d", len(got), len(awkwardText))
		}
		for _, want := range awkwardText {
			if !containsValue(got, want) {
				t.Errorf("the value %q did not survive the round trip; got %q", want, got)
			}
		}

		photo := e.Get("alderBadgePhoto")
		if len(photo) != 1 {
			t.Fatalf("alderBadgePhoto has %d values, want 1", len(photo))
		}
		if string(photo[0]) != string(binaryValue) {
			t.Errorf("the binary value came back as %v, want %v", photo[0], binaryValue)
		}
	})
}

// TestApplyRejectsInvalidUTF8InADirectoryString records a behaviour both
// servers share and Alder must not paper over: a Directory String is UTF-8, and
// bytes that are not get an invalid-syntax error. The editor's job is to show
// that error, not to silently re-encode the value.
func TestApplyRejectsInvalidUTF8InADirectoryString(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-utf8-"+s.name)
		target, _ := base.ChildAttr("cn", "bad-utf8")

		err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person", "organizationalPerson", "inetOrgPerson", "alderEmployee")},
				{Name: "cn", Values: bs("bad-utf8")},
				{Name: "sn", Values: bs("Bad")},
				{Name: "alderTeam", Values: bs("platform")},
				{Name: "alderNote", Values: [][]byte{{0xff, 0xfe}}},
			},
		})
		if err == nil {
			t.Fatal("the server accepted invalid UTF-8 in a Directory String")
		}
		var le *ldapdriver.Error
		if !errors.As(err, &le) || !le.IsConstraintViolation() {
			t.Errorf("error = %v, want it classified as a constraint violation", err)
		}
	})
}

func TestApplyNonASCIIDN(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-unicode-"+s.name)
		target, err := base.ChildAttr("cn", "Ünïcøde Nåme")
		if err != nil {
			t.Fatalf("building the DN: %v", err)
		}
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person")},
				{Name: "cn", Values: bs("Ünïcøde Nåme")},
				{Name: "sn", Values: bs("Nåme")},
			},
		}); err != nil {
			t.Fatalf("Apply(add) with a non-ASCII RDN: %v", err)
		}
		e, err := sess.Read(ctx(t), target, nil)
		if err != nil {
			t.Fatalf("Read(%s): %v", target, err)
		}
		if !e.DN.Equal(target) {
			t.Errorf("DN = %s, want %s", e.DN, target)
		}
	})
}

func TestSetPassword(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().PasswordModify {
			t.Fatal("the server does not advertise RFC 3062 Password Modify, " +
				"which Alder relies on rather than writing a hash itself")
		}
		base := writeBase(t, sess, "conf-passwd-"+s.name)
		target, _ := base.ChildAttr("cn", "pw-subject")

		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person")},
				{Name: "cn", Values: bs("pw-subject")},
				{Name: "sn", Values: bs("Subject")},
				{Name: "userPassword", Values: bs("first-password")},
			},
		}); err != nil {
			t.Fatalf("Apply(add): %v", err)
		}

		const replacement = "second-password-9!"
		if err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:          target,
			Type:        directory.ChangeSetPassword,
			NewPassword: replacement,
		}); err != nil {
			t.Fatalf("Apply(setpassword): %v", err)
		}

		// The only assertion that matters: the new password authenticates and
		// the old one does not. Reading userPassword back would prove nothing,
		// since the server stores a hash of its own choosing.
		if err := bindAs(t, s, target.String(), replacement); err != nil {
			t.Errorf("the new password does not authenticate: %v", err)
		}
		if err := bindAs(t, s, target.String(), "first-password"); err == nil {
			t.Error("the old password still authenticates after being changed")
		}

		// And the server, not Alder, chose how to store it.
		e, err := sess.Read(ctx(t), target, []string{"userPassword"})
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		stored := e.GetOne("userPassword")
		if stored == replacement {
			t.Error("the password was stored in plain text")
		}
		if !strings.HasPrefix(stored, "{") {
			t.Errorf("userPassword = %q, want a {scheme} prefix chosen by the server", first(stored, 12))
		}
		t.Logf("%s stored it as %s", s.name, first(stored, 8))
	})
}

// TestSetPasswordNeverRendersTheValue guards the rule that matters more than
// the feature: a password reaches the directory and nothing else.
func TestSetPasswordNeverRendersTheValue(t *testing.T) {
	const secret = "do-not-render-me-42"
	c := directory.ChangeRecord{
		DN:          dn.MustParse("cn=x,ou=people," + suffix),
		Type:        directory.ChangeSetPassword,
		NewPassword: secret,
	}
	task, err := ansible.Task(c)
	if err != nil {
		t.Fatalf("ansible.Task: %v", err)
	}
	for name, rendered := range map[string]string{
		"LDIF":       c.LDIF(),
		"LDIFFolded": c.LDIFFolded(),
		"Summary":    c.Summary(),
		"Ansible":    task,
	} {
		if strings.Contains(rendered, secret) {
			t.Errorf("%s contains the new password", name)
		}
	}
}

// bindAs opens a fresh connection as the given DN, which is the only honest way
// to test that a password works.
func bindAs(t *testing.T, s server, bindDN, password string) error {
	t.Helper()
	drv := ldapdriver.New(slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sess, err := drv.Connect(c, directory.ConnConfig{
		Host:           s.host,
		Port:           s.port,
		TLS:            directory.TLSModeLDAPS,
		CACertificates: caPool(t),
		ServerName:     "localhost",
		BindDN:         bindDN,
		BindPassword:   password,
	})
	if err != nil {
		return err
	}
	_ = sess.Close()
	return nil
}

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func TestApplyRejectsAnEmptyModify(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   dn.MustParse("cn=svc-alder,ou=services," + suffix),
			Type: directory.ChangeModify,
		})
		if !errors.Is(err, directory.ErrEmptyChange) {
			t.Errorf("Apply of an empty modify = %v, want ErrEmptyChange", err)
		}
	})
}

func TestApplySchemaViolationIsAConstraintError(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-violation-"+s.name)
		target, _ := base.ChildAttr("cn", "missing-sn")

		// person requires sn. Omitting it must be reported as a constraint
		// violation, so the UI can say which rule was broken rather than
		// showing a generic failure.
		err := sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person")},
				{Name: "cn", Values: bs("missing-sn")},
			},
		})
		if err == nil {
			t.Fatal("the server accepted an entry missing a required attribute")
		}
		var le *ldapdriver.Error
		if !errors.As(err, &le) {
			t.Fatalf("error = %v (%T), want an *ldapdriver.Error", err, err)
		}
		if !le.IsConstraintViolation() {
			t.Errorf("error = %v, want it classified as a constraint violation", le)
		}
	})
}

// --- helpers ----------------------------------------------------------------

func bs(values ...string) [][]byte {
	out := make([][]byte, len(values))
	for i, v := range values {
		out[i] = []byte(v)
	}
	return out
}

func containsFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), want) {
			return true
		}
	}
	return false
}

func containsByte(v []byte, c byte) bool {
	for _, b := range v {
		if b == c {
			return true
		}
	}
	return false
}

func containsValue(list [][]byte, want []byte) bool {
	for _, v := range list {
		if string(v) == string(want) {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = fmt.Sprintf

// --- schema editing ---------------------------------------------------------
//
// What happens underneath these cases differs completely between the two
// servers: on one it is a modify of the subschema subentry, on the other a
// modify of a configuration entry whose stored values carry an ordering prefix.
// The assertions do not know that, which is the whole claim being tested.

// The probe OID sits in an arc of its own, so a leftover from an interrupted
// run can never collide with the harness's own custom schema.
const probeOID = "1.3.6.1.4.1.99997.1.1"

func schemaProbe(desc string) schema.AttributeType {
	return schema.AttributeType{
		OID:         probeOID,
		Names:       []string{"alderProbeAttr"},
		Desc:        desc,
		Equality:    "caseIgnoreMatch",
		Syntax:      "1.3.6.1.4.1.1466.115.121.1.15",
		SingleValue: true,
	}
}

// schemaTarget is the collection a probe definition belongs in: the harness's
// own, which is last on both servers. Adding to a server's core schema would be
// a ruder test that proved nothing extra.
func schemaTarget(t *testing.T, sess directory.Session) string {
	t.Helper()
	w := sess.Capabilities().SchemaWrite
	if !w.Editable() {
		t.Skipf("schema editing is unavailable on this connection: %s", w.Unavailable)
	}
	return w.Targets[len(w.Targets)-1].DN
}

// applySchemaChange performs one schema change exactly as the application does:
// read the target as the server stores it, build the record, apply the record.
func applySchemaChange(t *testing.T, sess directory.Session, req directory.SchemaChangeRequest) error {
	t.Helper()
	stored, err := sess.SchemaDefinitions(ctx(t), req.TargetDN, req.Kind)
	if err != nil {
		return err
	}
	rec, err := directory.BuildSchemaChange(sess.Capabilities().SchemaWrite, req, stored)
	if err != nil {
		return err
	}
	return sess.Apply(ctx(t), rec)
}

func deleteProbe(t *testing.T, sess directory.Session, target string) error {
	t.Helper()
	return applySchemaChange(t, sess, directory.SchemaChangeRequest{
		TargetDN: target, Kind: directory.SchemaDefAttributeType,
		Op: directory.SchemaOpDelete, OID: probeOID,
	})
}

func TestSchemaWriteIsDiscovered(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		w := sess.Capabilities().SchemaWrite
		if !w.Editable() {
			t.Fatalf("no writable schema location was found: %s", w.Unavailable)
		}
		if w.ObjectClassAttr == "" || w.AttributeTypeAttr == "" {
			t.Errorf("a writable location named no attributes to write to: %+v", w)
		}
		for _, target := range w.Targets {
			if target.DN == "" {
				t.Errorf("a schema target has no DN: %+v", target)
			}
			if target.AttributeTypes == 0 && target.ObjectClasses == 0 {
				t.Errorf("schema target %s holds no definitions at all", target.DN)
			}
		}
	})
}

// A session that cannot reach the schema must say so, rather than offering an
// edit that will always fail. Where the schema is data this does not arise, and
// the case asserts the opposite there: the ordinary bind can edit.
func TestSchemaWriteReportsWhyItIsUnavailable(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		w := sess.Capabilities().SchemaWrite
		if s.schemaBindDN == "" {
			if !w.Editable() {
				t.Errorf("the ordinary bind cannot edit the schema, and here it should: %s", w.Unavailable)
			}
			return
		}
		if w.Editable() {
			t.Fatal("a bind with no rights in the configuration tree was offered schema editing")
		}
		if w.Unavailable == "" {
			t.Error("schema editing is unavailable and nothing says why")
		}
	})
}

func TestSchemaAddReplaceDelete(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)

		def, err := schemaProbe("probe").Definition()
		if err != nil {
			t.Fatalf("rendering the probe definition: %v", err)
		}

		// Clear any leftover from an interrupted run, so the suite is
		// repeatable without resetting the harness.
		_ = deleteProbe(t, sess, target)

		if err := applySchemaChange(t, sess, directory.SchemaChangeRequest{
			TargetDN: target, Kind: directory.SchemaDefAttributeType,
			Op: directory.SchemaOpAdd, Definition: def,
		}); err != nil {
			t.Fatalf("adding a definition: %v", err)
		}
		t.Cleanup(func() { _ = deleteProbe(t, sess, target) })

		// It must be visible through this session without reconnecting: the
		// entry editor decides what an entry may hold from this same schema,
		// and a stale one would offer an attribute the directory then refuses.
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("re-reading the schema: %v", err)
		}
		at := sch.AttributeType("alderProbeAttr")
		if at == nil {
			t.Fatal("the added attribute type is not visible through the session that added it")
		}
		if at.Desc != "probe" {
			t.Errorf("DESC is %q, want %q", at.Desc, "probe")
		}
		if !at.SingleValue {
			t.Error("SINGLE-VALUE did not survive being written and read back")
		}

		edited, err := schemaProbe("probe, edited").Definition()
		if err != nil {
			t.Fatal(err)
		}
		if err := applySchemaChange(t, sess, directory.SchemaChangeRequest{
			TargetDN: target, Kind: directory.SchemaDefAttributeType,
			Op: directory.SchemaOpReplace, OID: probeOID, Definition: edited,
		}); err != nil {
			t.Fatalf("replacing a definition: %v", err)
		}

		sch, err = sess.Schema(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		if at = sch.AttributeType("alderProbeAttr"); at == nil {
			t.Fatal("the attribute type vanished after a replace")
		}
		if at.Desc != "probe, edited" {
			t.Errorf("after the replace DESC is %q, want %q", at.Desc, "probe, edited")
		}
		// A replace that added instead of substituting would leave two, and the
		// lookup above would still have found one of them.
		var count int
		for i := range sch.AttributeTypes {
			if sch.AttributeTypes[i].OID == probeOID {
				count++
			}
		}
		if count != 1 {
			t.Errorf("after the replace the schema holds %d definitions of %s, want 1", count, probeOID)
		}

		if err := deleteProbe(t, sess, target); err != nil {
			t.Fatalf("deleting a definition: %v", err)
		}
		sch, err = sess.Schema(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		if sch.AttributeType("alderProbeAttr") != nil {
			t.Error("the attribute type is still in the schema after being deleted")
		}
	})
}

// The definition the browser displays is not always the one the server stores:
// where schema lives in configuration, the stored value carries an ordering
// prefix that the published subschema strips, so a delete built from the
// published form matches nothing. This asserts the change is built from the
// stored form — the single defect most likely to make schema editing look as
// though it works right up until it silently does not.
func TestSchemaChangeUsesTheStoredRendering(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)

		stored, err := sess.SchemaDefinitions(ctx(t), target, directory.SchemaDefAttributeType)
		if err != nil {
			t.Fatalf("reading the stored definitions: %v", err)
		}

		// alderTeam is in the harness's custom schema on both servers.
		var storedAlderTeam string
		for _, v := range stored {
			if strings.Contains(v, "'alderTeam'") {
				storedAlderTeam = v
			}
		}
		if storedAlderTeam == "" {
			t.Fatalf("alderTeam is not among the %d definitions stored at %s", len(stored), target)
		}

		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		published := sch.AttributeType("alderTeam")
		if published == nil {
			t.Fatal("alderTeam is stored but not published")
		}

		rec, err := directory.BuildSchemaChange(sess.Capabilities().SchemaWrite,
			directory.SchemaChangeRequest{
				TargetDN: target, Kind: directory.SchemaDefAttributeType,
				Op: directory.SchemaOpDelete, OID: published.OID,
			}, stored)
		if err != nil {
			t.Fatalf("building the delete: %v", err)
		}
		if len(rec.Mods) != 1 || len(rec.Mods[0].Values) != 1 {
			t.Fatalf("a delete should remove exactly one value, got %+v", rec.Mods)
		}
		if got := string(rec.Mods[0].Values[0]); got != storedAlderTeam {
			t.Errorf("the delete does not use the stored rendering\n  sends:  %q\n  stored: %q",
				got, storedAlderTeam)
		}
	})
}

func TestSchemaChangeRefusesAnUnknownDefinition(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)
		stored, err := sess.SchemaDefinitions(ctx(t), target, directory.SchemaDefAttributeType)
		if err != nil {
			t.Fatal(err)
		}
		_, err = directory.BuildSchemaChange(sess.Capabilities().SchemaWrite,
			directory.SchemaChangeRequest{
				TargetDN: target, Kind: directory.SchemaDefAttributeType,
				Op: directory.SchemaOpDelete, OID: "1.3.6.1.4.1.99997.999.999",
			}, stored)
		if !errors.Is(err, directory.ErrDefinitionNotFound) {
			t.Errorf("deleting a definition that is absent gave %v, want ErrDefinitionNotFound", err)
		}
	})
}

// The schema entries are not ordinary entries, and a modification addressed
// anywhere else must not be able to travel through the schema path.
func TestSchemaChangeRefusesAnUnlistedTarget(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		_ = schemaTarget(t, sess) // skips where editing is unavailable
		_, err := directory.BuildSchemaChange(sess.Capabilities().SchemaWrite,
			directory.SchemaChangeRequest{
				TargetDN: "uid=user0001,ou=people," + suffix,
				Kind:     directory.SchemaDefAttributeType,
				Op:       directory.SchemaOpAdd,
				Definition: "( 1.3.6.1.4.1.99997.5.1 NAME 'alderNotSchema' " +
					"SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
			}, nil)
		if err == nil {
			t.Error("a change aimed at an ordinary entry was accepted as a schema change")
		}
	})
}

// An attribute type with neither a syntax nor a superior to inherit one from
// parses cleanly and means nothing. Both servers would take it; the point of
// refusing it here is that the person is told which field is missing, while
// they are still looking at the form.
func TestSchemaChangeRefusesADefinitionWithNoSyntax(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)
		_, err := directory.BuildSchemaChange(sess.Capabilities().SchemaWrite,
			directory.SchemaChangeRequest{
				TargetDN:   target,
				Kind:       directory.SchemaDefAttributeType,
				Op:         directory.SchemaOpAdd,
				Definition: "( 1.3.6.1.4.1.99997.5.2 NAME 'alderNoSyntax' )",
			}, nil)
		if err == nil {
			t.Error("an attribute type with no syntax and no superior was accepted")
		}
	})
}

// --- the configuration tree -------------------------------------------------
//
// A directory keeps its configuration in the directory. Reaching it is what
// makes the schema browser able to say where the schema comes from, and what
// makes schema editing possible where the schema is kept there.
//
// The two servers differ in both of the ways that matter — one announces the
// tree and refuses the data administrator, the other announces nothing and lets
// the directory manager straight in — and these cases assert the same reachable
// end state for both.

func TestConfigTreeIsReachable(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		cfg := sess.Capabilities().Config
		if !cfg.Readable {
			t.Fatalf("the configuration tree is not readable: %s", cfg.Reason)
		}
		if cfg.DN == "" {
			t.Error("the configuration tree is readable but has no DN")
		}
		if cfg.BoundAs == "" {
			t.Error("nothing records which identity the configuration tree is read as")
		}
		// A second identity is used exactly where the table says one is needed.
		if want := s.schemaBindDN != ""; cfg.SeparateBind != want {
			t.Errorf("separateBind is %v, want %v", cfg.SeparateBind, want)
		}
	})
}

// The tree has to be genuinely browsable, not merely announced: the point is to
// look inside it.
func TestConfigTreeHasReadableChildren(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		cfg := sess.Capabilities().Config
		if !cfg.Readable {
			t.Skipf("the configuration tree is not readable: %s", cfg.Reason)
		}
		base, err := dn.Parse(cfg.DN)
		if err != nil {
			t.Fatalf("the configuration DN does not parse: %v", err)
		}
		browser, ok := sess.(interface {
			Children(context.Context, dn.DN, []string, int, []byte) (*directory.SearchResult, error)
		})
		if !ok {
			t.Skip("this session cannot list children")
		}
		res, err := browser.Children(ctx(t), base, []string{"objectClass"}, 50, nil)
		if err != nil {
			t.Fatalf("listing the configuration tree: %v", err)
		}
		if len(res.Entries) == 0 {
			t.Error("the configuration tree has no children, which no server's does")
		}
	})
}

// The second identity must be used only inside the configuration tree. If it
// leaked into ordinary reads, a session would silently browse data as the
// configuration administrator — a different account with different rights, and
// on some servers a much more powerful one.
func TestConfigIdentityDoesNotLeakIntoTheDataTree(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.SeparateBind {
			t.Skip("this server needs no second identity")
		}
		// The data tree must still be readable, which is the whole reason for
		// not simply binding as the configuration administrator instead.
		entry, err := sess.Read(ctx(t), dn.MustParse("uid=user0001,ou=people,"+suffix), []string{"uid"})
		if err != nil {
			t.Fatalf("the data tree became unreadable once a second identity was supplied: %v", err)
		}
		if len(entry.Attributes) == 0 {
			t.Error("the data entry came back empty")
		}
	})
}

// Where the schema is kept decides where it is written, and that is not the
// same question as whether a configuration tree happens to be reachable. Both
// servers here have a reachable configuration tree; only one keeps its schema
// in it.
func TestSchemaStyleFollowsWhereTheSchemaIsKept(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		caps := sess.Capabilities()
		if !caps.Config.Readable {
			t.Skipf("the configuration tree is not readable: %s", caps.Config.Reason)
		}
		style := caps.SchemaWrite.Style
		if style == directory.SchemaStyleNone {
			t.Fatalf("no writable schema location: %s", caps.SchemaWrite.Unavailable)
		}
		// The announcement is what distinguishes them, and it is the server's
		// statement about its own architecture rather than anything inferred.
		if caps.ConfigContext != "" && style != directory.SchemaStyleConfig {
			t.Errorf("this server announces a configContext, so its schema is generated from "+
				"configuration entries, but the style is %q", style)
		}
		if caps.ConfigContext == "" && style != directory.SchemaStyleSubschema {
			t.Errorf("this server announces no configContext, so its subschema subentry is the "+
				"schema, but the style is %q", style)
		}
	})
}

// A change addressed into the configuration tree has to actually land there.
//
// Editing configuration was not built as a feature: it falls out of the entry
// editor being general and writes being routed by DN. That makes it exactly the
// kind of capability that works until something quietly stops routing it, with
// nothing failing loudly in between — so it is asserted here like anything else.
//
// On the server whose configuration needs its own identity, this also proves
// the routing in the direction the other cases do not: a session bound to the
// data suffix, writing successfully into a tree that bind cannot even read.
func TestConfigEntryCanBeModified(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if s.configWriteDN == "" {
			t.Skip("no configuration setting is nominated for this server")
		}
		if !sess.Capabilities().Config.Readable {
			t.Skipf("the configuration tree is not readable: %s", sess.Capabilities().Config.Reason)
		}
		target := dn.MustParse(s.configWriteDN)

		read := func() []string {
			t.Helper()
			entry, err := sess.Read(ctx(t), target, []string{s.configWriteAttr})
			if err != nil {
				t.Fatalf("reading %s: %v", s.configWriteDN, err)
			}
			return entry.GetStrings(s.configWriteAttr)
		}
		set := func(values []string) error {
			t.Helper()
			vals := make([][]byte, 0, len(values))
			for _, v := range values {
				vals = append(vals, []byte(v))
			}
			return sess.Apply(ctx(t), directory.ChangeRecord{
				DN:   target,
				Type: directory.ChangeModify,
				Mods: []directory.Mod{{Op: directory.ModReplace, Name: s.configWriteAttr, Values: vals}},
			})
		}

		before := read()
		if len(before) == 0 {
			t.Fatalf("%s holds no %s to restore afterwards", s.configWriteDN, s.configWriteAttr)
		}
		if before[0] == s.configWriteValue {
			t.Fatalf("%s already holds the value the test would set, so a successful write "+
				"would be indistinguishable from no write at all", s.configWriteAttr)
		}
		// Restoration is registered before the change, so an assertion failing
		// mid-test still puts the server back.
		t.Cleanup(func() {
			if err := set(before); err != nil {
				t.Errorf("could not restore %s to %q: %v", s.configWriteAttr, before, err)
			}
		})

		if err := set([]string{s.configWriteValue}); err != nil {
			t.Fatalf("modifying the configuration entry: %v", err)
		}
		if after := read(); len(after) != 1 || after[0] != s.configWriteValue {
			t.Errorf("%s is %q after the change, want [%q]", s.configWriteAttr, after, s.configWriteValue)
		}
	})
}

// The ordering prefixes a configuration entry keeps on its multi-valued
// attributes are the same hazard the schema had: what the server stores is not
// what a naive round trip would send back. Access rules are where it matters
// most, so the suite asserts the values survive being read and written whole.
func TestConfigOrderedValuesSurviveARoundTrip(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		if !sess.Capabilities().Config.Readable {
			t.Skipf("the configuration tree is not readable: %s", sess.Capabilities().Config.Reason)
		}
		// Only one of the two servers keeps access rules as ordered values in
		// its configuration; on the other this finds nothing and says so rather
		// than pretending to have tested something.
		base := dn.MustParse(sess.Capabilities().Config.DN)
		browser, ok := sess.(interface {
			Children(context.Context, dn.DN, []string, int, []byte) (*directory.SearchResult, error)
		})
		if !ok {
			t.Skip("this session cannot list children")
		}
		res, err := browser.Children(ctx(t), base, []string{"olcAccess"}, 50, nil)
		if err != nil {
			t.Fatalf("listing the configuration tree: %v", err)
		}
		var holder *directory.Entry
		for i := range res.Entries {
			if len(res.Entries[i].Get("olcAccess")) > 1 {
				holder = res.Entries[i]
			}
		}
		if holder == nil {
			t.Skip("this server keeps no ordered access rules in its configuration")
		}

		before := holder.Get("olcAccess")
		// Every stored value carries its position. A round trip that dropped or
		// renumbered them would change which rule wins, silently.
		for i, v := range before {
			if !strings.HasPrefix(string(v), "{") {
				t.Errorf("access rule %d has no ordering prefix: %q", i, v)
			}
		}

		err = sess.Apply(ctx(t), directory.ChangeRecord{
			DN:   holder.DN,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModReplace, Name: "olcAccess", Values: before}},
		})
		if err != nil {
			t.Fatalf("writing the access rules back unchanged: %v", err)
		}

		entry, err := sess.Read(ctx(t), holder.DN, []string{"olcAccess"})
		if err != nil {
			t.Fatalf("re-reading the access rules: %v", err)
		}
		after := entry.Get("olcAccess")
		if len(after) != len(before) {
			t.Fatalf("the round trip changed the number of access rules: %d -> %d", len(before), len(after))
		}
		for i := range before {
			if string(after[i]) != string(before[i]) {
				t.Errorf("access rule %d changed\n  before: %q\n  after:  %q", i, before[i], after[i])
			}
		}
	})
}

// A server may store an entry under a name of its own choosing.
//
// Where an entry's position among its siblings forms part of that name, the
// server assigns the position and rewrites the RDN, so the DN a caller asked
// for resolves to nothing afterwards. Alder looks up where an added entry
// actually landed because of this; the case asserts the convention is real
// rather than something inferred from one observation.
//
// Nothing is created here. The harness's own schema collection already
// demonstrates it, and creating a collection would be a change this suite
// cannot undo: servers refuse to remove one while it is loaded.
func TestPositionPrefixedNamesAreTheServersOwn(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		w := sess.Capabilities().SchemaWrite
		if w.Style != directory.SchemaStyleConfig {
			t.Skip("this server does not put an entry's position into its name")
		}

		var prefixed string
		for _, target := range w.Targets {
			if strings.HasPrefix(strings.SplitN(target.DN, ",", 2)[0], "cn={") {
				prefixed = target.DN
			}
		}
		if prefixed == "" {
			t.Fatalf("no schema collection carries a position prefix: %+v", w.Targets)
		}

		// The prefixed name is the real one.
		if _, err := sess.Read(ctx(t), dn.MustParse(prefixed), []string{"cn"}); err != nil {
			t.Fatalf("reading %s: %v", prefixed, err)
		}

		// The name without the position is not, which is exactly the trap: a
		// caller who asked for this name and was told it succeeded would send
		// the next reader here.
		rdn, rest, _ := strings.Cut(prefixed, ",")
		bare := strings.Replace(rdn, "{", "", 1)
		if i := strings.Index(bare, "}"); i >= 0 {
			bare = bare[:strings.Index(bare, "=")+1] + bare[i+1:]
		}
		unprefixed := bare + "," + rest
		if _, err := sess.Read(ctx(t), dn.MustParse(unprefixed), []string{"cn"}); err == nil {
			t.Errorf("%s resolves as well as %s, so the position is not part of the name "+
				"on this server after all", unprefixed, prefixed)
		}
	})
}

// Where the schema is an entry rather than a view, that entry is not inside any
// naming context — which is why the tree has to be told about it separately.
//
// This is the fact behind the asymmetry a tester reported: on a server whose
// schema lives in configuration entries, browsing the configuration reaches it;
// on a server whose subschema subentry is the schema, nothing reaches it, and
// the schema browser was the only way in.
func TestTheSchemaEntrySitsOutsideEveryNamingContext(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		caps := sess.Capabilities()
		if caps.SchemaWrite.Style != directory.SchemaStyleSubschema {
			t.Skip("this server keeps its schema in configuration entries")
		}
		for _, target := range caps.SchemaWrite.Targets {
			for _, nc := range caps.NamingContexts {
				lower := strings.ToLower(target.DN)
				base := strings.ToLower(nc)
				if lower == base || strings.HasSuffix(lower, ","+base) {
					t.Errorf("%s is inside the naming context %s, so the tree would show it twice",
						target.DN, nc)
				}
			}
			// And it must be readable, or offering it as a root would be a
			// root that opens onto an error.
			if _, err := sess.Read(ctx(t), dn.MustParse(target.DN), []string{"objectClass"}); err != nil {
				t.Errorf("the schema entry %s cannot be read: %v", target.DN, err)
			}
		}
	})
}

// An attribute type that an object class uses must still be editable.
//
// This is the case a tester hit and the suite did not: every schema edit it
// covered was of a definition nothing referenced, which is the easy half. A
// server refuses to delete an attribute type named in a class's MUST or MAY,
// even inside the operation that puts it straight back — so expressing an edit
// as delete-and-add made almost every real edit fail, because an attribute type
// worth editing is usually one a class uses.
//
// alderTeam is MUST in alderEmployee on both servers, which is what makes it
// the right subject.
func TestSchemaReplaceOfAnAttributeAnObjectClassUses(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		target := schemaTarget(t, sess)
		const oid = "1.3.6.1.4.1.99999.1.1" // alderTeam

		stored, err := sess.SchemaDefinitions(ctx(t), target, directory.SchemaDefAttributeType)
		if err != nil {
			t.Fatalf("reading the stored definitions: %v", err)
		}
		var before string
		for _, v := range stored {
			if strings.Contains(v, "'alderTeam'") {
				before = v
			}
		}
		if before == "" {
			t.Skipf("alderTeam is not held at %s", target)
		}
		// Restoration is registered first, so a failure part way still leaves
		// the harness as it was.
		t.Cleanup(func() {
			_ = applySchemaChange(t, sess, directory.SchemaChangeRequest{
				TargetDN: target, Kind: directory.SchemaDefAttributeType,
				Op: directory.SchemaOpReplace, OID: oid,
				Definition: stripOrdering(before),
			})
		})

		// The definition is parsed and only its description changed, which is
		// what an edit through the UI is. Hand-writing the fields instead would
		// assert that a definition Alder composed survives, and miss the defect
		// that mattered: an edit built from an incomplete view of the original
		// silently dropped the fields it could not see.
		parsed, err := schema.ParseAttributeType(stripOrdering(before))
		if err != nil {
			t.Fatalf("parsing the stored definition: %v", err)
		}
		parsed.Desc = "Team the person belongs to, edited by the conformance suite"
		def, err := parsed.Definition()
		if err != nil {
			t.Fatal(err)
		}
		if err := applySchemaChange(t, sess, directory.SchemaChangeRequest{
			TargetDN: target, Kind: directory.SchemaDefAttributeType,
			Op: directory.SchemaOpReplace, OID: oid, Definition: def,
		}); err != nil {
			t.Fatalf("editing an attribute type that an object class uses: %v", err)
		}

		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		at := sch.AttributeType("alderTeam")
		if at == nil {
			t.Fatal("alderTeam disappeared")
		}
		// Everything the definition declared has to survive an edit that only
		// touched the description.
		if at.Equality != parsed.Equality {
			t.Errorf("EQUALITY is %q after the edit, want %q", at.Equality, parsed.Equality)
		}
		if at.Substr != parsed.Substr {
			t.Errorf("SUBSTR is %q after the edit, want %q — an edit dropped a field "+
				"nobody asked it to", at.Substr, parsed.Substr)
		}
		if at.Ordering != parsed.Ordering {
			t.Errorf("ORDERING is %q after the edit, want %q", at.Ordering, parsed.Ordering)
		}
		if at.Syntax != parsed.Syntax {
			t.Errorf("SYNTAX is %q after the edit, want %q", at.Syntax, parsed.Syntax)
		}
		if at.SingleValue != parsed.SingleValue {
			t.Errorf("SINGLE-VALUE is %v after the edit, want %v", at.SingleValue, parsed.SingleValue)
		}
		if !strings.Contains(at.Desc, "edited by the conformance suite") {
			t.Errorf("DESC is %q, so the edit did not take", at.Desc)
		}
		// And exactly one definition of the OID, whichever form the change took.
		var count int
		for i := range sch.AttributeTypes {
			if sch.AttributeTypes[i].OID == oid {
				count++
			}
		}
		if count != 1 {
			t.Errorf("the schema holds %d definitions of %s, want 1", count, oid)
		}
		// The class that uses it must still resolve, which is the reason the
		// server refused the other form in the first place.
		if oc := sch.ObjectClass("alderEmployee"); oc == nil {
			t.Error("alderEmployee no longer resolves after editing an attribute it requires")
		}
	})
}

// stripOrdering removes the position prefix a configuration entry keeps, so a
// stored value can be offered back as a definition to write.
func stripOrdering(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "{") {
		if end := strings.IndexByte(v, '}'); end > 0 {
			return strings.TrimSpace(v[end+1:])
		}
	}
	return v
}

// TestObjectViewAnchorsExistOnBothServers asserts the premise the object views
// rest on: that "user", "group" and "organizational unit" can be expressed in
// standards-track classes both servers define.
//
// The views themselves are derived in internal/api from the parsed schema, and
// that derivation is unit-tested there. What only a real server can answer is
// whether the anchors are actually published — if either server stopped
// defining person, the Users view would silently disappear on one of them and
// nothing else in the suite would notice.
func TestObjectViewAnchorsExistOnBothServers(t *testing.T) {
	// One anchor per view is enough for the view to be offered. These are the
	// ones both servers are expected to have; the derivation copes with the
	// rest being absent.
	required := map[string][]string{
		"users":               {"person"},
		"groups":              {"groupOfNames", "groupOfUniqueNames"},
		"organizationalUnits": {"organizationalUnit"},
	}

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}
		for view, anchors := range required {
			for _, name := range anchors {
				if sch.ObjectClass(name) == nil {
					t.Errorf("%s does not define %s, so the %s view would not be offered",
						s.name, name, view)
				}
			}
		}

		// The column derivation walks down from the anchors, not just across
		// them. inetOrgPerson is what puts mail and uid in the users table, and
		// it is only reachable because it inherits from person.
		person := sch.ObjectClass("person")
		inet := sch.ObjectClass("inetOrgPerson")
		if person == nil || inet == nil {
			t.Fatalf("%s: person=%v inetOrgPerson=%v; both are needed for the users columns",
				s.name, person != nil, inet != nil)
		}
		descends := false
		for _, sup := range sch.Supers(inet) {
			if sup == person {
				descends = true
				break
			}
		}
		if !descends {
			t.Errorf("%s: inetOrgPerson does not inherit from person, so an entry carrying it "+
				"would not be matched by the users filter", s.name)
		}

		// And the attributes those columns name have to exist, or the column is
		// dropped and the table is poorer on one server than the other.
		req := sch.Requirements([]string{"person", "inetOrgPerson"})
		permitted := map[string]bool{}
		for _, n := range append(append([]string{}, req.Must...), req.May...) {
			permitted[strings.ToLower(n)] = true
		}
		for _, attr := range []string{"cn", "uid", "mail", "telephoneNumber"} {
			if !permitted[strings.ToLower(attr)] {
				t.Errorf("%s: neither person nor inetOrgPerson permits %s, so the users table "+
					"loses that column here but not on the other server", s.name, attr)
			}
		}
	})
}

// TestBooleanSyntaxIsRecognisedOnBothServers asserts the fact the editor's
// Boolean control depends on: that an attribute declaring RFC 4517's Boolean
// syntax is reported as a Boolean, on either server, whether it declares the
// syntax itself or inherits it through SUP.
//
// The control is chosen from this and nothing else, which is what lets the two
// servers be handled without naming either. They diverge sharply in practice —
// one keeps its configuration switches as real Booleans, the other as
// DirectoryString holding "on" and "off" — and neither fact is written down
// anywhere in Alder. The syntax is asked, and the answer decides.
func TestBooleanSyntaxIsRecognisedOnBothServers(t *testing.T) {
	const booleanSyntax = "1.3.6.1.4.1.1466.115.121.1.7"

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}

		found := 0
		for _, at := range sch.AttributeTypes {
			if sch.EffectiveSyntax(at) != booleanSyntax {
				continue
			}
			found++
			if kind := sch.KindOf(at.Name()); kind.Kind != schema.KindBoolean {
				t.Errorf("%s: %s declares the Boolean syntax but is reported as %q, "+
					"so the editor would offer a text box for it",
					s.name, at.Name(), kind.Kind)
			}
		}
		t.Logf("%s: %d attribute types use the Boolean syntax", s.name, found)
		if found == 0 {
			t.Errorf("%s publishes no Boolean attribute at all, which no real "+
				"directory schema does — the syntax lookup is probably wrong", s.name)
		}

		// And the other half of the same claim: an attribute that is not a
		// Boolean must not be reported as one, or an operator gets a TRUE/FALSE
		// control over a value that is neither.
		for _, name := range []string{"cn", "description"} {
			at := sch.AttributeType(name)
			if at == nil {
				continue
			}
			if kind := sch.KindOf(name); kind.Kind == schema.KindBoolean {
				t.Errorf("%s: %s is reported as a Boolean", s.name, name)
			}
		}
	})
}

// TestPermittedAttributesAreAllDescribed asserts what the "Add an attribute"
// list depends on: every attribute an entry's classes permit is one the schema
// can describe.
//
// It could not be taken for granted. The editor used to look an added attribute
// up among the attributes the entry already had, which by definition never
// contains it, so everything added arrived badged "not in the schema" with a
// plain text box. The lookup is fixed; this asserts the data behind it is
// there on both servers.
func TestPermittedAttributesAreAllDescribed(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}
		base, err := dn.Parse(suffix)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := sess.Read(ctx(t), base, []string{"*"})
		if err != nil {
			t.Fatalf("Read(%s): %v", suffix, err)
		}

		req := sch.Requirements(entry.ObjectClasses())
		if len(req.May)+len(req.Must) == 0 {
			t.Fatalf("%s: the suffix entry permits no attributes at all", s.name)
		}
		for _, name := range append(append([]string{}, req.Must...), req.May...) {
			if kind := sch.KindOf(name); !kind.Known {
				t.Errorf("%s: %s is permitted by the suffix entry's classes but the "+
					"schema cannot describe it, so adding it would offer a bare text box",
					s.name, name)
			}
		}
	})
}

// TestOnlyStructuralClassesCanBeCreated asserts the rule the creation form
// offers classes by — and it is a rule, not a convention, because the two
// servers disagree about a class both of them define.
//
// posixGroup is STRUCTURAL on one server and AUXILIARY on the other. An entry
// whose only class is an auxiliary one is refused, so offering posixGroup as
// something to create would work against one server and fail against the other.
// Nothing in Alder knows which is which: the form offers what the connected
// server calls structural, and the divergence disappears.
func TestOnlyStructuralClassesCanBeCreated(t *testing.T) {
	kinds := map[string]map[string]schema.Kind{}

	for _, s := range servers {
		s := s
		t.Run(s.name, func(t *testing.T) {
			sess := connect(t, s, false)
			defer sess.Close()
			sch, err := sess.Schema(ctx(t))
			if err != nil {
				t.Fatalf("Schema: %v", err)
			}
			kinds[s.name] = map[string]schema.Kind{}
			for _, name := range []string{"posixGroup", "groupOfNames", "inetOrgPerson", "organizationalUnit"} {
				if oc := sch.ObjectClass(name); oc != nil {
					kinds[s.name][name] = oc.Kind
				}
			}

			// Whatever each server says, a class an entry is created as has to
			// be structural there.
			for _, name := range []string{"groupOfNames", "inetOrgPerson", "organizationalUnit"} {
				oc := sch.ObjectClass(name)
				if oc == nil {
					t.Errorf("%s does not define %s", s.name, name)
					continue
				}
				if oc.Kind != schema.KindStructural {
					t.Errorf("%s calls %s %v; the creation form would offer a class "+
						"that cannot stand alone", s.name, name, oc.Kind)
				}
			}
		})
	}

	// And the divergence itself, recorded so a future change to either server's
	// seed makes this visible rather than silently altering what is offered.
	if len(kinds) == 2 {
		a, b := kinds["openldap"]["posixGroup"], kinds["389ds"]["posixGroup"]
		t.Logf("posixGroup is %v on openldap and %v on 389ds", a, b)
		if a == b {
			t.Logf("the two servers now agree about posixGroup; nothing is wrong, " +
				"but the case this test was written for no longer exists")
		}
	}
}

// TestProvenanceIsAvailableOnBothServersByDifferentMeans asserts that "where
// did this definition come from" has an answer on each server — and that the
// answer arrives by a different route on each, which is the reason the schema
// table reports provenance rather than a shipped-or-custom flag.
//
// One server keeps its schema in configuration entries and discards X-ORIGIN
// when it loads a schema file, so the collection holding a definition is the
// only thing distinguishing them. The other keeps X-ORIGIN and has a single
// schema entry, where the collection would say nothing. A flag claiming
// "shipped" or "custom" would be a guess on the first of those.
func TestProvenanceIsAvailableOnBothServersByDifferentMeans(t *testing.T) {
	eachServerForSchema(t, func(t *testing.T, s server, sess directory.Session) {
		caps := sess.Capabilities()
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}

		byCollection := len(caps.SchemaWrite.Origin)
		byExtension := 0
		for _, oc := range sch.ObjectClasses {
			if v, ok := oc.Extensions["X-ORIGIN"]; ok && len(v) > 0 {
				byExtension++
			}
		}
		t.Logf("%s: %d definitions placed by collection, %d object classes carrying X-ORIGIN",
			s.name, byCollection, byExtension)

		if byCollection == 0 && byExtension == 0 {
			t.Errorf("%s offers no provenance by either route, so the schema table "+
				"could not say where anything came from", s.name)
		}

		// And the harness's own class must be attributable, whichever route the
		// server uses: "which of this schema is mine" is the question the column
		// exists to answer.
		const custom = "alderEmployee"
		oc := sch.ObjectClass(custom)
		if oc == nil {
			t.Fatalf("%s does not define %s, which the harness installs", s.name, custom)
		}
		_, placed := caps.SchemaWrite.Origin[oc.OID]
		_, extended := oc.Extensions["X-ORIGIN"]
		if !placed && !extended {
			t.Errorf("%s can say nothing about where %s came from", s.name, custom)
		}
	})
}

// TestReverseReferenceLookupFindsEveryShape asserts the question behind the
// "referenced by" link: given a person's DN, find every entry that names them.
//
// It is a conformance case rather than a unit test because the filter is only
// as good as the vocabulary the connected server publishes, and because the
// two DN-valued membership styles do not match the same way. member is a plain
// DN; uniqueMember carries RFC 4517's Name and Optional UID syntax, matched by
// uniqueMemberMatch, and a server is entitled to treat a bare DN and a
// DN#uid differently. Asserting it against both servers is the only way to
// know the same question gets the same answer.
func TestReverseReferenceLookupFindsEveryShape(t *testing.T) {
	subject := "uid=user0001,ou=people," + suffix

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}
		base, err := dn.Parse(suffix)
		if err != nil {
			t.Fatal(err)
		}

		// The three shapes the seed installs for user0001: a groupOfNames by
		// member, a groupOfUniqueNames by uniqueMember, and a group that names
		// them as its owner.
		for _, tc := range []struct {
			attribute string
			wantDN    string
		}{
			{"member", "cn=network,ou=groups," + suffix},
			{"uniqueMember", "cn=auditors,ou=groups," + suffix},
			{"owner", "cn=platform-owned,ou=groups," + suffix},
		} {
			at := sch.AttributeType(tc.attribute)
			if at == nil {
				t.Errorf("%s does not define %s, so the reverse lookup cannot ask about it",
					s.name, tc.attribute)
				continue
			}

			res, searchErr := sess.Search(ctx(t), directory.SearchRequest{
				BaseDN:     base,
				Scope:      directory.ScopeSubtree,
				Filter:     filter.Equal(at.Name(), subject),
				Attributes: []string{"1.1"},
				Limit:      50,
				PageSize:   50,
			})
			if searchErr != nil {
				t.Errorf("%s: searching %s=%s: %v", s.name, tc.attribute, subject, searchErr)
				continue
			}

			found := false
			for _, e := range res.Entries {
				if strings.EqualFold(e.DN.String(), tc.wantDN) {
					found = true
				}
			}
			if !found {
				var got []string
				for _, e := range res.Entries {
					got = append(got, e.DN.String())
				}
				t.Errorf("%s: %s=%s did not find %s; got %v",
					s.name, tc.attribute, subject, tc.wantDN, got)
			}
		}
	})
}

// And the same question asked as one filter, which is what the link actually
// sends: every shape at once, and the union is what the operator sees.
func TestReverseReferenceLookupAsOneFilter(t *testing.T) {
	subject := "uid=user0001,ou=people," + suffix

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}
		base, err := dn.Parse(suffix)
		if err != nil {
			t.Fatal(err)
		}

		var subs []filter.Filter
		for _, name := range []string{"member", "uniqueMember", "owner", "manager", "seeAlso"} {
			if at := sch.AttributeType(name); at != nil {
				subs = append(subs, filter.Equal(at.Name(), subject))
			}
		}
		if len(subs) < 3 {
			t.Fatalf("%s defines only %d of the reference attributes", s.name, len(subs))
		}

		res, searchErr := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN:     base,
			Scope:      directory.ScopeSubtree,
			Filter:     filter.Or(subs...),
			Attributes: []string{"1.1"},
			Limit:      50,
			PageSize:   50,
		})
		if searchErr != nil {
			t.Fatalf("%s: %v", s.name, searchErr)
		}

		got := map[string]bool{}
		for _, e := range res.Entries {
			got[strings.ToLower(e.DN.String())] = true
		}
		for _, want := range []string{
			"cn=network,ou=groups," + suffix,
			"cn=auditors,ou=groups," + suffix,
			"cn=platform-owned,ou=groups," + suffix,
		} {
			if !got[strings.ToLower(want)] {
				t.Errorf("%s: the combined filter missed %s (found %d entries)",
					s.name, want, len(res.Entries))
			}
		}
		t.Logf("%s: %d entries reference %s", s.name, len(res.Entries), subject)
	})
}

// The assumptions the import reconciler rests on, checked on both servers.
//
// Reconciling turns a content record whose entry exists into a modify that
// replaces the attributes the document names — and only those. Three things
// have to be true of a real directory for that to be safe, and none of them is
// obvious enough to take on trust across two vendors.
func TestReconcileAssumptionsHold(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-reconcile-"+s.name)
		target, err := base.ChildAttr("cn", "reconcile-subject")
		if err != nil {
			t.Fatalf("building the target DN: %v", err)
		}

		create := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person", "organizationalPerson", "inetOrgPerson")},
				{Name: "cn", Values: bs("reconcile-subject")},
				{Name: "sn", Values: bs("Subject")},
				{Name: "mail", Values: bs("first@alder.test", "second@alder.test")},
				{Name: "userPassword", Values: bs("a-password-the-document-never-carries")},
			},
		}
		if err := sess.Apply(ctx(t), create); err != nil {
			t.Fatalf("creating the subject: %v", err)
		}

		before, err := sess.Read(ctx(t), target, []string{"*"})
		if err != nil {
			t.Fatalf("reading the subject back: %v", err)
		}

		// 1. An export's values come back byte for byte, which is what makes an
		//    unchanged round trip reconcile to nothing rather than to a page of
		//    modifications nobody asked to confirm.
		for _, name := range []string{"cn", "sn", "mail"} {
			got := before.Get(name)
			want := attrValues(create, name)
			if !sameValueSet(got, want) {
				t.Errorf("%s came back as %q, was written as %q", name, got, want)
			}
		}

		// 2. Replacing an attribute with the values it already holds is
		//    accepted. The reconciler skips these, but a server that refused
		//    them would make any near-miss comparison dangerous.
		idempotent := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: "mail", Values: before.Get("mail")},
			},
		}
		if err := sess.Apply(ctx(t), idempotent); err != nil {
			t.Errorf("replacing an attribute with its own values was refused: %v", err)
		}

		// 3. The safety claim: a modify that does not name userPassword leaves
		//    it alone. An export omits it always, so reconciling must never be
		//    able to read its absence from a document as a request to remove it.
		narrow := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: "mail", Values: bs("changed@alder.test")},
			},
		}
		if err := sess.Apply(ctx(t), narrow); err != nil {
			t.Fatalf("the narrow modification was refused: %v", err)
		}

		after, err := sess.Read(ctx(t), target, []string{"*"})
		if err != nil {
			t.Fatalf("reading the subject after the change: %v", err)
		}
		if got := after.Get("mail"); len(got) != 1 || string(got[0]) != "changed@alder.test" {
			t.Errorf("mail is %q after the reconciliation", got)
		}
		if len(after.Get("userPassword")) == 0 {
			t.Error("the password was removed by a modification that never named it")
		}
		if got := after.Get("sn"); len(got) != 1 || string(got[0]) != "Subject" {
			t.Errorf("an unnamed attribute changed: sn is %q", got)
		}
	})
}

// attrValues reads one attribute off a change record.
func attrValues(rec directory.ChangeRecord, name string) [][]byte {
	for _, a := range rec.Attrs {
		if strings.EqualFold(a.Name, name) {
			return a.Values
		}
	}
	return nil
}

// sameValueSet compares two attribute values as the sets they are, which is
// what they are: a directory returns them in whatever order it likes.
func sameValueSet(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([][]byte(nil), a...)
	right := append([][]byte(nil), b...)
	sort.Slice(left, func(i, j int) bool { return bytes.Compare(left[i], left[j]) < 0 })
	sort.Slice(right, func(i, j int) bool { return bytes.Compare(right[i], right[j]) < 0 })
	for i := range left {
		if !bytes.Equal(left[i], right[i]) {
			return false
		}
	}
	return true
}

// --- the 0.12.0 features ----------------------------------------------------
//
// Group expansion, entry comparison and the value tally all live in
// internal/api, where their own logic is unit tested against a fake. What a
// fake cannot tell us is whether the directory-level facts they rest on are
// true of a real server, and true of *both* -- which is the only reason this
// suite exists. Each test below names the assumptions one feature depends on
// and checks them from one table against every server.

// classPermitsAttr reports whether an object class allows an attribute,
// mirroring how the expansion walk decides an entry is a group.
func classPermitsAttr(sch *schema.Schema, class, attr string) bool {
	if sch.ObjectClass(class) == nil {
		return false
	}
	req := sch.Requirements([]string{class})
	for _, name := range append(append([]string{}, req.Must...), req.May...) {
		if strings.EqualFold(schema.BaseName(name), attr) {
			return true
		}
	}
	return false
}

// The assumptions the group expansion walk rests on, checked on both servers.
//
// Expanding a group means reading each member to decide whether it is itself a
// group and, if so, walking into it. Two of the three facts that rests on have
// already been wrong once, and both failed the same way: the walk stopped a
// level short while reporting success, which is the hardest kind of wrong to
// notice.
func TestGroupExpansionAssumptionsHold(t *testing.T) {
	// The two DN-valued membership styles the harness installs, each with a
	// group that nests another group through it.
	styles := []struct {
		class     string
		attribute string
		group     string
		nested    string
	}{
		{
			class: "groupOfNames", attribute: "member",
			group:  "cn=everyone,ou=groups," + suffix,
			nested: "cn=infrastructure,ou=groups," + suffix,
		},
		{
			class: "groupOfUniqueNames", attribute: "uniqueMember",
			group:  "cn=auditors,ou=groups," + suffix,
			nested: "cn=platform,ou=groups," + suffix,
		},
	}

	// What a member has to be read with. The membership attributes are the
	// load-bearing half: a nested group read without them looks like an empty
	// group, which is exactly what happened the first time this ran live.
	readWith := []string{"objectClass", "cn", "uid", "member", "uniqueMember", "memberUid", "memberURL"}
	membership := []string{"member", "uniqueMember", "memberUid", "memberURL"}

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}

		for _, tc := range styles {
			// 1. The class permits the membership attribute. This is how an
			//    entry is recognised as a group at all, so a server whose
			//    schema describes the class differently turns every nested
			//    group into a leaf member and the walk never descends.
			if !classPermitsAttr(sch, tc.class, tc.attribute) {
				t.Errorf("%s: %s does not permit %s, so a nested group held that way is walked past as an ordinary member",
					s.name, tc.class, tc.attribute)
				continue
			}

			groupDN, parseErr := dn.Parse(tc.group)
			if parseErr != nil {
				t.Fatal(parseErr)
			}

			// 2. Reading the group with the membership attributes named
			//    returns them. The walk asks for an explicit attribute list
			//    rather than "*", and a server that withheld them here would
			//    report every group as empty.
			entry, readErr := sess.Read(ctx(t), groupDN, readWith)
			if readErr != nil {
				t.Errorf("%s: reading %s: %v", s.name, tc.group, readErr)
				continue
			}
			members := entry.Get(tc.attribute)
			if len(members) == 0 {
				t.Errorf("%s: %s came back with no %s when it was named in the attribute list",
					s.name, tc.group, tc.attribute)
				continue
			}

			// 3. The nested member is reachable and is itself recognised as a
			//    group, which is what makes the walk descend a second level.
			var nestedFound bool
			for _, raw := range members {
				if strings.EqualFold(string(raw), tc.nested) {
					nestedFound = true
				}
			}
			if !nestedFound {
				t.Errorf("%s: %s does not hold %s among its %d %s values",
					s.name, tc.group, tc.nested, len(members), tc.attribute)
				continue
			}

			nestedDN, parseErr := dn.Parse(tc.nested)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			nested, readErr := sess.Read(ctx(t), nestedDN, readWith)
			if readErr != nil {
				t.Errorf("%s: reading the nested group %s: %v", s.name, tc.nested, readErr)
				continue
			}

			var isNestedGroup bool
			for _, raw := range nested.Get("objectClass") {
				for _, attr := range membership {
					if classPermitsAttr(sch, string(raw), attr) {
						isNestedGroup = true
					}
				}
			}
			if !isNestedGroup {
				t.Errorf("%s: %s is not recognised as a group from its object classes %q, so the walk stops there",
					s.name, tc.nested, nested.Get("objectClass"))
			}
			if len(nested.Get("member")) == 0 && len(nested.Get("uniqueMember")) == 0 {
				t.Errorf("%s: the nested group %s came back with no members at all", s.name, tc.nested)
			}
		}
	})
}

// The assumptions entry comparison rests on, checked on both servers.
//
// Comparing two entries is mostly byte comparison, with one deliberate
// exception and one deliberate refusal. Both are decided from the schema, so
// both are worth checking against a server rather than against a fixture.
func TestCompareAssumptionsHold(t *testing.T) {
	left := "uid=user0001,ou=people," + suffix
	right := "uid=user0002,ou=people," + suffix

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}

		// 1. The DN-valued syntaxes. A DN-valued attribute is compared as DNs
		//    rather than as bytes, so that two spellings of one DN are not
		//    reported as a difference. That branch is chosen from the syntax
		//    OID alone, and a server publishing a different one degrades the
		//    comparison to bytes without saying so.
		for _, tc := range []struct{ attribute, syntax, label string }{
			{"member", "1.3.6.1.4.1.1466.115.121.1.12", "DN"},
			{"uniqueMember", "1.3.6.1.4.1.1466.115.121.1.34", "Name and Optional UID"},
		} {
			at := sch.AttributeType(tc.attribute)
			if at == nil {
				t.Errorf("%s does not define %s", s.name, tc.attribute)
				continue
			}
			if got := sch.EffectiveSyntax(at); got != tc.syntax {
				t.Errorf("%s: %s has syntax %s, want %s (%s) -- DN-aware comparison silently becomes byte comparison",
					s.name, tc.attribute, got, tc.syntax, tc.label)
			}
		}

		// 2. userPassword is sensitive on both. That is what makes the
		//    comparison withhold it rather than report whether two entries
		//    hold the same hash, which would be an oracle about password
		//    material the product offers nowhere else.
		if !sch.KindOf("userPassword").Sensitive {
			t.Errorf("%s: userPassword is not marked sensitive, so a comparison would print it", s.name)
		}

		// 3. Both entries read with the same attribute list, and both hold a
		//    password, which is the case the withholding has to cover.
		for _, target := range []string{left, right} {
			parsed, parseErr := dn.Parse(target)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			entry, readErr := sess.Read(ctx(t), parsed, []string{"*", "userPassword"})
			if readErr != nil {
				t.Errorf("%s: reading %s: %v", s.name, target, readErr)
				continue
			}
			if len(entry.Get("userPassword")) == 0 {
				t.Errorf("%s: %s holds no userPassword, so the withholding case is not exercised here",
					s.name, target)
			}
		}

		// 4. Operational attributes are readable, and the suite records which
		//    ones each server actually publishes. This is the documented
		//    divergence rather than an assertion of sameness: the comparison
		//    reads whatever the server names, and the two vendors genuinely
		//    disagree here -- entryUUID and entryCSN against nsUniqueId.
		parsed, parseErr := dn.Parse(left)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		operational, readErr := sess.Read(ctx(t), parsed, []string{"+"})
		if readErr != nil {
			t.Fatalf("%s: reading operational attributes: %v", s.name, readErr)
		}
		var names []string
		for name := range operational.Attributes {
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			t.Errorf("%s: no operational attributes came back for %s", s.name, left)
		}
		t.Logf("%s publishes %d operational attributes: %s", s.name, len(names), strings.Join(names, ", "))
	})
}

// The assumptions the value tally rests on, checked on both servers.
//
// The tally answers "what values does this attribute hold, and how many entries
// carry each" over a paged subtree search. Both servers are seeded from
// byte-identical LDIF, so both must produce identical counts -- which is what
// makes the feature's answer a fact about the directory rather than about the
// vendor.
func TestInventoryAssumptionsHold(t *testing.T) {
	const (
		attribute        = "alderTeam"
		wantExamined     = 320
		wantWithValue    = 302
		wantDistinct     = 6
		wantLargestValue = "platform"
		wantLargestCount = 52
	)

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}

		// 1. The attribute is inventoriable on both: known, not sensitive, not
		//    binary. Each of those refusals is decided from the schema.
		kind := sch.KindOf(attribute)
		if !kind.Known {
			t.Errorf("%s does not define %s, so the tally has nothing to canonicalise", s.name, attribute)
		}
		if kind.Sensitive {
			t.Errorf("%s: %s is marked sensitive and would be refused", s.name, attribute)
		}
		switch kind.Kind {
		case schema.KindBinary, schema.KindImage, schema.KindCertificate:
			t.Errorf("%s: %s is %v and would be refused as binary", s.name, attribute, kind.Kind)
		}

		// 2. userPassword is refused before the search runs. That refusal is
		//    the difference between never reading a password and reading every
		//    password and choosing not to print it.
		if !sch.KindOf("userPassword").Sensitive {
			t.Errorf("%s: userPassword is not sensitive, so an inventory of it would be a list of secrets", s.name)
		}

		// 3. The counts themselves agree across vendors.
		base, parseErr := dn.Parse(suffix)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		res, searchErr := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN:     base,
			Scope:      directory.ScopeSubtree,
			Filter:     filter.Present("objectClass"),
			Attributes: []string{attribute},
			Limit:      1000,
			PageSize:   100,
		})
		if searchErr != nil {
			t.Fatalf("%s: searching the subtree: %v", s.name, searchErr)
		}
		if res.Truncated {
			t.Fatalf("%s: the subtree search truncated, so these counts are not the whole answer", s.name)
		}

		// The tally, as the feature computes it: an entry counts once per
		// distinct value it holds, and holding one value twice is still one
		// entry.
		perValue := map[string]int{}
		withValue := 0
		for _, e := range res.Entries {
			values := e.Get(attribute)
			if len(values) == 0 {
				continue
			}
			withValue++
			distinct := map[string]bool{}
			for _, raw := range values {
				distinct[string(raw)] = true
			}
			for v := range distinct {
				perValue[v]++
			}
		}

		if len(res.Entries) != wantExamined {
			t.Errorf("%s examined %d entries, want %d", s.name, len(res.Entries), wantExamined)
		}
		if withValue != wantWithValue {
			t.Errorf("%s: %d entries hold %s, want %d", s.name, withValue, attribute, wantWithValue)
		}
		if len(perValue) != wantDistinct {
			t.Errorf("%s: %d distinct values of %s, want %d (got %v)",
				s.name, len(perValue), attribute, wantDistinct, perValue)
		}
		if got := perValue[wantLargestValue]; got != wantLargestCount {
			t.Errorf("%s: %q is held by %d entries, want %d",
				s.name, wantLargestValue, got, wantLargestCount)
		}
		t.Logf("%s: %d/%d entries hold %s across %d distinct values",
			s.name, withValue, len(res.Entries), attribute, len(perValue))
	})
}

// --- the delegated bind -----------------------------------------------------
//
// Everything above this point binds as the directory's own administrator, which
// on OpenLDAP is the rootdn and bypasses access control entirely. That made the
// whole suite a test of what Alder does when it is allowed to do everything --
// which is not how anybody runs it, and it meant the harness's own userPassword
// rule had never been applied to anything.
//
// These bind as cn=svc-alder instead: an account that administers people and
// groups and cannot see ou=services at all. The rules live in
// openldap/slapd.conf and ds389/access.ldif because that is the one thing the
// two servers genuinely express differently; that the account sees the same
// directory through them does not differ, and these assert it.

// hiddenEntries are the DNs the delegated account cannot see. The subtree holds
// three entries, one of which is there because its DN is not ASCII -- and which
// a grep for "^dn:.*ou=services" misses, because LDIF base64-encodes a
// non-ASCII DN. It is in the hidden subtree by luck rather than design, and it
// is a better test for it.
func hiddenEntries() []string {
	return []string{
		"ou=services," + suffix,
		"cn=svc-alder,ou=services," + suffix,
		"ou=Zweigstelle München,ou=services," + suffix,
	}
}

// dnSetUnder returns every DN the session can see below the suffix.
func dnSetUnder(t *testing.T, sess directory.Session) map[string]bool {
	t.Helper()
	base, err := dn.Parse(suffix)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sess.Search(ctx(t), directory.SearchRequest{
		BaseDN:     base,
		Scope:      directory.ScopeSubtree,
		Filter:     filter.Present("objectClass"),
		Attributes: []string{"1.1"},
		Limit:      1000,
		PageSize:   100,
	})
	if err != nil {
		t.Fatalf("searching the suffix: %v", err)
	}
	if res.Truncated {
		t.Fatal("the search truncated, so this is not the whole picture")
	}
	out := map[string]bool{}
	for _, e := range res.Entries {
		out[strings.ToLower(e.DN.String())] = true
	}
	return out
}

// A search under a delegated bind returns less, and returns it without
// complaining. Both servers hide exactly the same entries.
//
// This is the shape of the risk the rest of the product has to survive: nothing
// errors, nothing is flagged, the directory simply appears smaller. A feature
// that reports what it found without knowing this reports a smaller directory
// as a fact.
func TestADelegatedBindSeesLessAndSaysNothing(t *testing.T) {
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			asAdmin := dnSetUnder(t, connect(t, s, false))
			asDelegate := dnSetUnder(t, connectRestricted(t, s))

			if len(asDelegate) >= len(asAdmin) {
				t.Fatalf("the delegated account sees %d entries and the administrator %d; "+
					"the access rules are not in force", len(asDelegate), len(asAdmin))
			}

			for _, want := range hiddenEntries() {
				if asDelegate[strings.ToLower(want)] {
					t.Errorf("%s is visible to the delegated account", want)
				}
				if !asAdmin[strings.ToLower(want)] {
					t.Errorf("%s is not visible to the administrator either, so this "+
						"test proves nothing about access control", want)
				}
			}

			// And nothing else went missing: the difference is exactly the
			// hidden subtree, on both servers.
			if diff := len(asAdmin) - len(asDelegate); diff != len(hiddenEntries()) {
				t.Errorf("%d entries are hidden, want exactly the %d in the subtree",
					diff, len(hiddenEntries()))
			}
		})
	}
}

// An entry the bind cannot read is reported as absent, not as forbidden.
//
// This is the LDAP convention and both servers follow it: answering "you may
// not see this" would disclose that it exists. It is worth pinning because it
// means a 404 from Alder has two causes that cannot be told apart, and any
// message that says "no such entry" is asserting something the server did not.
func TestAnUnreadableEntryIsReportedAsAbsent(t *testing.T) {
	eachServerRestricted(t, func(t *testing.T, s server, sess directory.Session) {
		target, err := dn.Parse(s.hiddenDN)
		if err != nil {
			t.Fatal(err)
		}
		_, err = sess.Read(ctx(t), target, nil)
		if err == nil {
			t.Fatal("reading a subtree the account cannot see succeeded")
		}
		var le *ldapdriver.Error
		if !errors.As(err, &le) {
			t.Fatalf("error = %v (%T), want an *ldapdriver.Error", err, err)
		}
		if le.IsInsufficientAccess() {
			t.Errorf("the server disclosed that %s exists by refusing access to it", s.hiddenDN)
		}
		if !le.IsNoSuchObject() {
			t.Errorf("error = %v, want no such object", le)
		}
	})
}

// The rule the harness has carried since M0, applied for the first time.
//
// cn=admin is the rootdn and bypasses it, so until there was a bind subject to
// access control this asserted nothing. A password must not reach a client that
// has no right to it, whatever Alder would otherwise do with the value.
func TestADelegatedBindCannotReadPasswords(t *testing.T) {
	target, err := dn.Parse("uid=user0001,ou=people," + suffix)
	if err != nil {
		t.Fatal(err)
	}
	eachServerRestricted(t, func(t *testing.T, s server, sess directory.Session) {
		e, readErr := sess.Read(ctx(t), target, []string{"*", "userPassword"})
		if readErr != nil {
			t.Fatalf("reading an entry the account may read: %v", readErr)
		}
		if got := e.Get("userPassword"); len(got) != 0 {
			t.Errorf("userPassword came back with %d values under a bind with no right to it",
				len(got))
		}
		// The entry itself is readable, which is the point: the attribute is
		// missing rather than the entry.
		if len(e.Get("cn")) == 0 {
			t.Error("the entry came back empty, so this says nothing about the password")
		}
	})
}

// A write the directory refuses comes back as insufficient access, so the API
// can answer 403 rather than reporting a mysterious failure.
func TestADelegatedBindIsRefusedAWrite(t *testing.T) {
	target, err := dn.Parse("uid=user0001,ou=people," + suffix)
	if err != nil {
		t.Fatal(err)
	}
	eachServerRestricted(t, func(t *testing.T, s server, sess directory.Session) {
		change := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: "description", Values: bs("written by an account with no right to")},
			},
		}
		applyErr := sess.Apply(ctx(t), change)
		if applyErr == nil {
			t.Fatal("the delegated account wrote to an entry it may only read")
		}
		var le *ldapdriver.Error
		if !errors.As(applyErr, &le) {
			t.Fatalf("error = %v (%T), want an *ldapdriver.Error", applyErr, applyErr)
		}
		if !le.IsInsufficientAccess() {
			t.Errorf("error = %v, want insufficient access so the API can answer 403", le)
		}
	})
}

// The schema is readable without privilege, which everything else depends on.
//
// The editor, the comparison and the tally are all schema-driven, so a session
// that cannot read the schema is not a degraded Alder but a broken one. Both
// servers publish it to an ordinary bound account.
func TestTheSchemaIsReadableByADelegatedBind(t *testing.T) {
	eachServerRestricted(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("the delegated account cannot read the schema: %v", err)
		}
		if len(sch.AttributeTypes) == 0 || len(sch.ObjectClasses) == 0 {
			t.Fatalf("the schema came back empty: %d attribute types, %d classes",
				len(sch.AttributeTypes), len(sch.ObjectClasses))
		}
		// And the harness's own class is there, so it is the real schema and
		// not some minimal subset offered to the unprivileged.
		if sch.ObjectClass("alderEmployee") == nil {
			t.Error("alderEmployee is missing, so this is not the schema the administrator sees")
		}
	})
}

// An attribute withheld by access control is indistinguishable, on the wire,
// from one the entry does not have.
//
// Nothing in the protocol marks the difference: the entry simply comes back
// with fewer attributes. Every feature that compares two entries, or reports
// what an entry holds, is therefore making a claim it cannot support -- and
// this is the fact that makes that true, pinned on both servers so it cannot be
// forgotten while the features above it are written.
func TestAWithheldAttributeIsIndistinguishableFromAnAbsentOne(t *testing.T) {
	target, err := dn.Parse("uid=user0001,ou=people," + suffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			asAdmin, err := connect(t, s, false).Read(ctx(t), target, []string{"*", "userPassword"})
			if err != nil {
				t.Fatal(err)
			}
			asDelegate, err := connectRestricted(t, s).Read(ctx(t), target, []string{"*", "userPassword"})
			if err != nil {
				t.Fatal(err)
			}

			if len(asAdmin.Get("userPassword")) == 0 {
				t.Fatal("the administrator cannot see the password either")
			}
			if len(asDelegate.Get("userPassword")) != 0 {
				t.Fatal("the delegated account can see the password")
			}

			// The same read, the same entry, a different set of attributes,
			// and no error, no control and no flag to say why.
			if len(asDelegate.Order) >= len(asAdmin.Order) {
				t.Errorf("the delegated read returned %d attributes and the privileged one %d",
					len(asDelegate.Order), len(asAdmin.Order))
			}
		})
	}
}

// The server can tell absent from denied, and both target servers do it the
// same way.
//
// This is what makes the difference recoverable at all. A search omits a
// forbidden attribute exactly as it omits one the entry does not hold, so
// without this any feature reporting "only on the left" would be stating
// something it cannot know. Compare answers "no such attribute" for the first
// and "insufficient access" for the second, and Session.VisibilityOf is that
// question.
func TestVisibilityDistinguishesAbsentFromDenied(t *testing.T) {
	target, err := dn.Parse("uid=user0001,ou=people," + suffix)
	if err != nil {
		t.Fatal(err)
	}

	eachServerRestricted(t, func(t *testing.T, s server, sess directory.Session) {
		for _, tc := range []struct {
			attribute string
			want      directory.AttributeVisibility
			why       string
		}{
			{"sn", directory.VisibilityPresent, "held by the entry and readable by this bind"},
			{"telephoneNumber", directory.VisibilityAbsent, "the entry does not hold it"},
			{"userPassword", directory.VisibilityDenied, "the access rules forbid this bind from looking"},
		} {
			t.Run(tc.attribute, func(t *testing.T) {
				got, visErr := sess.VisibilityOf(ctx(t), target, tc.attribute)
				if visErr != nil {
					t.Fatalf("VisibilityOf(%s): %v", tc.attribute, visErr)
				}
				if got != tc.want {
					t.Errorf("%s is %v, want %v -- %s", tc.attribute, got, tc.want, tc.why)
				}
			})
		}
	})
}

// The privileged bind sees the password, so the same probe answers Present.
// Without this the case above would pass on a server that answered Denied to
// everybody.
func TestVisibilityFollowsTheBindNotTheAttribute(t *testing.T) {
	target, err := dn.Parse("uid=user0001,ou=people," + suffix)
	if err != nil {
		t.Fatal(err)
	}
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		got, visErr := sess.VisibilityOf(ctx(t), target, "userPassword")
		if visErr != nil {
			t.Fatalf("VisibilityOf: %v", visErr)
		}
		if got != directory.VisibilityPresent {
			t.Errorf("userPassword is %v to the administrator, want present", got)
		}
	})
}

// The fixture the comparison's one-sided resolution rests on: one attribute,
// readable on one entry and forbidden on its neighbour.
//
// Without a case shaped like this the resolution could only be tested against a
// fake, which would prove the code branches and nothing about a directory.
func TestAnAttributeCanBeDeniedOnOneEntryAndReadableOnAnother(t *testing.T) {
	readable, err := dn.Parse("uid=user0001,ou=people," + suffix)
	if err != nil {
		t.Fatal(err)
	}
	denied, err := dn.Parse("uid=user0002,ou=people," + suffix)
	if err != nil {
		t.Fatal(err)
	}

	eachServerRestricted(t, func(t *testing.T, s server, sess directory.Session) {
		got, visErr := sess.VisibilityOf(ctx(t), readable, "alderTeam")
		if visErr != nil {
			t.Fatalf("VisibilityOf on the readable entry: %v", visErr)
		}
		if got != directory.VisibilityPresent {
			t.Errorf("alderTeam on %s is %v, want present", readable, got)
		}

		got, visErr = sess.VisibilityOf(ctx(t), denied, "alderTeam")
		if visErr != nil {
			t.Fatalf("VisibilityOf on the restricted entry: %v", visErr)
		}
		if got != directory.VisibilityDenied {
			t.Errorf("alderTeam on %s is %v, want denied", denied, got)
		}

		// And a plain read shows nothing of the difference, which is the whole
		// reason the probe exists.
		e, readErr := sess.Read(ctx(t), denied, []string{"*"})
		if readErr != nil {
			t.Fatalf("reading the restricted entry: %v", readErr)
		}
		if len(e.Get("alderTeam")) != 0 {
			t.Error("the read returned alderTeam, so the rule is not in force")
		}
		if len(e.Get("cn")) == 0 {
			t.Error("the read returned nothing at all, so this says nothing about one attribute")
		}
	})
}

// A member the bind cannot read is indistinguishable from one that was deleted.
//
// The group expansion reports both in the same list, and this is why it must
// not call that list broken references: cn=svc-alder is a member of cn=auditors
// and exists, and a delegated bind is told exactly what it would be told about
// an entry somebody had removed. Someone auditing who can reach a system would
// otherwise read "deleted" about a member who is still in the group.
func TestAnUnreadableMemberLooksExactlyLikeADeletedOne(t *testing.T) {
	group, err := dn.Parse("cn=auditors,ou=groups," + suffix)
	if err != nil {
		t.Fatal(err)
	}
	member, err := dn.Parse("cn=svc-alder,ou=services," + suffix)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := dn.Parse("cn=never-existed,ou=services," + suffix)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			// The member is named by the group, whoever is asking.
			asAdmin := connect(t, s, false)
			g, readErr := asAdmin.Read(ctx(t), group, []string{"uniqueMember"})
			if readErr != nil {
				t.Fatalf("reading the group: %v", readErr)
			}
			var named bool
			for _, v := range g.Get("uniqueMember") {
				if strings.EqualFold(string(v), member.String()) {
					named = true
				}
			}
			if !named {
				t.Fatalf("%s does not name %s, so this case is not set up", group, member)
			}
			// And it exists.
			if _, readErr = asAdmin.Read(ctx(t), member, nil); readErr != nil {
				t.Fatalf("the member does not exist: %v", readErr)
			}

			// To the delegated bind, reading it fails the same way as reading
			// an entry that was never there. Same error, same code, nothing to
			// tell a walk which it is looking at.
			asDelegate := connectRestricted(t, s)
			_, hiddenErr := asDelegate.Read(ctx(t), member, nil)
			_, missingErr := asDelegate.Read(ctx(t), missing, nil)
			if hiddenErr == nil {
				t.Fatal("the delegated bind can read the member after all")
			}
			if missingErr == nil {
				t.Fatal("an entry that does not exist was read")
			}

			var hidden, gone *ldapdriver.Error
			if !errors.As(hiddenErr, &hidden) || !errors.As(missingErr, &gone) {
				t.Fatalf("errors are %v and %v", hiddenErr, missingErr)
			}
			if hidden.Code != gone.Code {
				t.Errorf("a hidden member answers %d and a deleted one %d; if these differ, "+
					"the expansion could tell them apart and should", hidden.Code, gone.Code)
			}
		})
	}
}

// Where a server counts a container's children, that count is not filtered by
// the access rules — so the gap between it and what a bind can enumerate is
// exactly what the bind may not see.
//
// This is the one hidden thing in the whole product that is directly countable.
// Whether a server publishes numSubordinates is a capability and not a vendor
// trait: it is read from the entry here, as the code does, rather than decided
// from which server answered.
func TestNumSubordinatesCountsWhatABindCannotSee(t *testing.T) {
	base, err := dn.Parse(suffix)
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			admin := connect(t, s, false)
			asAdmin, readErr := admin.Read(ctx(t), base, []string{"numSubordinates"})
			if readErr != nil {
				t.Fatalf("reading the suffix: %v", readErr)
			}
			published := asAdmin.GetOne("numSubordinates")
			if published == "" {
				t.Skipf("%s does not publish numSubordinates, so a hidden child "+
					"cannot be counted here and the tree says nothing rather than guessing", s.name)
			}

			delegate := connectRestricted(t, s)
			asDelegate, readErr := delegate.Read(ctx(t), base, []string{"numSubordinates"})
			if readErr != nil {
				t.Fatalf("reading the suffix as the delegated account: %v", readErr)
			}

			// The number is the same for both, which is what makes it useful:
			// a count the access rules filtered would just be the visible one
			// again and could reveal nothing.
			if got := asDelegate.GetOne("numSubordinates"); got != published {
				t.Errorf("the delegated bind is told %q children and the administrator %q; "+
					"a filtered count cannot reveal a hidden child", got, published)
			}

			total, convErr := strconv.Atoi(published)
			if convErr != nil {
				t.Fatalf("numSubordinates is %q, which is not a number", published)
			}

			res, searchErr := delegate.Search(ctx(t), directory.SearchRequest{
				BaseDN:     base,
				Scope:      directory.ScopeOneLevel,
				Filter:     filter.Present("objectClass"),
				Attributes: []string{"1.1"},
				Limit:      100,
				PageSize:   100,
			})
			if searchErr != nil {
				t.Fatalf("enumerating children: %v", searchErr)
			}
			if res.Truncated {
				t.Fatal("the enumeration truncated, so any gap would be paging rather than access")
			}
			if len(res.Entries) >= total {
				t.Errorf("the delegated bind enumerated %d of %d children, so the "+
					"ou=services rule is not in force", len(res.Entries), total)
			}
			t.Logf("%s: the server counts %d children, this bind can see %d",
				s.name, total, len(res.Entries))
		})
	}
}

// A subtree search returns parents before their children, on both servers.
//
// The LDIF export streams entries in the order the server gave them, so this is
// the assumption that makes an export re-importable: applied in order, an entry
// whose parent has not been created yet is refused. The export has always
// relied on it -- it never sorted -- and until now nothing said so.
//
// If a server ever stopped doing this the export would have to sort, which
// would mean holding the whole subtree again. This is where that would be found
// out.
func TestASubtreeSearchReturnsParentsFirst(t *testing.T) {
	base, err := dn.Parse(suffix)
	if err != nil {
		t.Fatal(err)
	}

	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		res, searchErr := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN:     base,
			Scope:      directory.ScopeSubtree,
			Filter:     filter.Present("objectClass"),
			Attributes: []string{"1.1"},
			Limit:      1000,
			PageSize:   100,
		})
		if searchErr != nil {
			t.Fatalf("searching the suffix: %v", searchErr)
		}
		if res.Truncated {
			t.Fatal("the search truncated, so the order of the rest is unknown")
		}

		seen := map[string]bool{}
		for _, e := range res.Entries {
			// Parent(): the DN package splits at the first unescaped comma, so
			// cn=Liddell\, Alice is one component rather than two. Doing this
			// by hand is how the harness's own edge cases get missed.
			parent := e.DN.Parent()
			p := foldDNString(parent.String())
			// A parent above the search base is not in this result set and
			// never could be -- the base entry's own parent, for one -- so it
			// says nothing about ordering. Marking the entry seen happens
			// either way: skipping that is how the base entry went missing
			// from the map and every one of its children looked out of order.
			inSubtree := len(parent) > 0 && strings.HasSuffix(p, foldDNString(suffix))
			if inSubtree && !seen[p] {
				// The parent is inside the subtree and has not been seen, so a
				// document applied in this order would try to create a child
				// under an entry that does not exist yet.
				t.Errorf("%s comes before its parent %s", e.DN, parent)
			}
			seen[foldDNString(e.DN.String())] = true
		}
		t.Logf("%s returned %d entries, every parent before its children", s.name, len(res.Entries))
	})
}

// foldDNString lowercases a DN for comparison. The suite compares DNs as
// strings only here, where the question is ordering rather than equality.
func foldDNString(s string) string { return strings.ToLower(s) }

// --- operational attributes an administrator is meant to set ----------------

// Operational is not read-only, and the schema says which is which.
//
// Reported by an operator testing Alder: "a lot of operational attributes don't
// show, they should be discovered and easy to set (for example nsAccountLock)".
// The cause was treating USAGE as though it meant ownership. It does not.
// NO-USER-MODIFICATION means the directory owns an attribute; a directoryOperation
// attribute without it is one the server keeps and still expects an
// administrator to set.
func TestTheSchemaSaysWhichOperationalAttributesAreSettable(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		sch, err := sess.Schema(ctx(t))
		if err != nil {
			t.Fatalf("Schema: %v", err)
		}

		settable := sch.SettableOperational()
		if len(settable) == 0 {
			t.Fatalf("%s declares no operational attribute a client may set, so the "+
				"editor has nothing to offer and an account cannot be locked", s.name)
		}

		// The one this server actually uses for locking is among them.
		if !slices.Contains(settable, s.lockAttr) {
			t.Errorf("%s does not offer %s: %v", s.name, s.lockAttr, settable)
		}

		// And the rule held for every one of them.
		for _, name := range settable {
			at := sch.AttributeType(name)
			if at == nil {
				t.Errorf("%s: %s is offered but not defined", s.name, name)
				continue
			}
			if sch.EffectiveNoUserModification(at) {
				t.Errorf("%s: %s is NO-USER-MODIFICATION and the server will refuse it", s.name, name)
			}
			if got := sch.EffectiveUsage(at); got != schema.UsageDirectoryOperation {
				t.Errorf("%s: %s has usage %v; only directoryOperation is about an entry",
					s.name, name, got)
			}
		}
		t.Logf("%s offers %d settable operational attributes, including %s",
			s.name, len(settable), s.lockAttr)
	})
}

// And the server accepts one, which is the claim that matters.
//
// A schema that says an attribute is settable and a server that refuses it
// would make the offer a lie, and the operator would find out only when their
// change failed.
func TestAnOperationalAttributeTheSchemaOffersIsAccepted(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		base := writeBase(t, sess, "conf-operational-"+s.name)
		target, err := base.ChildAttr("cn", "lockable")
		if err != nil {
			t.Fatal(err)
		}

		create := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeAdd,
			Attrs: []directory.Attribute{
				{Name: "objectClass", Values: bs("top", "person")},
				{Name: "cn", Values: bs("lockable")},
				{Name: "sn", Values: bs("Lockable")},
			},
		}
		if err := sess.Apply(ctx(t), create); err != nil {
			t.Fatalf("creating the subject: %v", err)
		}

		// Not present until set, which is the other half of what was reported:
		// nothing on the entry hints that it could be locked.
		before, err := sess.Read(ctx(t), target, []string{"*", "+"})
		if err != nil {
			t.Fatalf("reading the subject: %v", err)
		}
		if len(before.Get(s.lockAttr)) != 0 {
			t.Errorf("%s is already present before anything set it", s.lockAttr)
		}

		lock := directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{
				{Op: directory.ModReplace, Name: s.lockAttr, Values: bs(s.lockValue)},
			},
		}
		if err := sess.Apply(ctx(t), lock); err != nil {
			t.Fatalf("the server refused %s, which its own schema says is settable: %v",
				s.lockAttr, err)
		}

		after, err := sess.Read(ctx(t), target, []string{"*", "+"})
		if err != nil {
			t.Fatalf("reading the subject after locking: %v", err)
		}
		if got := after.Get(s.lockAttr); len(got) == 0 {
			t.Errorf("%s did not come back after being set", s.lockAttr)
		}
	})
}
