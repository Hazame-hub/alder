package api

import (
	"context"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// Why does this account work and that one not.
//
// The question is asked about two entries, and answered by the differences
// between them — but a naive diff answers it badly in three specific ways, and
// avoiding those is most of what this file is.
//
// It compares what is present, so an attribute one entry's classes *require*
// and it does not hold is invisible — and that absence is frequently the whole
// answer. It compares bytes, so two DNs naming the same entry in different case
// read as a difference the directory does not agree with. And its instinct is
// to show both sides of everything, which for `userPassword` is exactly what
// rule 6 forbids.
//
// So each side is annotated from its own object classes rather than from a
// shared idea of what an entry should hold, DN-valued attributes are compared
// as DNs, and a sensitive attribute is compared on presence alone.

// maxCompareValues bounds the values reported for one attribute. Comparing two
// large groups is otherwise thousands of DNs across the wire to be read by
// nobody.
const maxCompareValues = 500

type comparison struct {
	Attributes []AttributeComparison
	Counts     ComparisonCounts
	Truncated  bool
}

// compareEntries is the whole decision, with no Session and no fiber.Ctx, so it
// is tested against fixtures rather than against a directory.
func compareEntries(left, right *directory.Entry, sch *schema.Schema, limit int) comparison {
	if limit <= 0 || limit > maxCompareValues {
		limit = maxCompareValues
	}
	out := comparison{Attributes: []AttributeComparison{}}

	for _, name := range unionNames(left, right, sch) {
		row := compareOne(name, left, right, sch, limit)
		if row.Truncated != nil && *row.Truncated {
			out.Truncated = true
		}
		switch row.Status {
		case Same:
			out.Counts.Same++
		case Differs:
			out.Counts.Differs++
		case LeftOnly:
			out.Counts.LeftOnly++
		case RightOnly:
			out.Counts.RightOnly++
		case Withheld:
			out.Counts.Withheld++
		}
		out.Attributes = append(out.Attributes, row)
	}
	return out
}

func compareOne(name string, left, right *directory.Entry, sch *schema.Schema, limit int) AttributeComparison {
	lv := valuesOfDescription(left, name)
	rv := valuesOfDescription(right, name)
	kind := sch.KindOf(name)

	row := AttributeComparison{
		Name:  name,
		Left:  sideFacts(left, name, len(lv), sch),
		Right: sideFacts(right, name, len(rv), sch),
	}

	switch {
	case len(lv) > 0 && len(rv) == 0:
		row.Status = LeftOnly
	case len(lv) == 0 && len(rv) > 0:
		row.Status = RightOnly
	case len(lv) == 0 && len(rv) == 0:
		// Neither holds it. It is in the union because a class requires it,
		// which is the case a diff of present values cannot see at all.
		row.Status = Same
	default:
		if sameAttributeValues(lv, rv, name, sch) {
			row.Status = Same
		} else {
			row.Status = Differs
		}
	}

	if kind.Sensitive {
		// Presence only. /entry already reports that a password is set, so
		// saying so here reveals nothing new — but reporting whether two
		// entries hold the *same* hash would be an oracle about password
		// material that the product offers nowhere else, and rule 6 is about
		// not becoming one.
		row.Withheld = ptr(true)
		if len(lv) > 0 && len(rv) > 0 {
			// Withheld, not Same. This used to say Same with a flag beside it,
			// and a status of "same" about two passwords is a claim the server
			// has no basis for: it never compared them. A client reading only
			// the status must not be able to reach that conclusion, and ours
			// did -- the "only what differs" filter dropped the row entirely.
			row.Status = Withheld
		}
		if schemes := valueSchemes(lv); len(schemes) > 0 {
			row.Left.ValueSchemes = &schemes
		}
		if schemes := valueSchemes(rv); len(schemes) > 0 {
			row.Right.ValueSchemes = &schemes
		}
		return row
	}

	values, truncated := valueRows(lv, rv, name, sch, limit)
	row.Values = &values
	if truncated {
		row.Truncated = ptr(true)
	}
	return row
}

// sideFacts is what one entry's own schema says about this attribute.
//
// Read from that entry's own object classes, which the two entries need not
// share: `sn` is required on an inetOrgPerson and not permitted at all on an
// account, and reporting that as a plain "only on the left" hides the reason.
func sideFacts(e *directory.Entry, name string, count int, sch *schema.Schema) ComparedSide {
	req := sch.Requirements(entryClassNames(e))
	base := schema.BaseName(name)

	must, may := false, false
	for _, n := range req.Must {
		if strings.EqualFold(schema.BaseName(n), base) {
			must = true
		}
	}
	for _, n := range req.May {
		if strings.EqualFold(schema.BaseName(n), base) {
			may = true
		}
	}

	return ComparedSide{
		Present:    count > 0,
		ValueCount: count,
		Required:   must,
		// An attribute an entry holds that none of its classes permits is
		// reachable — an extensibleObject, or a DIT content rule — and is a
		// finding rather than a curiosity.
		Permitted: must || may,
	}
}

// unionNames is every attribute description either entry holds, plus every one
// either entry's classes require, ordered so the answer is near the top.
func unionNames(left, right *directory.Entry, sch *schema.Schema) []string {
	seen := map[string]string{}
	add := func(name string) {
		key := foldDescription(name)
		if _, ok := seen[key]; !ok {
			seen[key] = name
		}
	}
	for _, n := range left.Order {
		add(n)
	}
	for _, n := range right.Order {
		add(n)
	}
	// A required attribute the entry does not hold is invisible to a diff of
	// what is present, and is often the whole answer.
	for _, e := range []*directory.Entry{left, right} {
		for _, n := range sch.Requirements(entryClassNames(e)).Must {
			add(n)
		}
	}

	out := make([]string, 0, len(seen))
	for _, name := range seen {
		out = append(out, name)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := compareRank(out[i], sch), compareRank(out[j], sch)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

// compareRank bands the rows: what the entry is, then what it must have, then
// the rest, then what the directory owns.
func compareRank(name string, sch *schema.Schema) int {
	base := schema.BaseName(name)
	if strings.EqualFold(base, "objectClass") {
		return 0
	}
	if at := sch.AttributeType(base); at != nil {
		if sch.EffectiveNoUserModification(at) || sch.EffectiveUsage(at).Operational() {
			// entryUUID and modifyTimestamp always differ and are almost always
			// noise — but pwdAccountLockedTime is operational and is sometimes
			// the answer, so they are ordered last rather than dropped.
			return 3
		}
	}
	return 1
}

// sameAttributeValues compares two attributes as the sets they are.
func sameAttributeValues(a, b [][]byte, name string, sch *schema.Schema) bool {
	if len(a) != len(b) {
		return false
	}
	if dnValued(name, sch) {
		return sameDNSet(a, b)
	}
	return sameValues(a, b)
}

// dnValued reports whether this attribute's values name entries.
//
// Checked by syntax OID rather than by KindOf: Name-and-Optional-UID — which is
// what uniqueMember carries — maps to KindString, so a Kind == KindDN test
// misses exactly the attribute most likely to differ only in spelling.
func dnValued(name string, sch *schema.Schema) bool {
	at := sch.AttributeType(schema.BaseName(name))
	if at == nil {
		return false
	}
	switch sch.EffectiveSyntax(at) {
	case "1.3.6.1.4.1.1466.115.121.1.12", // DN
		"1.3.6.1.4.1.1466.115.121.1.34": // Name and Optional UID
		return true
	}
	return false
}

func sameDNSet(a, b [][]byte) bool {
	left := normalisedDNs(a)
	right := normalisedDNs(b)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// normalisedDNs renders each value through the DN parser so two spellings of
// one entry compare equal — the same allowance references.go makes, for the
// same reason. A value that is not a DN keeps its bytes.
func normalisedDNs(values [][]byte) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		s := trimUIDSuffix(string(v))
		if parsed, err := dn.Parse(s); err == nil {
			out = append(out, strings.ToLower(parsed.String()))
			continue
		}
		out = append(out, string(v))
	}
	return out
}

// valueRows lines the two sides up value by value.
func valueRows(lv, rv [][]byte, name string, sch *schema.Schema, limit int) ([]ValueComparison, bool) {
	isDN := dnValued(name, sch)
	key := func(v []byte) string {
		if isDN {
			return normalisedDNs([][]byte{v})[0]
		}
		return string(v)
	}

	inRight := map[string]bool{}
	for _, v := range rv {
		inRight[key(v)] = true
	}
	inLeft := map[string]bool{}
	for _, v := range lv {
		inLeft[key(v)] = true
	}

	kind := sch.KindOf(name)
	out := []ValueComparison{}
	truncated := false

	emit := func(v []byte, side ValueComparisonSide) {
		if len(out) >= limit {
			truncated = true
			return
		}
		out = append(out, ValueComparison{
			Side:  side,
			Value: encodeValue(v, kind.Kind),
		})
	}

	for _, v := range lv {
		if inRight[key(v)] {
			emit(v, Both)
			continue
		}
		emit(v, Left)
	}
	for _, v := range rv {
		if !inLeft[key(v)] {
			emit(v, Right)
		}
	}
	return out, truncated
}

func valuesOfDescription(e *directory.Entry, name string) [][]byte {
	for _, have := range e.Order {
		if foldDescription(have) == foldDescription(name) {
			return e.Attributes[have]
		}
	}
	return nil
}

// entryClassNames is the object classes an entry actually carries.
// classNames in handlers.go answers a different question, over parsed classes.
func entryClassNames(e *directory.Entry) []string {
	out := []string{}
	for _, v := range e.Get("objectClass") {
		out = append(out, string(v))
	}
	return out
}

// CompareEntries answers what differs between two entries.
func (s *Server) CompareEntries(c *fiber.Ctx, params CompareEntriesParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	leftDN, ok := parseDNParam(c, params.Left)
	if !ok {
		return nil
	}
	rightDN, ok := parseDNParam(c, params.Right)
	if !ok {
		return nil
	}

	attrs := []string{"*"}
	if params.IncludeOperational == nil || *params.IncludeOperational {
		// On by default, unlike the LDIF export: nsAccountLock and
		// pwdAccountLockedTime are operational on the target servers and are
		// frequently the entire answer to "why does that one not work".
		attrs = append(attrs, "+")
	}

	ctx, cancel := reqCtx(c)
	defer cancel()

	left, err := sess.Conn.Read(ctx, leftDN, attrs)
	if err != nil {
		return s.fail(c, err)
	}
	right, err := sess.Conn.Read(ctx, rightDN, attrs)
	if err != nil {
		return s.fail(c, err)
	}
	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	got := compareEntries(left, right, sch, clamp(deref(params.Limit), 100, 1, maxCompareValues))

	// One-sided rows are the ones a read cannot be trusted about: an attribute
	// the access rules hide is missing from the result exactly as one the entry
	// does not hold is. Ask the server which, for those rows only.
	resolved, probesTruncated := resolveOneSided(ctx, sess.Conn.VisibilityOf, leftDN, rightDN, got.Attributes)
	if resolved > 0 {
		got.Counts = recount(got.Attributes)
	}

	return c.JSON(EntryComparison{
		Left:       comparedEntry(left, sch),
		Right:      comparedEntry(right, sch),
		Attributes: got.Attributes,
		Counts:     got.Counts,
		Truncated:  got.Truncated || probesTruncated,
	})
}

// structuralName is the entry's single structural class, or empty where it has
// none — which is itself worth seeing on a comparison.
func structuralName(sch *schema.Schema, classes []string) string {
	if oc := sch.Requirements(classes).Structural; oc != nil {
		return oc.Name()
	}
	return ""
}

func comparedEntry(e *directory.Entry, sch *schema.Schema) ComparedEntry {
	classes := entryClassNames(e)
	// The structural class explains most of the rows below it and should be
	// read first.
	return ComparedEntry{
		Dn:            e.DN.String(),
		Rdn:           ptr(rdnLabel(e.DN)),
		ObjectClasses: ptr(classes),
		Structural:    ptrIfSet(structuralName(sch, classes)),
	}
}

// maxVisibilityProbes bounds the round trips one comparison spends resolving
// one-sided rows.
//
// Each probe is a Compare against the side that lacked the attribute, so the
// cost is one per one-sided row and nothing at all for two entries that differ
// only in their values. Two entries of different structural classes can differ
// in dozens of attributes, and at that point the operator is looking at a
// difference of kind rather than of detail; the rows past this stay as they
// were and the comparison says it stopped.
const maxVisibilityProbes = 50

// visibilityProbe asks the directory whether an attribute is absent from an
// entry or merely hidden from this bind.
//
// A function rather than a Session so the resolution below can be tested
// without one, the same reason expandGroup takes an entryReader.
type visibilityProbe func(ctx context.Context, target dn.DN, attribute string) (directory.AttributeVisibility, error)

// resolveOneSided turns "only on the left" into "cannot tell" where the reason
// the other side lacked the attribute was access control.
//
// A read cannot distinguish those: a forbidden attribute is missing from the
// result exactly as one the entry does not hold is. Reporting the first as a
// difference between two entries is a claim about the directory that the
// session has no basis for -- and it is the claim somebody acts on, because
// "only on the left" is what a person reads before copying a value across.
//
// Only one-sided rows are probed. A row that is the same, differs, or is
// withheld was answered by values that both sides returned, and needs nothing.
func resolveOneSided(
	ctx context.Context,
	probe visibilityProbe,
	leftDN, rightDN dn.DN,
	rows []AttributeComparison,
) (resolved int, truncated bool) {
	spent := 0
	for i := range rows {
		var missing dn.DN
		var side *ComparedSide
		switch rows[i].Status {
		case LeftOnly:
			missing, side = rightDN, &rows[i].Right
		case RightOnly:
			missing, side = leftDN, &rows[i].Left
		default:
			continue
		}

		if spent >= maxVisibilityProbes {
			truncated = true
			return resolved, truncated
		}
		spent++

		visibility, err := probe(ctx, missing, rows[i].Name)
		if err != nil {
			// The probe is an improvement on the answer, not a precondition
			// for it. A comparison that fails because one extra question could
			// not be asked would be worse than one that answers as it always
			// did.
			continue
		}
		if visibility == directory.VisibilityDenied {
			rows[i].Status = Undetermined
			side.Denied = ptr(true)
			resolved++
		}
	}
	return resolved, truncated
}

// recount tallies the buckets again after rows have been reclassified.
func recount(rows []AttributeComparison) ComparisonCounts {
	var c ComparisonCounts
	for _, row := range rows {
		switch row.Status {
		case Same:
			c.Same++
		case Differs:
			c.Differs++
		case LeftOnly:
			c.LeftOnly++
		case RightOnly:
			c.RightOnly++
		case Withheld:
			c.Withheld++
		case Undetermined:
			c.Undetermined++
		}
	}
	return c
}
