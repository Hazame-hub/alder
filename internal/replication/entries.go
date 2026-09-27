package replication

import (
	"context"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/snapshot"
)

// Replication as it reaches one entry, and the entries a server has marked as
// conflicting.
//
// Two questions the topology view cannot answer. "Has *this* change arrived"
// is asked about one entry, and the answer is its change sequence -- the same
// number the suffix cursor is made of, at entry scale, which two servers can
// be compared on. "What went wrong while I was not looking" is asked about a
// subtree, and only one of the two servers keeps an answer.

// EntryState is what a server records about one entry, for replication.
type EntryState struct {
	DN string
	// Changed is when this entry was last changed and by which server, read
	// from the entry's own change sequence. Zero Origin where the server
	// keeps no per-entry sequence.
	Changed Cursor
	// Identity is the server-assigned identifier that survives a rename, and
	// is how the same entry is recognised on another server.
	Identity string
	// Conflict is the server's own words where it has marked this entry as
	// conflicting, and empty otherwise.
	Conflict string
	// Notes explain what the server does and does not record here.
	Notes []string
	// Disclaimer is the constant, carried with the data.
	Disclaimer string
}

// entryAttributes are the per-entry replication attributes, on either server.
// Asked for by name because every one of them is operational: a plain read
// returns none of them.
var entryAttributes = []string{
	// OpenLDAP
	"entryCSN", "entryUUID",
	// 389 Directory Server
	"nsUniqueId", "nsds5ReplConflict",
	// Both
	"modifyTimestamp", "modifiersName",
}

// ForEntry reports what this server records about one entry's replication.
func ForEntry(ctx context.Context, r Reader, target dn.DN) (*EntryState, error) {
	entry, err := r.Read(ctx, target, entryAttributes)
	if err != nil {
		return nil, err
	}
	state := &EntryState{DN: entry.DN.String(), Disclaimer: Disclaimer}

	if csn := firstValue(entry, "entryCSN"); csn != "" {
		if cursor, ok := parseOpenLDAPCSN(csn); ok {
			state.Changed = cursor
		}
	}
	state.Identity = firstValue(entry, "entryUUID")
	if state.Identity == "" {
		state.Identity = firstValue(entry, "nsUniqueId")
	}

	if state.Changed.Raw == "" {
		// 389 DS stamps no per-entry change sequence a client can read. What
		// it has is the modification time, which is a weaker answer and is
		// said to be one rather than dressed up as the same thing.
		if when := parseGeneralizedTime(firstValue(entry, "modifyTimestamp")); !when.IsZero() {
			state.Changed = Cursor{At: when, Raw: firstValue(entry, "modifyTimestamp")}
			state.Notes = append(state.Notes,
				"This server records no per-entry change sequence a client can read, so this is the "+
					"modification time. Two servers agreeing on it is weaker evidence than a change "+
					"sequence would be: two changes in the same second are indistinguishable.")
		}
	}

	if conflict := firstValue(entry, "nsds5ReplConflict"); conflict != "" {
		state.Conflict = conflict
	}
	if state.Changed.Raw == "" && state.Identity == "" {
		state.Notes = append(state.Notes,
			"This server records nothing about this entry's replication, or this session may not read it.")
	}
	return state, nil
}

// Conflicts is what a server has marked as conflicting below a base.
type Conflicts struct {
	Base string
	// Recorded reports whether this server marks a conflict at all. False is
	// an answer about the server, not about the tree.
	Recorded bool
	// Why explains what this server does when two changes collide.
	Why string
	// Entries are the ones it has marked.
	Entries []Conflict
	// Truncated reports that there are more than one read returns.
	Truncated  bool
	Disclaimer string
}

// Conflict is one entry the server has marked.
type Conflict struct {
	DN string
	// Reason is the server's own words.
	Reason string
	// Kind is what sort of collision it was, where the server's words say:
	// naming, a missing parent, or something else.
	Kind string
}

// Kinds of collision, from the server's own words.
const (
	ConflictNaming = "naming"
	ConflictGlue   = "missing-parent"
	ConflictOther  = "other"
)

// FindConflicts looks for entries this server has marked as conflicting.
//
// Only one of the two servers marks anything. OpenLDAP resolves a collision
// by change sequence and discards the loser, leaving nothing behind to find,
// so the honest report there is that there is nothing to look for -- said
// plainly, because an empty list would read as "none, and that is good news".
func FindConflicts(ctx context.Context, r Reader, base dn.DN, opts Options) (*Conflicts, error) {
	out := &Conflicts{Base: base.String(), Disclaimer: Disclaimer}

	provider := providerOf(nil, r.Capabilities())
	switch provider {
	case snapshot.Provider389DS:
		out.Recorded = true
		out.Why = "389 Directory Server keeps both sides of a collision and marks the loser, " +
			"so a conflict is an entry you can read, fix and remove."
	case snapshot.ProviderOpenLDAP:
		out.Why = "OpenLDAP does not mark a conflicting entry. When two servers change the same one, " +
			"the later change sequence wins and the other is discarded, so there is nothing left to find " +
			"here -- an empty list is not evidence that nothing collided."
	default:
		out.Why = "Alder has no model for what this server does when two changes collide, " +
			"so an empty list says nothing either way."
	}

	page := opts.PageSize
	if page <= 0 || page > directory.MaxPageSize {
		page = directory.MaxPageSize
	}
	// Asked on both servers regardless of the sentence above: a server that
	// marks nothing answers with nothing, and a filter naming an attribute a
	// server has never heard of is answered rather than refused. Deciding not
	// to ask would mean the report depended on Alder's model being right,
	// rather than on the directory's answer.
	marked := filter.Or(
		filter.Present("nsds5ReplConflict"),
		filter.Equal("objectClass", "glue"),
	)
	var cookie []byte
	for {
		res, err := r.Search(ctx, directory.SearchRequest{
			BaseDN: base, Scope: directory.ScopeSubtree, Filter: marked,
			Attributes: []string{"nsds5ReplConflict", "objectClass"},
			Limit:      page, PageSize: page, Cookie: cookie,
		})
		if err != nil {
			if len(out.Entries) == 0 {
				return nil, err
			}
			out.Truncated = true
			break
		}
		for _, entry := range res.Entries {
			out.Entries = append(out.Entries, conflictOf(entry))
		}
		if len(out.Entries) >= maxEntries {
			out.Entries = out.Entries[:maxEntries]
			out.Truncated = true
			break
		}
		if len(res.Cookie) == 0 {
			out.Truncated = out.Truncated || res.Truncated
			break
		}
		cookie = res.Cookie
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].DN < out.Entries[j].DN })
	return out, nil
}

func conflictOf(entry *directory.Entry) Conflict {
	reason := firstValue(entry, "nsds5ReplConflict")
	c := Conflict{DN: entry.DN.String(), Reason: reason, Kind: ConflictOther}
	switch {
	case strings.Contains(strings.ToLower(reason), "namingconflict"):
		c.Kind = ConflictNaming
	case hasClass(entry, "glue"):
		c.Kind = ConflictGlue
		if c.Reason == "" {
			c.Reason = "The server made this entry up to hold children whose real parent it does not have."
		}
	}
	return c
}
