package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/schema"
)

// The reverse lookup asks the directory a question phrased in whatever
// vocabulary the connected server has, with a DN somebody else chose in it.
// Both halves are tested here: which attributes get asserted, and what happens
// to the DN on the way into the filter.

func refSchema(t *testing.T, attrs ...string) *schema.Schema {
	t.Helper()
	base := []string{
		"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
		"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	}
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses:  {"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )"},
		schema.AttrAttributeTypes: append(base, attrs...),
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("test schema does not parse: %v", sch.Errors)
	}
	return sch
}

const (
	defMember  = "( 2.5.4.31 NAME 'member' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )"
	defUnique  = "( 2.5.4.50 NAME 'uniqueMember' SYNTAX 1.3.6.1.4.1.1466.115.121.1.34 )"
	defOwner   = "( 2.5.4.32 NAME 'owner' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )"
	defManager = "( 0.9.2342.19200300.100.1.10 NAME 'manager' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )"
)

func TestReferencedByAssertsOnlyWhatTheServerDefines(t *testing.T) {
	subject := "uid=alice,ou=people,dc=alder,dc=test"

	// A server with two of them phrases the question with two.
	got := referencedByFilter(refSchema(t, defMember, defOwner), subject)
	want := "(|(member=uid=alice,ou=people,dc=alder,dc=test)(owner=uid=alice,ou=people,dc=alder,dc=test))"
	if got != want {
		t.Errorf("got\n  %s\nwant\n  %s", got, want)
	}

	// A server with one of them phrases it with one, and does not wrap a single
	// assertion in a pointless OR.
	got = referencedByFilter(refSchema(t, defMember), subject)
	if want := "(member=" + subject + ")"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}

	// A server with none of them gets no link at all, rather than a filter that
	// can only ever match nothing.
	if got := referencedByFilter(refSchema(t), subject); got != "" {
		t.Errorf("got %q; this schema defines no reference attribute", got)
	}
}

// A reference the directory owns cannot be removed, so offering to find and
// unpick it is offering something that can only fail.
func TestReferencedBySkipsWhatTheDirectoryOwns(t *testing.T) {
	// The rule has to be proved on a name that is actually in referenceAttrs,
	// so member is redefined here as something the directory owns.
	locked := refSchema(t,
		"( 2.5.4.31 NAME 'member' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 NO-USER-MODIFICATION USAGE directoryOperation )",
		defOwner,
	)
	got := referencedByFilter(locked, "uid=alice,dc=test")
	if strings.Contains(got, "member=") {
		t.Errorf("got %s; member is NO-USER-MODIFICATION on this server", got)
	}
	if !strings.Contains(got, "owner=") {
		t.Errorf("got %s; owner is still modifiable and should be asserted", got)
	}
}

// The DN is somebody else's data. It goes through the filter builder, which is
// where RFC 4515 escaping lives; concatenating it in the browser would put that
// rule in a second place.
func TestReferencedByEscapesTheSubjectDN(t *testing.T) {
	for _, subject := range []string{
		`cn=Smith\, John,ou=people,dc=alder,dc=test`,
		`cn=a(b)c,dc=test`,
		`cn=star*,dc=test`,
		`cn=back\\slash,dc=test`,
	} {
		got := referencedByFilter(refSchema(t, defMember), subject)
		if got == "" {
			t.Fatalf("no filter for %q", subject)
		}
		// The structural characters must not survive as structure. Everything
		// after "(member=" and before the final ")" is the assertion value.
		inner := strings.TrimSuffix(strings.TrimPrefix(got, "(member="), ")")
		for _, bad := range []string{"(", ")", "*"} {
			if strings.Contains(inner, bad) {
				t.Errorf("subject %q produced %s — %q survived into the filter",
					subject, got, bad)
			}
		}
	}
}

// A group's own DN is a legitimate subject: a group can be a member of another
// group, and "what names this group" is the question before deleting it.
func TestReferencedByWorksForANestedGroup(t *testing.T) {
	got := referencedByFilter(refSchema(t, defMember, defUnique),
		"cn=infrastructure,ou=groups,dc=alder,dc=test")
	for _, want := range []string{"member=cn=infrastructure", "uniqueMember=cn=infrastructure"} {
		if !strings.Contains(got, want) {
			t.Errorf("got %s, want it to contain %s", got, want)
		}
	}
}

func TestReferencedByNeedsASubject(t *testing.T) {
	if got := referencedByFilter(refSchema(t, defMember), ""); got != "" {
		t.Errorf("got %q for an empty DN", got)
	}
	if got := referencedByFilter(nil, "uid=alice,dc=test"); got != "" {
		t.Errorf("got %q with no schema", got)
	}
}

// Every name in the list is a real attribute type somebody could define, and
// the list is the whole vocabulary this feature speaks. If a name is added, it
// should be added deliberately rather than swept in.
func TestReferenceAttrsAreTheStandardsTrackNames(t *testing.T) {
	want := map[string]bool{
		"member": true, "uniqueMember": true, "owner": true, "manager": true,
		"seeAlso": true, "roleOccupant": true, "secretary": true,
	}
	if len(referenceAttrs) != len(want) {
		t.Errorf("referenceAttrs is %v; it is meant to be the seven standards-track names", referenceAttrs)
	}
	for _, name := range referenceAttrs {
		if !want[name] {
			t.Errorf("%q is not one of the standards-track reference attributes", name)
		}
	}
	// memberUid holds a login name, not a DN, so it cannot answer this question
	// and must not be swept in alongside the DN-valued ones.
	for _, name := range referenceAttrs {
		if name == "memberUid" {
			t.Error("memberUid holds a name, not a DN; it cannot match a subject DN")
		}
	}
}
