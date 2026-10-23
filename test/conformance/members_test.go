//go:build conformance

package conformance

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/api"
	"github.com/hazame-hub/alder/internal/directory"
)

// What GET /members calls dangling, against the real servers.
//
// "Dangling" is the directory's own answer, "no such object", and nothing
// else: a read refused, timed out or cut off says nothing about the entry and
// is reported in `errors` instead. The case here is the one both servers can
// be made to give on demand -- a member DN that never existed, one beside the
// group and one under a parent that is not there either -- and it holds that
// both are dangling, the member that does exist is still listed, and nothing
// is reported as an error on the walk.

const membersOU = "ou=alder-members," + suffix

func clearMembersOU(t *testing.T, sess directory.Session) {
	t.Helper()
	_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, "cn=team,"+membersOU), Type: directory.ChangeDelete})
	_ = sess.Apply(ctx(t), directory.ChangeRecord{DN: mustDN(t, membersOU), Type: directory.ChangeDelete})
}

func TestMembersDanglingIsOnlyNoSuchObject(t *testing.T) {
	eachServer(t, func(t *testing.T, s server, sess directory.Session) {
		clearMembersOU(t, sess)
		t.Cleanup(func() { clearMembersOU(t, sess) })

		present := "uid=user0004,ou=people," + suffix
		beside := "uid=never-existed," + membersOU
		nowhere := "uid=never-existed,ou=not-there," + membersOU
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, membersOU), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("organizationalUnit")}},
			{Name: "ou", Values: [][]byte{[]byte("alder-members")}},
		}})
		// Both servers accept a DN-syntax value naming no entry: neither
		// harness server runs a plugin that checks a member exists.
		mustApply(t, sess, directory.ChangeRecord{DN: mustDN(t, "cn=team,"+membersOU), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("groupOfNames")}},
			{Name: "cn", Values: [][]byte{[]byte("team")}},
			{Name: "member", Values: [][]byte{[]byte(present), []byte(beside), []byte(nowhere)}},
		}})

		client, base := alder(t, s)
		res := get(t, client, base+"/members?dn="+url.QueryEscape("cn=team,"+membersOU))
		if res.status != http.StatusOK {
			t.Fatalf("expanding: status %d\n%s", res.status, res.body)
		}
		got := decodeInto[api.MemberList](t, res)

		if got.Errors != nil && len(*got.Errors) > 0 {
			t.Errorf("the walk reported errors %+v; every read either succeeded or was no such object", *got.Errors)
		}
		dangling := map[string]bool{}
		if got.Dangling != nil {
			for _, d := range *got.Dangling {
				dangling[strings.ToLower(d)] = true
			}
		}
		for _, want := range []string{beside, nowhere} {
			if !dangling[strings.ToLower(want)] {
				t.Errorf("%s is not dangling; got %v", want, got.Dangling)
			}
		}
		if len(dangling) != 2 {
			t.Errorf("dangling is %v, want exactly the two that never existed", got.Dangling)
		}
		if len(got.Members) != 1 || !strings.EqualFold(got.Members[0].Dn, present) {
			t.Errorf("members are %+v, want only %s", got.Members, present)
		}
	})
}
