package config

import (
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Indexes (1.23).
//
// "Index mail for equality" is one intention and two entirely different
// writes. OpenLDAP keeps its indexes as values of one multi-valued setting on
// the database entry:
//
//	olcDbIndex: mail eq,sub
//
// 389 Directory Server keeps each one as an entry of its own beneath the
// backend:
//
//	dn: cn=mail,cn=index,cn=userRoot,cn=ldbm database,cn=plugins,cn=config
//	nsIndexType: eq
//	nsIndexType: sub
//
// Neither is translated into the other. What Alder does is report both as the
// same *kind* of thing -- an index on an attribute, with the types it covers,
// belonging to a backend -- so that a comparison can say "this server indexes
// mail and that one does not", which is the question an operator has. The
// change each server needs is then derived in that server's own terms.

// indexResource is one index, in the shape a comparison reports.
//
// The name is the backend and the attribute, never a position: an index is
// identified by what it indexes, and "the third value of olcDbIndex" is not
// something anybody can act on.
// The DN is where the index is written, which is not the same place on the
// two servers: the database entry on OpenLDAP, the index's own entry on 389
// DS. It is what a change is sent to, so it is passed in rather than guessed.
func indexResource(backend Resource, attribute, where string) Resource {
	return Resource{
		// An index is a tuning decision rather than part of what the backend
		// holds, and it is grouped with the other ones on both servers.
		Section: SectionPerformance,
		Kind:    KindIndex,
		Name:    strings.ToLower(backend.Name) + "/" + strings.ToLower(attribute),
		DN:      where,
		Label:   attribute,
	}
}

// indexAttribute is the attribute an index resource is about.
func indexAttribute(r Resource) string {
	if r.Label != "" {
		return r.Label
	}
	_, attribute, _ := strings.Cut(r.Name, "/")
	return attribute
}

// openldapEntryIndexes reports the indexes an OpenLDAP database entry
// declares, and names the attribute they were read from.
//
// A value may name several attributes at once -- "uid,cn eq,sub" is legal and
// means both -- so one value can produce two indexes. They are reported
// separately because that is what they are; what Alder will not do is rewrite
// such a value to remove one of them, and removeIndexRecord says so.
//
// The setting holds the value as slapd wrote it rather than the types alone,
// because the value is the unit: "uid,cn eq,sub" is one value, and what can
// be done to cn's index depends on uid sharing it. IndexTypesOf reads the
// types back out for a comparison that has to hold against 389 DS, where they
// are values of their own.
//
// The settings are read-only. An index is changed by creating or removing the
// index, not by replacing olcDbIndex on the database: a replace rewrites every
// index on that database at once, and slapd silently leaves the on-disk index
// stale until it is reindexed. The comparison acts on the object; the setting
// is there so the difference can be read.
func openldapEntryIndexes(entry *directory.Entry, resource Resource) ([]Resource, []Setting, string) {
	if resource.Kind != KindDatabase {
		return nil, nil, ""
	}
	var resources []Resource
	var settings []Setting
	for _, value := range entry.GetStrings("olcDbIndex") {
		attributes, _ := splitIndexValue(value)
		for _, attribute := range attributes {
			index := indexResource(resource, attribute, entry.DN.String())
			resources = append(resources, index)
			settings = append(settings, Setting{
				Section:    SectionPerformance,
				Key:        "olcDbIndex",
				Resource:   index.ID(),
				DN:         entry.DN.String(),
				Values:     []string{value},
				Type:       TypeStructured,
				Mutability: snapshot.MutabilityReadOnly,
				Comparison: snapshot.ComparisonRaw,
			})
		}
	}
	return resources, settings, "olcDbIndex"
}

// splitIndexValue reads "uid,cn eq,sub" into the attributes and the types.
// A value with no types at all indexes for presence only, which slapd writes
// as a bare attribute name.
func splitIndexValue(value string) ([]string, []string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	names, types, ok := strings.Cut(value, " ")
	var kinds []string
	if ok {
		for _, t := range strings.Split(types, ",") {
			if t = strings.TrimSpace(t); t != "" {
				kinds = append(kinds, t)
			}
		}
	}
	var attributes []string
	for _, name := range strings.Split(names, ",") {
		if name = strings.TrimSpace(name); name != "" {
			attributes = append(attributes, name)
		}
	}
	sort.Strings(kinds)
	return attributes, kinds
}

// indexValueFor renders the olcDbIndex value for one attribute.
func indexValueFor(attribute string, types []string) string {
	if len(types) == 0 {
		return attribute
	}
	sorted := append([]string(nil), types...)
	sort.Strings(sorted)
	return attribute + " " + strings.Join(sorted, ",")
}

// ldbmBase is where 389 DS keeps its backends, and so its indexes.
var ldbmBase = dn.MustParse("cn=ldbm database,cn=plugins,cn=config")

// ds389IsIndex reports an entry that is one index of a backend: a child of a
// cn=index container under cn=ldbm database. The container itself is not one,
// and neither is a VLV index, which is a different object with a different
// entry and is left where it is.
//
// Read through the DN type rather than by cutting the text at commas: a
// backend may be named anything, and a name holding an escaped comma would
// shift every position in a split string.
func ds389IsIndex(d dn.DN) bool {
	// cn=<attribute>,cn=index,cn=<backend>,cn=ldbm database,cn=plugins,cn=config
	return len(d) == 6 && d.HasSuffix(ldbmBase) && rdnIs(d.Parent(), "cn", "index")
}

// ds389IndexBackend finds the cn=index container an index entry belongs to,
// and the backend above it, from the resources already resolved.
func ds389IndexBackend(d dn.DN, parents map[string]Resource) (Resource, bool) {
	// cn=<attr>,cn=index,<backend>
	container := d.Parent()
	if !rdnIs(container, "cn", "index") {
		return Resource{}, false
	}
	backend, ok := parents[strings.ToLower(container.Parent().String())]
	return backend, ok
}

// rdnIs reports a DN whose own RDN is exactly this one assertion.
func rdnIs(d dn.DN, attrType, value string) bool {
	r := d.RDN()
	return len(r) == 1 && strings.EqualFold(r[0].Type, attrType) && strings.EqualFold(r[0].Value, value)
}

// createIndexRecord is the change that adds an index on this server.
func createIndexRecord(live *snapshot.ConfigSnapshot, want snapshot.ConfigResource,
	types []string) (directory.ChangeRecord, string) {
	attribute := indexAttribute(want)
	if attribute == "" {
		return directory.ChangeRecord{}, RefusalUnnamed
	}
	if len(types) == 0 {
		// Decided once, above the two branches: an index with no types is
		// an index for equality on both servers. Left to each branch, the
		// OpenLDAP value would have been a bare attribute name, which slapd
		// reads as presence only -- a different index from the one 389 DS
		// would have got from the same request.
		types = []string{"eq"}
	}
	backend, ok := indexBackendOf(live, want)
	if !ok {
		return directory.ChangeRecord{}, RefusalNoParent
	}
	parent, err := dn.Parse(backend.DN)
	if err != nil {
		return directory.ChangeRecord{}, RefusalNoParent
	}

	if strings.EqualFold(live.Source.Provider, snapshot.ProviderOpenLDAP) {
		// A value on the database entry, added beside the ones already there.
		return directory.ChangeRecord{
			DN:   parent,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModAdd, Name: "olcDbIndex",
				Values: [][]byte{[]byte(indexValueFor(attribute, types))}}},
		}, ""
	}

	// 389 DS: an entry of its own, under the backend's index container.
	target, err := dn.Parse("cn=" + dn.EscapeValue(attribute) + ",cn=index," + parent.String())
	if err != nil {
		return directory.ChangeRecord{}, RefusalUnnamed
	}
	values := make([][]byte, 0, len(types))
	for _, t := range types {
		values = append(values, []byte(t))
	}
	return directory.ChangeRecord{
		DN:   target,
		Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("top"), []byte("nsIndex")}},
			{Name: "cn", Values: [][]byte{[]byte(attribute)}},
			{Name: "nsSystemIndex", Values: [][]byte{[]byte("false")}},
			{Name: "nsIndexType", Values: values},
		},
	}, ""
}

// removeIndexRecord is the change that takes an index away.
func removeIndexRecord(live *snapshot.ConfigSnapshot, have snapshot.ConfigResource) (directory.ChangeRecord, string) {
	attribute := indexAttribute(have)
	if attribute == "" {
		return directory.ChangeRecord{}, RefusalUnnamed
	}
	resource, ok := live.ResourceByID(have.ID())
	if !ok {
		return directory.ChangeRecord{}, RefusalNotPresent
	}

	if strings.EqualFold(live.Source.Provider, snapshot.ProviderOpenLDAP) {
		// The entry the value is written on, which is the database's.
		target, err := dn.Parse(resource.DN)
		if err != nil {
			return directory.ChangeRecord{}, RefusalNotPresent
		}
		raw, shared, ok := openldapIndexValue(live, resource)
		if !ok {
			return directory.ChangeRecord{}, RefusalNotPresent
		}
		if shared {
			// The value names other attributes too. Removing this one means
			// rewriting a value that is also somebody else's index, and a
			// rewrite is not a deletion: Alder says so rather than doing it.
			return directory.ChangeRecord{}, RefusalIndexShared
		}
		// The value deleted is the one the server holds, character for
		// character, rather than one rebuilt from the parts: slapd matches on
		// the value, and a rebuilt one that differs by a space deletes
		// nothing while reporting success.
		return directory.ChangeRecord{
			DN:   target,
			Type: directory.ChangeModify,
			Mods: []directory.Mod{{Op: directory.ModDelete, Name: "olcDbIndex",
				Values: [][]byte{[]byte(raw)}}},
		}, ""
	}

	if resource.DN == "" {
		return directory.ChangeRecord{}, RefusalNotPresent
	}
	if ds389SystemIndex(live, *resource) {
		return directory.ChangeRecord{}, RefusalSystemIndex
	}
	target, err := dn.Parse(resource.DN)
	if err != nil {
		return directory.ChangeRecord{}, RefusalNotPresent
	}
	return directory.ChangeRecord{DN: target, Type: directory.ChangeDelete}, ""
}

// indexBackendOf finds the backend an index belongs to, by the name it shares
// with it.
func indexBackendOf(live *snapshot.ConfigSnapshot, index snapshot.ConfigResource) (snapshot.ConfigResource, bool) {
	backendName, _, ok := strings.Cut(index.Name, "/")
	if !ok {
		return snapshot.ConfigResource{}, false
	}
	for _, kind := range []string{KindDatabase, KindBackend} {
		if r, ok := live.ResourceByID(kind + ":" + strings.ToLower(backendName)); ok {
			return *r, true
		}
	}
	return snapshot.ConfigResource{}, false
}

// openldapIndexValue is the olcDbIndex value this index is written in, as the
// server holds it, and whether that value names other attributes too.
func openldapIndexValue(live *snapshot.ConfigSnapshot, index *snapshot.ConfigResource) (string, bool, bool) {
	for _, setting := range live.Settings {
		if !strings.EqualFold(setting.Key, "olcDbIndex") ||
			!strings.EqualFold(setting.Resource, index.ID()) || len(setting.Values) == 0 {
			continue
		}
		raw := setting.Values[0]
		attributes, _ := splitIndexValue(raw)
		return raw, len(attributes) > 1, true
	}
	return "", false, false
}

// ds389SystemIndex reports an index the server maintains for itself.
func ds389SystemIndex(live *snapshot.ConfigSnapshot, index snapshot.ConfigResource) bool {
	for _, setting := range live.Settings {
		if setting.DN == index.DN && strings.EqualFold(setting.Key, "nsSystemIndex") {
			for _, v := range setting.Values {
				if strings.EqualFold(v, "true") {
					return true
				}
			}
		}
	}
	return false
}
