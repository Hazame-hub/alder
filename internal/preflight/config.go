package preflight

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/hazame-hub/alder/internal/diff"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Preflighting a configuration snapshot (1.14).
//
// The question is the same as for every other artifact -- what of this would
// carry across to the directory I am connected to -- asked of one server's own
// configuration. It is answered only where it means something: when the
// snapshot and the target are the same server software. Configuration is
// provider-specific unless equivalence is explicitly proven, and nothing has
// proved any, so a snapshot of one product against a target of another is
// reported as not evaluated, in the words every other report already uses.
//
// Within one provider the comparison is the one POST /diff makes, with the
// target as the live source, and each difference becomes a finding:
//
//   - the target already holds the value: already satisfied;
//   - it holds another value Alder changes through a plan: a prerequisite, and
//     the comparison is where to stage it;
//   - it holds another value Alder does not change: a prerequisite that is
//     manual work for an operator;
//   - it lacks a resource the snapshot has -- a database, an overlay, a
//     plugin -- or a setting on one it has: a prerequisite, manual;
//   - a secret: excluded, because its value was never read;
//   - a path, a host, a port: excluded, because it names the machine rather
//     than configures the software;
//   - access rules: not evaluated, as in every report;
//   - something this bind could not read on the target: unknown.
//
// A snapshot is state, not intent. A setting only the target has is not one to
// remove and is not a finding.

// SourceConfigSnapshot is a configuration snapshot's source type.
const SourceConfigSnapshot = "config_snapshot"

// CategoryConfiguration is the report section for configuration findings.
const CategoryConfiguration Category = "configuration"

// Configuration finding codes. Stable identifiers.
const (
	CodeConfigPresent          = "config_setting_present"
	CodeConfigChangeable       = "config_setting_changeable"
	CodeConfigManual           = "config_setting_manual"
	CodeConfigMissing          = "config_setting_missing"
	CodeConfigResourceMissing  = "config_resource_missing"
	CodeConfigEnvironment      = "config_environment_specific"
	CodeConfigUnknown          = "config_setting_unknown"
	CodeConfigProviderMismatch = "config_provider_mismatch"
	CodeConfigUnreadable       = "config_target_unreadable"
)

// ConfigSnapshot preflights a configuration snapshot against a target.
func ConfigSnapshot(ctx context.Context, s *snapshot.ConfigSnapshot, integrity string, t Target, opts Options) (*Report, error) {
	caps := t.Capabilities()
	b := newBuilder(SourceInfo{Type: SourceConfigSnapshot, Format: s.Format, Version: s.Version, Kind: s.Kind,
		Checksum: s.Checksum, Integrity: integrity, Vendor: s.Source.Vendor, VendorVersion: s.Source.VendorVersion,
		Objects: len(s.Settings)}, targetInfo(caps, s.Source.Vendor), opts.now())

	if s.Completeness != snapshot.ConfigComplete {
		b.incomplete(CodeSourcePartial)
		b.add(Finding{ID: "artifact:partial", Code: CodeSourcePartial, Classification: Unknown, Category: CategoryArtifact,
			Scope: ScopeArtifact, Count: len(s.Incomplete),
			Explanation: "The snapshot was not a complete capture of its server's configuration, so what it lacks is not known."})
	}

	if opts.CaptureConfig == nil {
		return unreadableConfig(b, "this server offers no way to read its configuration"), nil
	}
	live, err := opts.CaptureConfig(ctx)
	if err != nil {
		return unreadableConfig(b, err.Error()), nil
	}

	if !strings.EqualFold(live.Source.Provider, s.Source.Provider) {
		// Different software. Nothing is judged setting by setting, and the
		// report says so exactly as every other report does: configuration is
		// not evaluated.
		b.incomplete(CodeConfigProviderMismatch)
		b.add(Finding{ID: "artifact:provider", Code: CodeConfigProviderMismatch, Classification: Unknown,
			Category: CategoryArtifact, Scope: ScopeArtifact,
			Target: &TargetFact{Fact: "provider_differs", Detail: live.Source.Provider},
			Explanation: "The snapshot is " + s.Source.Provider + " configuration and the target is " + live.Source.Provider +
				". Their settings are not the same settings, so configuration compatibility between them is not evaluated."})
		return b.finish(), nil
	}

	// Same provider: server configuration is what this report is about, so it
	// is no longer listed as not evaluated. Access control still is.
	b.report.NotEvaluated = configNotEvaluated()
	b.require("config_read", live.Completeness == snapshot.ConfigComplete, "configuration",
		"Reading the target's whole configuration tree, so that a setting it lacks is known to be lacking.")
	if live.Completeness != snapshot.ConfigComplete {
		b.incomplete("target_config_partial")
	}

	liveSide := diff.ConfigSide{Snapshot: live, Live: true}
	compared := diff.CompareConfig(liveSide, diff.ConfigSide{Snapshot: s}, diff.ConfigOptions{IncludeUnchanged: true})
	eval := configEval{b: b, source: s, live: live, compared: compared, liveSide: liveSide}
	eval.all()
	return b.finish(), nil
}

func unreadableConfig(b *builder, detail string) *Report {
	b.incomplete(CodeConfigUnreadable)
	b.add(Finding{ID: "artifact:target-config", Code: CodeConfigUnreadable, Classification: Unknown,
		Category: CategoryArtifact, Scope: ScopeArtifact, Target: &TargetFact{Fact: "config_unreadable", Detail: bound(detail, MaxFactRunes)},
		Explanation: "The target's configuration could not be read, so no setting could be judged."})
	return b.finish()
}

func configNotEvaluated() []NotEvaluated {
	out := []NotEvaluated{}
	for _, area := range alwaysNotEvaluated {
		if area.Area != "server_configuration" {
			out = append(out, area)
		}
	}
	return out
}

type configEval struct {
	b        *builder
	source   *snapshot.ConfigSnapshot
	live     *snapshot.ConfigSnapshot
	compared *diff.ConfigResult
	liveSide diff.ConfigSide
}

func (e configEval) all() {
	// Resources the target lacks come first, so the settings on them can name
	// the missing resource as their cause.
	missing := map[string]string{}
	resources := append([]snapshot.ConfigResource(nil), e.source.Resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].ID() < resources[j].ID() })
	for _, r := range resources {
		if _, ok := e.live.ResourceByID(r.ID()); ok {
			continue
		}
		if e.live.Completeness != snapshot.ConfigComplete {
			continue // not missing, unread; its settings say so
		}
		label := r.Label
		if label == "" {
			label = r.Name
		}
		missing[r.ID()] = e.b.add(Finding{ID: "config-resource:" + r.ID(), Code: CodeConfigResourceMissing,
			Classification: PrerequisiteRequired, Category: CategoryConfiguration, Scope: ScopeItem,
			Source:            SourceRef{Resource: r.ID(), Name: bound(label, MaxFactRunes), DN: bound(r.DN, MaxFactRunes)},
			Target:            &TargetFact{Fact: "absent"},
			BlocksPortability: true, ManualAction: true,
			Prerequisites: []Prerequisite{{Type: "configuration", Resource: r.ID()}},
			Explanation: "The target has no " + r.Kind + " " + strconv.Quote(bound(label, 200)) +
				". Alder does not create configuration objects; one has to exist before its settings can match."})
	}

	items := map[string]diff.ConfigItem{}
	for _, item := range e.compared.Items {
		items[item.ID] = item
	}
	settings := append([]snapshot.ConfigSetting(nil), e.source.Settings...)
	sort.Slice(settings, func(i, j int) bool { return settings[i].ID() < settings[j].ID() })
	for _, st := range settings {
		item, ok := items[st.ID()]
		if !ok {
			continue
		}
		e.setting(st, item, missing[st.Resource])
	}
}

func (e configEval) setting(st snapshot.ConfigSetting, item diff.ConfigItem, missingResource string) {
	ref := SourceRef{Setting: st.ID(), Resource: st.Resource, Attribute: bound(st.Key, MaxFactRunes), DN: bound(st.DN, MaxFactRunes)}
	f := Finding{ID: "config:" + st.ID(), Category: CategoryConfiguration, Scope: ScopeAttribute, Source: ref}

	switch {
	case st.Section == "access_control":
		// Not evaluated, in every report; saying something here would
		// contradict the list at the top.
		return
	case st.Sensitive:
		f.Code, f.Classification, f.Category = CodeSensitiveNotMigratable, Excluded, CategorySensitive
		f.Explanation = st.Key + " is a secret, and its value was never read into the snapshot. It has to be set on the target separately."
	case item.Kind == diff.Unknown:
		f.Code, f.Classification = CodeConfigUnknown, Unknown
		f.Target = &TargetFact{Fact: "unread"}
		f.Explanation = st.Key + " could not be read on the target, so whether it matches is not known."
	case item.Kind == diff.Unchanged:
		f.Code, f.Classification = CodeConfigPresent, AlreadySatisfied
		f.Target = &TargetFact{Fact: "present"}
		f.Explanation = "The target already holds this value of " + st.Key + "."
	case st.Operational:
		f.Code, f.Classification, f.Category = CodeConfigEnvironment, Excluded, CategoryOperational
		f.Target = &TargetFact{Fact: "differs", Detail: bound(strings.Join(item.Source, ", "), MaxFactRunes)}
		f.Explanation = st.Key + " names something about the machine -- a path, a host, a port -- rather than configuring the software. It is expected to differ between servers."
	case missingResource != "":
		f.Code, f.Classification = CodeConfigMissing, PrerequisiteRequired
		f.Target = &TargetFact{Fact: "resource_absent"}
		f.ManualAction, f.BlocksPortability = true, true
		f.Causes = []string{missingResource}
		f.Explanation = st.Key + " belongs to a configuration object the target does not have."
	case item.Kind == diff.Added:
		// Added from the live side's point of view: the snapshot has it and
		// the target does not.
		f.Code, f.Classification = CodeConfigMissing, PrerequisiteRequired
		f.Target = &TargetFact{Fact: "absent"}
		f.ManualAction = true
		f.Explanation = "The target does not set " + st.Key + ". Alder changes settings an entry already holds; adding this one is left to an operator."
	case item.Actionable == diff.ActionableWritable:
		f.Code, f.Classification = CodeConfigChangeable, PrerequisiteRequired
		f.Target = &TargetFact{Fact: "differs", Detail: bound(strings.Join(item.Source, ", "), MaxFactRunes)}
		f.Prerequisites = []Prerequisite{{Type: "configuration", Setting: st.ID()}}
		f.Explanation = "The target holds " + quotedValues(item.Source) + " for " + st.Key + ", and the snapshot " +
			quotedValues(item.Target) + ". Alder changes this setting through a plan: compare the snapshot with the directory and stage it."
	default:
		f.Code, f.Classification = CodeConfigManual, PrerequisiteRequired
		f.Target = &TargetFact{Fact: "differs", Detail: bound(strings.Join(item.Source, ", "), MaxFactRunes)}
		f.ManualAction = true
		f.Prerequisites = []Prerequisite{{Type: "configuration", Setting: st.ID()}}
		why := "Alder has no proven way to change it"
		if st.Comparison == snapshot.ComparisonRaw {
			why = "its value has a syntax Alder does not parse, and was compared as text"
		}
		f.Explanation = "The target holds " + quotedValues(item.Source) + " for " + st.Key + ", and the snapshot " +
			quotedValues(item.Target) + ". " + strings.ToUpper(why[:1]) + why[1:] + ", so an operator has to change it on the server."
	}
	e.b.add(f)
}

func quotedValues(values []string) string {
	if len(values) == 0 {
		return "no value"
	}
	joined := strings.Join(values, ", ")
	return strconv.Quote(bound(joined, 120))
}
