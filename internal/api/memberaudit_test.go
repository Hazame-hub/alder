package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// The membership audit (docs/proposals/group-membership-tools.md, Part 2),
// against a tree the test holds. The limits a server can be made to return on
// demand only with configuration the harness does not have -- a size, time or
// administrative limit on the existence search -- are returned here by the
// double, as the proposal allows; the conformance suite covers the rest on
// both servers.

const auditSuffix = "dc=alder,dc=test"

var auditClasses = []string{
	"( 2.5.6.17 NAME 'groupOfUniqueNames' SUP top STRUCTURAL MUST ( uniqueMember $ cn ) MAY description )",
	"( 1.3.6.1.1.1.2.2 NAME 'posixGroup' SUP top STRUCTURAL MUST ( cn $ gidNumber ) MAY ( memberUid $ description ) )",
}

// auditTree is a directory.Reader over entries the test adds.
type auditTree struct {
	sch     *schema.Schema
	caps    directory.Capabilities
	entries []*directory.Entry

	// Keyed by the DN's Key: what a search at that base, or a read of that
	// DN, answers instead of entries.
	searchErr map[string]error
	readErr   map[string]error
	referrals map[string][]string
	// truncated answers a search at that base with a truncation and no
	// cookie, which is how the driver reports a server size limit.
	truncated map[string]bool

	searches []string
	reads    int
}

func newAuditTree(t *testing.T) *auditTree {
	t.Helper()
	tree := &auditTree{
		sch: testSchema(t, auditClasses...), caps: defaultCaps(),
		searchErr: map[string]error{}, readErr: map[string]error{},
		referrals: map[string][]string{}, truncated: map[string]bool{},
	}
	tree.add(t, auditSuffix, "top", "domain")
	tree.add(t, "ou=people,"+auditSuffix, "top", "organizationalUnit")
	tree.add(t, "ou=groups,"+auditSuffix, "top", "organizationalUnit")
	return tree
}

func (a *auditTree) add(t *testing.T, raw string, classes ...string) *directory.Entry {
	t.Helper()
	e := directory.NewEntry(dn.MustParse(raw))
	values := make([][]byte, len(classes))
	for i, c := range classes {
		values[i] = []byte(c)
	}
	e.Set("objectClass", values)
	a.entries = append(a.entries, e)
	return e
}

func (a *auditTree) group(t *testing.T, cn, class, attr string, members ...string) {
	t.Helper()
	e := a.add(t, "cn="+cn+",ou=groups,"+auditSuffix, "top", class)
	values := make([][]byte, len(members))
	for i, m := range members {
		values[i] = []byte(m)
	}
	if len(values) > 0 {
		e.Set(attr, values)
	}
}

func auditPerson(uid string) string { return "uid=" + uid + ",ou=people," + auditSuffix }

func (a *auditTree) Capabilities() directory.Capabilities                  { return a.caps }
func (a *auditTree) Schema(context.Context) (*schema.Schema, error)        { return a.sch, nil }
func (a *auditTree) RefreshSchema(context.Context) (*schema.Schema, error) { return a.sch, nil }
func (a *auditTree) SchemaDefinitions(context.Context, string, directory.SchemaDefKind) ([]string, error) {
	return nil, nil
}

func (a *auditTree) VisibilityOf(context.Context, dn.DN, string) (directory.AttributeVisibility, error) {
	return directory.VisibilityUnknown, nil
}

func (a *auditTree) Read(_ context.Context, target dn.DN, _ []string) (*directory.Entry, error) {
	a.reads++
	if err := a.readErr[target.Key()]; err != nil {
		return nil, err
	}
	for _, e := range a.entries {
		if e.DN.Equal(target) {
			return e, nil
		}
	}
	return nil, &ldapdriver.Error{Code: ldap.LDAPResultNoSuchObject}
}

func (a *auditTree) Search(_ context.Context, req directory.SearchRequest) (*directory.SearchResult, error) {
	req.Normalise()
	a.searches = append(a.searches, req.BaseDN.String()+"|"+req.Scope.String())
	key := req.BaseDN.Key()
	if err := a.searchErr[key]; err != nil {
		return nil, err
	}
	if a.truncated[key] {
		return &directory.SearchResult{Truncated: true}, nil
	}
	namesOnly := slices.Contains(req.Attributes, "1.1")
	var matched []*directory.Entry
	for _, e := range a.entries {
		if !inScope(e.DN, req.BaseDN, req.Scope) {
			continue
		}
		if !namesOnly && !isAuditGroup(e) {
			continue
		}
		matched = append(matched, e)
	}
	start := 0
	if len(req.Cookie) > 0 {
		start, _ = strconv.Atoi(string(req.Cookie))
	}
	end := min(start+req.Limit, len(matched))
	out := &directory.SearchResult{Entries: matched[start:end]}
	if start == 0 {
		out.Referrals = a.referrals[key]
	}
	if end < len(matched) {
		out.Cookie = []byte(strconv.Itoa(end))
		out.Truncated = true
	}
	return out, nil
}

func inScope(d, base dn.DN, scope directory.Scope) bool {
	switch scope {
	case directory.ScopeBase:
		return d.Equal(base)
	case directory.ScopeOneLevel:
		return d.IsChildOf(base)
	}
	return d.HasSuffix(base)
}

func isAuditGroup(e *directory.Entry) bool {
	for _, c := range e.ObjectClasses() {
		switch strings.ToLower(c) {
		case "groupofnames", "groupofuniquenames", "posixgroup":
			return true
		}
	}
	return false
}

func runAudit(t *testing.T, tree *auditTree) *MemberAudit {
	t.Helper()
	got, err := auditMembers(context.Background(), tree, dn.MustParse(auditSuffix), 500)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	return got
}

func groupNamed(t *testing.T, audit *MemberAudit, cn string) MemberAuditGroup {
	t.Helper()
	for _, g := range audit.Groups {
		if strings.HasPrefix(strings.ToLower(g.Dn), "cn="+cn+",") {
			return g
		}
	}
	t.Fatalf("no group %s in %+v", cn, audit.Groups)
	return MemberAuditGroup{}
}

func refusalOf(t *testing.T, err error) *auditRefusal {
	t.Helper()
	var r *auditRefusal
	if !errors.As(err, &r) {
		t.Fatalf("got %v, want the audit refused", err)
	}
	return r
}

func TestTheAuditReportsValuesNotFoundAndTheGroupsTheyWouldEmpty(t *testing.T) {
	tree := newAuditTree(t)
	tree.add(t, auditPerson("ana"), "top", "person")
	tree.group(t, "mixed", "groupOfNames", "member", auditPerson("ana"), auditPerson("gone"))
	tree.group(t, "orphan", "groupOfNames", "member", auditPerson("never"))
	tree.group(t, "unique", "groupOfUniqueNames", "uniqueMember", auditPerson("Never")+"#'0101'B")
	tree.group(t, "posix", "posixGroup", "memberUid", "ana")

	audit := runAudit(t, tree)

	mixed := groupNamed(t, audit, "mixed")
	if len(mixed.NotFound) != 1 || mixed.NotFound[0].Value != auditPerson("gone") || mixed.Remaining != 1 {
		t.Errorf("mixed = %+v, want gone not found and one remaining", mixed)
	}
	orphan := groupNamed(t, audit, "orphan")
	if !orphan.ClassesRequireMember || orphan.Remaining != 0 || len(orphan.NotFound) != 1 {
		t.Errorf("orphan = %+v, want a required member and nothing remaining", orphan)
	}
	// Compared by DN key: a different case is the same name, and the value
	// comes back exactly as stored, suffix and all, so a removal names it.
	unique := groupNamed(t, audit, "unique")
	if len(unique.NotFound) != 1 || unique.NotFound[0].Value != auditPerson("Never")+"#'0101'B" || !unique.ClassesRequireMember {
		t.Errorf("unique = %+v", unique)
	}
	posix := groupNamed(t, audit, "posix")
	if posix.ClassesRequireMember || len(posix.NotChecked) != 1 || posix.NotChecked[0].Reason != UncheckedNotADn ||
		posix.Remaining != 1 {
		t.Errorf("posix = %+v, want memberUid not checked and no required member", posix)
	}

	// Few names, so each was read on its own: ana, gone, never.
	if tree.reads != 3 {
		t.Errorf("%d reads, want 3: one per distinct name", tree.reads)
	}
	for _, r := range audit.Read[1:] {
		if r.Scope != MemberAuditReadScopeBase {
			t.Errorf("read %+v, want base reads after the groups' search", r)
		}
	}
}

func TestAValueOutsideEveryNamingContextIsNotChecked(t *testing.T) {
	tree := newAuditTree(t)
	tree.group(t, "far", "groupOfNames", "member", "uid=x,dc=elsewhere", "not a dn")
	far := groupNamed(t, runAudit(t, tree), "far")
	reasons := []MemberAuditUncheckedReason{far.NotChecked[0].Reason, far.NotChecked[1].Reason}
	if !slices.Equal(reasons, []MemberAuditUncheckedReason{UncheckedOutsideContexts, UncheckedNotADn}) || len(far.NotFound) != 0 {
		t.Errorf("far = %+v", far)
	}
}

func TestManyNamesAreCheckedUnderTheirParentsNotOneByOne(t *testing.T) {
	tree := newAuditTree(t)
	var members []string
	for i := range 250 {
		uid := fmt.Sprintf("u%03d", i)
		if i%10 != 0 {
			tree.add(t, auditPerson(uid), "top", "person")
		}
		members = append(members, auditPerson(uid))
	}
	// Entries the existence check has no reason to read.
	for i := range 40 {
		tree.add(t, fmt.Sprintf("cn=device%d,%s", i, auditSuffix), "top", "device")
	}
	tree.group(t, "big", "groupOfNames", "member", members...)

	audit := runAudit(t, tree)
	if tree.reads != 0 {
		t.Errorf("%d reads one by one, want none past %d names", tree.reads, auditDirectReads)
	}
	big := groupNamed(t, audit, "big")
	if len(big.NotFound) != 25 || big.Remaining != 225 {
		t.Errorf("big: %d not found, %d remaining; want 25 and 225", len(big.NotFound), big.Remaining)
	}
	last := audit.Read[len(audit.Read)-1]
	if last.Base != "ou=people,"+auditSuffix || last.Scope != MemberAuditReadScopeOne || last.Entries != 225 {
		t.Errorf("existence read = %+v, want one level under ou=people", last)
	}
}

func TestNarrowingMergesParentsAndNeverClimbsPastTheNamingContext(t *testing.T) {
	contexts := []dn.DN{dn.MustParse(auditSuffix)}
	var names []dn.DN
	for i := range 60 {
		names = append(names, dn.MustParse(fmt.Sprintf("uid=a,ou=team%d,ou=people,%s", i, auditSuffix)))
	}
	names = append(names, dn.MustParse("uid=b,ou=team1,ou=people,"+auditSuffix))
	names = append(names, dn.MustParse("uid=c,"+auditSuffix))
	names = append(names, dn.MustParse(auditSuffix))

	got := narrowBases(names, contexts)
	if len(got) > auditMaxBases {
		t.Fatalf("%d bases, want at most %d", len(got), auditMaxBases)
	}
	// Sixty team parents merge into ou=people; the suffix's own child and the
	// suffix itself are under the suffix, which cannot be climbed past and
	// covers everything else, so it is the one base, as a subtree.
	if len(got) != 1 || !got[0].dn.Equal(contexts[0]) || got[0].scope != directory.ScopeSubtree {
		t.Errorf("bases = %+v", got)
	}

	few := narrowBases(names[:3], contexts)
	if len(few) != 3 || few[0].scope != directory.ScopeOneLevel {
		t.Errorf("three teams = %+v, want each parent one level down", few)
	}
	nested := narrowBases([]dn.DN{
		dn.MustParse("uid=a,ou=people," + auditSuffix),
		dn.MustParse("uid=b,ou=x,ou=people," + auditSuffix),
	}, contexts)
	if len(nested) != 1 || nested[0].scope != directory.ScopeSubtree {
		t.Errorf("a parent below another = %+v, want one subtree base", nested)
	}
}

func TestAnyServerLimitRefusesTheAuditWhole(t *testing.T) {
	limits := map[string]uint16{
		"sizeLimitExceeded":  ldap.LDAPResultSizeLimitExceeded,
		"timeLimitExceeded":  ldap.LDAPResultTimeLimitExceeded,
		"adminLimitExceeded": ldap.LDAPResultAdminLimitExceeded,
	}
	for name, code := range limits {
		t.Run(name+" on the groups' search", func(t *testing.T) {
			tree := newAuditTree(t)
			tree.searchErr[dn.MustParse(auditSuffix).Key()] = &ldapdriver.Error{Code: code}
			_, err := auditMembers(context.Background(), tree, dn.MustParse(auditSuffix), 500)
			if r := refusalOf(t, err); r.code != ErrorErrorAuditIncomplete || !strings.Contains(r.detail, name) {
				t.Errorf("refusal = %+v, want audit_incomplete naming %s", r, name)
			}
		})
		t.Run(name+" on a direct read", func(t *testing.T) {
			tree := newAuditTree(t)
			tree.group(t, "g", "groupOfNames", "member", auditPerson("ana"))
			tree.readErr[dn.MustParse(auditPerson("ana")).Key()] = &ldapdriver.Error{Code: code}
			_, err := auditMembers(context.Background(), tree, dn.MustParse(auditSuffix), 500)
			if r := refusalOf(t, err); r.code != ErrorErrorAuditIncomplete || !strings.Contains(r.detail, auditPerson("ana")) {
				t.Errorf("refusal = %+v", r)
			}
		})
		t.Run(name+" on the existence search", func(t *testing.T) {
			tree := manyNames(t)
			tree.searchErr[dn.MustParse("ou=people,"+auditSuffix).Key()] = &ldapdriver.Error{Code: code}
			_, err := auditMembers(context.Background(), tree, dn.MustParse("ou=groups,"+auditSuffix), 500)
			if r := refusalOf(t, err); r.code != ErrorErrorAuditIncomplete || !strings.Contains(r.detail, "ou=people") {
				t.Errorf("refusal = %+v", r)
			}
		})
	}
	t.Run("a truncated existence search", func(t *testing.T) {
		tree := manyNames(t)
		tree.truncated[dn.MustParse("ou=people,"+auditSuffix).Key()] = true
		_, err := auditMembers(context.Background(), tree, dn.MustParse("ou=groups,"+auditSuffix), 500)
		if r := refusalOf(t, err); r.code != ErrorErrorAuditIncomplete || !strings.Contains(r.detail, "truncated") {
			t.Errorf("refusal = %+v", r)
		}
	})
	t.Run("a truncated groups' search", func(t *testing.T) {
		tree := newAuditTree(t)
		tree.truncated[dn.MustParse(auditSuffix).Key()] = true
		_, err := auditMembers(context.Background(), tree, dn.MustParse(auditSuffix), 500)
		if r := refusalOf(t, err); r.code != ErrorErrorAuditIncomplete {
			t.Errorf("refusal = %+v", r)
		}
	})
}

// manyNames is a group naming more people than are read one by one.
func manyNames(t *testing.T) *auditTree {
	t.Helper()
	tree := newAuditTree(t)
	var members []string
	for i := range auditDirectReads + 1 {
		members = append(members, auditPerson(fmt.Sprintf("m%d", i)))
	}
	tree.group(t, "many", "groupOfNames", "member", members...)
	return tree
}

func TestMoreGroupsThanTheLimitRefusesTheAudit(t *testing.T) {
	tree := newAuditTree(t)
	for i := range 3 {
		tree.group(t, fmt.Sprintf("g%d", i), "groupOfNames", "member", auditPerson("ana"))
	}
	_, err := auditMembers(context.Background(), tree, dn.MustParse(auditSuffix), 2)
	if r := refusalOf(t, err); r.code != ErrorErrorAuditIncomplete || !strings.Contains(r.message, "more than 2 groups") {
		t.Errorf("refusal = %+v", r)
	}
}

func TestReadingMoreThanTheCeilingRefusesTheAudit(t *testing.T) {
	tree := manyNames(t)
	for i := range auditMaxRead {
		tree.entries = append(tree.entries, directory.NewEntry(dn.MustParse(fmt.Sprintf("uid=f%d,ou=people,%s", i, auditSuffix))))
	}
	_, err := auditMembers(context.Background(), tree, dn.MustParse("ou=groups,"+auditSuffix), 500)
	if r := refusalOf(t, err); r.code != ErrorErrorSnapshotTooLarge || !strings.Contains(r.message, "50000") {
		t.Errorf("refusal = %+v", r)
	}
}

func TestAReferralMakesWhatIsBelowItNotCheckedNeverNotFound(t *testing.T) {
	t.Run("returned by the groups' search", func(t *testing.T) {
		tree := newAuditTree(t)
		tree.group(t, "g", "groupOfNames", "member", "uid=x,ou=remote,"+auditSuffix, auditPerson("gone"))
		tree.referrals[dn.MustParse(auditSuffix).Key()] = []string{"ldap://elsewhere.test/ou=remote," + auditSuffix + "??sub"}
		audit := runAudit(t, tree)
		g := groupNamed(t, audit, "g")
		if len(g.NotChecked) != 1 || g.NotChecked[0].Reason != UncheckedBelowReferral ||
			len(g.NotFound) != 1 || g.NotFound[0].Value != auditPerson("gone") {
			t.Errorf("g = %+v", g)
		}
		if len(audit.Referrals) != 1 || audit.Referrals[0].Dn == nil || *audit.Referrals[0].Dn != "ou=remote,"+auditSuffix {
			t.Errorf("referrals = %+v", audit.Referrals)
		}
		// Never read: the referral says it is held elsewhere.
		if tree.reads != 1 {
			t.Errorf("%d reads, want only gone", tree.reads)
		}
	})
	t.Run("answered on a direct read", func(t *testing.T) {
		tree := newAuditTree(t)
		target := "uid=x,ou=remote," + auditSuffix
		tree.group(t, "g", "groupOfNames", "member", target)
		tree.readErr[dn.MustParse(target).Key()] = &ldapdriver.Error{Code: ldap.LDAPResultReferral,
			ReferralDN: "ou=remote," + auditSuffix, Referrals: []string{"ldap://elsewhere.test/" + target}}
		g := groupNamed(t, runAudit(t, tree), "g")
		if len(g.NotChecked) != 1 || g.NotChecked[0].Reason != UncheckedBelowReferral || len(g.NotFound) != 0 {
			t.Errorf("g = %+v", g)
		}
	})
	t.Run("whose URL names no DN", func(t *testing.T) {
		tree := manyNames(t)
		people := dn.MustParse("ou=people," + auditSuffix)
		tree.referrals[people.Key()] = []string{"ldap://elsewhere.test/"}
		audit, err := auditMembers(context.Background(), tree, dn.MustParse("ou=groups,"+auditSuffix), 500)
		if err != nil {
			t.Fatal(err)
		}
		g := groupNamed(t, audit, "many")
		if len(g.NotFound) != 0 || len(g.NotChecked) != auditDirectReads+1 {
			t.Errorf("%d not found and %d not checked; everything under the search's base is not checked",
				len(g.NotFound), len(g.NotChecked))
		}
	})
}

func TestNestingIsJudgedFromTheSameRead(t *testing.T) {
	tree := newAuditTree(t)
	g := func(cn string) string { return "cn=" + cn + ",ou=groups," + auditSuffix }
	tree.add(t, auditPerson("ana"), "top", "person")
	tree.group(t, "empty", "groupOfNames", "member", auditPerson("gone"))
	tree.group(t, "outer", "groupOfNames", "member", g("empty"))
	tree.group(t, "staffed", "groupOfNames", "member", g("empty"), auditPerson("ana"))
	tree.group(t, "ring-a", "groupOfNames", "member", g("ring-b"))
	tree.group(t, "ring-b", "groupOfNames", "member", g("ring-a"))

	audit := runAudit(t, tree)
	for cn, want := range map[string]bool{"empty": false, "outer": true, "staffed": false, "ring-a": true, "ring-b": true} {
		if got := groupNamed(t, audit, cn).NoPersonThroughNesting; got != want {
			t.Errorf("%s: noPersonThroughNesting = %v, want %v", cn, got, want)
		}
	}
	if !slices.Equal(audit.Cycles, []string{g("ring-a"), g("ring-b")}) {
		t.Errorf("cycles = %q", audit.Cycles)
	}
}

func TestTheAuditEndpoint(t *testing.T) {
	fake := &fakeSession{caps: defaultCaps(), sch: testSchema(t), entry: entryFixture(t)}
	fake.caps.ConfigContext = "cn=config"
	rig := newRig(t, Config{}, fake)

	res := rig.do(t, http.MethodPost, "/api/v1/members/audit", strings.NewReader(`{"base":"olcDatabase={1}mdb,cn=config"}`))
	if res.Status != http.StatusBadRequest {
		t.Errorf("a configuration base: status %d, want 400\n%s", res.Status, res.Body)
	}

	fake.searchErr = &ldapdriver.Error{Code: ldap.LDAPResultAdminLimitExceeded}
	res = rig.do(t, http.MethodPost, "/api/v1/members/audit", strings.NewReader(`{"base":"`+auditSuffix+`"}`))
	if body := decode[Error](t, res); res.Status != http.StatusBadRequest || body.Error != ErrorErrorAuditIncomplete {
		t.Errorf("an admin limit: status %d, %+v", res.Status, body)
	}

	fake.searchErr = nil
	res = rig.do(t, http.MethodPost, "/api/v1/members/audit", strings.NewReader(`{"base":"`+auditSuffix+`"}`))
	if res.Status != http.StatusOK {
		t.Fatalf("status %d\n%s", res.Status, res.Body)
	}
	if body := decode[MemberAudit](t, res); body.Bind != "cn=admin,dc=alder,dc=test" {
		t.Errorf("bind = %q, want the session's identity", body.Bind)
	}
}
