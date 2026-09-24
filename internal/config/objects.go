package config

import (
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Creating and removing a configuration object (1.16).
//
// Alder changes settings on entries that already exist. This is the one
// exception, and it is deliberately one thing: an OpenLDAP overlay whose
// module the server has already loaded. Adding the overlay is then an ordinary
// entry add into the configuration tree -- the server itself assigns the
// position in its name -- and removing it is an ordinary delete.
//
// Everything else stays out. A database is where the data lives; a module is a
// shared library the server must be able to find; a 389 DS plugin is a fixed
// set the server ships, switched with a setting rather than created. None of
// those is an entry Alder invents, and each would need a different proof.
//
// The precondition is not a preference. An overlay whose module is not loaded
// is refused by the server with a message about a handler, after Alder has
// already sent a write; refusing it here means the operator is told what is
// actually wrong, before anything is sent.

// Refusals: why an object cannot be created or removed. Stable identifiers.
const (
	// RefusalNotCreatable: this provider does not create objects of that kind.
	RefusalNotCreatable = "not_creatable"
	// RefusalModuleNotLoaded: the overlay's module is not loaded on the target.
	RefusalModuleNotLoaded = "module_not_loaded"
	// RefusalNoParent: the database or backend the object belongs to is not
	// there.
	RefusalNoParent = "parent_missing"
	// RefusalUnnamed: the object's identity does not name what it needs to.
	RefusalUnnamed = "unnamed"
	// RefusalNotPresent: it is not on the live server, so there is nothing to
	// remove.
	RefusalNotPresent = "not_present"
)

// CreateRecord is the change that would create a configuration object on the
// server the live snapshot came from, or the reason there is none.
func CreateRecord(live *snapshot.ConfigSnapshot, want snapshot.ConfigResource) (directory.ChangeRecord, string) {
	if live == nil || !strings.EqualFold(live.Source.Provider, snapshot.ProviderOpenLDAP) || want.Kind != KindOverlay {
		return directory.ChangeRecord{}, RefusalNotCreatable
	}
	database, overlay, ok := strings.Cut(want.Name, "/")
	if !ok || strings.TrimSpace(overlay) == "" {
		// An overlay is named for the database it sits on; without that, there
		// is nowhere to put it.
		return directory.ChangeRecord{}, RefusalUnnamed
	}
	parent, ok := live.ResourceByID(KindDatabase + ":" + strings.ToLower(database))
	if !ok || parent.DN == "" {
		return directory.ChangeRecord{}, RefusalNoParent
	}
	if !moduleLoaded(live, overlay) {
		return directory.ChangeRecord{}, RefusalModuleNotLoaded
	}
	parentDN, err := dn.Parse(parent.DN)
	if err != nil {
		return directory.ChangeRecord{}, RefusalNoParent
	}
	// The RDN the server is asked for carries no position: slapd assigns one
	// and stores the entry under the name it chose, which Alder already
	// reports as the DN a change came to rest at.
	target, err := dn.Parse("olcOverlay=" + dn.EscapeValue(overlay) + "," + parentDN.String())
	if err != nil {
		return directory.ChangeRecord{}, RefusalUnnamed
	}
	return directory.ChangeRecord{
		DN:   target,
		Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{
			{Name: "objectClass", Values: [][]byte{[]byte("olcOverlayConfig")}},
			{Name: "olcOverlay", Values: [][]byte{[]byte(overlay)}},
		},
	}, ""
}

// RemoveRecord is the change that would remove a configuration object from the
// server the live snapshot came from, or the reason there is none.
func RemoveRecord(live *snapshot.ConfigSnapshot, have snapshot.ConfigResource) (directory.ChangeRecord, string) {
	if live == nil || !strings.EqualFold(live.Source.Provider, snapshot.ProviderOpenLDAP) || have.Kind != KindOverlay {
		return directory.ChangeRecord{}, RefusalNotCreatable
	}
	// The entry removed is the live server's own, never the DN another
	// server's document happens to carry.
	resource, ok := live.ResourceByID(have.ID())
	if !ok || resource.DN == "" {
		return directory.ChangeRecord{}, RefusalNotPresent
	}
	target, err := dn.Parse(resource.DN)
	if err != nil {
		return directory.ChangeRecord{}, RefusalNotPresent
	}
	return directory.ChangeRecord{DN: target, Type: directory.ChangeDelete}, ""
}

// moduleLoaded reports whether the server has loaded the module an overlay
// needs, from what the capture recorded: the module list is configuration, and
// a loaded module is a fact about the running server.
func moduleLoaded(live *snapshot.ConfigSnapshot, overlay string) bool {
	for _, setting := range live.Settings {
		if !strings.EqualFold(setting.Key, "olcModuleLoad") {
			continue
		}
		for _, value := range setting.Values {
			// A module is written as a file name, sometimes with a path and a
			// suffix: memberof, memberof.la, /usr/lib/ldap/memberof.so.
			name := strings.ToLower(value)
			if i := strings.LastIndexAny(name, `/\`); i >= 0 {
				name = name[i+1:]
			}
			name = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".la"), ".so"), "-2.4")
			if name == strings.ToLower(overlay) {
				return true
			}
		}
	}
	return false
}

// CreatableKinds names the object kinds a provider's model creates, for a
// report and for the documentation to be written from.
func CreatableKinds(provider string) []string {
	if strings.EqualFold(provider, snapshot.ProviderOpenLDAP) {
		return []string{KindOverlay}
	}
	return nil
}
