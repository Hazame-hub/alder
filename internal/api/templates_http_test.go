package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/entrytemplate"
)

const testTemplate = `
name: staff
title: Staff member
view: users
parent: ou=people,dc=alder,dc=test
rdn: uid
objectClasses: [inetOrgPerson, posixAccount]
fields:
  - {name: uid, label: User name, required: true}
  - {name: sn, required: true}
attributes:
  uid: ["{uid}"]
  sn: ["{sn}"]
  cn: ["{sn}"]
  gidNumber: ["5000"]
allocate:
  uidNumber: {from: 10000, to: 20000}
`

const testPosixAccount = "( 1.3.6.1.1.1.2.0 NAME 'posixAccount' SUP top AUXILIARY " +
	"MUST ( cn $ uid $ uidNumber $ gidNumber ) )"

func templateRig(t *testing.T, uidNumbers ...string) *testRig {
	t.Helper()
	tpl, err := entrytemplate.Parse([]byte(testTemplate))
	if err != nil {
		t.Fatal(err)
	}
	set, err := entrytemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	set = entrytemplate.NewSet(append(set.All(), tpl)...)
	fake := &fakeSession{caps: defaultCaps(), sch: testSchema(t, testPosixAccount)}
	for i, n := range uidNumbers {
		d, _ := dn.New("uid", "u"+string(rune('a'+i)), "dc", "alder", "dc", "test")
		e := directory.NewEntry(d)
		e.Set("uidNumber", [][]byte{[]byte(n)})
		fake.entries = append(fake.entries, e)
	}
	return newRig(t, Config{Templates: set}, fake)
}

func TestListTemplatesSaysWhichFitThisServer(t *testing.T) {
	rig := templateRig(t)
	res := rig.do(t, http.MethodGet, "/api/v1/templates", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	list := decode[TemplateList](t, res)
	byName := map[string]EntryTemplate{}
	for _, tpl := range list.Templates {
		byName[tpl.Name] = tpl
	}
	staff, ok := byName["staff"]
	if !ok || !staff.Usable || staff.Parent == nil || len(staff.Allocates) != 1 {
		t.Fatalf("staff = %+v", staff)
	}
	// The test schema has no homeDirectory, so the built-in user does not
	// fit, and a built-in that does not fit is not offered.
	if _, ok := byName["posix-user"]; ok {
		t.Errorf("posix-user is offered on a server it does not fit")
	}
}

func TestRenderTemplateAllocatesTheNextFreeNumber(t *testing.T) {
	rig := templateRig(t, "10007", "10011", "65534")
	res := rig.do(t, http.MethodPost, "/api/v1/templates/render", strings.NewReader(
		`{"template":"staff","parent":"ou=people,dc=alder,dc=test","values":{"uid":"jdoe","sn":"Doe"}}`))
	if res.Status != http.StatusOK {
		t.Fatalf("status %d: %s", res.Status, res.Body)
	}
	got := decode[TemplateRendering](t, res)
	if got.Change.Dn != "uid=jdoe,ou=people,dc=alder,dc=test" || got.Change.Type != ChangeRequestTypeAdd {
		t.Errorf("change = %+v", got.Change)
	}
	values := map[string]string{}
	for _, a := range *got.Change.Attributes {
		var vs []string
		for _, v := range a.Values {
			vs = append(vs, *v.Text)
		}
		values[a.Name] = strings.Join(vs, "|")
	}
	if values["uidNumber"] != "10012" || values["uid"] != "jdoe" || values["objectClass"] != "top|inetOrgPerson|posixAccount" {
		t.Errorf("attributes = %v", values)
	}
	if len(got.Allocated) != 1 || got.Allocated[0].Value != 10012 || got.Allocated[0].HighestInUse == nil ||
		*got.Allocated[0].HighestInUse != 10011 || got.Allocated[0].Scanned != 3 {
		t.Errorf("allocated = %+v", got.Allocated)
	}
	if len(rig.fake.applied) != 0 {
		t.Errorf("rendering wrote: %v", rig.fake.applied)
	}
}

func TestRenderTemplateRefusesBeforeSearching(t *testing.T) {
	rig := templateRig(t, "10001")
	cases := map[string]struct {
		body   string
		status int
	}{
		"a missing field":           {`{"template":"staff","parent":"ou=people,dc=alder,dc=test","values":{"uid":"jdoe"}}`, http.StatusBadRequest},
		"an unknown one":            {`{"template":"nope","parent":"ou=people,dc=alder,dc=test","values":{}}`, http.StatusNotFound},
		"a parent that is not a DN": {`{"template":"staff","parent":"not a dn","values":{"uid":"jdoe","sn":"Doe"}}`, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			before := rig.fake.searches.Load()
			res := rig.do(t, http.MethodPost, "/api/v1/templates/render", strings.NewReader(tc.body))
			if res.Status != tc.status {
				t.Errorf("status %d, want %d: %s", res.Status, tc.status, res.Body)
			}
			if rig.fake.searches.Load() != before {
				t.Errorf("searched the directory for a request it then refused")
			}
		})
	}
}

func TestRenderTemplateRefusesARangeThatIsFull(t *testing.T) {
	rig := templateRig(t, "20000")
	res := rig.do(t, http.MethodPost, "/api/v1/templates/render", strings.NewReader(
		`{"template":"staff","parent":"ou=people,dc=alder,dc=test","values":{"uid":"jdoe","sn":"Doe"}}`))
	if res.Status != http.StatusConflict || !strings.Contains(res.Body, "every number from 10000 to 20000 is taken") {
		t.Errorf("status %d: %s", res.Status, res.Body)
	}
}
