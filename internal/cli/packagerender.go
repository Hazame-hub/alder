package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/api"
)

// Printing a package and what a target makes of it.
//
// Everything here comes from a document someone handed the client, so every
// string goes through safe(): a package is untrusted input, and a DN or a
// definition can carry anything a directory can hold.

func renderPackageInspection(w io.Writer, in api.PackageInspection) {
	title := safe(text(in.Title))
	if title == "" {
		title = "(untitled)"
	}
	writef(w, "%s\n", title)
	writef(w, "  package %s, created %s, checksum %s\n", safe(in.PackageId), safe(in.CreatedAt), in.Integrity)
	if description := safe(text(in.Description)); description != "" {
		writef(w, "  %s\n", description)
	}
	if method := in.Source.Method; method != nil {
		from := "  made from " + string(*method)
		if vendor := safe(text(in.Source.Vendor)); vendor != "" {
			from += " on " + vendor
		}
		if version := safe(text(in.Source.AlderVersion)); version != "" {
			from += ", by Alder " + version
		}
		writeln(w, from)
	}
	writeln(w)

	c := in.Counts
	writef(w, "%s: %d schema, %d data", plural(c.Changes, "change", "changes"), c.Schema, c.Data)
	if c.Destructive > 0 {
		writef(w, ", %d destructive", c.Destructive)
	}
	writeln(w)
	renderAssumptions(w, in.Assumptions)
	writeln(w)

	for _, id := range in.Order {
		for _, change := range in.Changes {
			if change.Id == id {
				renderPackageChange(w, change)
			}
		}
	}
	if len(in.Omitted) > 0 {
		writeln(w)
		writeln(w, "Left out of this package, with the reason:")
		for _, omitted := range in.Omitted {
			writef(w, "  %s %s: %s\n", safe(text(omitted.Kind)), safe(text(omitted.Subject)), omitted.Reason)
			if detail := safe(text(omitted.Detail)); detail != "" {
				writef(w, "      %s\n", detail)
			}
		}
	}
}

func renderAssumptions(w io.Writer, a api.PackageAssumptions) {
	var parts []string
	for _, context := range derefSlice(a.NamingContexts) {
		parts = append(parts, "naming context "+safe(context))
	}
	for _, oid := range derefSlice(a.SchemaOids) {
		parts = append(parts, "schema OID "+safe(oid))
	}
	for _, name := range derefSlice(a.ObjectClasses) {
		parts = append(parts, "object class "+safe(name))
	}
	if len(parts) == 0 {
		return
	}
	writef(w, "Assumes: %s\n", strings.Join(parts, ", "))
}

// text is an optional string as a string. The client's deref is for numbers.
func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefSlice(list *[]string) []string {
	if list == nil {
		return nil
	}
	return *list
}

func renderPackageChange(w io.Writer, change api.PackageChange) {
	mark := " "
	if change.Destructive {
		mark = "!"
	}
	writef(w, "%s %-10s %s\n", mark, change.Id, packageChangeLine(change))
	if len(derefSlice(change.DependsOn)) > 0 {
		writef(w, "             after %s\n", safe(strings.Join(derefSlice(change.DependsOn), ", ")))
	}
	if label := safe(text(change.Label)); label != "" {
		writef(w, "             %s\n", label)
	}
}

// packageChangeLine is one change in a line: what it does, to what.
func packageChangeLine(change api.PackageChange) string {
	if change.Schema != nil {
		s := change.Schema
		line := fmt.Sprintf("%s %s %s", s.Op, s.Element, safe(s.Oid))
		if name := definitionName(text(s.Definition)); name != "" {
			line += " " + safe(name)
		}
		return line
	}
	if change.Data != nil {
		d := change.Data
		line := fmt.Sprintf("%s %s", d.Type, safe(d.Dn))
		switch {
		case d.Type == api.PackageOpRename && d.NewSuperior != nil && d.NewRdn != nil:
			line += fmt.Sprintf(" -> %s,%s", safe(*d.NewRdn), safe(*d.NewSuperior))
		case d.Type == api.PackageOpRename && d.NewRdn != nil:
			line += " -> " + safe(*d.NewRdn)
		case d.Type == api.PackageOpRename && d.NewSuperior != nil:
			line += " -> under " + safe(*d.NewSuperior)
		case d.Type == api.PackageOpModify && d.Mods != nil:
			var names []string
			for _, m := range *d.Mods {
				names = append(names, string(m.Op)+" "+safe(m.Name))
			}
			line += ": " + strings.Join(names, ", ")
		}
		if change.Intent != nil && *change.Intent == api.PackageIntentDesired {
			line += " (desired state)"
		}
		return line
	}
	return string(change.Kind)
}

// definitionName is the first NAME in a definition, for the line. It is a
// display convenience: the OID is the identity.
func definitionName(definition string) string {
	at := strings.Index(definition, "NAME '")
	if at < 0 {
		return ""
	}
	rest := definition[at+len("NAME '"):]
	end := strings.IndexByte(rest, '\'')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func renderPackageValidation(w io.Writer, v api.PackageValidation) {
	writef(w, "Package %s against %s\n", safe(v.PackageId), targetLine(v.Target))
	writef(w, "  checksum %s\n", v.Integrity)
	writeln(w)

	c := v.Counts
	rows := []struct {
		n    int
		name string
	}{
		{c.Ready, "ready"}, {c.AlreadySatisfied, "already satisfied"}, {c.NoOp, "nothing to do"},
		{c.Conflict, "conflict"}, {c.DependencyMissing, "dependency missing"},
		{c.Unsupported, "unsupported here"}, {c.TargetIncompatible, "not for this directory"},
		{c.Unknown, "unknown"},
	}
	total := 0
	for _, row := range rows {
		total += row.n
	}
	writef(w, "%s\n", plural(total, "change", "changes"))
	for _, row := range rows {
		if row.n > 0 {
			writef(w, "  %3d %s\n", row.n, row.name)
		}
	}

	var unmet []string
	for _, a := range v.Assumptions {
		if !a.Satisfied {
			unmet = append(unmet, fmt.Sprintf("%s %s", a.Kind, safe(a.Value)))
		}
	}
	if len(unmet) > 0 {
		writeln(w)
		writef(w, "This package assumes %s, and this directory does not have that.\n", strings.Join(unmet, ", "))
	}

	writeln(w)
	for _, item := range sortedItems(v) {
		mark := " "
		if item.Destructive {
			mark = "!"
		}
		writef(w, "%s %-18s %-10s %s\n", mark, item.Status, item.Id, safe(text(item.Label)))
		for _, p := range derefProblems(item.Problems) {
			line := "      " + string(p.Code)
			if subject := safe(text(p.Subject)); subject != "" {
				line += " " + subject
			}
			writeln(w, line)
			if detail := safe(text(p.Detail)); detail != "" {
				writef(w, "        %s\n", detail)
			}
		}
	}
	writeln(w)
	writeln(w, "Nothing has been applied. alder plan --package shows what would be, and")
	writeln(w, "alder apply --package applies exactly the plan it shows.")
}

func derefProblems(problems *[]api.PackageProblem) []api.PackageProblem {
	if problems == nil {
		return nil
	}
	return *problems
}

// sortedItems lists the ready changes in the order they would be applied, then
// everything else by identifier: the work first, the reasons after.
func sortedItems(v api.PackageValidation) []api.PackageValidationItem {
	position := map[string]int{}
	for i, id := range v.Order {
		position[id] = i
	}
	out := append([]api.PackageValidationItem(nil), v.Items...)
	sort.SliceStable(out, func(i, j int) bool {
		left, leftOrdered := position[out[i].Id]
		right, rightOrdered := position[out[j].Id]
		switch {
		case leftOrdered && rightOrdered:
			return left < right
		case leftOrdered != rightOrdered:
			return leftOrdered
		}
		return out[i].Id < out[j].Id
	})
	return out
}

func targetLine(t api.PackageTarget) string {
	line := strings.Join(t.NamingContexts, ", ")
	if line == "" {
		line = "a directory holding no naming context"
	}
	if vendor := safe(text(t.Vendor)); vendor != "" {
		line += " (" + vendor + ")"
	}
	return safe(line)
}
