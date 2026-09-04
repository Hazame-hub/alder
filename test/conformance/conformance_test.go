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
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
	},
	{
		name:   "389ds",
		host:   "localhost",
		port:   11636,
		bindDN: "cn=Directory Manager",
		bindPW: "alder-directory-manager",
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

func connect(t *testing.T, s server) directory.Session {
	t.Helper()
	// The driver logs at info; the suite discards it unless a test fails, and
	// a failing test's useful output is the assertion, not the connection log.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	drv := ldapdriver.New(logger, false)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := drv.Connect(ctx, directory.ConnConfig{
		Host:           s.host,
		Port:           s.port,
		TLS:            directory.TLSModeLDAPS,
		CACertificates: caPool(t),
		ServerName:     "localhost",
		BindDN:         s.bindDN,
		BindPassword:   s.bindPW,
	})
	if err != nil {
		t.Fatalf("connecting to %s at %s:%d: %v\nis the harness up? run \"task compose:up\"",
			s.name, s.host, s.port, err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// connectForSchema binds as the identity that may write schema on this server,
// which is the ordinary bind wherever the schema is data rather than
// configuration.
func connectForSchema(t *testing.T, s server) directory.Session {
	t.Helper()
	if s.schemaBindDN == "" {
		return connect(t, s)
	}
	alt := s
	alt.bindDN, alt.bindPW = s.schemaBindDN, s.schemaBindPW
	return connect(t, alt)
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

// eachServer runs fn against every server as a subtest.
func eachServer(t *testing.T, fn func(t *testing.T, s server, sess directory.Session)) {
	t.Helper()
	for _, s := range servers {
		t.Run(s.name, func(t *testing.T) {
			fn(t, s, connect(t, s))
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
		sess := connect(t, s)
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
		sess := connect(t, s)
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
		sess := connect(t, s)
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
