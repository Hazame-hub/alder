package api

import (
	"errors"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

// What values does this attribute actually hold, and how many entries carry
// each.
//
// The question that finds a team name spelled "platfrm" with four people on
// it, a cost centre nobody has used since a reorganisation, or the accounts
// still pointing at a decommissioned site. A directory owner can ask it today
// only by exporting the subtree and counting in a shell.
//
// The whole difficulty is honesty. This is a tally over a *bounded* search —
// rule 4 admits no other kind — so it can never claim to describe the
// directory, only the entries it examined. Every number here is scoped that
// way, and `truncated` says the bound was reached. A tally that quietly
// summarises a truncated result set is not a weaker answer than the truth, it
// is a confident wrong one, and the point of this feature is to be trusted.

const (
	// maxInventoryValues bounds the rows rendered. The counts behind them stay
	// exact: the tail is reported as a remainder rather than dropped.
	maxInventoryValues = 200
	// maxInventoryEntries bounds the entries examined.
	//
	// Raised from 10,000 once the tally streamed: the old ceiling was there
	// because every entry was held at once, and a hundred-thousand-entry
	// directory -- the size at which an attribute has actually drifted -- could
	// not be tallied at all. What bounds it now is time and the map of distinct
	// values, not the entries.
	maxInventoryEntries = 250000
	// maxInventoryRounds guards the paging loop against a server that returns a
	// continuation cookie for ever. At one page per round the cap above is
	// reached long before this is.
	maxInventoryRounds = 10000
)

var (
	errSensitiveAttribute = errors.New(
		"that attribute holds secrets, and an inventory of it would be a list of them")
	errBinaryAttribute = errors.New(
		"that attribute holds binary values, and an inventory of it would be a list of sizes")
)

// inventoryTarget vets the attribute before any search runs.
//
// Refusing here rather than filtering the result afterwards is the difference
// between never reading a password and reading every password and choosing not
// to say. Rule 6 is about the first.
func inventoryTarget(sch *schema.Schema, requested string) (string, schema.AttributeKind, error) {
	base := schema.BaseName(strings.TrimSpace(requested))
	if base == "" {
		return "", schema.AttributeKind{}, errors.New("no attribute was named")
	}
	// The same RFC 4512 attribute-description check the filter builder makes,
	// reused rather than written twice: "alderTeam)(uid=*" dies here.
	if err := filter.Present(base).Err(); err != nil {
		return "", schema.AttributeKind{}, err
	}

	kind := sch.KindOf(base)
	switch {
	case kind.Sensitive:
		// KindOf marks an attribute sensitive even where the schema does not
		// define it, so an unknown sambaNTPassword is still refused.
		return "", kind, errSensitiveAttribute
	case kind.Kind == schema.KindBinary, kind.Kind == schema.KindImage,
		kind.Kind == schema.KindCertificate:
		return "", kind, errBinaryAttribute
	}
	return sch.CanonicalAttrName(base), kind, nil
}

type inventoryResult struct {
	Values          []InventoryRow
	WithValue       int
	WithoutValue    int
	DistinctValues  int
	SingletonValues int
	OtherValues     int
	OtherEntries    int
}

// tally counts one attribute across the entries a search returned.
//
// Pure, so it is tested from fixtures rather than against a directory.
// tallier folds entries into counts without holding on to them.
//
// Counting incrementally is what lets the search behind it be consumed a page
// at a time. A tally of a hundred thousand entries then costs one page of
// entries plus one map entry per *distinct value* — and for the attributes
// anybody tallies, distinct values are few. The pathological case is an
// attribute that identifies entries rather than grouping them, where the map
// grows with the directory; that is bounded by the examined cap, and the
// interface says such a tally is a list rather than an answer.
type tallier struct {
	want   string
	kind   schema.AttributeKind
	counts map[string]int
	// order preserves first appearance, so the sort below is stable across
	// runs rather than following Go's map iteration.
	order        []string
	withValue    int
	withoutValue int
}

func newTallier(attribute string, kind schema.AttributeKind) *tallier {
	return &tallier{
		want: foldName(attribute),
		kind: kind,
		// Keyed by the exact bytes. A Go string is a byte container here, not
		// text: values are bytes, and folding two spellings into one row would
		// be inventing an equality the directory did not agree to.
		counts: map[string]int{},
	}
}

// examined is how many entries have been folded in so far.
func (t *tallier) examined() int { return t.withValue + t.withoutValue }

// add folds one page of entries into the running counts.
func (t *tallier) add(entries []*directory.Entry) {
	for _, e := range entries {
		// An entry counts once per distinct value it holds, however many times
		// it holds it and across whichever attribute options it uses:
		// alderTeam and alderTeam;lang-fr are the same attribute.
		seen := map[string]bool{}
		for _, name := range e.Order {
			if foldName(name) != t.want {
				continue
			}
			for _, raw := range e.Attributes[name] {
				key := string(raw)
				if seen[key] {
					continue
				}
				seen[key] = true
				if _, known := t.counts[key]; !known {
					t.order = append(t.order, key)
				}
				t.counts[key]++
			}
		}
		if len(seen) > 0 {
			t.withValue++
		} else {
			t.withoutValue++
		}
	}
}

// result renders the counts, listing at most maxValues rows and reporting the
// rest as a remainder rather than dropping it.
func (t *tallier) result(maxValues int) inventoryResult {
	if maxValues <= 0 || maxValues > maxInventoryValues {
		maxValues = maxInventoryValues
	}
	out := inventoryResult{
		Values:         []InventoryRow{},
		WithValue:      t.withValue,
		WithoutValue:   t.withoutValue,
		DistinctValues: len(t.counts),
	}
	for _, n := range t.counts {
		if n == 1 {
			// The typo signal. A value one entry holds among three hundred is
			// usually a misspelling of one that ninety hold.
			out.SingletonValues++
		}
	}

	// Commonest first, then by value, so the answer is stable and the rare
	// ones — the interesting ones — are found at the end or by sorting.
	order := append([]string(nil), t.order...)
	sort.SliceStable(order, func(i, j int) bool {
		if t.counts[order[i]] != t.counts[order[j]] {
			return t.counts[order[i]] > t.counts[order[j]]
		}
		return order[i] < order[j]
	})

	for i, key := range order {
		if i >= maxValues {
			out.OtherValues++
			out.OtherEntries += t.counts[key]
			continue
		}
		out.Values = append(out.Values, InventoryRow{
			Value:   encodeValue([]byte(key), t.kind.Kind),
			Entries: t.counts[key],
		})
	}
	return out
}

// tally counts one attribute across entries already in hand.
//
// The handler streams instead; this is the same fold in one call, and is what
// the unit tests exercise.
func tally(entries []*directory.Entry, attribute string, kind schema.AttributeKind, maxValues int) inventoryResult {
	t := newTallier(attribute, kind)
	t.add(entries)
	return t.result(maxValues)
}

// InventoryValues tallies one attribute across a bounded search.
func (s *Server) InventoryValues(c *fiber.Ctx) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	var body InventoryRequest
	if err := c.BodyParser(&body); err != nil {
		return badRequest(c, "The request body is not valid JSON.", err.Error())
	}
	base, ok := parseDNParam(c, body.BaseDn)
	if !ok {
		return nil
	}
	scope, err := directory.ParseScope(string(body.Scope))
	if err != nil {
		return badRequest(c, "Unknown search scope.", err.Error())
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	name, kind, err := inventoryTarget(sch, body.Attribute)
	if err != nil {
		return badRequest(c, "That attribute cannot be inventoried.", err.Error())
	}

	// Parsed into a tree, never pasted into one.
	scan := filter.Present("objectClass")
	if raw := strings.TrimSpace(deref(body.Filter)); raw != "" {
		parsed, parseErr := filter.Parse(raw)
		if parseErr != nil {
			return badRequest(c, "The filter is not a valid RFC 4515 filter.", parseErr.Error())
		}
		scan = parsed
	}

	limit := clamp(deref(body.Limit), 1000, 1, maxInventoryEntries)

	// Consumed a page at a time and folded as it arrives, so that examining a
	// hundred thousand entries costs one page of them rather than all of them.
	// The whole point of this feature is a question about a directory, and a
	// directory large enough to have drifted is exactly the one too large to
	// hold in memory.
	counter := newTallier(name, kind)
	var cookie []byte
	for rounds := 0; counter.examined() < limit && rounds < maxInventoryRounds; rounds++ {
		want := limit - counter.examined()
		if want > directory.MaxPageSize {
			want = directory.MaxPageSize
		}
		res, err := sess.Conn.Search(ctx, directory.SearchRequest{
			BaseDN: base,
			Scope:  scope,
			Filter: scan,
			// Only the attribute being tallied, so the cost is the search
			// rather than every value of every entry.
			Attributes: []string{name},
			Limit:      want,
			PageSize:   want,
			Cookie:     cookie,
		})
		if err != nil {
			return s.fail(c, err)
		}
		counter.add(res.Entries)
		cookie = res.Cookie
		if len(cookie) == 0 {
			// The server has nothing further to give.
			break
		}
		if len(res.Entries) == 0 {
			// A page with no entries and a cookie should not happen; treating
			// it as the end is better than looping on it.
			break
		}
	}
	// A cookie still in hand means the search stopped at the cap rather than
	// at the end of the directory.
	truncated := len(cookie) > 0

	got := counter.result(clamp(deref(body.MaxValues), maxInventoryValues, 1, maxInventoryValues))

	return c.JSON(InventoryResponse{
		Attribute: name,
		// Every number below is about these entries, and says so. The response
		// has no field that describes the directory, because a bounded search
		// cannot produce one.
		Examined:        counter.examined(),
		Limit:           limit,
		Truncated:       truncated,
		WithValue:       got.WithValue,
		WithoutValue:    got.WithoutValue,
		DistinctValues:  got.DistinctValues,
		SingletonValues: got.SingletonValues,
		OtherValues:     got.OtherValues,
		OtherEntries:    got.OtherEntries,
		Values:          got.Values,
	})
}
