package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// fakeDirectory is a set of entries a traversal can read.
type fakeDirectory map[string]*directory.Entry

// read honours the attribute list, which is not fussiness: the traversal has to
// ask for the membership attributes or a nested group comes back looking empty,
// and a fake that returns everything regardless cannot fail that way.
func (f fakeDirectory) read(_ context.Context, target dn.DN, attrs []string) (*directory.Entry, error) {
	full, ok := f[foldName(target.String())]
	if !ok {
		return nil, errors.New("no such object")
	}
	out := directory.NewEntry(full.DN)
	for _, name := range full.Order {
		for _, want := range attrs {
			if want == "*" || strings.EqualFold(want, name) {
				out.Set(name, full.Attributes[name])
				break
			}
		}
	}
	return out, nil
}

func (f fakeDirectory) add(t *testing.T, d string, classes []string, members ...string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(mustParse(t, d))
	raw := make([][]byte, 0, len(classes))
	for _, c := range classes {
		raw = append(raw, []byte(c))
	}
	e.Set("objectClass", raw)
	if len(members) > 0 {
		vals := make([][]byte, 0, len(members))
		for _, m := range members {
			vals = append(vals, []byte(m))
		}
		e.Set("member", vals)
	}
	f[foldName(d)] = e
	return e
}

const (
	groupsOU = "ou=groups,dc=alder,dc=test"
	peopleOU = "ou=people,dc=alder,dc=test"
)

func person(t *testing.T, f fakeDirectory, uid string) string {
	t.Helper()
	d := "uid=" + uid + "," + peopleOU
	f.add(t, d, []string{"top", "inetOrgPerson"})
	return d
}

// The case the feature exists for. A group of groups lists members and
// contains no people; expanding is the difference between a list that answers
// the question and one that only looks like it does.
func TestNestedGroupsAreWalked(t *testing.T) {
	f := fakeDirectory{}
	alice := person(t, f, "alice")
	bob := person(t, f, "bob")

	f.add(t, "cn=platform,"+groupsOU, []string{"top", "groupOfNames"}, alice)
	f.add(t, "cn=network,"+groupsOU, []string{"top", "groupOfNames"}, bob)
	everyone := f.add(t, "cn=everyone,"+groupsOU, []string{"top", "groupOfNames"},
		"cn=platform,"+groupsOU, "cn=network,"+groupsOU)

	got := expandGroup(context.Background(), f.read, testSchema(t), everyone, 100)

	people := leafDNs(got)
	if len(people) != 2 {
		t.Fatalf("reached %d people, want alice and bob: %v", len(people), people)
	}
	if !containsFold(people, alice) || !containsFold(people, bob) {
		t.Errorf("reached %v", people)
	}
}

// The part a flat list cannot say, and the part somebody needs to remove an
// unwanted member from the right group.
func TestAMemberSaysWhichGroupsBroughtItIn(t *testing.T) {
	f := fakeDirectory{}
	alice := person(t, f, "alice")
	f.add(t, "cn=platform,"+groupsOU, []string{"top", "groupOfNames"}, alice)
	f.add(t, "cn=infrastructure,"+groupsOU, []string{"top", "groupOfNames"}, "cn=platform,"+groupsOU)
	everyone := f.add(t, "cn=everyone,"+groupsOU, []string{"top", "groupOfNames"},
		"cn=infrastructure,"+groupsOU)

	got := expandGroup(context.Background(), f.read, testSchema(t), everyone, 100)

	var found *ExpandedMember
	for i := range got.Members {
		if strings.EqualFold(got.Members[i].Dn, alice) {
			found = &got.Members[i]
		}
	}
	if found == nil {
		t.Fatal("alice was not reached at all")
	}
	if found.Direct {
		t.Error("alice is two groups away and was reported as a direct member")
	}
	if found.Via == nil || len(*found.Via) != 2 {
		t.Fatalf("the chain is %v, want infrastructure then platform", found.Via)
	}
	if !strings.Contains((*found.Via)[0], "infrastructure") ||
		!strings.Contains((*found.Via)[1], "platform") {
		t.Errorf("the chain is in the wrong order: %v", *found.Via)
	}
}

// A group that contains itself terminates, and says so.
func TestACycleTerminatesAndIsReported(t *testing.T) {
	f := fakeDirectory{}
	f.add(t, "cn=b,"+groupsOU, []string{"top", "groupOfNames"}, "cn=a,"+groupsOU)
	a := f.add(t, "cn=a,"+groupsOU, []string{"top", "groupOfNames"}, "cn=b,"+groupsOU)

	done := make(chan *expansion, 1)
	go func() { done <- expandGroup(context.Background(), f.read, testSchema(t), a, 100) }()

	got := <-done
	if len(got.Cycles) == 0 {
		t.Error("a group containing itself was not reported as a cycle")
	}
}

// A member DN whose entry is gone is exactly the broken link the referenced-by
// panel exists to prevent, seen from the other side.
func TestADanglingMemberIsReported(t *testing.T) {
	f := fakeDirectory{}
	alice := person(t, f, "alice")
	group := f.add(t, "cn=team,"+groupsOU, []string{"top", "groupOfNames"},
		alice, "uid=ghost,"+peopleOU)

	got := expandGroup(context.Background(), f.read, testSchema(t), group, 100)

	if len(got.Dangling) != 1 || !strings.Contains(got.Dangling[0], "ghost") {
		t.Errorf("dangling is %v, want the missing entry", got.Dangling)
	}
	// The one that does resolve still comes back.
	if len(leafDNs(got)) != 1 {
		t.Errorf("a dangling member cost the live one: %v", leafDNs(got))
	}
}

// memberUid holds a login name, not a DN, so it cannot be followed — and
// saying so is better than reporting the group as empty.
func TestANonDNMembershipValueIsReportedNotDropped(t *testing.T) {
	f := fakeDirectory{}
	group := directory.NewEntry(mustParse(t, "cn=posix,"+groupsOU))
	group.Set("objectClass", [][]byte{[]byte("top"), []byte("posixGroup")})
	group.Set("memberUid", [][]byte{[]byte("alice"), []byte("bob")})

	got := expandGroup(context.Background(), f.read, testSchema(t), group, 100)

	if len(got.Members) != 0 {
		t.Errorf("a login name was resolved to an entry: %v", got.Members)
	}
	if len(got.Unresolvable) != 2 {
		t.Fatalf("unresolvable is %v, want both names reported", got.Unresolvable)
	}
	if !strings.Contains(got.Unresolvable[0], "memberUid") {
		t.Errorf("the report does not name the attribute: %q", got.Unresolvable[0])
	}
}

// Somebody in two teams is ordinary; they are one member, not two, and not a
// cycle.
func TestAPersonInTwoGroupsIsReportedOnce(t *testing.T) {
	f := fakeDirectory{}
	alice := person(t, f, "alice")
	f.add(t, "cn=platform,"+groupsOU, []string{"top", "groupOfNames"}, alice)
	f.add(t, "cn=network,"+groupsOU, []string{"top", "groupOfNames"}, alice)
	everyone := f.add(t, "cn=everyone,"+groupsOU, []string{"top", "groupOfNames"},
		"cn=platform,"+groupsOU, "cn=network,"+groupsOU)

	got := expandGroup(context.Background(), f.read, testSchema(t), everyone, 100)

	if n := len(leafDNs(got)); n != 1 {
		t.Errorf("alice appears %d times", n)
	}
	if len(got.Cycles) != 0 {
		t.Errorf("a shared member was reported as a cycle: %v", got.Cycles)
	}
}

// A membership list that quietly omits people is worse than none when the
// question is "who can get in".
func TestTheLimitIsReportedAsTruncation(t *testing.T) {
	f := fakeDirectory{}
	members := make([]string, 0, 5)
	for _, uid := range []string{"a", "b", "c", "d", "e"} {
		members = append(members, person(t, f, uid))
	}
	group := f.add(t, "cn=big,"+groupsOU, []string{"top", "groupOfNames"}, members...)

	got := expandGroup(context.Background(), f.read, testSchema(t), group, 3)
	if !got.Truncated {
		t.Error("the walk stopped at the limit and did not say so")
	}
	if len(got.Members) > 3 {
		t.Errorf("returned %d members past a limit of 3", len(got.Members))
	}
}

func containsFold(all []string, want string) bool {
	for _, v := range all {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func leafDNs(e *expansion) []string {
	out := []string{}
	for _, m := range e.Members {
		if !m.Group {
			out = append(out, m.Dn)
		}
	}
	return out
}
