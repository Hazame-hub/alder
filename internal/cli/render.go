package cli

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/hazame-hub/alder/internal/api"
)

// Human output. Every string that came from a directory goes through safe.

// maxValuesShown bounds the values printed per attribute side. The JSON carries
// everything Alder sent.
const maxValuesShown = 20

func renderDiff(w io.Writer, d api.Diff, sourceName, targetName string, summaryOnly, sourceLive bool) {
	if d.Kind == api.StateKindSchema && d.Schema != nil {
		renderSchemaDiff(w, d, sourceName, targetName, summaryOnly, sourceLive)
		return
	}
	if d.Kind == api.StateKindConfig && d.Config != nil {
		renderConfigDiff(w, d, sourceName, targetName, summaryOnly, sourceLive)
		return
	}
	writef(w, "Source: %s\n", sideLabel(sourceName, d.Source))
	writef(w, "Target: %s\n", sideLabel(targetName, d.Target))
	writeln(w, "Added means in the target and not in the source; removed, in the source and not in the target.")
	writeln(w)

	c := d.Counts
	writef(w, "%d entries compared\n", c.Compared)
	width := len(fmt.Sprint(maxInt(c.Added, c.Modified, c.Removed, c.Renamed, c.Unchanged, c.Unknown)))
	for _, row := range []struct {
		n    int
		name string
	}{{c.Added, "added"}, {c.Modified, "modified"}, {c.Removed, "removed"}, {c.Renamed, "renamed"},
		{c.Unchanged, "unchanged"}, {c.Unknown, "unknown"}} {
		writef(w, "  %*d %s\n", width, row.n, row.name)
	}

	if !d.Complete {
		writeln(w)
		writeln(w, "INCOMPLETE: this comparison could not see everything, so it is not the whole answer.")
		if d.Reasons != nil {
			for _, r := range *d.Reasons {
				writef(w, "  %s: %s\n", r.Code, safe(r.Detail))
			}
		}
	}
	if d.CrossVendor {
		writeln(w, "The sides come from different server products, or one did not identify itself;")
		writeln(w, "server-specific attributes can differ for that reason alone.")
	}
	var byBytes []string
	if d.ComparedByBytes != nil {
		byBytes = append(byBytes, *d.ComparedByBytes...)
	}
	if d.RuleDifferences != nil {
		byBytes = append(byBytes, *d.RuleDifferences...)
	}
	if len(byBytes) > 0 {
		writef(w, "Compared byte for byte, with no shared equality rule: %s\n", safe(strings.Join(byBytes, ", ")))
	}
	if d.OperationalIgnored {
		writeln(w, "Operational attributes were not compared: only one side captured them.")
	}
	if summaryOnly || len(d.Items) == 0 {
		return
	}

	for _, it := range d.Items {
		writeln(w)
		dnText := ""
		switch {
		case it.Kind == api.DiffKindRenamed && it.SourceDn != nil && it.TargetDn != nil:
			dnText = safe(*it.SourceDn) + "\n          -> " + safe(*it.TargetDn)
		case it.TargetDn != nil:
			dnText = safe(*it.TargetDn)
		case it.SourceDn != nil:
			dnText = safe(*it.SourceDn)
		}
		writef(w, "%-9s %s\n", it.Kind, dnText)
		if it.Reason != nil {
			writef(w, "          could not be decided: %s\n", *it.Reason)
		}
		if it.Attributes != nil {
			for _, a := range *it.Attributes {
				renderAttributeChange(w, a)
			}
		}
		if sourceLive && it.Candidate != nil {
			cand := it.Candidate
			switch {
			case cand.Blocked != nil:
				writef(w, "          no change offered: %s\n", *cand.Blocked)
			case cand.Destructive:
				writeln(w, "          change offered: deletes the entry (select with --stage-deletion)")
			case len(cand.Changes) > 0:
				writef(w, "          change offered: %s (select with --stage)\n", plural(len(cand.Changes), "request", "requests"))
			}
		}
	}
}

func renderAttributeChange(w io.Writer, a api.DiffAttributeChange) {
	var notes []string
	if a.Operational != nil && *a.Operational {
		notes = append(notes, "operational")
	}
	if a.ComparedByBytes != nil && *a.ComparedByBytes {
		notes = append(notes, "compared byte for byte")
	}
	if a.UnknownReason != nil {
		notes = append(notes, "could not be decided: "+string(*a.UnknownReason))
	}
	if a.Sensitive != nil && *a.Sensitive {
		notes = append(notes, fmt.Sprintf("sensitive, values never shown: %s -> %s",
			plural(deref(a.WithheldSource), "value", "values"), plural(deref(a.WithheldTarget), "value", "values")))
	}
	head := "  " + safe(a.Name)
	if len(notes) > 0 {
		head += "  (" + strings.Join(notes, "; ") + ")"
	}
	writeln(w, head)
	printValues(w, "-", a.Removed, deref(a.RemovedOmitted))
	printValues(w, "+", a.Added, deref(a.AddedOmitted))
}

func printValues(w io.Writer, mark string, values *[]api.SnapshotValue, omitted int) {
	shown := 0
	if values != nil {
		for _, v := range *values {
			if shown == maxValuesShown {
				break
			}
			writef(w, "    %s %s\n", mark, valueText(v))
			shown++
		}
		omitted += len(*values) - shown
	}
	if omitted > 0 {
		writef(w, "    %s ... and %d more\n", mark, omitted)
	}
}

func valueText(v api.SnapshotValue) string {
	switch {
	case v.Text != nil:
		return safe(*v.Text)
	case v.Base64 != nil:
		n, err := base64.StdEncoding.DecodeString(*v.Base64)
		if err != nil {
			return "(binary value)"
		}
		return fmt.Sprintf("(binary value, %d bytes)", len(n))
	}
	return "(empty)"
}

func sideLabel(name string, s api.DiffSideSummary) string {
	scope := fmt.Sprintf("%s of %s", s.Scope, safe(s.Base))
	if s.Filter != "" && s.Filter != "(objectClass=*)" {
		scope += " matching " + safe(s.Filter)
	}
	vendor := ""
	if s.Vendor != nil && *s.Vendor != "" {
		vendor = ", " + safe(*s.Vendor)
	}
	if s.Kind == api.DiffSideKindLive {
		return fmt.Sprintf("%s (%s%s)", name, scope, vendor)
	}
	details := []string{scope, plural(s.EntryCount, "entry", "entries")}
	if s.CreatedAt != nil {
		details = append(details, "captured "+safe(*s.CreatedAt))
	}
	if s.Integrity != nil {
		details = append(details, "checksum "+string(*s.Integrity))
	}
	return fmt.Sprintf("%s (%s%s)", safe(name), strings.Join(details, ", "), vendor)
}

func renderPlan(w io.Writer, p api.Plan, showItems bool) {
	c := p.Counts
	invalid := deref(c.Invalid)
	writef(w, "Plan: %s examined\n", plural(c.Examined, "change", "changes"))
	width := len(fmt.Sprint(maxInt(c.Add, c.Modify, c.Delete, c.Rename, c.SetPassword, c.Unchanged, c.Conflict, invalid)))
	for _, row := range []struct {
		n    int
		name string
	}{{c.Add, "add"}, {c.Modify, "modify"}, {c.Delete, "delete"}, {c.Rename, "rename"},
		{c.SetPassword, "set password"}, {c.Unchanged, "unchanged"}, {c.Conflict, "conflict"}, {invalid, "invalid"}} {
		writef(w, "  %*d %s\n", width, row.n, row.name)
	}

	if p.Impact != nil {
		k, m, r := p.Impact.Kinds, p.Impact.Membership, p.Impact.References
		writef(w, "Touches: %d data, %d schema, %d configuration\n", k.Data, k.Schema, k.Config)
		if m.Gained+m.Removed > 0 {
			writef(w, "Membership: %d gained, %d removed, across %s\n", m.Gained, m.Removed, plural(m.Groups, "group", "groups"))
		}
		if r.Analysed && r.Found > 0 {
			writef(w, "References: %d found to entries this deletes or renames, %d would be left dangling\n", r.Found, r.Dangling)
		}
	}
	if p.Subtrees != nil {
		for _, s := range *p.Subtrees {
			writef(w, "Deletes the subtree %s (%s)\n", safe(s.Root), plural(s.Entries, "entry", "entries"))
		}
	}
	if p.Warnings != nil {
		for _, warning := range *p.Warnings {
			writef(w, "Warning: %s\n", safe(warning))
		}
	}
	if !showItems {
		return
	}

	for _, it := range p.Items {
		writeln(w)
		var notes []string
		if it.Kind != nil && *it.Kind != api.PlanTargetData {
			notes = append(notes, string(*it.Kind))
		}
		if it.Intent != nil && *it.Intent == api.PlanIntentDesired {
			notes = append(notes, "desired state")
		}
		line := fmt.Sprintf("%-12s %s", it.Action, safe(it.Dn))
		if len(notes) > 0 {
			line += "  (" + strings.Join(notes, ", ") + ")"
		}
		writeln(w, line)
		if it.Problem != nil {
			problem := string(it.Problem.Code)
			if it.Problem.Attribute != nil {
				problem += " (" + safe(*it.Problem.Attribute) + ")"
			}
			writef(w, "             problem: %s\n", problem)
		}
		if it.Reason != nil && *it.Reason != "" {
			writef(w, "             %s\n", safe(*it.Reason))
		}
		if it.Membership != nil {
			for _, m := range *it.Membership {
				writef(w, "             %s: %d gained, %d removed\n", safe(m.Attribute), len(m.Gained), len(m.Removed))
			}
		}
		if it.References != nil && it.References.Count > 0 {
			writef(w, "             named by %s; %d would be left dangling\n",
				plural(it.References.Count, "entry", "entries"), it.References.Dangling)
		}
		if it.SkippedAttributes != nil && len(*it.SkippedAttributes) > 0 {
			writef(w, "             not enforced, the directory owns them: %s\n", safe(strings.Join(*it.SkippedAttributes, ", ")))
		}
		if it.Preview != nil && it.Record != nil {
			writeln(w)
			for _, l := range strings.Split(strings.TrimRight(it.Preview.Ldif, "\n"), "\n") {
				writef(w, "    %s\n", safe(strings.TrimSuffix(l, "\r")))
			}
		}
	}
	writeln(w)
}

func renderApplyResult(w io.Writer, r api.ChangesetResult, sent int) {
	for _, o := range r.Outcomes {
		status := "not applied"
		if o.Applied {
			status = "applied"
		}
		line := fmt.Sprintf("  %-11s %s", status, safe(o.Dn))
		if o.Error != nil {
			line += fmt.Sprintf("  [%s] %s", o.Error.Error, safe(o.Error.Message))
		}
		writeln(w, line)
	}
	if r.FailedIndex == nil {
		writef(w, "Applied %s.\n", plural(r.AppliedCount, "change", "changes"))
	}
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func maxInt(values ...int) int {
	m := 0
	for _, v := range values {
		if v > m {
			m = v
		}
	}
	return m
}
