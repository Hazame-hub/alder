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
	if objects := actionableObjects(c); len(objects) > 0 {
		writeln(w)
		writeln(w, "Configuration objects")
		for _, object := range objects {
			name := safe(object.Name)
			if label := safe(text(object.Label)); label != "" && label != name {
				name += " (" + label + ")"
			}
			writef(w, "  %-10s %-10s %s\n", object.Kind, safe(object.Object), name)
			removal := object.Destructive != nil && *object.Destructive
			switch {
			case object.Actionable == api.ConfigActionableWritable && removal:
				writef(w, "      Alder can remove it from the server (select with --stage-deletion %s)\n", safe(object.Id))
			case object.Actionable == api.ConfigActionableWritable:
				writef(w, "      Alder can create it (select with --stage %s)\n", safe(object.Id))
			default:
				writef(w, "      reported only%s\n", refusalText(object.Refusal))
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

// actionableObjects are the object differences worth a line: an object both
// sides hold is the ordinary case and says nothing.
func actionableObjects(c *api.ConfigDiff) []api.ConfigDiffObject {
	var out []api.ConfigDiffObject
	for _, object := range c.Objects {
		if object.Kind != api.DiffKindUnchanged {
			out = append(out, object)
		}
	}
	return out
}

// refusalText turns the reason Alder cannot act on an object into a clause.
func refusalText(refusal *string) string {
	switch text(refusal) {
	case "":
		return ""
	case "module_not_loaded":
		return ": the server has not loaded the module this overlay needs"
	case "not_creatable":
		return ": Alder does not create or remove objects of this kind"
	case "parent_missing":
		return ": the database it belongs to is not on this server"
	case "source_not_live":
		return ": a change is proposed only when the source is the directory itself"
	case "not_present":
		return ": it is not on this server"
	}
	return ": " + safe(text(refusal))
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

// stageConfigSelected writes the change requests Alder derived for what was
// named on the command line: a setting to change, an object to create, or --
// only when it is named with --stage-deletion -- an object to remove.
//
// Anything Alder offered no change for is refused by name rather than skipped,
// and a removal is never selected by --stage: a person asking to remove a
// configuration object says so in as many words.
func stageConfigSelected(env *Env, d api.Diff, body []byte, o diffOptions) error {
	var raw struct {
		Config struct {
			Items   []rawCandidate `json:"items"`
			Objects []rawCandidate `json:"objects"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &raw); err != nil ||
		len(raw.Config.Items) != len(d.Config.Items) || len(raw.Config.Objects) != len(d.Config.Objects) {
		return failf("unexpected_response", "cannot read the change requests in Alder's comparison")
	}

	objects := map[int]bool{}
	settings := map[int]bool{}
	for _, selection := range append(staged(o.stage, false), staged(o.stageDeletion, true)...) {
		want := selection.name
		if i, found := matchObject(d.Config.Objects, want); found {
			object := d.Config.Objects[i]
			removal := object.Destructive != nil && *object.Destructive
			switch {
			case object.Actionable != api.ConfigActionableWritable:
				return notApplicable("selection_not_applicable", "%s is reported only%s",
					safe(want), refusalText(object.Refusal))
			case removal && !selection.deletion:
				return notApplicable("deletion_not_selected",
					"%s would be removed from the server; name it with --stage-deletion to select that", safe(want))
			case !removal && selection.deletion:
				return notApplicable("selection_not_applicable",
					"%s is not a removal; select it with --stage", safe(want))
			case raw.Config.Objects[i].Candidate == nil || len(raw.Config.Objects[i].Candidate.Changes) == 0:
				return notApplicable("selection_blocked", "%s offers no change", safe(want))
			}
			objects[i] = true
			continue
		}
		if selection.deletion {
			return notApplicable("selection_not_found",
				"no configuration object is called %s; only an object is removed, and a setting is changed with --stage", safe(want))
		}

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
			return notApplicable("selection_not_found",
				"no difference is about %s; name a setting or an object as diff prints it", safe(want))
		}
		item := d.Config.Items[found]
		switch {
		case item.Actionable != api.ConfigActionableWritable:
			return notApplicable("selection_not_applicable",
				"%s is %s: Alder has no proven way to change that setting, so it is reported only", safe(want), item.Actionable)
		case raw.Config.Items[found].Candidate == nil || len(raw.Config.Items[found].Candidate.Changes) == 0:
			return notApplicable("selection_blocked", "%s offers no change", safe(want))
		}
		settings[found] = true
	}

	// An object is created before the settings on it are touched, and removed
	// after everything else: the order a person would do it in by hand.
	changes := []json.RawMessage{}
	changes = append(changes, chosen(raw.Config.Objects, objects, func(i int) bool {
		object := d.Config.Objects[i]
		return object.Destructive == nil || !*object.Destructive
	})...)
	changes = append(changes, chosen(raw.Config.Items, settings, nil)...)
	changes = append(changes, chosen(raw.Config.Objects, objects, func(i int) bool {
		object := d.Config.Objects[i]
		return object.Destructive != nil && *object.Destructive
	})...)

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
	writef(env.Stderr, "Wrote %d change request(s) for %d setting(s) and %d object(s) to %s.\n"+
		"Review them with: alder plan --changes %s\n",
		len(changes), len(settings), len(objects), o.changesOut, o.changesOut)
	return nil
}

// rawCandidate is the part of a comparison this command sends on: the change
// requests Alder derived, exactly as Alder wrote them.
type rawCandidate struct {
	ID        string `json:"id"`
	Candidate *struct {
		Changes []json.RawMessage `json:"changes"`
	} `json:"candidate"`
}

type configSelection struct {
	name     string
	deletion bool
}

func staged(names []string, deletion bool) []configSelection {
	out := make([]configSelection, 0, len(names))
	for _, name := range names {
		out = append(out, configSelection{name: strings.TrimSpace(name), deletion: deletion})
	}
	return out
}

// matchObject finds a configuration object by the identity diff prints.
func matchObject(objects []api.ConfigDiffObject, want string) (int, bool) {
	for i, object := range objects {
		if strings.EqualFold(object.Id, want) {
			return i, true
		}
	}
	return 0, false
}

// chosen collects the change requests of the selected entries, in the order
// Alder returned them, optionally narrowed to some of them.
func chosen(all []rawCandidate, selected map[int]bool, keep func(int) bool) []json.RawMessage {
	indexes := make([]int, 0, len(selected))
	for i := range selected {
		if keep == nil || keep(i) {
			indexes = append(indexes, i)
		}
	}
	sort.Ints(indexes)
	out := make([]json.RawMessage, 0, len(indexes))
	for _, i := range indexes {
		out = append(out, all[i].Candidate.Changes...)
	}
	return out
}
