package diff

import (
	"errors"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Turning a configuration difference into plan input.
//
// The same single destination as everything else: a change record, judged by
// the plan against the directory as it is at that moment. A configuration
// change is an ordinary modification of an ordinary entry that happens to live
// in the configuration tree, which is the only way Alder has ever written
// configuration -- and this milestone adds no other.
//
// So very little is derivable, deliberately:
//
//   - the source must be the live directory, because the change is made there;
//   - the setting must exist on both sides, so this is a modification of an
//     attribute that is already on an entry that is already there;
//   - the provider's model must state that the setting is one an administrator
//     sets, and Alder must already change it -- both are the same list, and
//     the list is short;
//   - a secret is never written back: its value was never read.
//
// A setting that appears or disappears would mean adding or removing a
// configuration entry or an attribute whose absence means something
// provider-specific. That is reported and never derived.

// Reasons a configuration difference proposes no change.
const (
	BlockedConfigSourceNotLive = "source_not_live"
	BlockedConfigNothing       = "nothing_to_change"
	BlockedConfigReadOnly      = "read_only"
	BlockedConfigSensitive     = "sensitive"
	BlockedConfigAddRemove     = "add_or_remove"
	BlockedConfigUnknownTarget = "unknown_location"
	// BlockedConfigUnread: one side did not read the setting, so there is no
	// difference to act on -- only an unanswered question.
	BlockedConfigUnread = "not_read"
)

// ConfigCandidate is what a configuration difference would become as plan input.
type ConfigCandidate struct {
	Records []directory.ChangeRecord
	Blocked string
}

// DeriveConfig turns one configuration difference into the change record that
// would move the live source toward the target.
func DeriveConfig(r *ConfigResult, item ConfigItem, source ConfigSide) ConfigCandidate {
	if r.ProviderMismatch {
		return ConfigCandidate{Blocked: BlockedConfigSourceNotLive}
	}
	if !source.Live {
		return ConfigCandidate{Blocked: BlockedConfigSourceNotLive}
	}
	switch item.Kind {
	case Unchanged:
		return ConfigCandidate{Blocked: BlockedConfigNothing}
	case Added, Removed:
		return ConfigCandidate{Blocked: BlockedConfigAddRemove}
	case Unknown:
		return ConfigCandidate{Blocked: BlockedConfigUnread}
	}
	if item.Sensitive {
		return ConfigCandidate{Blocked: BlockedConfigSensitive}
	}
	if item.Actionable != ActionableWritable {
		return ConfigCandidate{Blocked: BlockedConfigReadOnly}
	}
	target, err := configEntryDN(r, item, source)
	if err != nil {
		return ConfigCandidate{Blocked: BlockedConfigUnknownTarget}
	}
	values := make([][]byte, 0, len(item.Target))
	for _, v := range item.Target {
		values = append(values, []byte(inLiveSpelling(item, v)))
	}
	return ConfigCandidate{Records: []directory.ChangeRecord{{
		DN:   target,
		Type: directory.ChangeModify,
		Mods: []directory.Mod{{Op: directory.ModReplace, Name: item.Key, Values: values}},
	}}}
}

// configEntryDN is the entry on the live side that holds the setting. It comes
// from that side's own snapshot, never from the other side's: the two servers
// may keep the same setting in differently named entries.
func configEntryDN(r *ConfigResult, item ConfigItem, source ConfigSide) (dn.DN, error) {
	if source.Snapshot == nil {
		return nil, errors.New("diff: the live side has no configuration snapshot")
	}
	setting, ok := source.Snapshot.SettingByID(item.ID)
	if !ok || setting.DN == "" {
		return nil, errors.New("diff: the live configuration does not hold that setting")
	}
	return dn.Parse(setting.DN)
}

// ConfigWritable reports whether a provider's model states that Alder changes
// this setting through the ordinary plan. It is what the interface asks before
// offering anything.
func ConfigWritable(setting *snapshot.ConfigSetting) bool {
	return setting != nil && setting.Mutability == snapshot.MutabilityWritable && !setting.Sensitive
}

// inLiveSpelling writes a boolean the way the live server spells its own. A
// document from 1.13 lower-cased booleans, and OpenLDAP refuses a lower-case
// TRUE; the live side's value is the server's own answer to how it spells one.
func inLiveSpelling(item ConfigItem, value string) string {
	if item.Type != "bool" || len(item.Source) == 0 {
		return value
	}
	switch live := item.Source[0]; {
	case live == strings.ToUpper(live):
		return strings.ToUpper(value)
	case live == strings.ToLower(live):
		return strings.ToLower(value)
	}
	return value
}
