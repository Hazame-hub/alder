package cli

import (
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/hazame-hub/alder/internal/api"
)

// Configuration comparisons, as the command line shows and stages them.
//
// Alder does the comparing; this file prints what it answered and picks out the
// change requests a person named. It knows nothing about either server's
// configuration -- not which settings exist, not which are secrets, and not
// which can be changed. When the two sides are different providers, all there
// is to print is that, and what each side holds.

func renderConfigDiff(w io.Writer, d api.Diff, sourceName, targetName string, summaryOnly, sourceLive bool) {
	c := d.Config
	writef(w, "Source: %s\n", sideLabel(sourceName, d.Source))
	writef(w, "Target: %s\n", sideLabel(targetName, d.Target))

	if c.ProviderMismatch {
		writeln(w)
		writeln(w, "These configurations belong to different servers' software, and their settings")
		writeln(w, "are not the same settings. Alder does not translate configuration between")
		writeln(w, "providers, and does not compare them setting by setting.")
		writeln(w)
		for _, side := range []struct {
			name    string
			summary api.ConfigProviderSummary
		}{{sourceName, c.Source}, {targetName, c.Target}} {
			writef(w, "%s: %s, %d settings in %d resources (%s)\n", side.name, side.summary.Provider,
				side.summary.Settings, side.summary.Resources, side.summary.Completeness)
			for _, section := range side.summary.Sections {
				writef(w, "    %-18s %5d\n", section.Section, section.Settings)
			}
		}
		return
	}

	writef(w, "Configuration of %s. Added means set on the target and not on the source; removed, the other way round.\n",
		string(derefProvider(c.Provider)))
	writeln(w)
	counts := c.Counts
	writef(w, "%d settings compared\n", counts.Compared)
	for _, row := range []struct {
		n    int
		name string
	}{{counts.Added, "added"}, {counts.Modified, "modified"}, {counts.Removed, "removed"},
		{counts.Unchanged, "unchanged"}, {counts.Unknown, "unknown"}} {
		writef(w, "  %5d %s\n", row.n, row.name)
	}
	writef(w, "  %5d that Alder can change through a plan\n", counts.Actionable)

	if len(c.Sections) > 0 {
		writeln(w)
		writef(w, "%-18s %9s %9s %9s %9s\n", "section", "added", "modified", "removed", "actionable")
		for _, section := range c.Sections {
			writef(w, "%-18s %9d %9d %9d %9d\n", safe(section.Section), section.Counts.Added,
				section.Counts.Modified, section.Counts.Removed, section.Counts.Actionable)
		}
	}

	if !d.Complete {
		writeln(w)
		writeln(w, "INCOMPLETE: part of a configuration could not be read, so this is not the whole answer.")
		if d.Reasons != nil {
			for _, r := range *d.Reasons {
				writef(w, "  %s: %s\n", r.Code, safe(r.Detail))
			}
		}
	}
	if summaryOnly || len(c.Items) == 0 {
		return
	}

	for _, item := range c.Items {
		writeln(w)
		resource := safe(text(item.Resource))
		if label := safe(text(item.ResourceLabel)); label != "" && label != resource {
			resource += " (" + label + ")"
		}
		writef(w, "%-10s %-18s %s\n", item.Kind, safe(item.Section), safe(item.Key))
		if resource != "" {
			writef(w, "    resource %s\n", resource)
		}
		if dnText := safe(text(item.Dn)); dnText != "" {
			writef(w, "    entry    %s\n", dnText)
		}
		if item.Sensitive != nil && *item.Sensitive {
			writeln(w, "    value    withheld: this setting is a secret, and Alder never reads one")
		} else {
			for _, line := range valueLines("-", item.Source) {
				writef(w, "    %s\n", line)
			}
			for _, line := range valueLines("+", item.Target) {
				writef(w, "    %s\n", line)
			}
		}
		writef(w, "    %s\n", actionLine(item))
		if item.Problems != nil {
			for _, p := range *item.Problems {
				writef(w, "    note     %s\n", safe(p))
			}
		}
	}

	writeln(w)
	writeln(w, "Nothing here changes the server. A difference Alder can change is staged with")
	writeln(w, "--stage and its setting, and goes through alder plan like every other change.")
}

// actionLine says whether Alder could make this change, and why not.
func actionLine(item api.ConfigDiffItem) string {
	switch item.Actionable {
	case api.ConfigActionableWritable:
		return "Alder     can change this setting through a plan (--stage " + item.Id + ")"
	case api.ConfigActionableUnknown:
		return "Alder     does not know whether this setting can be changed; it is reported only"
	}
	return "Alder     has no proven way to change this setting; it is reported only"
}

func valueLines(sign string, values *[]string) []string {
	if values == nil || len(*values) == 0 {
		return []string{sign + "        (none)"}
	}
	shown := *values
	extra := 0
	if len(shown) > maxValuesShown {
		extra = len(shown) - maxValuesShown
		shown = shown[:maxValuesShown]
	}
	out := make([]string, 0, len(shown)+1)
	for _, v := range shown {
		out = append(out, sign+"        "+safe(v))
	}
	if extra > 0 {
		out = append(out, sign+"        … and "+strconv.Itoa(extra)+" more")
	}
	return out
}

func derefProvider(p *api.ConfigProvider) api.ConfigProvider {
	if p == nil {
		return ""
	}
	return *p
}

// stageConfigSelected writes the change requests Alder derived for the
// settings named on the command line. A setting Alder offered no change for is
// refused rather than skipped, and a configuration comparison has no
// deletions to select: adding or removing a configuration entry is not
// something Alder does.
func stageConfigSelected(env *Env, d api.Diff, body []byte, o diffOptions) error {
	if len(o.stageDeletion) > 0 {
		return notApplicable("config_deletion_not_supported",
			"a configuration comparison stages no deletions: adding or removing configuration entries is not something Alder does")
	}
	var raw struct {
		Config struct {
			Items []struct {
				ID        string `json:"id"`
				Candidate *struct {
					Changes []json.RawMessage `json:"changes"`
				} `json:"candidate"`
			} `json:"items"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || len(raw.Config.Items) != len(d.Config.Items) {
		return failf("unexpected_response", "cannot read the change requests in Alder's comparison")
	}

	selected := map[int]bool{}
	for _, wanted := range o.stage {
		want := strings.TrimSpace(wanted)
		found := -1
		for i, item := range d.Config.Items {
			if strings.EqualFold(item.Id, want) || strings.EqualFold(item.Key, want) {
				if found >= 0 {
					return notApplicable("selection_ambiguous",
						"%s names more than one setting; use the identifier diff prints", safe(want))
				}
				found = i
			}
		}
		if found < 0 {
			return notApplicable("selection_not_found", "no difference is about %s; name a setting as diff prints it", safe(want))
		}
		item := d.Config.Items[found]
		switch {
		case item.Actionable != api.ConfigActionableWritable:
			return notApplicable("selection_not_applicable",
				"%s is %s: Alder has no proven way to change that setting, so it is reported only", safe(want), item.Actionable)
		case raw.Config.Items[found].Candidate == nil || len(raw.Config.Items[found].Candidate.Changes) == 0:
			return notApplicable("selection_blocked", "%s offers no change", safe(want))
		}
		selected[found] = true
	}

	indexes := make([]int, 0, len(selected))
	for i := range selected {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	changes := []json.RawMessage{}
	for _, i := range indexes {
		changes = append(changes, raw.Config.Items[i].Candidate.Changes...)
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
	writef(env.Stderr, "Wrote %d change request(s) for %d selected setting(s) to %s.\n"+
		"Review them with: alder plan --changes %s\n", len(changes), len(indexes), o.changesOut, o.changesOut)
	return nil
}
