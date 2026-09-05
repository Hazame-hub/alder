package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/schema"
)

// A miniature directory schema, close enough to RFC 4519 and RFC 2307 to
// exercise the derivation without dragging a thousand definitions in.
//
// The point of the shape is inetOrgPerson: it inherits from person, permits
// mail, and person does not. A derivation that only looks at the anchors gets
// the filter right and the columns wrong, and the columns are the half a user
// notices.
func testSchema(t *testing.T, extraClasses ...string) *schema.Schema {
	t.Helper()
	classes := []string{
		"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
		"( 2.5.6.6 NAME 'person' SUP top STRUCTURAL MUST ( sn $ cn ) " +
			"MAY ( userPassword $ telephoneNumber $ description ) )",
		"( 2.5.6.7 NAME 'organizationalPerson' SUP person STRUCTURAL " +
			"MAY ( title $ ou $ l ) )",
		"( 2.16.840.1.113730.3.2.2 NAME 'inetOrgPerson' SUP organizationalPerson " +
			"STRUCTURAL MAY ( uid $ mail $ givenName $ displayName ) )",
		"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou " +
			"MAY ( description $ l $ telephoneNumber ) )",
		"( 2.5.6.9 NAME 'groupOfNames' SUP top STRUCTURAL MUST ( member $ cn ) " +
			"MAY ( description $ ou ) )",
	}
	classes = append(classes, extraClasses...)

	attrs := []string{
		"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
		"( 2.5.4.3 NAME 'cn' DESC 'Common name' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.4 NAME 'sn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.11 NAME 'ou' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.7 NAME 'l' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.12 NAME 'title' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.13 NAME 'description' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.20 NAME 'telephoneNumber' SYNTAX 1.3.6.1.4.1.1466.115.121.1.50 )",
		"( 2.5.4.31 NAME 'member' SYNTAX 1.3.6.1.4.1.1466.115.121.1.12 )",
		"( 2.5.4.50 NAME 'uniqueMember' SYNTAX 1.3.6.1.4.1.1466.115.121.1.34 )",
		"( 1.3.6.1.1.1.1.12 NAME 'memberUid' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 2.5.4.35 NAME 'userPassword' SYNTAX 1.3.6.1.4.1.1466.115.121.1.40 )",
		"( 2.5.4.42 NAME 'givenName' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 0.9.2342.19200300.100.1.1 NAME 'uid' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		"( 0.9.2342.19200300.100.1.3 NAME 'mail' DESC 'RFC822 mailbox' " +
			"SYNTAX 1.3.6.1.4.1.1466.115.121.1.26 )",
		"( 1.3.6.1.1.1.1.0 NAME 'uidNumber' SYNTAX 1.3.6.1.4.1.1466.115.121.1.27 )",
		"( 1.3.6.1.1.1.1.1 NAME 'gidNumber' SYNTAX 1.3.6.1.4.1.1466.115.121.1.27 )",
		"( 2.16.840.1.113730.3.1.241 NAME 'displayName' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
	}

	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses:  classes,
		schema.AttrAttributeTypes: attrs,
	})
	if len(sch.Errors) != 0 {
		t.Fatalf("the test schema does not parse: %v", sch.Errors)
	}
	return sch
}

func viewByID(views []ObjectView, id ObjectViewId) *ObjectView {
	for i := range views {
		if views[i].Id == id {
			return &views[i]
		}
	}
	return nil
}

func columnNames(v *ObjectView) []string {
	out := make([]string, 0, len(v.Columns))
	for _, c := range v.Columns {
		out = append(out, c.Attribute)
	}
	return out
}

// The filter asserts only the anchors, and only the ones this server defines.
// posixAccount is absent from the test schema, so asserting it would be a term
// that can never match.
func TestUsersViewAssertsOnlyTheAnchorsTheSchemaDefines(t *testing.T) {
	views := objectViews(testSchema(t))
	users := viewByID(views, ViewUsers)
	if users == nil {
		t.Fatal("there is no users view")
	}
	if want := "(objectClass=person)"; users.Filter != want {
		t.Errorf("filter is %q, want %q", users.Filter, want)
	}
	if len(users.Anchors) != 1 || users.Anchors[0] != "person" {
		t.Errorf("anchors are %v, want [person]", users.Anchors)
	}
}

// The whole reason the columns walk the class tree.
//
// person permits no mail and no uid. inetOrgPerson permits both, inherits from
// person, and is therefore matched by the users filter — so an inetOrgPerson
// entry is a user, and a users table without an email column would be missing
// the column people came for.
func TestUsersViewTakesColumnsFromDescendantsNotJustAnchors(t *testing.T) {
	users := viewByID(objectViews(testSchema(t)), ViewUsers)
	if users == nil {
		t.Fatal("there is no users view")
	}
	got := columnNames(users)
	for _, want := range []string{"cn", "uid", "mail"} {
		if !contains(got, want) {
			t.Errorf("column %q is missing from %v; it is permitted by inetOrgPerson, "+
				"which the filter matches", want, got)
		}
	}
	// And the ones nothing permits stay out.
	if contains(got, "uidNumber") {
		t.Errorf("uidNumber is a column in %v, and no class this view matches permits it", got)
	}
}

// A directory that defines no person class gets no users view. An empty view is
// worse than none: it looks like a directory with no users rather than like a
// question Alder cannot answer.
func TestAViewWithNoAnchorIsNotOffered(t *testing.T) {
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 2.5.6.5 NAME 'organizationalUnit' SUP top STRUCTURAL MUST ou )",
		},
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.11 NAME 'ou' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		},
	})
	views := objectViews(sch)
	if viewByID(views, ViewUsers) != nil {
		t.Error("a users view was offered by a schema with no person class")
	}
	if viewByID(views, ViewGroups) != nil {
		t.Error("a groups view was offered by a schema with no group class")
	}
	if viewByID(views, ViewOrganizationalUnits) == nil {
		t.Error("the organizational units view is missing, and its anchor is defined")
	}
}

// Several anchors are OR-ed, and the assertion is on the schema's own spelling
// of the class rather than on the name written in the spec.
func TestSeveralAnchorsAreJoined(t *testing.T) {
	sch := testSchema(t,
		"( 2.5.6.17 NAME 'groupOfUniqueNames' SUP top STRUCTURAL MUST ( uniqueMember $ cn ) )",
		"( 1.3.6.1.1.1.2.2 NAME 'posixGroup' SUP top STRUCTURAL MUST ( cn $ gidNumber ) )",
	)
	groups := viewByID(objectViews(sch), ViewGroups)
	if groups == nil {
		t.Fatal("there is no groups view")
	}
	want := "(|(objectClass=groupOfNames)(objectClass=groupOfUniqueNames)(objectClass=posixGroup))"
	if groups.Filter != want {
		t.Errorf("filter is\n  %q\nwant\n  %q", groups.Filter, want)
	}
	if !contains(columnNames(groups), "gidNumber") {
		t.Error("gidNumber is not a column, and posixGroup requires it")
	}
}

// An alias resolves to the same class. Asserting it twice would be harmless to
// the server and confusing to read, and the anchors list is shown to the user.
func TestAnAliasIsNotAssertedTwice(t *testing.T) {
	sch := testSchema(t)
	// 'account' aliased onto a class already anchored on.
	spec := viewSpec{
		id:      ViewUsers,
		label:   "Users",
		anchors: []string{"person", "2.5.6.6", "PERSON"},
	}
	view, ok := buildView(sch, spec)
	if !ok {
		t.Fatal("the view was dropped")
	}
	if view.Filter != "(objectClass=person)" {
		t.Errorf("filter is %q; the three names are one class", view.Filter)
	}
	if len(view.Anchors) != 1 {
		t.Errorf("anchors are %v, want one entry", view.Anchors)
	}
}

// A class name is server data. It reaches the filter through the builder, so a
// name carrying filter metacharacters becomes an escaped assertion rather than
// structure — the same rule every other filter in the codebase follows.
func TestAClassNameIsEscapedIntoTheFilter(t *testing.T) {
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			"( 1.2.3.4 NAME 'ev*il)(cn=x' SUP top STRUCTURAL MUST cn )",
		},
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		},
	})
	view, ok := buildView(sch, viewSpec{id: ViewUsers, anchors: []string{"ev*il)(cn=x"}})
	if !ok {
		t.Fatal("the view was dropped")
	}
	for _, raw := range []string{"*", ")(", "(cn=x"} {
		if strings.Contains(view.Filter, raw) {
			t.Fatalf("filter %q carries %q unescaped", view.Filter, raw)
		}
	}
	if want := `(objectClass=ev\2ail\29\28cn=x)`; view.Filter != want {
		t.Errorf("filter is %q, want %q", view.Filter, want)
	}
}

// The table is meant to be read, not scrolled sideways.
func TestColumnsAreCapped(t *testing.T) {
	sch := testSchema(t,
		"( 1.3.6.1.1.1.2.0 NAME 'posixAccount' SUP top AUXILIARY "+
			"MUST ( cn $ uid $ uidNumber $ gidNumber ) )",
	)
	users := viewByID(objectViews(sch), ViewUsers)
	if users == nil {
		t.Fatal("there is no users view")
	}
	if len(users.Columns) > maxColumns {
		t.Errorf("%d columns, and the cap is %d: %v",
			len(users.Columns), maxColumns, columnNames(users))
	}
}

// The heading is a convenience; the attribute name is the fact. Both travel, so
// the UI is never forced to choose one and hide the other.
func TestEveryColumnCarriesItsRealNameAndItsDescription(t *testing.T) {
	users := viewByID(objectViews(testSchema(t)), ViewUsers)
	if users == nil {
		t.Fatal("there is no users view")
	}
	for _, col := range users.Columns {
		if col.Attribute == "" {
			t.Error("a column has no attribute name")
		}
		if col.Label == "" {
			t.Errorf("column %q has no label", col.Attribute)
		}
		switch col.Attribute {
		case "cn":
			if col.Label != "Name" {
				t.Errorf("cn is labelled %q, want Name", col.Label)
			}
			if col.Desc == nil || *col.Desc != "Common name" {
				t.Errorf("cn carries desc %v, want the schema's own", col.Desc)
			}
		case "mail":
			if col.Desc == nil || *col.Desc != "RFC822 mailbox" {
				t.Errorf("mail carries desc %v, want the schema's own", col.Desc)
			}
		}
	}
}

// An attribute with no entry in the label table keeps its own name, rather than
// arriving blank or being guessed at.
func TestAnUnlabelledAttributeKeepsItsName(t *testing.T) {
	if got := columnLabel("displayName"); got != "displayName" {
		t.Errorf("displayName is labelled %q, want its own name", got)
	}
	// Case and attribute options both fold, so cn;lang-fr finds cn's heading.
	if got := columnLabel("CN;lang-fr"); got != "Name" {
		t.Errorf("CN;lang-fr is labelled %q, want Name", got)
	}
}

func TestNoSchemaMeansNoViews(t *testing.T) {
	if views := objectViews(nil); len(views) != 0 {
		t.Errorf("got %d views from no schema", len(views))
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// Membership is answered from what the entry's classes permit, not from what it
// currently holds. An empty group is still a group, and a control that appeared
// only once a group had a member would be useless for adding the first one.
func TestMembershipAttributesComeFromTheClassesNotTheValues(t *testing.T) {
	sch := testSchema(t,
		"( 2.5.6.17 NAME 'groupOfUniqueNames' SUP top STRUCTURAL MUST ( uniqueMember $ cn ) )",
		"( 1.3.6.1.1.1.2.2 NAME 'posixGroup' SUP top STRUCTURAL MUST ( cn $ gidNumber ) "+
			"MAY memberUid )",
	)

	for _, tc := range []struct {
		classes []string
		want    []string
	}{
		{[]string{"groupOfNames"}, []string{"member"}},
		{[]string{"groupOfUniqueNames"}, []string{"uniqueMember"}},
		{[]string{"posixGroup"}, []string{"memberUid"}},
		// An entry carrying both styles permits both, and they are reported in
		// a stable order so the UI does not shuffle between reads.
		{[]string{"groupOfNames", "groupOfUniqueNames"}, []string{"member", "uniqueMember"}},
		// Not a group.
		{[]string{"inetOrgPerson"}, nil},
		{[]string{"organizationalUnit"}, nil},
	} {
		got := membershipAttributes(sch, sch.Requirements(tc.classes))
		if !equalStrings(got, tc.want) {
			t.Errorf("classes %v give %v, want %v", tc.classes, got, tc.want)
		}
	}
}

// A membership attribute the server does not define is not offered, however
// standard its name.
func TestMembershipAttributesNeedTheServerToDefineThem(t *testing.T) {
	sch := schema.Load("cn=subschema", map[string][]string{
		schema.AttrObjectClasses: {
			"( 2.5.6.0 NAME 'top' ABSTRACT MUST objectClass )",
			// Permits uniqueMember, which the attribute list below omits.
			"( 2.5.6.17 NAME 'groupOfUniqueNames' SUP top STRUCTURAL MUST ( uniqueMember $ cn ) )",
		},
		schema.AttrAttributeTypes: {
			"( 2.5.4.0 NAME 'objectClass' SYNTAX 1.3.6.1.4.1.1466.115.121.1.38 )",
			"( 2.5.4.3 NAME 'cn' SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 )",
		},
	})
	if got := membershipAttributes(sch, sch.Requirements([]string{"groupOfUniqueNames"})); got != nil {
		t.Errorf("got %v; uniqueMember is not an attribute type this schema defines", got)
	}
}

// The creation form offers structural classes only. An abstract class cannot be
// instantiated and an auxiliary one cannot stand alone, so offering either
// produces an entry the server refuses.
func TestCreateClassesAreStructuralOnly(t *testing.T) {
	sch := testSchema(t,
		"( 1.2.9 NAME 'alderEmployee' SUP person AUXILIARY MAY title )",
		"( 1.2.10 NAME 'alderAbstractPerson' SUP person ABSTRACT )",
	)
	users := viewByID(objectViews(sch), ViewUsers)
	if users == nil {
		t.Fatal("there is no users view")
	}
	if users.CreateClasses == nil {
		t.Fatal("the users view offers no classes to create")
	}
	got := *users.CreateClasses
	for _, unwanted := range []string{"alderEmployee", "alderAbstractPerson"} {
		if contains(got, unwanted) {
			t.Errorf("%s is offered for creation and it is not structural: %v", unwanted, got)
		}
	}
	// The structural descendants are what should be there.
	for _, want := range []string{"person", "organizationalPerson", "inetOrgPerson"} {
		if !contains(got, want) {
			t.Errorf("%s is missing from the classes a user can be created as: %v", want, got)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
