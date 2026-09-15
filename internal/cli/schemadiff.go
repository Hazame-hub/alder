package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hazame-hub/alder/internal/api"
)

// Schema comparisons, as the command line shows and stages them.
//
// Alder does the comparing; this file only prints what it answered and picks
// out the change requests a person named. It parses no definition and decides
// no difference.

func renderSchemaDiff(w io.Writer, d api.Diff, sourceName, targetName string, summaryOnly, sourceLive bool) {
	s := d.Schema
	writef(w, "Source: %s\n", schemaSideLabel(sourceName, d.Source))
	writef(w, "Target: %s\n", schemaSideLabel(targetName, d.Target))
	writeln(w, "Schema, compared by OID. Added means defined in the target and not in the source; removed, in the source and not in the target.")
	writeln(w)

	writef(w, "%-16s %9s %9s %9s %9s %9s %9s %9s\n", "", "compared", "added", "modified", "removed", "metadata", "unchanged", "unknown")
	for _, row := range []struct {
		name string
		c    api.SchemaDiffCounts
	}{{"attribute types", s.AttributeTypes}, {"object classes", s.ObjectClasses}} {
		writef(w, "%-16s %9d %9d %9d %9d %9d %9d %9d\n", row.name, row.c.Compared, row.c.Added, row.c.Modified,
			row.c.Removed, row.c.MetadataOnly, row.c.Unchanged, row.c.Unknown)
	}
	if s.AttributeTypes.MetadataOnly+s.ObjectClasses.MetadataOnly > 0 {
		writeln(w, "Metadata differences are in X- extensions only: reported, never proposed as changes.")
	}

	if !d.Complete {
		writeln(w)
		writeln(w, "INCOMPLETE: a definition could not be parsed, so this is not the whole answer and no removal is proposed.")
		if d.Reasons != nil {
			for _, r := range *d.Reasons {
				writef(w, "  %s: %s\n", r.Code, safe(r.Detail))
			}
		}
	}
	if d.CrossVendor {
		writeln(w, "The sides come from different server products, or one did not identify itself;")
		writeln(w, "server-defined schema and extensions can differ for that reason alone.")
	}
	if summaryOnly || len(s.Items) == 0 {
		return
	}

	for _, it := range s.Items {
		writeln(w)
		names := ""
		if it.Names != nil && len(*it.Names) > 0 {
			names = " " + safe(strings.Join(*it.Names, ", "))
		}
		writef(w, "%-13s %-13s %s%s\n", it.Kind, it.Element, safe(it.Oid), names)
		if it.Fields != nil {
			for _, f := range *it.Fields {
				writef(w, "  %s (%s)\n", safe(f.Field), f.Category)
				if f.Source != nil {
					for _, v := range *f.Source {
						writef(w, "    - %s\n", safe(v))
					}
				}
				if f.Target != nil {
					for _, v := range *f.Target {
						writef(w, "    + %s\n", safe(v))
					}
				}
			}
		}
		if it.Problems != nil && len(*it.Problems) > 0 {
			codes := make([]string, 0, len(*it.Problems))
			for _, p := range *it.Problems {
				codes = append(codes, string(p))
			}
			writef(w, "          problems: %s\n", strings.Join(codes, ", "))
		}
		if !sourceLive || it.Candidate == nil {
			continue
		}
		cand := it.Candidate
		switch {
		case cand.Blocked != nil:
			writef(w, "          no change offered: %s\n", *cand.Blocked)
		case cand.Destructive:
			writef(w, "          change offered: removes the definition (select with --stage-deletion %s)\n", safe(it.Oid))
		case len(cand.Changes) > 0:
			writef(w, "          change offered: %s (select with --stage %s)\n",
				plural(len(cand.Changes), "request", "requests"), safe(it.Oid))
		}
		if cand.Blocked == nil && cand.Impact != nil && len(*cand.Impact) > 0 {
			codes := make([]string, 0, len(*cand.Impact))
			for _, p := range *cand.Impact {
				codes = append(codes, string(p))
			}
			writef(w, "          impact: %s\n", strings.Join(codes, ", "))
		}
		if cand.Blocked == nil && cand.Requires != nil && len(*cand.Requires) > 0 {
			writef(w, "          stage together with: %s\n", safe(strings.Join(*cand.Requires, ", ")))
		}
	}
}

func schemaSideLabel(name string, s api.DiffSideSummary) string {
	vendor := ""
	if s.Vendor != nil && *s.Vendor != "" {
		vendor = ", " + safe(*s.Vendor)
	}
	if s.Kind == api.DiffSideKindLive {
		return fmt.Sprintf("%s (the schema at %s%s)", name, safe(s.Base), vendor)
	}
	details := []string{"the schema at " + safe(s.Base) + vendor}
	if s.DefinitionCount != nil {
		details = append(details, plural(*s.DefinitionCount, "definition", "definitions"))
	}
	if s.CreatedAt != nil {
		details = append(details, "captured "+safe(*s.CreatedAt))
	}
	if s.Integrity != nil {
		details = append(details, "checksum "+string(*s.Integrity))
	}
	return fmt.Sprintf("%s (%s)", name, strings.Join(details, ", "))
}

// stageSchemaSelected writes the change requests Alder derived for the schema
// differences named on the command line, in the dependency order Alder gave.
// Every difference is named by OID; a removal is named as one; and a change
// that needs another is refused unless that one is named too, because leaving
// it out would plan a change the directory refuses.
func stageSchemaSelected(env *Env, d api.Diff, body []byte, o diffOptions) error {
	var raw struct {
		Schema struct {
			Items []struct {
				Key       string `json:"key"`
				Candidate *struct {
					Changes []json.RawMessage `json:"changes"`
				} `json:"candidate"`
			} `json:"items"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || d.Schema == nil || len(raw.Schema.Items) != len(d.Schema.Items) {
		return failf("unexpected_response", "cannot read the change requests in Alder's comparison")
	}
	items := d.Schema.Items

	find := func(ref string) (int, error) {
		want := strings.ToLower(strings.TrimSpace(ref))
		var found []int
		for i, it := range items {
			if strings.ToLower(it.Key) == want || strings.ToLower(it.Oid) == want {
				found = append(found, i)
			}
		}
		switch len(found) {
		case 0:
			return 0, notApplicable("selection_not_found",
				"no schema difference has OID %s; name an OID, or attributeType:OID or objectClass:OID, as diff prints it", safe(ref))
		case 1:
			return found[0], nil
		}
		return 0, notApplicable("selection_ambiguous",
			"%s is the OID of an attribute type and of an object class; name it as attributeType:%s or objectClass:%s",
			safe(ref), safe(ref), safe(ref))
	}
	selected := map[string]int{}
	pick := func(ref string, deletion bool) error {
		i, err := find(ref)
		if err != nil {
			return err
		}
		it := items[i]
		c := it.Candidate
		switch {
		case c == nil:
			return notApplicable("selection_not_applicable", "%s is %s, and Alder offers no change for it", safe(ref), it.Kind)
		case c.Blocked != nil:
			return notApplicable("selection_blocked", "%s offers no change: %s", safe(ref), *c.Blocked)
		case c.Destructive && !deletion:
			return notApplicable("deletion_not_selected", "the change for %s removes the definition; a removal is selected with --stage-deletion, never with --stage", safe(ref))
		case !c.Destructive && deletion:
			return notApplicable("not_a_deletion", "the change for %s is not a removal; select it with --stage", safe(ref))
		}
		selected[it.Key] = i
		return nil
	}
	for _, ref := range o.stage {
		if err := pick(ref, false); err != nil {
			return err
		}
	}
	for _, ref := range o.stageDeletion {
		if err := pick(ref, true); err != nil {
			return err
		}
	}
	for key, i := range selected {
		if reqs := items[i].Candidate.Requires; reqs != nil {
			for _, needed := range *reqs {
				if _, ok := selected[needed]; !ok {
					return notApplicable("dependency_required",
						"%s needs %s staged with it: select that too, or leave both out", safe(key), safe(needed))
				}
			}
		}
	}

	changes := []json.RawMessage{}
	placed := 0
	for _, key := range d.Schema.Order {
		if i, ok := selected[key]; ok {
			changes = append(changes, raw.Schema.Items[i].Candidate.Changes...)
			placed++
		}
	}
	if placed != len(selected) {
		return failf("unexpected_response", "Alder's comparison did not place every selected difference in its order")
	}
	doc, err := json.MarshalIndent(changes, "", "  ")
	if err != nil {
		return failf("output", "cannot encode the selected change requests: %v", err)
	}
	if err := writeFile(o.changesOut, o.force, func(w io.Writer) error {
		_, werr := w.Write(append(doc, '\n'))
		return werr
	}, nil); err != nil {
		return err
	}
	writef(env.Stderr, "Wrote %d change request(s) for %d selected difference(s), in dependency order, to %s.\n"+
		"Review them with: alder plan --changes %s\n", len(changes), len(selected), o.changesOut, o.changesOut)
	return nil
}
