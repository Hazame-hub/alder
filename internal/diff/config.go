package diff

import (
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/snapshot"
)

// Comparing configuration.
//
// The same rules as the other comparisons, applied to settings instead of
// entries -- with one rule of its own that matters more than the rest:
//
//	Two configurations are only comparable when they are the same provider's.
//
// OpenLDAP's configuration and 389 Directory Server's are different models of
// different software. Lining them up would produce hundreds of settings
// "added" and hundreds "removed", every one of them an artefact of the
// comparison rather than a fact about either server. So a comparison of two
// providers reports that, says what each side holds in broad terms, and stops.
//
// Within one provider:
//
//   - Identity is provider, section, resource and key, never a position. A
//     database is paired with the database of the same suffix, a plugin with
//     the plugin of the same name.
//   - Values are compared as the provider's model normalised them: an ordered
//     setting keeps its order, an unordered one does not, and a value nothing
//     parses is compared as text and says so.
//   - A setting that could not be read is unknown, not removed. A partial
//     capture can never report that the two sides are equal.
//   - A difference is a fact. Whether it can be changed is a separate field,
//     and it is true only where Alder already changes that setting through the
//     ordinary plan.

// ConfigSide is one configuration state being compared.
type ConfigSide struct {
	Snapshot *snapshot.ConfigSnapshot
	// Live reports that this side was read from the session's directory just
	// now rather than loaded from a file.
	Live bool
}

// Actionability says what Alder could do about a difference.
const (
	// ActionableWritable: Alder already changes this setting through the
	// ordinary plan, and a change request can be derived from the difference.
	ActionableWritable = "writable"
	// ActionableReadOnly: the provider maintains the setting, or Alder has no
	// proven way to change it. It is reported and never staged.
	ActionableReadOnly = "read_only"
	// ActionableUnknown: it could not be decided.
	ActionableUnknown = "unknown"
)

// Reasons a configuration comparison is partial, beyond the shared ones.
const (
	ReasonConfigPartial  = "config_partial"
	ReasonProviderUnread = "provider_unreadable"
)

// Problems a configuration item can carry.
const (
	ProblemResourceMissing  = "resource_missing"
	ProblemNotComparable    = "not_comparable"
	ProblemSensitive        = "sensitive_withheld"
	ProblemProviderSpecific = "provider_specific"
	ProblemNoWritePath      = "no_write_path"
	// ProblemUnread is a setting missing from a side that was not read in
	// full. Its absence says nothing, so neither does the comparison.
	ProblemUnread = "not_read"
)

// ConfigItem is one setting's difference.
type ConfigItem struct {
	Kind Kind `json:"kind"`
	// ID is the setting's identity within the provider's model.
	ID       string `json:"id"`
	Section  string `json:"section"`
	Resource string `json:"resource,omitempty"`
	// ResourceLabel is what a person calls the resource.
	ResourceLabel string `json:"resourceLabel,omitempty"`
	Key           string `json:"key"`
	// Source and Target are the values each side holds, in the form that side
	// holds them. A withheld setting has none on either side.
	Source []string `json:"source,omitempty"`
	Target []string `json:"target,omitempty"`
	// Ordered reports that the order of the values is part of the difference.
	Ordered bool   `json:"ordered,omitempty"`
	Type    string `json:"type,omitempty"`
	// Comparison is how the values were compared: normalised, or as text.
	Comparison string `json:"comparison,omitempty"`
	// Mutability is the provider's view of the setting.
	Mutability string `json:"mutability,omitempty"`
	// Actionable says whether Alder could change it, which is not the same
	// thing: it is true only where Alder already has a proven write path.
	Actionable string `json:"actionable"`
	// Sensitive reports a setting whose values are never recorded. A change to
	// one is reported as a change in how many values it has, or not at all.
	Sensitive bool `json:"sensitive,omitempty"`
	// Operational reports a value that names something about the machine.
	Operational bool `json:"operational,omitempty"`
	// DN is where the setting lives, for a person to go and look.
	DN       string   `json:"dn,omitempty"`
	Problems []string `json:"problems,omitempty"`
}

// ConfigCounts tallies the items.
type ConfigCounts struct {
	Compared  int `json:"compared"`
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Modified  int `json:"modified"`
	Unchanged int `json:"unchanged"`
	Unknown   int `json:"unknown"`
	// Actionable is how many differences Alder could change.
	Actionable int `json:"actionable"`
}

// ConfigSectionCounts is one section's tally.
type ConfigSectionCounts struct {
	Section string       `json:"section"`
	Counts  ConfigCounts `json:"counts"`
}

// ConfigProviderSummary is what one side holds, in the broad terms that are
// comparable across providers: which sections it has, and how much is in them.
type ConfigProviderSummary struct {
	Provider      string                `json:"provider"`
	Vendor        string                `json:"vendor,omitempty"`
	VendorVersion string                `json:"vendorVersion,omitempty"`
	Root          string                `json:"root,omitempty"`
	Completeness  string                `json:"completeness"`
	Settings      int                   `json:"settings"`
	Resources     int                   `json:"resources"`
	Sections      []ConfigSectionTotals `json:"sections"`
}

// ConfigSectionTotals is how many settings one side holds in one section.
type ConfigSectionTotals struct {
	Section  string `json:"section"`
	Settings int    `json:"settings"`
}

// ConfigResult is a whole configuration comparison.
type ConfigResult struct {
	// ProviderMismatch reports that the two sides are different providers'
	// configuration. When it is set, nothing was compared setting by setting.
	ProviderMismatch bool `json:"providerMismatch"`
	// Provider is the model both sides share, when they share one.
	Provider string   `json:"provider,omitempty"`
	Complete bool     `json:"complete"`
	Reasons  []Reason `json:"reasons,omitempty"`
	// CrossVendor reports two different products, which for configuration is
	// the same thing as a provider mismatch.
	CrossVendor bool                  `json:"crossVendor"`
	Counts      ConfigCounts          `json:"counts"`
	Sections    []ConfigSectionCounts `json:"sections"`
	Items       []ConfigItem          `json:"items"`
	// Objects are the configuration objects each side holds -- databases,
	// overlays, backends, plugins -- and what Alder could do about a
	// difference. Added in 1.16.
	Objects []ConfigObject `json:"objects"`
	// Source and Target describe each side, and are the whole answer when the
	// providers differ.
	Source ConfigProviderSummary `json:"source"`
	Target ConfigProviderSummary `json:"target"`
}

// ConfigOptions tunes a configuration comparison.
type ConfigOptions struct {
	// IncludeUnchanged lists unchanged settings as items.
	IncludeUnchanged bool
}

// CompareConfig compares two configuration states.
//
// Source is where you are and target is what you are comparing with, as
// everywhere else: "added" means the target holds a setting the source does
// not.
func CompareConfig(source, target ConfigSide, opts ConfigOptions) *ConfigResult {
	s, t := source.Snapshot, target.Snapshot
	r := &ConfigResult{Complete: true, Items: []ConfigItem{}, Sections: []ConfigSectionCounts{},
		Objects: []ConfigObject{}, Source: summarise(s), Target: summarise(t)}
	r.CrossVendor = !strings.EqualFold(strings.TrimSpace(s.Source.Vendor), strings.TrimSpace(t.Source.Vendor))

	if !strings.EqualFold(s.Source.Provider, t.Source.Provider) {
		// Different software. There is no setting-by-setting answer to give,
		// and inventing one would be worse than saying so.
		r.ProviderMismatch = true
		r.Complete = false
		r.Reasons = append(r.Reasons, Reason{Code: ReasonProviderMismatch,
			Detail: "these are " + s.Source.Provider + " and " + t.Source.Provider +
				" configurations; their settings are not the same settings, and Alder does not translate configuration between them"})
		return r
	}
	r.Provider = s.Source.Provider

	for name, side := range map[string]*snapshot.ConfigSnapshot{"source": s, "target": t} {
		if side.Completeness == snapshot.ConfigComplete {
			continue
		}
		r.Complete = false
		for _, reason := range side.Incomplete {
			r.Reasons = append(r.Reasons, Reason{Code: ReasonConfigPartial,
				Detail: "the " + name + " configuration was not read in full (" + reason.Reason + ": " + reason.Scope + ")"})
		}
	}

	labels := map[string]string{}
	for _, side := range []*snapshot.ConfigSnapshot{s, t} {
		for _, resource := range side.Resources {
			if label := strings.TrimSpace(resource.Label); label != "" {
				labels[resource.ID()] = label
			}
		}
	}

	sourceByID := index(s)
	targetByID := index(t)
	// A side that was not read in full cannot be used to say a setting is not
	// there: absence in a partial capture is something unread, not something
	// removed. This is the configuration form of the rule the other
	// comparisons already follow for an entry nobody may see.
	partial := sides{source: s.Completeness != snapshot.ConfigComplete, target: t.Completeness != snapshot.ConfigComplete}
	for _, id := range unionKeys(sourceByID, targetByID) {
		left, right := sourceByID[id], targetByID[id]
		item := configItem(left, right, labels, partial)
		r.record(item, opts.IncludeUnchanged)
	}
	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].ID < r.Items[j].ID })
	r.Objects = compareObjects(s, t, live(source, target))
	r.Sections = sectionCounts(r.Items)
	sort.SliceStable(r.Reasons, func(i, j int) bool { return r.Reasons[i].Detail < r.Reasons[j].Detail })
	return r
}

// ReasonProviderMismatch is why a cross-provider comparison reports nothing
// setting by setting.
const ReasonProviderMismatch = "provider_mismatch"

func summarise(s *snapshot.ConfigSnapshot) ConfigProviderSummary {
	out := ConfigProviderSummary{Provider: s.Source.Provider, Vendor: s.Source.Vendor,
		VendorVersion: s.Source.VendorVersion, Root: s.Source.Root, Completeness: s.Completeness,
		Settings: len(s.Settings), Resources: len(s.Resources), Sections: []ConfigSectionTotals{}}
	totals := map[string]int{}
	for _, setting := range s.Settings {
		totals[setting.Section]++
	}
	names := make([]string, 0, len(totals))
	for name := range totals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.Sections = append(out.Sections, ConfigSectionTotals{Section: name, Settings: totals[name]})
	}
	return out
}

func index(s *snapshot.ConfigSnapshot) map[string]*snapshot.ConfigSetting {
	out := make(map[string]*snapshot.ConfigSetting, len(s.Settings))
	for i := range s.Settings {
		out[s.Settings[i].ID()] = &s.Settings[i]
	}
	return out
}

func unionKeys(a, b map[string]*snapshot.ConfigSetting) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]*snapshot.ConfigSetting{a, b} {
		for key := range m {
			if !seen[key] {
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}

// sides reports something about each side of a comparison.
type sides struct{ source, target bool }

// configItem decides one setting's difference.
func configItem(source, target *snapshot.ConfigSetting, labels map[string]string, partial sides) ConfigItem {
	held := source
	if held == nil {
		held = target
	}
	item := ConfigItem{
		ID: held.ID(), Section: held.Section, Resource: held.Resource, Key: held.Key,
		Ordered: held.Ordered, Type: held.Type, Comparison: held.Comparison,
		Mutability: held.Mutability, Sensitive: held.Sensitive, Operational: held.Operational,
		DN: held.DN, ResourceLabel: labels[held.Resource],
	}
	if source != nil {
		item.Source = source.Values
	}
	if target != nil {
		item.Target = target.Values
	}
	switch {
	case source == nil && partial.source, target == nil && partial.target:
		// It may be there and unread, or it may not be there. Saying which
		// would be inventing an answer.
		item.Kind = Unknown
		item.Problems = append(item.Problems, ProblemUnread)
	case source == nil:
		item.Kind = Added
	case target == nil:
		item.Kind = Removed
	case source.Sensitive || target.Sensitive:
		// A secret is never recorded, so the only thing that can be compared
		// is how many values there are -- and even that is reported as a
		// change only when it differs.
		item.Sensitive = true
		item.Problems = append(item.Problems, ProblemSensitive)
		item.Kind = Unchanged
		if source.Withheld != target.Withheld {
			item.Kind = Modified
		}
	case sameValues(source, target):
		item.Kind = Unchanged
	default:
		item.Kind = Modified
	}
	if item.Comparison == snapshot.ComparisonRaw && item.Kind != Unchanged {
		item.Problems = append(item.Problems, ProblemNotComparable)
	}
	item.Actionable = actionability(source, target, item.Kind)
	if item.Actionable == ActionableReadOnly && item.Kind != Unchanged {
		item.Problems = append(item.Problems, ProblemNoWritePath)
	}
	return item
}

// sameValues compares two settings' values, honouring order only where the
// model said order means something.
//
// A boolean keeps the server's spelling in a snapshot, because that spelling is
// what gets written back -- OpenLDAP refuses a lower-case TRUE -- so booleans
// are the one type compared without regard to case.
func sameValues(a, b *snapshot.ConfigSetting) bool {
	if len(a.Values) != len(b.Values) {
		return false
	}
	if a.Type == "bool" && b.Type == "bool" {
		left := make([]string, len(a.Values))
		right := make([]string, len(b.Values))
		for i := range a.Values {
			left[i] = strings.ToLower(a.Values[i])
			right[i] = strings.ToLower(b.Values[i])
		}
		return equalStrings(left, right, a.Ordered || b.Ordered)
	}
	if a.Ordered || b.Ordered {
		for i := range a.Values {
			if a.Values[i] != b.Values[i] {
				return false
			}
		}
		return true
	}
	left := append([]string(nil), a.Values...)
	right := append([]string(nil), b.Values...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// actionability decides whether Alder could change a difference.
//
// Only a setting both sides agree is writable, that exists on the side being
// changed, and that is not a secret. A setting that appears or disappears is a
// configuration entry being added or removed, which is not something Alder
// does: it is reported and left alone.
func actionability(source, target *snapshot.ConfigSetting, kind Kind) string {
	switch kind {
	case Unchanged:
		return ActionableReadOnly
	case Added, Removed:
		return ActionableReadOnly
	case Unknown:
		return ActionableUnknown
	}
	if source == nil || target == nil {
		return ActionableUnknown
	}
	if source.Sensitive || target.Sensitive {
		return ActionableReadOnly
	}
	// A value nothing here parses is compared as text, and a textual
	// difference is not a change anyone has understood. Writing the other
	// side's text back would replace a structure Alder cannot read.
	if source.Comparison == snapshot.ComparisonRaw || target.Comparison == snapshot.ComparisonRaw {
		return ActionableReadOnly
	}
	if source.Mutability == snapshot.MutabilityWritable && target.Mutability == snapshot.MutabilityWritable {
		return ActionableWritable
	}
	if source.Mutability == snapshot.MutabilityUnknown || target.Mutability == snapshot.MutabilityUnknown {
		return ActionableUnknown
	}
	return ActionableReadOnly
}

func (r *ConfigResult) record(item ConfigItem, includeUnchanged bool) {
	r.Counts.Compared++
	switch item.Kind {
	case Added:
		r.Counts.Added++
	case Removed:
		r.Counts.Removed++
	case Modified:
		r.Counts.Modified++
	case Unknown:
		r.Counts.Unknown++
	default:
		r.Counts.Unchanged++
	}
	if item.Kind != Unchanged && item.Actionable == ActionableWritable {
		r.Counts.Actionable++
	}
	if item.Kind == Unchanged && !includeUnchanged {
		return
	}
	r.Items = append(r.Items, item)
}

func sectionCounts(items []ConfigItem) []ConfigSectionCounts {
	totals := map[string]*ConfigCounts{}
	var order []string
	for _, item := range items {
		if totals[item.Section] == nil {
			totals[item.Section] = &ConfigCounts{}
			order = append(order, item.Section)
		}
		counts := totals[item.Section]
		counts.Compared++
		switch item.Kind {
		case Added:
			counts.Added++
		case Removed:
			counts.Removed++
		case Modified:
			counts.Modified++
		case Unknown:
			counts.Unknown++
		default:
			counts.Unchanged++
		}
		if item.Kind != Unchanged && item.Actionable == ActionableWritable {
			counts.Actionable++
		}
	}
	sort.Strings(order)
	out := make([]ConfigSectionCounts, 0, len(order))
	for _, section := range order {
		out = append(out, ConfigSectionCounts{Section: section, Counts: *totals[section]})
	}
	return out
}

func equalStrings(a, b []string, ordered bool) bool {
	if !ordered {
		a = append([]string(nil), a...)
		b = append([]string(nil), b...)
		sort.Strings(a)
		sort.Strings(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// live is whichever side was read from the directory, where one was.
func live(source, target ConfigSide) ConfigSide {
	switch {
	case source.Live:
		return source
	case target.Live:
		return target
	}
	return ConfigSide{}
}
