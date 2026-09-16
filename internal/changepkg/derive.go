package changepkg

import (
	"strings"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Working out what depends on what.
//
// A changeset is a list someone built in the order they happened to work in,
// and that order is not a dependency graph: it says nothing about an object
// class needing its attribute types, or an entry needing its parent. A package
// carries the graph, so the order it arrived in stops mattering.
//
// The edges here are the ones that can be read off the changes themselves.
// Anything Alder cannot see -- an entry that needs a group that another team
// creates -- is not invented; the operator can say so by editing the package,
// and the target reports what is missing when it validates.

// Derive adds the dependencies implied by the changes, keeping any the caller
// already stated. It never removes one.
func Derive(items []Item) []Item {
	out := append([]Item(nil), items...)
	index := newIndex(out)
	for i := range out {
		item := &out[i]
		var needs []string
		switch {
		case item.Kind == KindSchema && item.Schema != nil:
			needs = index.schemaNeeds(*item)
		case item.Kind == KindData && item.Data != nil:
			needs = index.dataNeeds(*item)
		}
		for _, id := range needs {
			if id != item.ID && !contains(item.DependsOn, id) {
				item.DependsOn = append(item.DependsOn, id)
			}
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// index is what the package provides, by the names other changes would use.
type index struct {
	// schemaAdds maps a folded OID or NAME to the item adding or replacing it.
	schemaAdds map[string]string
	// schemaDeletes maps a folded OID or NAME to the item removing it.
	schemaDeletes map[string]string
	// classOf maps a folded object class name to the item adding it, for
	// entries that use it.
	classOf map[string]string
	// attributeOf maps a folded attribute name to the item adding it.
	attributeOf map[string]string
	// entryAdds maps a folded DN to the item creating it.
	entryAdds map[string]string
	// entryDeletes maps a folded DN to the item removing it.
	entryDeletes map[string]string
	// references maps a folded OID or NAME to the items whose definitions name
	// it, so a removal can be ordered after the removals that free it.
	references map[string][]string
}

func newIndex(items []Item) *index {
	ix := &index{
		schemaAdds: map[string]string{}, schemaDeletes: map[string]string{},
		classOf: map[string]string{}, attributeOf: map[string]string{},
		entryAdds: map[string]string{}, entryDeletes: map[string]string{},
		references: map[string][]string{},
	}
	for _, item := range items {
		switch {
		case item.Kind == KindSchema && item.Schema != nil:
			ix.addSchema(item)
		case item.Kind == KindData && item.Data != nil:
			switch item.Data.Type {
			case OpAdd:
				ix.entryAdds[foldDN(item.Data.DN)] = item.ID
			case OpDelete:
				ix.entryDeletes[foldDN(item.Data.DN)] = item.ID
			}
		}
	}
	return ix
}

func (ix *index) addSchema(item Item) {
	s := item.Schema
	target := ix.schemaAdds
	if s.Op == SchemaDelete {
		target = ix.schemaDeletes
	}
	target[strings.ToLower(s.OID)] = item.ID
	names, _ := definitionNames(s)
	for _, name := range names {
		key := strings.ToLower(name)
		target[key] = item.ID
		if s.Op != SchemaDelete {
			switch s.Element {
			case ElementObjectClass:
				ix.classOf[key] = item.ID
			case ElementAttributeType:
				ix.attributeOf[key] = item.ID
			}
		}
	}
	for _, ref := range definitionReferences(s) {
		key := strings.ToLower(ref)
		ix.references[key] = append(ix.references[key], item.ID)
	}
}

// definitionReferences are the definitions a definition names.
func definitionReferences(s *SchemaChange) []string {
	if s.Definition == "" {
		return nil
	}
	switch s.Element {
	case ElementAttributeType:
		at, err := schema.ParseAttributeType(s.Definition)
		if err != nil || at.SuperName == "" {
			return nil
		}
		return []string{at.SuperName}
	case ElementObjectClass:
		oc, err := schema.ParseObjectClass(s.Definition)
		if err != nil {
			return nil
		}
		out := append([]string{}, oc.SuperNames...)
		out = append(out, oc.Must...)
		return append(out, oc.May...)
	}
	return nil
}

// schemaNeeds are the items a schema change must follow.
func (ix *index) schemaNeeds(item Item) []string {
	s := item.Schema
	var out []string
	if s.Op == SchemaDelete {
		// What still refers to this definition has to go first, or the server
		// refuses the removal.
		names, _ := definitionNames(s)
		for _, key := range append([]string{s.OID}, names...) {
			for _, id := range ix.references[strings.ToLower(key)] {
				if isDeleteOf(ix, id) {
					out = append(out, id)
				}
			}
		}
		return out
	}
	// What this definition names has to exist first, where this package is
	// what provides it.
	for _, ref := range definitionReferences(s) {
		if id, ok := ix.schemaAdds[strings.ToLower(ref)]; ok {
			out = append(out, id)
		}
	}
	return out
}

// isDeleteOf reports whether an item identifier belongs to a schema removal.
func isDeleteOf(ix *index, id string) bool {
	for _, held := range ix.schemaDeletes {
		if held == id {
			return true
		}
	}
	return false
}

// dataNeeds are the items a data change must follow.
func (ix *index) dataNeeds(item Item) []string {
	d := item.Data
	var out []string
	switch d.Type {
	case OpAdd:
		// The parent, where this package creates it, and any ancestor above it.
		for _, ancestor := range ancestors(d.DN) {
			if id, ok := ix.entryAdds[ancestor]; ok {
				out = append(out, id)
			}
		}
		// The schema this entry uses, where this package adds it.
		for _, class := range objectClassValues(d) {
			if id, ok := ix.classOf[strings.ToLower(class)]; ok {
				out = append(out, id)
			}
		}
		for _, a := range d.Attributes {
			if id, ok := ix.attributeOf[strings.ToLower(schema.BaseName(a.Name))]; ok {
				out = append(out, id)
			}
		}
	case OpModify:
		if id, ok := ix.entryAdds[foldDN(d.DN)]; ok {
			out = append(out, id)
		}
		for _, m := range d.Mods {
			if id, ok := ix.attributeOf[strings.ToLower(schema.BaseName(m.Name))]; ok {
				out = append(out, id)
			}
			if !strings.EqualFold(schema.BaseName(m.Name), "objectClass") {
				continue
			}
			for _, v := range m.Values {
				if id, ok := ix.classOf[strings.ToLower(v.Text)]; ok {
					out = append(out, id)
				}
			}
		}
	case OpRename:
		if id, ok := ix.entryAdds[foldDN(d.DN)]; ok {
			out = append(out, id)
		}
		if target, ok := renameTarget(d); ok {
			for _, ancestor := range ancestors(target) {
				if id, ok := ix.entryAdds[ancestor]; ok {
					out = append(out, id)
				}
			}
		}
	case OpDelete:
		// Children first: a directory refuses to delete an entry that has any.
		for folded, id := range ix.entryDeletes {
			if folded != foldDN(d.DN) && isBelow(folded, foldDN(d.DN)) {
				out = append(out, id)
			}
		}
	}
	return out
}

// ancestors are the folded DNs above one, nearest first.
func ancestors(target string) []string {
	parsed, err := dn.Parse(target)
	if err != nil {
		return nil
	}
	var out []string
	for len(parsed) > 1 {
		parsed = parsed.Parent()
		out = append(out, strings.ToLower(parsed.String()))
	}
	return out
}

func isBelow(folded, ancestor string) bool {
	child, err := dn.Parse(folded)
	if err != nil {
		return false
	}
	parent, err := dn.Parse(ancestor)
	if err != nil {
		return false
	}
	return len(child) > len(parent) && child.HasSuffix(parent)
}
