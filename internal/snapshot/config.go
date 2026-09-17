package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Configuration snapshots.
//
// The same format as the other two -- "alder-snapshot", version 1 -- with kind
// "config" and a field set of its own, exactly as kind "schema" was added in
// 1.10. Version 1 admits new kinds: a reader refuses a kind it does not know,
// and each kind refuses fields it does not know, so no two kinds can be
// mistaken for one another and a release before 1.13 refuses a config snapshot
// rather than misreading it.
//
// What it holds is one server's persistent configuration, normalised by a
// model belonging to that server's software:
//
//   - A setting is one attribute of one configuration entry, identified by
//     provider, section, the resource it belongs to and its key -- never by its
//     position in a list, and never by a display label.
//   - A resource is named by what identifies it in the provider's own terms: a
//     database by its suffix, an overlay by its name, a plugin by its name.
//     Two captures of the same server pair the same resources whatever order
//     the server returned them in.
//   - Runtime state is not configuration and is not here: monitoring entries,
//     counters, and the attributes a server maintains for itself.
//   - A sensitive value is recorded as present and counted, never as a value
//     and never as anything derived from one.
//
// There is no shared model across providers. Two providers may have a section
// with the same name without any of their settings meaning the same thing,
// which is why a comparison of two different providers' configuration reports a
// provider mismatch rather than a list of differences.

// KindConfig is the kind of a configuration snapshot. Introduced in Alder 1.13.
const KindConfig = "config"

// Providers Alder has a configuration model for.
const (
	ProviderOpenLDAP = "openldap"
	Provider389DS    = "389ds"
)

// Completeness of a configuration snapshot.
const (
	// ConfigComplete means every section the model covers was read.
	ConfigComplete = "complete"
	// ConfigPartial means something was not, and Incomplete says why.
	ConfigPartial = "partial"
)

// Why a configuration capture is partial. Stable identifiers.
const (
	IncompleteAccess      = "insufficient_access"
	IncompleteUnreadable  = "read_failed"
	IncompleteUnsupported = "unsupported_section"
	IncompleteLimit       = "read_limit_reached"
)

// Mutability of a setting, as the provider's model states it -- not as this
// session's rights make it. Whether this bind may write is a separate
// question, and a comparison answers it separately.
const (
	// MutabilityWritable: the provider treats the setting as configuration an
	// administrator sets.
	MutabilityWritable = "writable"
	// MutabilityReadOnly: the server maintains it, or it is fixed at build or
	// start time.
	MutabilityReadOnly = "read_only"
	// MutabilityUnknown: the model does not know.
	MutabilityUnknown = "unknown"
)

// How a setting's values are compared.
const (
	// ComparisonNormalised: the model understands the value and compares what
	// it means.
	ComparisonNormalised = "normalised"
	// ComparisonRaw: the value is compared as text, because nothing here
	// parses it.
	ComparisonRaw = "raw"
)

// Bounds on a configuration snapshot. A configuration is small; these stop a
// server -- or a document claiming to be one -- from being unbounded.
const (
	MaxConfigSettings  = 20000
	MaxConfigResources = 4000
	MaxConfigValues    = 2000
	maxConfigValueLen  = 64 << 10
)

// ConfigSource is where a configuration snapshot was captured.
type ConfigSource struct {
	// Provider is the configuration model used, not a display name.
	Provider      string `json:"provider"`
	Vendor        string `json:"vendor,omitempty"`
	VendorVersion string `json:"vendorVersion,omitempty"`
	// Root is the configuration tree the server announced or answered at.
	Root string `json:"root"`
}

// ConfigCoverage names what the model reads and what it deliberately leaves to
// something else.
type ConfigCoverage struct {
	// Sections are the categories this provider's model covers.
	Sections []string `json:"sections"`
	// Excluded names what is left out on purpose, as stable identifiers.
	Excluded []string `json:"excluded"`
}

// What a configuration snapshot deliberately excludes.
const (
	ExcludedRuntime     = "runtime-state"
	ExcludedSchema      = "schema-definitions"
	ExcludedSecrets     = "sensitive-values"
	ExcludedServerOwned = "server-maintained-attributes"
)

// ConfigIncomplete is one reason a capture did not cover everything.
type ConfigIncomplete struct {
	// Reason is one of the stable identifiers above.
	Reason string `json:"reason"`
	// Scope is the section or location concerned.
	Scope  string `json:"scope,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ConfigResource is one configuration object a provider repeats: a database, an
// overlay, a plugin, a backend, a replication agreement.
type ConfigResource struct {
	// Section is the category it belongs to.
	Section string `json:"section"`
	// Kind is what it is in the provider's terms: database, overlay, module,
	// plugin, backend, mapping-tree, encryption, global.
	Kind string `json:"kind"`
	// Name identifies it within its kind: a suffix, an overlay's name, a
	// plugin's name. Stable across captures, and never a position.
	Name string `json:"name"`
	// DN is where it was read from, kept for diagnostics.
	DN string `json:"dn,omitempty"`
	// Label is what a person would call it, when that differs from Name.
	Label string `json:"label,omitempty"`
}

// ID identifies a resource within a snapshot.
func (r ConfigResource) ID() string { return r.Kind + ":" + strings.ToLower(r.Name) }

// ConfigSetting is one attribute of one configuration object.
type ConfigSetting struct {
	Section string `json:"section"`
	// Resource is the resource's ID; empty for a provider's global settings.
	Resource string `json:"resource,omitempty"`
	// Key is the setting's name in the provider's own vocabulary, in the
	// spelling the provider uses.
	Key string `json:"key"`
	// Values are the values, canonicalised by the provider's model. A
	// withheld setting has none.
	Values []string `json:"values,omitempty"`
	// Ordered reports that the order of Values carries meaning, so a
	// comparison must not treat them as a set.
	Ordered bool `json:"ordered,omitempty"`
	// Type is what the value is: string, int, bool, dn, path, url or
	// structured. Display and comparison use it; nothing branches on it.
	Type string `json:"type"`
	// Mutability is the provider's view of whether this is something an
	// administrator sets.
	Mutability string `json:"mutability"`
	// Comparison is how the values are compared.
	Comparison string `json:"comparison"`
	// Sensitive reports that the value is a secret. It is never recorded.
	Sensitive bool `json:"sensitive,omitempty"`
	// Withheld is how many values a sensitive setting had.
	Withheld int `json:"withheld,omitempty"`
	// Operational reports a value that is not secret and still says something
	// about the machine: a path, a host name, a port, a file name.
	Operational bool `json:"operational,omitempty"`
	// DN is the entry the setting was read from.
	DN string `json:"dn,omitempty"`
}

// ID identifies a setting within a snapshot: provider, section, resource, key.
func (s ConfigSetting) ID() string {
	return s.Section + "/" + s.Resource + "/" + strings.ToLower(s.Key)
}

// ConfigCounts counts what a snapshot holds.
type ConfigCounts struct {
	Resources   int `json:"resources"`
	Settings    int `json:"settings"`
	Withheld    int `json:"withheld"`
	Operational int `json:"operational"`
	ReadOnly    int `json:"readOnly"`
}

// ConfigSnapshot is a configuration snapshot document.
type ConfigSnapshot struct {
	Format    string       `json:"format"`
	Version   int          `json:"version"`
	Kind      string       `json:"kind"`
	CreatedAt string       `json:"createdAt"`
	Source    ConfigSource `json:"source"`
	// Completeness is complete or partial; Incomplete says why when partial.
	Completeness string             `json:"completeness"`
	Incomplete   []ConfigIncomplete `json:"incomplete"`
	Coverage     ConfigCoverage     `json:"coverage"`
	Counts       ConfigCounts       `json:"counts"`
	Resources    []ConfigResource   `json:"resources"`
	Settings     []ConfigSetting    `json:"settings"`
	// Checksum is "sha256:<hex>" over everything but CreatedAt and Checksum,
	// with the same meaning as the other kinds': integrity, not authenticity.
	// A withheld value contributes its count, never a digest of itself.
	Checksum string `json:"checksum,omitempty"`
}

// ConfigCapture describes a capture for BuildConfig.
type ConfigCapture struct {
	Provider      string
	Vendor        string
	VendorVersion string
	Root          string
	Sections      []string
	Excluded      []string
	Incomplete    []ConfigIncomplete
	CreatedAt     time.Time
}

// BuildConfig turns a provider's normalised configuration into a canonical
// snapshot.
func BuildConfig(c ConfigCapture, resources []ConfigResource, settings []ConfigSetting) (*ConfigSnapshot, error) {
	if len(settings) > MaxConfigSettings {
		return nil, &Error{Code: CodeTooLarge,
			Detail: fmt.Sprintf("%d settings, and a configuration snapshot holds at most %d", len(settings), MaxConfigSettings)}
	}
	if len(resources) > MaxConfigResources {
		return nil, &Error{Code: CodeTooLarge,
			Detail: fmt.Sprintf("%d resources, and a configuration snapshot holds at most %d", len(resources), MaxConfigResources)}
	}
	s := &ConfigSnapshot{
		Format: Format, Version: Version, Kind: KindConfig,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		Source: ConfigSource{Provider: c.Provider, Vendor: c.Vendor, VendorVersion: c.VendorVersion,
			Root: c.Root},
		Coverage:   ConfigCoverage{Sections: sortedUnique(c.Sections), Excluded: sortedUnique(c.Excluded)},
		Incomplete: append([]ConfigIncomplete{}, c.Incomplete...),
		Resources:  append([]ConfigResource{}, resources...),
		Settings:   append([]ConfigSetting{}, settings...),
	}
	if s.Coverage.Sections == nil {
		s.Coverage.Sections = []string{}
	}
	if s.Coverage.Excluded == nil {
		s.Coverage.Excluded = []string{}
	}
	s.canonicaliseConfig()
	if err := s.validateConfig(); err != nil {
		return nil, err
	}
	sum, err := s.configChecksum()
	if err != nil {
		return nil, err
	}
	s.Checksum = sum
	return s, nil
}

// canonicaliseConfig puts a snapshot in the one order two captures of an
// unchanged configuration both produce.
//
// Values are sorted only where the provider's model says their order carries
// no meaning. An ordered setting keeps the order the server gave, because for
// those the order is the configuration.
func (s *ConfigSnapshot) canonicaliseConfig() {
	sort.SliceStable(s.Resources, func(i, j int) bool {
		if s.Resources[i].Section != s.Resources[j].Section {
			return s.Resources[i].Section < s.Resources[j].Section
		}
		return s.Resources[i].ID() < s.Resources[j].ID()
	})
	sort.SliceStable(s.Settings, func(i, j int) bool { return s.Settings[i].ID() < s.Settings[j].ID() })
	sort.SliceStable(s.Incomplete, func(i, j int) bool {
		if s.Incomplete[i].Reason != s.Incomplete[j].Reason {
			return s.Incomplete[i].Reason < s.Incomplete[j].Reason
		}
		return s.Incomplete[i].Scope < s.Incomplete[j].Scope
	})
	counts := ConfigCounts{Resources: len(s.Resources), Settings: len(s.Settings)}
	for i := range s.Settings {
		setting := &s.Settings[i]
		if setting.Sensitive {
			setting.Values = nil
		} else if !setting.Ordered {
			sort.Strings(setting.Values)
		}
		if setting.Values == nil {
			setting.Values = []string{}
		}
		if setting.Type == "" {
			setting.Type = "string"
		}
		if setting.Mutability == "" {
			setting.Mutability = MutabilityUnknown
		}
		if setting.Comparison == "" {
			setting.Comparison = ComparisonRaw
		}
		if setting.Sensitive {
			counts.Withheld++
		}
		if setting.Operational {
			counts.Operational++
		}
		if setting.Mutability == MutabilityReadOnly {
			counts.ReadOnly++
		}
	}
	s.Counts = counts
	s.Completeness = ConfigComplete
	if len(s.Incomplete) > 0 {
		s.Completeness = ConfigPartial
	}
	if s.Incomplete == nil {
		s.Incomplete = []ConfigIncomplete{}
	}
	if s.Resources == nil {
		s.Resources = []ConfigResource{}
	}
	if s.Settings == nil {
		s.Settings = []ConfigSetting{}
	}
}

func (s *ConfigSnapshot) configChecksum() (string, error) {
	content := *s
	content.CreatedAt = ""
	content.Checksum = ""
	raw, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// EncodeConfig writes the snapshot as its canonical document.
func EncodeConfig(w io.Writer, s *ConfigSnapshot) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// DecodeConfig reads and validates a configuration snapshot document.
//
// Everything is checked before it is used, as for the other kinds: the format,
// version and kind, that no field is unknown and nothing follows the document,
// that every setting names a resource the snapshot holds, that no setting is
// recorded twice, that the counts are what the document holds, that a withheld
// setting carries no values, and that the checksum matches.
func DecodeConfig(data []byte) (*ConfigSnapshot, Integrity, error) {
	var probe struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
		Kind    string `json:"kind"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil || probe.Format != Format {
		return nil, "", &Error{Code: CodeNotSnapshot, Detail: "the document is not an Alder snapshot (no \"format\": \"alder-snapshot\")"}
	}
	if probe.Version > Version {
		return nil, "", &Error{Code: CodeUnsupportedVersion,
			Detail: fmt.Sprintf("snapshot version %d is newer than this Alder reads (%d); use a newer Alder", probe.Version, Version)}
	}
	if probe.Version < 1 {
		return nil, "", &Error{Code: CodeUnsupportedVersion, Detail: fmt.Sprintf("snapshot version %d does not exist", probe.Version)}
	}
	if probe.Kind != KindConfig {
		return nil, "", &Error{Code: CodeInvalid, Detail: fmt.Sprintf("kind %q is not a configuration snapshot", probe.Kind)}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s ConfigSnapshot
	if err := dec.Decode(&s); err != nil {
		return nil, "", &Error{Code: CodeInvalid, Detail: err.Error()}
	}
	if dec.More() {
		return nil, "", &Error{Code: CodeInvalid, Detail: "the document continues after the snapshot"}
	}
	claimedCompleteness, claimedCounts := s.Completeness, s.Counts
	if err := s.validateConfig(); err != nil {
		return nil, "", err
	}
	claimed := s.Checksum
	s.canonicaliseConfig()
	if s.Completeness != claimedCompleteness {
		return nil, "", &Error{Code: CodeInvalid,
			Detail: fmt.Sprintf("completeness says %q and the document is %q", claimedCompleteness, s.Completeness)}
	}
	if s.Counts != claimedCounts {
		return nil, "", &Error{Code: CodeInvalid, Detail: "counts do not match what the document holds"}
	}
	sum, err := s.configChecksum()
	if err != nil {
		return nil, "", err
	}
	s.Checksum = sum
	if claimed == "" {
		return &s, IntegrityUnverified, nil
	}
	if claimed != sum {
		return nil, "", &Error{Code: CodeChecksumMismatch,
			Detail: "the content does not match its checksum: the file was corrupted or edited after capture"}
	}
	return &s, IntegrityVerified, nil
}

func (s *ConfigSnapshot) validateConfig() error {
	invalid := func(format string, args ...any) error {
		return &Error{Code: CodeInvalid, Detail: fmt.Sprintf(format, args...)}
	}
	if s.Kind != KindConfig {
		return invalid("kind %q is not a configuration snapshot", s.Kind)
	}
	switch s.Source.Provider {
	case ProviderOpenLDAP, Provider389DS:
	default:
		return invalid("provider %q is not one this Alder has a configuration model for", s.Source.Provider)
	}
	switch s.Completeness {
	case ConfigComplete, ConfigPartial:
	default:
		return invalid("completeness %q is not complete or partial", s.Completeness)
	}
	if len(s.Settings) > MaxConfigSettings || len(s.Resources) > MaxConfigResources {
		return &Error{Code: CodeTooLarge, Detail: "the snapshot holds more settings or resources than Alder reads"}
	}
	for _, reason := range s.Incomplete {
		switch reason.Reason {
		case IncompleteAccess, IncompleteUnreadable, IncompleteUnsupported, IncompleteLimit:
		default:
			return invalid("%q is not a reason a capture is incomplete", reason.Reason)
		}
		if err := boundedText(reason.Scope, "an incomplete scope"); err != nil {
			return err
		}
		if err := boundedText(reason.Detail, "an incomplete detail"); err != nil {
			return err
		}
	}
	if s.Completeness == ConfigPartial && len(s.Incomplete) == 0 {
		return invalid("the snapshot is partial and says nothing about why")
	}

	sections := map[string]bool{}
	for _, name := range s.Coverage.Sections {
		sections[name] = true
	}
	resources := map[string]bool{}
	for _, r := range s.Resources {
		if r.Kind == "" || r.Name == "" {
			return invalid("a resource has no kind or no name")
		}
		if err := boundedText(r.Name, "a resource name"); err != nil {
			return err
		}
		if err := boundedText(r.DN, "a resource DN"); err != nil {
			return err
		}
		if err := boundedText(r.Label, "a resource label"); err != nil {
			return err
		}
		if resources[r.ID()] {
			return invalid("resource %s is in the snapshot twice", r.ID())
		}
		if len(sections) > 0 && !sections[r.Section] {
			return invalid("resource %s is in section %q, which the coverage does not name", r.ID(), r.Section)
		}
		resources[r.ID()] = true
	}

	seen := map[string]bool{}
	for _, setting := range s.Settings {
		if setting.Key == "" {
			return invalid("a setting has no key")
		}
		if err := boundedText(setting.Key, "a setting key"); err != nil {
			return err
		}
		if err := boundedText(setting.DN, "a setting DN"); err != nil {
			return err
		}
		if len(sections) > 0 && !sections[setting.Section] {
			return invalid("setting %s is in section %q, which the coverage does not name", setting.ID(), setting.Section)
		}
		if setting.Resource != "" && !resources[setting.Resource] {
			return invalid("setting %s names resource %q, which the snapshot does not hold", setting.ID(), setting.Resource)
		}
		switch setting.Mutability {
		case MutabilityWritable, MutabilityReadOnly, MutabilityUnknown:
		default:
			return invalid("setting %s has mutability %q", setting.ID(), setting.Mutability)
		}
		switch setting.Comparison {
		case ComparisonNormalised, ComparisonRaw:
		default:
			return invalid("setting %s has comparison %q", setting.ID(), setting.Comparison)
		}
		if setting.Sensitive && len(setting.Values) > 0 {
			return invalid("setting %s is sensitive and carries values", setting.ID())
		}
		if !setting.Sensitive && setting.Withheld > 0 {
			return invalid("setting %s withholds values without being sensitive", setting.ID())
		}
		if len(setting.Values) > MaxConfigValues {
			return &Error{Code: CodeTooLarge, Detail: fmt.Sprintf("setting %s has more values than Alder reads", setting.ID())}
		}
		for _, v := range setting.Values {
			if err := boundedText(v, "a value of "+setting.Key); err != nil {
				return err
			}
		}
		if seen[setting.ID()] {
			return invalid("setting %s is in the snapshot twice", setting.ID())
		}
		seen[setting.ID()] = true
	}
	return nil
}

// boundedText refuses text that is too long or not valid UTF-8, so a document
// from anywhere cannot carry something no reader should have to handle.
func boundedText(s, what string) error {
	if len(s) > maxConfigValueLen {
		return &Error{Code: CodeTooLarge, Detail: what + " is longer than Alder reads"}
	}
	if !utf8.ValidString(s) {
		return &Error{Code: CodeInvalid, Detail: what + " is not valid UTF-8"}
	}
	return nil
}

// SettingByID returns a setting by its identity.
func (s *ConfigSnapshot) SettingByID(id string) (*ConfigSetting, bool) {
	for i := range s.Settings {
		if s.Settings[i].ID() == id {
			return &s.Settings[i], true
		}
	}
	return nil, false
}

// ResourceByID returns a resource by its identity.
func (s *ConfigSnapshot) ResourceByID(id string) (*ConfigResource, bool) {
	for i := range s.Resources {
		if s.Resources[i].ID() == id {
			return &s.Resources[i], true
		}
	}
	return nil, false
}
