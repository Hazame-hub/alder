package indexadvice

import (
	"strings"

	"github.com/hazame-hub/alder/internal/config"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// FromConfig is the index list a configuration snapshot holds, live or read
// from a file, in the shape Read takes.
//
// A backend is one holding at least one index. The configuration model calls
// a great many things a backend (thirty-nine on the harness's 389 DS, one of
// which holds data), and a backend that serves directory data always holds
// some: both servers index objectClass on every database they create. A
// file carries no RootDSE, so this is the rule that works the same from a
// file and from the live server.
func FromConfig(snap *snapshot.ConfigSnapshot) []Backend {
	if snap == nil {
		return nil
	}
	var out []Backend
	for _, resource := range snap.Resources {
		if resource.Kind != config.KindDatabase && resource.Kind != config.KindBackend {
			continue
		}
		b := Backend{Suffix: resource.Name, Indexes: map[string][]string{}}
		for _, index := range snap.Resources {
			if index.Kind != config.KindIndex {
				continue
			}
			backend, attribute, ok := strings.Cut(index.Name, "/")
			if !ok || !strings.EqualFold(backend, resource.Name) {
				continue
			}
			if index.Label != "" {
				attribute = index.Label
			}
			b.Indexes[strings.ToLower(attribute)] = config.IndexTypesOf(index, snap.Settings)
		}
		if len(b.Indexes) == 0 {
			continue
		}
		name := resource.Name
		b.Blocked = func(attribute string, types []string) string {
			want := snapshot.ConfigResource{
				Section: config.SectionPerformance,
				Kind:    config.KindIndex,
				Name:    name + "/" + strings.ToLower(attribute),
				Label:   attribute,
			}
			if _, refusal := config.CreateRecord(snap, want, types); refusal != "" {
				return config.RefusalInWords(refusal)
			}
			return ""
		}
		out = append(out, b)
	}
	return out
}
