package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/changepkg"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

var errNoSuchEntry = errors.New("no such entry")

func notFound(err error) bool { return errors.Is(err, errNoSuchEntry) }

// fakeTarget is a directory that can be read, and counts anything that would
// write. It implements the whole session surface a preflight could be handed,
// Apply included, so a test can prove Apply is never called.
type fakeTarget struct {
	caps    directory.Capabilities
	sch     *schema.Schema
	entries map[string]*directory.Entry
	// hidden holds entries that exist and that this bind may not read: a read
	// finds nothing, and a Compare admits they are there.
	hidden map[string]bool
	// concealed holds entries that exist and that the server denies exist,
	// answering exactly as for a missing one.
	concealed map[string]bool
	stored    map[string][]string
	reads     int
	writes    int
}

func (f *fakeTarget) Capabilities() directory.Capabilities { return f.caps }

func (f *fakeTarget) RefreshSchema(context.Context) (*schema.Schema, error) { return f.sch, nil }

func (f *fakeTarget) Schema(context.Context) (*schema.Schema, error) { return f.sch, nil }

func (f *fakeTarget) Read(_ context.Context, target dn.DN, _ []string) (*directory.Entry, error) {
	f.reads++
	key := strings.ToLower(target.String())
	if entry, ok := f.entries[key]; ok && !f.hidden[key] {
		return entry, nil
	}
	return nil, errNoSuchEntry
}

func (f *fakeTarget) SchemaDefinitions(_ context.Context, target string, kind directory.SchemaDefKind) ([]string, error) {
	return f.stored[strings.ToLower(target)+"|"+string(kind)], nil
}

// VisibilityOf answers as the two servers do: a hidden entry is "insufficient
// access", and a missing or concealed one is "no such object", which the
// driver reports as unknown with no error. Only cn is a safe probe: any other
// attribute answers with a syntax error, as OpenLDAP does for objectClass.
func (f *fakeTarget) VisibilityOf(_ context.Context, target dn.DN, attribute string) (directory.AttributeVisibility, error) {
	if !strings.EqualFold(attribute, "cn") {
		return directory.VisibilityUnknown, errors.New("invalid attribute syntax")
	}
	key := strings.ToLower(target.String())
	switch {
	case f.hidden[key]:
		return directory.VisibilityDenied, nil
	case f.entries[key] != nil:
		return directory.VisibilityPresent, nil
	}
	return directory.VisibilityUnknown, nil
}

func (f *fakeTarget) Search(context.Context, directory.SearchRequest) (*directory.SearchResult, error) {
	f.reads++
	return &directory.SearchResult{}, nil
}

func (f *fakeTarget) Apply(context.Context, directory.ChangeRecord) error {
	f.writes++
	return errors.New("a preflight must never write")
}

func (f *fakeTarget) Close() error { return nil }

const (
	syntaxString = "1.3.6.1.4.1.1466.115.121.1.15"
	syntaxOID    = "1.3.6.1.4.1.1466.115.121.1.38"
	syntaxOctets = "1.3.6.1.4.1.1466.115.121.1.40"
	syntaxTime   = "1.3.6.1.4.1.1466.115.121.1.24"
)

func targetSchema(t testing.TB, extraAT, extraOC []string) *schema.Schema {
	t.Helper()
	return schema.Load("cn=schema", map[string][]string{
		schema.AttrLDAPSyntaxes: {
			"( " + syntaxString + " DESC 'Directory String' )",
			"( " + syntaxDN + " DESC 'DN' )",
			"( " + syntaxOID + " DESC 'OID' )",
			"( " + syntaxOctets + " DESC 'Octet String' )",
			"( " + syntaxTime + " DESC 'Generalized Time' )",
		},
		schema.AttrMatchingRules: {
			"( 2.5.13.2 NAME 'caseIgnoreMatch' SYNTAX " + syntaxString + " )",
			"( 2.5.13.1 NAME 'distinguishedNameMatch' SYNTAX " + syntaxDN + " )",
			"( 2.5.13.4 NAME 'caseIgnoreSubstringsMatch' SYNTAX 1.3.6.1.4.1.1466.115.121.1.58 )",
		},
		schema.AttrAttributeTypes: append([]string{
			"( 2.5.4.0 NAME 'objectClass' SYNTAX " + syntaxOID + " )",
			"( 2.5.4.41 NAME 'name' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )",
			"( 2.5.4.3 NAME 'cn' SUP name )",
			"( 2.5.4.4 NAME 'sn' SUP name )",
			"( 2.5.4.11 NAME 'ou' SUP name )",
			"( 2.5.4.13 NAME 'description' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )",
			"( 0.9.2342.19200300.100.1.1 NAME 'uid' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " )",
			"( 2.5.4.31 NAME 'member' EQUALITY distinguishedNameMatch SYNTAX " + syntaxDN + " )",
			"( 0.9.2342.19200300.100.1.10 NAME 'manager' EQUALITY distinguishedNameMatch SYNTAX " + syntaxDN + " )",
			"( 2.5.4.35 NAME 'userPassword' SYNTAX " + syntaxOctets + " )",
			"( 1.3.6.1.4.1.4203.1.3.1 NAME 'entryUUID' SYNTAX " + syntaxString + " SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )",
			"( 2.5.18.1 NAME 'createTimestamp' SYNTAX " + syntaxTime + " SINGLE-VALUE NO-USER-MODIFICATION USAGE directoryOperation )",
			"( 1.3.6.1.4.1.99999.1.9 NAME 'alderEmployeeNumber' EQUALITY caseIgnoreMatch SYNTAX " + syntaxString + " SINGLE-VALUE )",
		}, extraAT...),
		schema.AttrObjectClasses: append([]string{
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.4 NAME 'organization' SUP top STRUCTURAL MUST o )",
			"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou MAY description )",
			"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( cn $ sn ) MAY ( userPassword $ description $ uid $ manager $ alderEmployeeNumber ) )",
			"( 2.5.6.9 NAME 'groupOfNames' SUP top STRUCTURAL MUST ( cn $ member ) MAY description )",
		}, extraOC...),
	})
}

func newTarget(t testing.TB, sch *schema.Schema, entries ...*directory.Entry) *fakeTarget {
	t.Helper()
	target := &fakeTarget{
		caps: directory.Capabilities{
			VendorName: "Target Directory", NamingContexts: []string{"dc=alder,dc=test"},
			SubschemaSubentry: "cn=schema", Paging: true,
			SchemaWrite: directory.SchemaWrite{
				Style:             directory.SchemaStyleSubschema,
				Targets:           []directory.SchemaTarget{{DN: "cn=schema", Name: "cn=schema"}},
				AttributeTypeAttr: "attributeTypes", ObjectClassAttr: "objectClasses",
			},
		},
		sch: sch, entries: map[string]*directory.Entry{}, hidden: map[string]bool{}, concealed: map[string]bool{},
		stored: map[string][]string{},
	}
	for _, e := range entries {
		target.entries[strings.ToLower(e.DN.String())] = e
	}
	return target
}

func mustDN(t testing.TB, s string) dn.DN {
	t.Helper()
	d, err := dn.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// entry builds a directory entry from name/value pairs; a name repeated adds a
// value.
func entry(t testing.TB, target string, pairs ...string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustDN(t, target))
	values := map[string][][]byte{}
	var order []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if _, seen := values[pairs[i]]; !seen {
			order = append(order, pairs[i])
		}
		values[pairs[i]] = append(values[pairs[i]], []byte(pairs[i+1]))
	}
	for _, name := range order {
		e.Set(name, values[name])
	}
	return e
}

// baseEntries are the suffix and the people container every fixture has.
func baseEntries(t testing.TB) []*directory.Entry {
	return []*directory.Entry{
		entry(t, "dc=alder,dc=test", "objectClass", "top", "objectClass", "dcObject", "dc", "alder"),
		entry(t, "ou=people,dc=alder,dc=test", "objectClass", "top", "objectClass", "organizationalUnit", "ou", "people"),
		entry(t, "ou=groups,dc=alder,dc=test", "objectClass", "top", "objectClass", "organizationalUnit", "ou", "groups"),
	}
}

func textValues(values ...string) []changepkg.Value {
	out := make([]changepkg.Value, 0, len(values))
	for _, v := range values {
		out = append(out, changepkg.Value{Text: v})
	}
	return out
}

func schemaChange(id, element, op, oid, definition string) changepkg.Item {
	return changepkg.Item{ID: id, Kind: changepkg.KindSchema, Destructive: op == changepkg.SchemaDelete,
		Schema: &changepkg.SchemaChange{Element: element, Op: op, OID: oid, Definition: definition}}
}

func addEntry(id, target string, attrs ...changepkg.Attribute) changepkg.Item {
	return changepkg.Item{ID: id, Kind: changepkg.KindData, Data: &changepkg.DataChange{DN: target, Type: changepkg.OpAdd, Attributes: attrs}}
}

func attr(name string, values ...string) changepkg.Attribute {
	return changepkg.Attribute{Name: name, Values: textValues(values...)}
}

func buildPackage(t testing.TB, items ...changepkg.Item) *changepkg.Package {
	t.Helper()
	p, err := changepkg.Build(&changepkg.Package{ID: "7f0c1a9e-2d4b-4c1e-9a55-0b7f3c2d1e00", Title: "fixture",
		Changes: changepkg.Derive(items)}, time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var fixedClock = func() time.Time { return time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC) }

func preflightPackage(t testing.TB, p *changepkg.Package, target *fakeTarget) *Report {
	t.Helper()
	r, err := Package(context.Background(), p, "verified", target, Options{NotFound: notFound, Now: fixedClock})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// find returns the findings with a code, optionally about one source object.
func find(r *Report, code string, match func(Finding) bool) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Code == code && (match == nil || match(f)) {
			out = append(out, f)
		}
	}
	return out
}

func byItem(id string) func(Finding) bool { return func(f Finding) bool { return f.Source.Item == id } }

func one(t testing.TB, r *Report, code string, match func(Finding) bool) Finding {
	t.Helper()
	got := find(r, code, match)
	if len(got) != 1 {
		t.Fatalf("want one %s finding, got %d\n%s", code, len(got), describe(r))
	}
	return got[0]
}

func byID(r *Report, id string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].ID == id {
			return &r.Findings[i]
		}
	}
	return nil
}

func describe(r *Report) string {
	var b strings.Builder
	b.WriteString("overall " + string(r.Overall) + " complete " + boolText(r.Complete) + " reasons " + strings.Join(r.Reasons, ",") + "\n")
	for _, f := range r.Findings {
		b.WriteString("  " + f.ID + " " + f.Code + " " + string(f.Classification) + " item=" + f.Source.Item + " oid=" + f.Source.OID +
			" dn=" + f.Source.DN + " attr=" + f.Source.Attribute + " value=" + f.Source.Value + " causes=" + strings.Join(f.Causes, ",") + "\n")
	}
	return b.String()
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
