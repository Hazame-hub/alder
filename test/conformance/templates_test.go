//go:build conformance

package conformance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/filter"
)

// 1.44: entry templates, against both servers.
//
// "New user" and "New group" render a built-in template into an add change,
// with the next free uidNumber or gidNumber read from the directory. This
// checks that the built-ins each server is offered are ones it accepts --
// posixGroup is STRUCTURAL on OpenLDAP and AUXILIARY on 389 DS, so the two
// get different group variants -- and that the number is one more than the
// highest in use, by planning and applying what the template rendered.

// highestInUse is the highest value of attr in [from, to] anywhere under the
// suffix, read straight from the directory, or from-1 when there is none.
func highestInUse(t *testing.T, sess directory.Session, attr string, from, to int64) int64 {
	t.Helper()
	highest := from - 1
	var cookie []byte
	for {
		res, err := sess.Search(ctx(t), directory.SearchRequest{
			BaseDN: mustDN(t, suffix), Scope: directory.ScopeSubtree,
			Filter: filter.Present(attr), Attributes: []string{attr},
			PageSize: directory.MaxPageSize, Cookie: cookie,
		})
		if err != nil {
			t.Fatalf("searching %s: %v", attr, err)
		}
		for _, e := range res.Entries {
			for _, v := range e.Get(attr) {
				if n, err := strconv.ParseInt(string(v), 10, 64); err == nil && n >= from && n <= to && n > highest {
					highest = n
				}
			}
		}
		if cookie = res.Cookie; len(cookie) == 0 {
			return highest
		}
	}
}

func renderTemplate(t *testing.T, client *http.Client, base, name, parent string, values map[string]string) api.TemplateRendering {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"template": name, "parent": parent, "values": values})
	res := post(t, client, base+"/templates/render", string(body))
	if res.status != http.StatusOK {
		t.Fatalf("rendering %s: status %d\n%s", name, res.status, res.body)
	}
	return decodeInto[api.TemplateRendering](t, res)
}

func TestBuiltinTemplatesCreateEntriesWithTheNextFreeNumber(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		client, base := alder(t, s)
		scratch := writeBase(t, sess, "alder-templates")

		res := get(t, client, base+"/templates")
		if res.status != http.StatusOK {
			t.Fatalf("%s: GET /templates: %d\n%s", s.name, res.status, res.body)
		}
		list := decodeInto[api.TemplateList](t, res)
		offered := map[string]api.EntryTemplate{}
		for _, tpl := range list.Templates {
			if !tpl.Usable {
				t.Errorf("%s: built-in %s is listed as not usable: %v", s.name, tpl.Name, tpl.Problems)
			}
			offered[tpl.Name] = tpl
		}
		if _, ok := offered["posix-user"]; !ok {
			t.Fatalf("%s: no posix-user template offered: %s", s.name, res.body)
		}
		_, plain := offered["posix-group"]
		_, withNames := offered["posix-group-of-names"]
		if plain == withNames {
			t.Errorf("%s: want exactly one posix group variant, got posix-group=%v posix-group-of-names=%v",
				s.name, plain, withNames)
		}

		// A user: the number is one more than the highest in use.
		wantUID := highestInUse(t, sess, "uidNumber", 10000, 60000) + 1
		user := renderTemplate(t, client, base, "posix-user", scratch.String(), map[string]string{
			"uid": "alder-tpl-user", "givenName": "Tem", "sn": "Plate", "gidNumber": "20001",
		})
		if len(user.Allocated) != 1 || user.Allocated[0].Value != wantUID {
			t.Fatalf("%s: allocated %s, want uidNumber %d", s.name, mustEncode(t, user.Allocated), wantUID)
		}
		planAndApplyChanges(t, client, base, []api.ChangeRequest{user.Change})
		if got := readOne(t, sess, user.Change.Dn, "uidNumber"); got != strconv.FormatInt(wantUID, 10) {
			t.Errorf("%s: uidNumber = %q after applying, want %d", s.name, got, wantUID)
		}
		if got := readOne(t, sess, user.Change.Dn, "homeDirectory"); got != "/home/alder-tpl-user" {
			t.Errorf("%s: homeDirectory = %q", s.name, got)
		}

		// The next render sees the number just taken.
		again := renderTemplate(t, client, base, "posix-user", scratch.String(), map[string]string{
			"uid": "alder-tpl-user2", "sn": "Plate", "gidNumber": "20001",
		})
		if again.Allocated[0].Value != wantUID+1 {
			t.Errorf("%s: second allocation = %d, want %d", s.name, again.Allocated[0].Value, wantUID+1)
		}

		// A group, in whichever variant this server accepts.
		groupTemplate := "posix-group"
		if withNames {
			groupTemplate = "posix-group-of-names"
		}
		wantGID := highestInUse(t, sess, "gidNumber", 10000, 60000) + 1
		group := renderTemplate(t, client, base, groupTemplate, scratch.String(), map[string]string{
			"cn": "alder-tpl-group", "description": "made from a template",
		})
		if len(group.Allocated) != 1 || group.Allocated[0].Value != wantGID {
			t.Fatalf("%s: allocated %s, want gidNumber %d", s.name, mustEncode(t, group.Allocated), wantGID)
		}
		planAndApplyChanges(t, client, base, []api.ChangeRequest{group.Change})
		if got := readOne(t, sess, group.Change.Dn, "gidNumber"); got != fmt.Sprint(wantGID) {
			t.Errorf("%s: gidNumber = %q after applying, want %d", s.name, got, wantGID)
		}
	})
}
