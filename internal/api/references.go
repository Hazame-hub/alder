package api

import (
	"context"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

// What names an entry, and by which attribute.
//
// The entry page already carries a filter that finds these — that is the link
// shipped alongside it, and it is enough for reading. It is not enough for
// removing one: a search says which entries matched, never which term matched,
// and unpicking a reference means deleting one value of one named attribute on
// the referring entry. Offering that without knowing whether a group holds this
// DN in member or in owner would be guessing at a write.
//
// So the matching happens here. One bounded search asks for the reference
// attributes, and each returned entry's values are compared against the subject
// where they already are, rather than being sent to a browser to be compared
// and thrown away — a group with five hundred members is five hundred DNs that
// never need to cross the wire.

// ListReferences returns the entries naming this one.
func (s *Server) ListReferences(c *fiber.Ctx, params ListReferencesParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}
	subject, ok := parseDNParam(c, params.Dn)
	if !ok {
		return nil
	}
	limit := clamp(deref(params.Limit), 200, 1, 1000)

	ctx, cancel := reqCtx(c)
	defer cancel()

	sch, err := sess.Conn.Schema(ctx)
	if err != nil {
		return s.fail(c, err)
	}

	// The same filter the entry page shows, from the same builder, so the link
	// and the panel cannot come to disagree about the question.
	tree, ok := referencedByFilterTree(sch, subject.String())
	if !ok {
		return c.JSON(ReferenceList{References: []Reference{}, Truncated: false})
	}

	caps := sess.Conn.Capabilities()
	base, searched := referenceSearchBase(caps, subject)
	if base.IsEmpty() {
		return c.JSON(ReferenceList{References: []Reference{}, Truncated: false})
	}

	attrs := definedReferenceAttrs(sch)
	res, err := searchReferences(ctx, sess.Conn, base, tree, attrs, limit)
	if err != nil {
		return s.fail(c, err)
	}

	out := ReferenceList{
		References:   matchReferences(res.Entries, subject, attrs),
		Truncated:    res.Truncated,
		SearchedBase: ptrIfSet(searched),
	}
	return c.JSON(out)
}

func searchReferences(
	ctx context.Context,
	conn directory.Session,
	base dn.DN,
	f filter.Filter,
	attrs []string,
	limit int,
) (*directory.SearchResult, error) {
	return conn.Search(ctx, directory.SearchRequest{
		BaseDN:     base,
		Scope:      directory.ScopeSubtree,
		Filter:     f,
		Attributes: attrs,
		Limit:      limit,
		PageSize:   directory.MaxPageSize,
	})
}

// referencesSubject reports whether one stored value names the subject.
//
// Compared as DNs rather than as strings, because a directory may return a
// reference spelled differently from the entry's own DN — a different case in
// an attribute name, a different but equivalent escaping — and a string
// comparison would report no reference where there is one. That is the worst
// available answer to "what would break if I deleted this".
//
// uniqueMember carries RFC 4517's Name and Optional UID syntax, which is a DN
// with an optional "#uid" appended. The suffix is not part of the DN, so it is
// trimmed and the comparison retried — the entry being named is the same entry
// either way.
//
// The retry is not conditional on the first parse failing. "#" is only special
// at the start of an RFC 4514 value, so "dc=test#'01'B" parses perfectly well
// as a naming attribute whose value happens to end in a bit string; the parser
// has no way to know the suffix was not meant. Trimming only on a parse error
// would therefore skip exactly the syntax the trimming exists for.
func referencesSubject(value string, subject dn.DN) bool {
	if candidate, err := dn.Parse(value); err == nil && candidate.Equal(subject) {
		return true
	}
	// A bit string holds no comma, so a "#" past the last one cannot be part of
	// an RDN this side of it.
	if hash := strings.LastIndexByte(value, '#'); hash > 0 && !strings.ContainsRune(value[hash:], ',') {
		if trimmed, err := dn.Parse(value[:hash]); err == nil {
			return trimmed.Equal(subject)
		}
	}
	// Anything else is not a DN naming the subject.
	return false
}

// referenceSearchBase picks the naming context the subject sits in.
//
// Searching from the subject itself would find only the subject: a reference
// points down from somewhere else in the tree, usually a sibling branch. The
// containing suffix is the narrowest base that can hold all of them.
func referenceSearchBase(caps directory.Capabilities, subject dn.DN) (dn.DN, string) {
	for _, ctx := range caps.NamingContexts {
		parsed, err := dn.Parse(ctx)
		if err != nil {
			continue
		}
		if subject.HasSuffix(parsed) {
			return parsed, ctx
		}
	}
	// An entry outside every naming context — the schema entry, or something in
	// the configuration tree. Nothing in the data tree references those, and
	// searching a suffix that does not contain it would be answering a
	// different question.
	return dn.DN{}, ""
}

// definedReferenceAttrs is the reference vocabulary this server actually has,
// in the schema's own spelling.
func definedReferenceAttrs(sch *schema.Schema) []string {
	out := make([]string, 0, len(referenceAttrs))
	for _, name := range referenceAttrs {
		at := sch.AttributeType(name)
		if at == nil {
			continue
		}
		if sch.EffectiveNoUserModification(at) || sch.EffectiveUsage(at).Operational() {
			continue
		}
		out = append(out, at.Name())
	}
	return out
}

// matchReferences works out which attribute each entry names the subject by.
//
// DNs are compared as DNs, not as strings: a directory may return a reference
// spelled differently from the entry's own DN — different case in an attribute
// name, different escaping of the same value — and a string comparison would
// report no reference where there is one, which is the worst possible answer to
// "what would break if I deleted this".
//
// One entry can name the subject more than once, by different attributes: a
// group can hold somebody as both a member and its owner. Each is a separate
// row, because each is a separate thing to remove.
func matchReferences(entries []*directory.Entry, subject dn.DN, attrs []string) []Reference {
	out := []Reference{}
	for _, e := range entries {
		// Attribute names are case-insensitive in LDAP and the map is keyed by
		// whatever spelling the server used, which need not be the spelling
		// that was asked for.
		byFold := make(map[string][][]byte, len(e.Attributes))
		for name, values := range e.Attributes {
			byFold[foldName(name)] = values
		}

		for _, attr := range attrs {
			for _, raw := range byFold[foldName(attr)] {
				if !referencesSubject(string(raw), subject) {
					continue
				}
				out = append(out, Reference{
					Dn:        e.DN.String(),
					Rdn:       ptr(rdnLabel(e.DN)),
					Attribute: attr,
					// Verbatim, because removing this is a delete of this
					// value and the directory compares it with the
					// attribute's own matching rule.
					Value: string(raw),
				})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Attribute != out[j].Attribute {
			return out[i].Attribute < out[j].Attribute
		}
		return out[i].Dn < out[j].Dn
	})
	return out
}
