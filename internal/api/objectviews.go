package api

import (
	"sort"

	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

// Users, groups and organizational units, derived from the schema.
//
// A directory has no notion of a "user". It has object classes, and entries
// that carry one. So a view is a saved search, and the whole job here is
// working out — from what this server published, and nothing else — which
// search to save and which columns are worth showing.
//
// Two things keep it honest. The anchor classes are standards-track: RFC 4519
// for person and the organizational classes, RFC 4524 for account, RFC 2307 for
// the posix ones, and the dynamic-group class where a server defines one. None
// is a vendor name, and nothing here branches on which server answered. And a
// class is used only when the connected server actually defines it, so a
// directory with no person class gets no Users view rather than one that always
// returns nothing.
//
// Site classes need no special handling. An entry carries every superclass in
// its own objectClass attribute, so myCompanyPerson SUP person is matched by
// (objectClass=person) without ever being named here.

// maxColumns caps how wide a view gets. The table is meant to be read at a
// glance; past five attributes it is a spreadsheet, and the entry panel is one
// click away for everything the table leaves out.
const maxColumns = 5

type viewSpec struct {
	id    ObjectViewId
	label string
	desc  string
	// anchors are asserted on objectClass in the filter, where defined.
	anchors []string
	// columns are candidates in preference order. One survives when the schema
	// defines the attribute and some class the view matches permits it.
	columns []string
}

var viewSpecs = []viewSpec{
	{
		id:      ViewUsers,
		label:   "Users",
		desc:    "Entries carrying a person or account class.",
		anchors: []string{"person", "account", "posixAccount"},
		columns: []string{
			"cn", "uid", "mail", "telephoneNumber", "uidNumber", "title", "sn",
		},
	},
	{
		id:    ViewGroups,
		label: "Groups",
		desc:  "Entries whose purpose is to hold members.",
		// The membership styles a directory actually uses — DN-valued,
		// name-valued and URL-valued — plus posix groups, which are none of them.
		anchors: []string{
			"groupOfNames", "groupOfUniqueNames", "posixGroup", "groupOfURLs",
		},
		columns: []string{"cn", "description", "gidNumber", "ou"},
	},
	{
		id:      ViewOrganizationalUnits,
		label:   "Organizational units",
		desc:    "The containers the directory is organised into.",
		anchors: []string{"organizationalUnit"},
		columns: []string{"ou", "description", "l", "telephoneNumber"},
	},
}

// membershipAttrs are the attributes a directory holds members in.
//
// Standards-track names, like the anchor classes above: RFC 4519 for member and
// uniqueMember, RFC 2307 for memberUid, and the dynamic-group draft for
// memberURL. One is used only where the connected server defines it and the
// entry's own classes permit it, so an entry that is not a group reports none
// and gets no membership controls.
var membershipAttrs = []string{"member", "uniqueMember", "memberUid", "memberURL"}

// membershipAttributes returns the membership attributes this entry may hold.
//
// It answers from the class requirements rather than from the values present,
// so an empty group is still a group. Reading it off the values would mean the
// controls for adding the first member appear only after somebody has added the
// first member by some other route.
func membershipAttributes(sch *schema.Schema, req schema.AttributeRequirements) []string {
	permitted := make(map[string]bool, len(req.Must)+len(req.May))
	for _, name := range req.Must {
		permitted[foldName(name)] = true
	}
	for _, name := range req.May {
		permitted[foldName(name)] = true
	}

	var out []string
	for _, candidate := range membershipAttrs {
		at := sch.AttributeType(candidate)
		if at == nil || !permitted[foldName(at.Name())] {
			continue
		}
		out = append(out, at.Name())
	}
	return out
}

// columnLabels gives a heading a reader recognises.
//
// Deliberately small, and covering only the attributes the views above ask for.
// An attribute that is not here keeps its own name as its heading, which is the
// right answer rather than a gap: the real name is shown beside the label in
// every case, so the label is a convenience and never the only thing the reader
// is told.
var columnLabels = map[string]string{
	"cn":              "Name",
	"description":     "Description",
	"gidnumber":       "GID number",
	"l":               "Locality",
	"mail":            "Email",
	"ou":              "Unit",
	"sn":              "Surname",
	"telephonenumber": "Telephone",
	"title":           "Title",
	"uid":             "User ID",
	"uidnumber":       "UID number",
}

// objectViews builds the views this schema supports, ordered as viewSpecs is.
// A spec whose anchors are all undefined is dropped rather than shipped empty.
func objectViews(sch *schema.Schema) []ObjectView {
	views := make([]ObjectView, 0, len(viewSpecs))
	if sch == nil {
		return views
	}
	for _, spec := range viewSpecs {
		view, ok := buildView(sch, spec)
		if !ok {
			continue
		}
		views = append(views, view)
	}
	return views
}

func buildView(sch *schema.Schema, spec viewSpec) (ObjectView, bool) {
	anchors := resolveClasses(sch, spec.anchors)
	if len(anchors) == 0 {
		return ObjectView{}, false
	}

	f, err := anchorFilter(anchors)
	if err != nil {
		// Only reachable if the schema published a class name that cannot be an
		// assertion value, which would make the filter a lie. Dropping the view
		// beats shipping one that cannot be run.
		return ObjectView{}, false
	}

	return ObjectView{
		Id:            spec.id,
		Label:         spec.label,
		Description:   ptrIfSet(spec.desc),
		Filter:        f,
		Anchors:       classNames(anchors),
		Columns:       viewColumns(sch, spec, anchors),
		CreateClasses: ptrIfAny(structuralNames(withDescendants(sch, anchors))),
	}, true
}

// resolveClasses keeps the named classes this server defines, in the order
// named, without duplicates. Two names can resolve to one class when one is an
// alias of the other, and the filter must not assert it twice.
func resolveClasses(sch *schema.Schema, names []string) []*schema.ObjectClass {
	var out []*schema.ObjectClass
	seen := map[*schema.ObjectClass]bool{}
	for _, name := range names {
		oc := sch.ObjectClass(name)
		if oc == nil || seen[oc] {
			continue
		}
		seen[oc] = true
		out = append(out, oc)
	}
	return out
}

// anchorFilter renders the objectClass assertion for a view.
//
// Built with the filter package rather than by concatenation, for the reason
// every other filter in this codebase is: a class name arrives from the
// server's schema, and that is not a reason to trust it into a filter
// unescaped.
func anchorFilter(anchors []*schema.ObjectClass) (string, error) {
	subs := make([]filter.Filter, 0, len(anchors))
	for _, oc := range anchors {
		subs = append(subs, filter.Equal("objectClass", oc.Name()))
	}
	if len(subs) == 1 {
		return subs[0].Render()
	}
	return filter.Or(subs...).Render()
}

// viewColumns keeps the candidate columns this directory can actually fill.
//
// The permitted set is computed over every class the view matches, not just the
// anchors, because that is what the filter selects. person permits no mail;
// inetOrgPerson does, and an inetOrgPerson entry is a user. Deriving columns
// from the anchors alone would drop the most useful column in the table on the
// grounds that a superclass does not mention it.
func viewColumns(sch *schema.Schema, spec viewSpec, anchors []*schema.ObjectClass) []ObjectViewColumn {
	req := sch.Requirements(classNames(withDescendants(sch, anchors)))

	permitted := make(map[string]bool, len(req.Must)+len(req.May))
	for _, name := range req.Must {
		permitted[foldName(name)] = true
	}
	for _, name := range req.May {
		permitted[foldName(name)] = true
	}

	cols := make([]ObjectViewColumn, 0, maxColumns)
	for _, candidate := range spec.columns {
		if len(cols) == maxColumns {
			break
		}
		at := sch.AttributeType(candidate)
		if at == nil || !permitted[foldName(at.Name())] {
			continue
		}
		cols = append(cols, ObjectViewColumn{
			Attribute: at.Name(),
			Label:     columnLabel(at.Name()),
			Desc:      ptrIfSet(at.Desc),
		})
	}
	return cols
}

// structuralNames keeps the structural classes of a set, sorted.
//
// Only a structural class can be the class of a new entry — an abstract one
// cannot be instantiated and an auxiliary one has to accompany a structural
// one — so these are exactly the classes a creation form may offer.
func structuralNames(classes []*schema.ObjectClass) []string {
	var out []string
	for _, oc := range classes {
		if oc.Kind == schema.KindStructural {
			out = append(out, oc.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ptrIfAny omits an empty list rather than sending one.
func ptrIfAny(v []string) *[]string {
	if len(v) == 0 {
		return nil
	}
	return &v
}

// withDescendants returns the anchors plus every class inheriting from one,
// which together are exactly the classes the view's filter matches.
func withDescendants(sch *schema.Schema, anchors []*schema.ObjectClass) []*schema.ObjectClass {
	isAnchor := make(map[*schema.ObjectClass]bool, len(anchors))
	for _, oc := range anchors {
		isAnchor[oc] = true
	}
	out := append([]*schema.ObjectClass(nil), anchors...)
	for _, oc := range sch.ObjectClasses {
		if isAnchor[oc] {
			continue
		}
		for _, sup := range sch.Supers(oc) {
			if isAnchor[sup] {
				out = append(out, oc)
				break
			}
		}
	}
	return out
}

// columnLabel is keyed on the folded name — see foldName in convert.go, which
// also strips attribute options, so cn;lang-fr finds the cn heading.
func columnLabel(attr string) string {
	if label, ok := columnLabels[foldName(attr)]; ok {
		return label
	}
	return attr
}
