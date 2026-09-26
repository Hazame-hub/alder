package config

import (
	"context"
	"errors"
	"strings"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// What the provider's model says about one configuration entry (1.18).
//
// The entry editor treats every entry alike, which is right for directory data
// and misleading for a server's own configuration: it offered all forty-odd
// attributes of cn=config as identical fields, with nothing to say that two of
// them are writable, that one takes effect only at the next start, and that
// another is maintained by the server. The model has known all of that since
// 1.13, and the only way to reach it was to capture the configuration and
// compare it with something.
//
// So this answers the same question about one entry, from the same model, read
// the same way. It never says what a bind may write -- that is the directory's
// answer, and Alder does not pretend to know it -- only what the model states
// about the setting.

// ErrNotConfiguration is returned for a DN that is not an entry in this
// server's configuration tree, or is one the model reads as something else:
// the schema, a task, a monitor.
var ErrNotConfiguration = errors.New("config: that entry is not part of this server's configuration model")

// AttributeModel is what the model says about one attribute of one entry.
type AttributeModel struct {
	Name    string `json:"name"`
	Section string `json:"section"`
	// Mutability is the model's own answer, the same one a capture records:
	// writable, read_only or unknown. A setting that needs a restart is
	// read_only here, because a change that does nothing until the server is
	// restarted is not a change Alder makes.
	Mutability string `json:"mutability"`
	// RestartRequired is why, where that is the reason: the server itself
	// named this setting as one that takes effect at the next start.
	RestartRequired bool `json:"restartRequired,omitempty"`
	// Sensitive marks a value a capture withholds. The editor still shows the
	// field; it is the document that never carries the value.
	Sensitive bool `json:"sensitive,omitempty"`
	// Excluded marks an attribute the model does not treat as configuration at
	// all: runtime state, or something the directory maintains.
	Excluded bool `json:"excluded,omitempty"`
}

// EntryModel is the model's answer about one configuration entry.
type EntryModel struct {
	DN       string `json:"dn"`
	Provider string `json:"provider"`
	// Resource is the configuration object the entry is, in the provider's own
	// words: the global entry, a database, an overlay, a plugin.
	Resource   snapshot.ConfigResource `json:"resource"`
	Attributes []AttributeModel        `json:"attributes"`
	// Incomplete says the tree could not be read in full, so the resource this
	// entry belongs to may have been named from less than the whole picture.
	Incomplete bool `json:"incomplete,omitempty"`
}

// Attribute finds the model's answer for one attribute, by name, folded.
func (m *EntryModel) Attribute(name string) (AttributeModel, bool) {
	for _, a := range m.Attributes {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return AttributeModel{}, false
}

// Describe answers what the provider's model says about one entry of the
// server's own configuration.
//
// It reads the tree, because the answer depends on it: which settings need a
// restart is read from the server, which plugins it can run without is read
// from its plugin entries, and an overlay is named after the database above
// it. Answering from the entry alone would mean answering from a list of
// vendor facts, which is the thing this package exists not to do.
func Describe(ctx context.Context, r Reader, target dn.DN) (*EntryModel, error) {
	read, err := readModel(ctx, r, Options{})
	if err != nil {
		return nil, err
	}
	classify := read.classify()
	wanted := strings.ToLower(target.String())

	byDN := map[string]Resource{}
	for _, entry := range read.entries {
		if read.model.skipTree != nil && read.model.skipTree(entry) {
			if strings.EqualFold(entry.DN.String(), target.String()) {
				// The schema, a task queue, a monitor: real entries, and not
				// what this model calls configuration.
				return nil, ErrNotConfiguration
			}
			continue
		}
		resource, ok := read.model.resource(entry, byDN)
		if !ok {
			continue
		}
		byDN[strings.ToLower(entry.DN.String())] = resource
		if strings.ToLower(entry.DN.String()) != wanted {
			continue
		}

		out := &EntryModel{
			DN: entry.DN.String(), Provider: read.model.provider,
			Resource: resource, Incomplete: read.truncated,
		}
		for _, name := range attributeNames(entry) {
			key := strings.ToLower(name)
			attribute := AttributeModel{
				Name:      name,
				Section:   classify.section(resource, name),
				Sensitive: classify.isSensitive(name),
			}
			if serverMaintained[key] || classify.skipped[key] {
				// Not configuration: runtime state, or the directory's own.
				attribute.Excluded = true
				attribute.Mutability = snapshot.MutabilityReadOnly
			} else {
				attribute.Mutability = classify.mutability(resource, name)
				attribute.RestartRequired = classify.restart[key]
			}
			out.Attributes = append(out.Attributes, attribute)
		}
		return out, nil
	}
	return nil, ErrNotConfiguration
}
