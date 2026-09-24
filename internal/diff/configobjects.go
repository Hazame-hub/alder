package diff

import (
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Configuration objects in a comparison (1.16).
//
// Until now a comparison answered about settings only, and an object one side
// had and the other did not was reported through its settings. That was
// honest and it was also unhelpful: "the target has no memberof overlay" is
// one fact, not fourteen.
//
// So a comparison now lists the objects too -- databases, overlays, backends,
// plugins -- as added, removed or unchanged. Listing is all it does for most
// of them. Exactly one kind can be acted on, and only where its precondition
// holds: an OpenLDAP overlay whose module the server has already loaded. That
// is the narrow list this release was scoped to, and the model decides it, not
// this file.

// ConfigObject is one configuration object's difference.
type ConfigObject struct {
	Kind Kind `json:"kind"`
	// ID is the object's identity: its own kind and name.
	ID       string `json:"id"`
	Section  string `json:"section"`
	Object   string `json:"object"`
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	DN       string `json:"dn,omitempty"`
	Settings int    `json:"settings"`
	// Actionable says whether Alder could create or remove it.
	Actionable string `json:"actionable"`
	// Refusal is why not, when it is not actionable: not_creatable,
	// module_not_loaded, parent_missing, unnamed, not_present.
	Refusal string `json:"refusal,omitempty"`
	// Destructive marks a removal, which is never selected for anyone.
	Destructive bool `json:"destructive,omitempty"`
}

// compareObjects lists what objects each side holds, and what Alder could do
// about the difference.
//
// Direction is the comparison's own: added means the target has it and the
// source does not, so making the source match means creating it.
func compareObjects(source, target *snapshot.ConfigSnapshot, live ConfigSide) []ConfigObject {
	settings := map[string]int{}
	for _, side := range []*snapshot.ConfigSnapshot{source, target} {
		for _, setting := range side.Settings {
			if setting.Resource != "" {
				settings[setting.Resource]++
			}
		}
	}

	byID := map[string]snapshot.ConfigResource{}
	var order []string
	for _, side := range []*snapshot.ConfigSnapshot{source, target} {
		for _, resource := range side.Resources {
			if _, seen := byID[resource.ID()]; !seen {
				order = append(order, resource.ID())
			}
			// The live side's own record wins, so a DN is the one on the
			// server a change would be sent to.
			if _, ok := byID[resource.ID()]; !ok || side == live.Snapshot {
				byID[resource.ID()] = resource
			}
		}
	}
	sort.Strings(order)

	out := make([]ConfigObject, 0, len(order))
	for _, id := range order {
		resource := byID[id]
		_, inSource := source.ResourceByID(id)
		_, inTarget := target.ResourceByID(id)
		object := ConfigObject{
			ID: id, Section: resource.Section, Object: resource.Kind, Name: resource.Name,
			Label: resource.Label, DN: resource.DN, Settings: settings[id],
			Actionable: ActionableReadOnly,
		}
		switch {
		case inSource && inTarget:
			object.Kind = Unchanged
		case inTarget:
			object.Kind = Added
		default:
			object.Kind = Removed
			object.Destructive = true
		}
		object.Actionable, object.Refusal = objectAction(object, live)
		out = append(out, object)
	}
	return out
}

// objectAction decides whether Alder could make this object difference, by
// asking the provider's model for the change it would send.
func objectAction(object ConfigObject, live ConfigSide) (string, string) {
	if object.Kind == Unchanged || !live.Live || live.Snapshot == nil {
		if object.Kind == Unchanged {
			return ActionableReadOnly, ""
		}
		return ActionableReadOnly, BlockedConfigSourceNotLive
	}
	want := snapshot.ConfigResource{Section: object.Section, Kind: object.Object, Name: object.Name,
		DN: object.DN, Label: object.Label}
	var refusal string
	if object.Kind == Added {
		_, refusal = config.CreateRecord(live.Snapshot, want)
	} else {
		_, refusal = config.RemoveRecord(live.Snapshot, want)
	}
	if refusal != "" {
		return ActionableReadOnly, refusal
	}
	return ActionableWritable, ""
}

// DeriveConfigObject turns one object difference into the change that would
// make the live server match.
//
// A removal is derived only when the caller asks for it by name: nothing in
// Alder selects a deletion for anyone, and a configuration object holds
// settings that go with it.
func DeriveConfigObject(r *ConfigResult, object ConfigObject, source ConfigSide, includeRemoval bool) ConfigCandidate {
	if r.ProviderMismatch || !source.Live || source.Snapshot == nil {
		return ConfigCandidate{Blocked: BlockedConfigSourceNotLive}
	}
	if object.Kind == Unchanged {
		return ConfigCandidate{Blocked: BlockedConfigNothing}
	}
	if object.Kind == Removed && !includeRemoval {
		return ConfigCandidate{Blocked: BlockedConfigRemovalNotSelected}
	}
	want := snapshot.ConfigResource{Section: object.Section, Kind: object.Object, Name: object.Name,
		DN: object.DN, Label: object.Label}
	var record directory.ChangeRecord
	var refusal string
	if object.Kind == Added {
		record, refusal = config.CreateRecord(source.Snapshot, want)
	} else {
		record, refusal = config.RemoveRecord(source.Snapshot, want)
	}
	if refusal != "" {
		return ConfigCandidate{Blocked: refusal}
	}
	return ConfigCandidate{Records: []directory.ChangeRecord{record}}
}

// BlockedConfigRemovalNotSelected is why a removal proposes nothing until it
// is named.
const BlockedConfigRemovalNotSelected = "removal_not_selected"

// ObjectByID finds one object difference.
func (r *ConfigResult) ObjectByID(id string) (ConfigObject, bool) {
	for _, object := range r.Objects {
		if strings.EqualFold(object.ID, id) {
			return object, true
		}
	}
	return ConfigObject{}, false
}
