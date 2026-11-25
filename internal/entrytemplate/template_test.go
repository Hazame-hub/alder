package entrytemplate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// posixSchema is OpenLDAP's shape: posixGroup STRUCTURAL. With structural
// false it is 389 Directory Server's: posixGroup AUXILIARY.
func posixSchema(t *testing.T, posixGroupStructural bool, memberRequired bool) *schema.Schema {
	t.Helper()
	groupKind := "AUXILIARY"
	if posixGroupStructural {
		groupKind = "STRUCTURAL"
	}
	gon := "( 2.5.6.9 NAME 'groupOfNames' SUP top STRUCTURAL MUST cn MAY ( member $ description ) )"
	if memberRequired {
		gon = "( 2.5.6.9 NAME 'groupOfNames' SUP top STRUCTURAL MUST ( member $ cn ) MAY description )"
	}
	classes := []string{
		"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
		"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) MAY description )",
		"( 2.5.6.7 NAME 'organizationalPerson' SUP person STRUCTURAL )",
		"( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP organizationalPerson STRUCTURAL MAY ( uid $ mail $ givenName ) )",
		"( 1.3.6.1.1.1.2.0 NAME 'posixAccount' SUP top AUXILIARY MUST ( cn $ uid $ uidNumber $ gidNumber $ homeDirectory ) MAY ( loginShell $ description ) )",
		"( 1.3.6.1.1.1.2.2 NAME 'posixGroup' SUP top " + groupKind + " MUST ( cn $ gidNumber ) MAY ( memberUid $ description ) )",
		gon,
	}
	attrs := []string{
		"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
		"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.4 NAME 'sn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.13 NAME 'description' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.31 NAME 'member' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
		"( 2.5.4.42 NAME 'givenName' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 0.9.2342.19200300.100.1.1 NAME 'uid' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 0.9.2342.19200300.100.1.3 NAME 'mail' SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
		"( 1.3.6.1.1.1.1.0 NAME 'uidNumber' SYNTAX 1.3.6.1.4.1.1466.115.121.1.27 )",
		"( 1.3.6.1.1.1.1.1 NAME 'gidNumber' SYNTAX 1.3.6.1.4.1.1466.115.121.1.27 )",
		"( 1.3.6.1.1.1.1.3 NAME 'homeDirectory' SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
		"( 1.3.6.1.1.1.1.4 NAME 'loginShell' SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
		"( 1.3.6.1.1.1.1.12 NAME 'memberUid' SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
	}
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses:  classes,
		schema.AttrAttributeTypes: attrs,
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("schema: %v", sch.Errors)
	}
	return sch
}

func builtin(t *testing.T, name string) *Template {
	t.Helper()
	for _, b := range Builtins() {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("no built-in %s", name)
	return nil
}

func TestEveryBuiltinIsValid(t *testing.T) {
	for _, b := range Builtins() {
		if err := b.Validate(); err != nil {
			t.Error(err)
		}
	}
}

// Each server sees the posix group variant it accepts, and only that one.
func TestTheBuiltinsFitTheServerTheyAreOfferedOn(t *testing.T) {
	cases := []struct {
		name      string
		sch       *schema.Schema
		usable    []string
		notUsable []string
	}{
		{"OpenLDAP", posixSchema(t, true, true),
			[]string{"posix-user", "posix-group", "group-of-names"}, []string{"posix-group-of-names"}},
		{"389 Directory Server", posixSchema(t, false, false),
			[]string{"posix-user", "posix-group-of-names", "group-of-names"}, []string{"posix-group"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range tc.usable {
				if p := builtin(t, n).Problems(tc.sch); len(p) > 0 {
					t.Errorf("%s: %v", n, p)
				}
			}
			for _, n := range tc.notUsable {
				if p := builtin(t, n).Problems(tc.sch); len(p) == 0 {
					t.Errorf("%s fits, and should not", n)
				}
			}
		})
	}
}

func TestRenderFillsFieldsAndDropsWhatIsBlank(t *testing.T) {
	r, err := builtin(t, "posix-user").Render(
		map[string]string{"uid": "jdoe", "sn": " Doe ", "gidNumber": "20001"},
		map[string]int64{"uidNumber": 10301})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range r.Attributes {
		got[a.Name] = strings.Join(a.Values, "|")
	}
	want := map[string]string{
		"objectClass":   "top|inetOrgPerson|posixAccount",
		"uid":           "jdoe",
		"sn":            "Doe",
		"cn":            "Doe",
		"gidNumber":     "20001",
		"uidNumber":     "10301",
		"homeDirectory": "/home/jdoe",
		"loginShell":    "/bin/bash",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, absent := range []string{"givenName", "mail"} {
		if _, ok := got[absent]; ok {
			t.Errorf("%s is set, though it was left blank", absent)
		}
	}
	if r.RDNType != "uid" || r.RDNValue != "jdoe" {
		t.Errorf("rdn = %s=%s", r.RDNType, r.RDNValue)
	}
	if r.Attributes[0].Name != "objectClass" {
		t.Errorf("objectClass is not first: %v", r.Attributes)
	}
}

func TestRenderRefusesUnusableInput(t *testing.T) {
	tpl := builtin(t, "posix-user")
	cases := map[string]map[string]string{
		"a required field blank":   {"uid": "jdoe", "gidNumber": "1"},
		"a number that is not":     {"uid": "jdoe", "sn": "Doe", "gidNumber": "twenty"},
		"a field it does not have": {"uid": "jdoe", "sn": "Doe", "gidNumber": "1", "userPassword": "x"},
		"two lines":                {"uid": "jdoe\nx", "sn": "Doe", "gidNumber": "1"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := tpl.Render(in, map[string]int64{"uidNumber": 10000})
			if !errors.Is(err, ErrInput) {
				t.Errorf("err = %v, want ErrInput", err)
			}
		})
	}
}

func TestValidateNamesWhatIsWrong(t *testing.T) {
	_, err := Parse([]byte(`
name: Bad Name
title: x
view: people
rdn: uid
objectClasses: [inetOrgPerson]
attributes:
  cn: ["{nope}"]
  uidNumber: ["1"]
allocate:
  uidNumber: {from: 10, to: 5}
`))
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"name", "view", "{nope}", "uidNumber is also under attributes", "from <= to", "rdn uid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q not in %v", want, err)
		}
	}
	if _, err := Parse([]byte("name: x\nattrs: {}\n")); err == nil || !strings.Contains(err.Error(), "attrs") {
		t.Errorf("a misspelt key: %v", err)
	}
}

func TestLoadReplacesABuiltinAndRefusesABadFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("user.template.yaml", `
name: posix-user
title: Staff member
view: users
parent: ou=people,dc=alder,dc=test
rdn: uid
objectClasses: [inetOrgPerson, posixAccount]
fields:
  - {name: uid, required: true}
  - {name: sn, required: true}
attributes:
  uid: ["{uid}"]
  sn: ["{sn}"]
  cn: ["{sn}"]
  gidNumber: ["5000"]
  homeDirectory: ["/srv/home/{uid}"]
allocate:
  uidNumber: {from: 50000, to: 59999}
`)
	write("notes.txt", "not a template")
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := set.Get("posix-user")
	if got == nil || got.Title != "Staff member" || got.Source == "built-in" {
		t.Fatalf("the file did not replace the built-in: %+v", got)
	}
	if set.Get("group-of-names") == nil {
		t.Error("the other built-ins are gone")
	}

	write("broken.template.yaml", "name: broken\n")
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "broken.template.yaml") {
		t.Errorf("a broken file: %v", err)
	}
}

// reader answers searches from a fixed list, one page at a time.
type reader struct {
	directory.Reader
	pages     [][]string
	truncated bool
	calls     int
}

func (r *reader) Search(_ context.Context, req directory.SearchRequest) (*directory.SearchResult, error) {
	page := r.pages[r.calls]
	r.calls++
	res := &directory.SearchResult{}
	for _, v := range page {
		e := directory.NewEntry(req.BaseDN)
		e.Set("uidNumber", [][]byte{[]byte(v)})
		res.Entries = append(res.Entries, e)
	}
	if r.calls < len(r.pages) {
		res.Cookie = []byte("more")
		res.Truncated = true
	} else {
		res.Truncated = r.truncated
	}
	return res, nil
}

func TestAllocationIsOneMoreThanTheHighestInRange(t *testing.T) {
	tpl := builtin(t, "posix-user")
	r := &reader{pages: [][]string{{"10001", "10300", "0", "65534"}, {"10042", "not a number"}}}
	got, err := tpl.AllocateNumbers(context.Background(), r, []string{"dc=alder,dc=test"})
	if err != nil {
		t.Fatal(err)
	}
	a := got["uidNumber"]
	if a.Value != 10301 || a.Highest != 10300 || a.Scanned != 6 || r.calls != 2 {
		t.Errorf("allocation = %+v after %d searches", a, r.calls)
	}

	empty := &reader{pages: [][]string{{"500"}}}
	got, err = tpl.AllocateNumbers(context.Background(), empty, []string{"dc=alder,dc=test"})
	if err != nil || got["uidNumber"].Value != builtinIDFrom {
		t.Errorf("nothing in range: %+v, %v", got, err)
	}
}

func TestAllocationRefusesWhatItCannotKnow(t *testing.T) {
	tpl := builtin(t, "posix-user")
	cut := &reader{pages: [][]string{{"10001"}}, truncated: true}
	if _, err := tpl.AllocateNumbers(context.Background(), cut, []string{"dc=alder,dc=test"}); !errors.Is(err, ErrAllocation) {
		t.Errorf("a search the server stopped: %v", err)
	}
	full := &reader{pages: [][]string{{"60000"}}}
	if _, err := tpl.AllocateNumbers(context.Background(), full, []string{"dc=alder,dc=test"}); !errors.Is(err, ErrAllocation) {
		t.Errorf("a full range: %v", err)
	}
}
