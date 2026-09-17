package preflight

import (
	"context"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Target is what a preflight reads. It is a directory session without the one
// method that writes: a type that cannot write cannot be made to by accident.
type Target interface {
	Capabilities() directory.Capabilities
	RefreshSchema(ctx context.Context) (*schema.Schema, error)
	Read(ctx context.Context, target dn.DN, attrs []string) (*directory.Entry, error)
	SchemaDefinitions(ctx context.Context, targetDN string, kind directory.SchemaDefKind) ([]string, error)
	VisibilityOf(ctx context.Context, target dn.DN, attribute string) (directory.AttributeVisibility, error)
}

// Presence is what the target shows about one DN.
type Presence int

const (
	// PresenceUnknown: the read failed in a way that says nothing.
	PresenceUnknown Presence = iota
	// PresencePresent: the entry was read.
	PresencePresent
	// PresenceAbsent: the server says there is no such entry, and nothing
	// suggests it is concealing one. On a server that conceals entries by
	// answering exactly as it would for a missing one, this is the most that
	// can be known; the report says so.
	PresenceAbsent
	// PresenceHidden: the server declined to show an entry that, by its own
	// answer, exists.
	PresenceHidden
)

// maxPresenceProbes bounds the Compare round trips one preflight spends telling
// a hidden entry from an absent one. Past it, an unread entry is unknown.
const maxPresenceProbes = 500

// presenceProbeAttribute is the attribute a presence probe compares. It has to
// accept the probe's value under its syntax on every server: objectClass does
// not -- its syntax is an OID, and OpenLDAP answers a compare with any other
// value "invalid attribute syntax" before it looks at the entry at all, which
// made every absent entry unknown. cn is a directory string on both servers,
// and an entry that does not hold one still answers "no such attribute", which
// is as much an admission that it exists as a match.
const presenceProbeAttribute = "cn"

// presence resolves DNs against the target, once each.
type presence struct {
	target   Target
	notFound func(error) bool
	cache    map[string]presenceResult
	probes   int
}

type presenceResult struct {
	state Presence
	entry *directory.Entry
}

func newPresence(t Target, notFound func(error) bool) *presence {
	return &presence{target: t, notFound: notFound, cache: map[string]presenceResult{}}
}

// of reads a DN and decides whether it is there.
//
// A read that finds nothing is not yet an absence. A server that hides an entry
// from this bind may answer "no such object", or answer with no entries; asked
// with a Compare, some servers then say "insufficient access", which is an
// admission that the entry exists. Only an answer of "no such object" to both
// is taken as absent.
func (p *presence) of(ctx context.Context, target dn.DN) presenceResult {
	key := strings.ToLower(target.String())
	if got, ok := p.cache[key]; ok {
		return got
	}
	result := p.resolve(ctx, target)
	p.cache[key] = result
	return result
}

func (p *presence) resolve(ctx context.Context, target dn.DN) presenceResult {
	entry, err := p.target.Read(ctx, target, []string{"*"})
	if err == nil {
		return presenceResult{state: PresencePresent, entry: entry}
	}
	if p.notFound == nil || !p.notFound(err) {
		return presenceResult{state: PresenceUnknown}
	}
	if p.probes >= maxPresenceProbes {
		return presenceResult{state: PresenceUnknown}
	}
	p.probes++
	visibility, probeErr := p.target.VisibilityOf(ctx, target, presenceProbeAttribute)
	switch {
	case probeErr != nil:
		return presenceResult{state: PresenceUnknown}
	case visibility == directory.VisibilityDenied, visibility == directory.VisibilityPresent,
		visibility == directory.VisibilityAbsent:
		// The server answered about the entry's attribute rather than about a
		// missing entry: whatever it said about the attribute, the entry is
		// there, and this bind could not read it.
		return presenceResult{state: PresenceHidden}
	}
	return presenceResult{state: PresenceAbsent}
}

// inNamingContexts reports whether a DN is under one of the target's naming
// contexts. With none announced, nothing can be said, and the answer is true
// with known false.
func inNamingContexts(caps directory.Capabilities, target dn.DN) (inside bool, known bool) {
	if len(caps.NamingContexts) == 0 {
		return true, false
	}
	for _, context := range caps.NamingContexts {
		suffix, err := dn.Parse(context)
		if err != nil {
			continue
		}
		if target.HasSuffix(suffix) {
			return true, true
		}
	}
	return false, true
}
