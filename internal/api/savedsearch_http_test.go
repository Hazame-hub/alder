package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
)

// Saved searches ride on POST /project/plan, and a saved search's attributes
// on GET /export/ldif.

const projectLockedSearch = `version: 1
title: Locked accounts
base: ou=people,dc=alder,dc=test
filter: (pwdAccountLockedTime=*)
columns: [uid, pwdAccountLockedTime]
limit: 50
`

func TestAProjectsSearchesComeBackBesideItsPlan(t *testing.T) {
	rig := projectRig(t, Config{})
	files := append(validProjectFiles(),
		sentFile{Path: "people/locked.search.yaml", Content: projectLockedSearch},
		sentFile{Path: "all.search.yaml", Content: "version: 1\nbase: dc=alder,dc=test\nfilter: (objectClass=*)\n"})
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, files...)))
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	got := decode[ProjectPlan](t, res)
	if got.Searches == nil || len(*got.Searches) != 2 {
		t.Fatalf("searches = %+v", got.Searches)
	}
	all, locked := (*got.Searches)[0], (*got.Searches)[1]
	if all.Name != "all" || all.Title != nil || all.Scope != SavedSearchScopeSub || all.Limit != 100 ||
		all.Attributes == nil || len(all.Attributes) != 0 {
		t.Errorf("all = %+v", all)
	}
	if locked.Name != "people/locked" || locked.File != "people/locked.search.yaml" || deref(locked.Title) != "Locked accounts" ||
		locked.Base != "ou=people,dc=alder,dc=test" || locked.Filter != "(pwdAccountLockedTime=*)" || locked.Limit != 50 ||
		strings.Join(locked.Attributes, ",") != "uid,pwdAccountLockedTime,objectClass" ||
		strings.Join(locked.Columns, ",") != "uid,pwdAccountLockedTime" {
		t.Errorf("people/locked = %+v", locked)
	}
	// A search is not state: the plan is the one without it.
	if len(got.Plan.Items) != 2 {
		t.Errorf("the plan has %d items", len(got.Plan.Items))
	}

	without := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, validProjectFiles()...)))
	if strings.Contains(without.Body, `"searches"`) {
		t.Errorf("a project without searches answered with a searches key:\n%s", without.Body)
	}
}

func TestASearchFileThatDoesNotValidateMakesTheProjectInvalid(t *testing.T) {
	rig := projectRig(t, Config{})
	files := append(validProjectFiles(), sentFile{Path: "people/bad.search.yaml",
		Content: "version: 1\nbase: ou=people,dc=alder,dc=test\nfilter: (uid=*\n"})
	res := rig.do(t, http.MethodPost, "/api/v1/project/plan", strings.NewReader(projectBody(t, files...)))
	if res.Status != http.StatusBadRequest {
		t.Fatalf("status = %d\n%s", res.Status, res.Body)
	}
	got := decode[Error](t, res)
	if got.Error != ErrorErrorProjectInvalid || got.Problems == nil || len(*got.Problems) != 1 {
		t.Fatalf("error = %+v", got)
	}
	p := (*got.Problems)[0]
	if deref(p.File) != "people/bad.search.yaml" || deref(p.Line) != 3 || !strings.Contains(p.Message, "not a valid RFC 4515 filter") {
		t.Errorf("problem = %+v", p)
	}
}

func TestTheLdifExportAsksForTheAttributesItIsGiven(t *testing.T) {
	target := "/api/v1/export/ldif?dn=ou%3Dpeople%2Cdc%3Dalder%2Cdc%3Dtest&scope=sub"
	for _, tc := range []struct {
		query, want string
	}{
		{"", "*"},
		{"&attributes=uid,%20cn,mail", "uid,cn,mail"},
		{"&attributes=uid&includeOperational=true", "uid,+"},
	} {
		fake := &fakeSession{caps: defaultCaps(), entries: []*directory.Entry{entryFixture(t)}}
		rig := newRig(t, Config{}, fake)
		res := rig.do(t, http.MethodGet, target+tc.query, nil)
		if res.Status != fiber.StatusOK {
			t.Fatalf("%q: status %d: %s", tc.query, res.Status, res.Body)
		}
		fake.searchMu.Lock()
		got := strings.Join(fake.lastSearch.Attributes, ",")
		fake.searchMu.Unlock()
		if got != tc.want {
			t.Errorf("%q: asked for %s, want %s", tc.query, got, tc.want)
		}
	}

	// Naming a secret is not a way around leaving it out.
	fake := &fakeSession{caps: defaultCaps(), entries: []*directory.Entry{entryFixture(t)}}
	if body := newRig(t, Config{}, fake).do(t, http.MethodGet, target+"&attributes=uid,userPassword", nil).Body; strings.Contains(body, "averyrealsecret") {
		t.Error("a password hash was exported because its name was asked for")
	}

	for _, bad := range []string{"uid,(cn)", "uid,,cn", strings.Repeat("cn,", 100) + "cn"} {
		rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), entries: []*directory.Entry{entryFixture(t)}})
		if res := rig.do(t, http.MethodGet, target+"&attributes="+bad, nil); res.Status != fiber.StatusBadRequest {
			t.Errorf("attributes=%.40s: status %d, want 400", bad, res.Status)
		}
	}
}
