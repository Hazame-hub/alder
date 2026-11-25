package entrytemplate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
)

// MaxScanned bounds how many entries one allocation reads. Past it the answer
// is refused rather than guessed: a number taken from part of the directory
// could already be in use in the rest.
const MaxScanned = 500_000

// ErrAllocation is a number that could not be found honestly.
var ErrAllocation = errors.New("entrytemplate: no number could be allocated")

// Allocation is what was found for one attribute.
type Allocation struct {
	Attribute string
	Value     int64
	// Highest is the highest number in the range already in use, or -1.
	Highest int64
	// Scanned is how many entries holding the attribute were read.
	Scanned int
	// Bases are where it looked.
	Bases []string
}

// AllocateNumbers finds the next free number for each allocated attribute: one more
// than the highest already in use within its range, or the start of the range
// when none is.
//
// "Highest plus one" rather than the first gap, as LDAP Account Manager does
// by default: a gap is often a number that belonged to someone who left, and
// handing it out again gives a new person the old one's files.
//
// The directory is read with ordinary paged searches for (attr=*). No
// ordering filter, because uidNumber and gidNumber have no ordering rule in
// the schema OpenLDAP ships, and a range filter there matches nothing.
func (t *Template) AllocateNumbers(ctx context.Context, r directory.Reader, namingContexts []string) (map[string]Allocation, error) {
	out := map[string]Allocation{}
	for _, attr := range t.AllocatedNames() {
		rng := t.Allocate[attr]
		bases := namingContexts
		if rng.Base != "" {
			bases = []string{rng.Base}
		}
		if len(bases) == 0 {
			return nil, fmt.Errorf("%w: %s: the server publishes no naming context to look in", ErrAllocation, attr)
		}
		a := Allocation{Attribute: attr, Highest: -1, Bases: bases}
		for _, b := range bases {
			base, err := dn.Parse(b)
			if err != nil {
				return nil, fmt.Errorf("%w: %s: base %q: %v", ErrAllocation, attr, b, err)
			}
			if err := scan(ctx, r, base, attr, rng, &a); err != nil {
				return nil, err
			}
		}
		next := rng.From
		if a.Highest >= rng.From {
			next = a.Highest + 1
		}
		if next > rng.To {
			return nil, fmt.Errorf("%w: %s: every number from %d to %d is taken", ErrAllocation, attr, rng.From, rng.To)
		}
		a.Value = next
		out[attr] = a
	}
	return out, nil
}

func scan(ctx context.Context, r directory.Reader, base dn.DN, attr string, rng Range, a *Allocation) error {
	var cookie []byte
	for {
		res, err := r.Search(ctx, directory.SearchRequest{
			BaseDN:     base,
			Scope:      directory.ScopeSubtree,
			Filter:     filter.Present(attr),
			Attributes: []string{attr},
			PageSize:   directory.MaxPageSize,
			Limit:      directory.MaxResults,
			Cookie:     cookie,
		})
		if err != nil {
			return fmt.Errorf("%w: %s: reading the numbers in use under %s: %w", ErrAllocation, attr, base, err)
		}
		for _, e := range res.Entries {
			a.Scanned++
			for _, raw := range e.Get(attr) {
				n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
				if err != nil || n < rng.From || n > rng.To {
					continue
				}
				if n > a.Highest {
					a.Highest = n
				}
			}
		}
		if len(res.Referrals) > 0 {
			return fmt.Errorf("%w: %s: part of %s is held by another server (a referral), so numbers in use there cannot be seen", ErrAllocation, attr, base)
		}
		cookie = res.Cookie
		if len(cookie) == 0 {
			if res.Truncated {
				// The server stopped the search (its size limit for this
				// bind) and offered no way on.
				return fmt.Errorf("%w: %s: the directory stopped the search under %s after %d entries, so the numbers in use are not all known; "+
					"a bind with no size limit, or a narrower allocate base, is needed", ErrAllocation, attr, base, a.Scanned)
			}
			return nil
		}
		if a.Scanned >= MaxScanned {
			return fmt.Errorf("%w: %s: more than %d entries hold it under %s; set a narrower allocate base", ErrAllocation, attr, MaxScanned, base)
		}
	}
}
