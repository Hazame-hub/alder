package api

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
)

// One box that takes a DN, a filter, or a name.
//
// The target user lives in a terminal and can already name the thing they want.
// Making them click down a tree to reach an entry whose DN is on their
// clipboard is the papercut this removes.
//
// The interesting part is that the three input kinds overlap. "cn=platform" is
// a perfectly valid one-component DN *and* almost certainly a request to find
// something called platform. Guessing between those would be wrong half the
// time, so an ambiguous input returns both destinations and the operator picks
// — which is also the only version of this that can be explained.
//
// It parses and never searches. `dn.Parse` and `filter.Parse` are the
// authorities on what these strings are, and a regex in the browser would drift
// from them the first time either grew a case; but a probe to see whether the
// entry actually exists would turn every keystroke into a directory read, and
// answers a question the operator is about to answer themselves by pressing
// enter.

// resolveQuery works out what an input could be.
//
// Pure: no session, no directory, no clock. The base is passed in because the
// caller knows the session's naming contexts and this function should not.
func resolveQuery(raw, base string) []Destination {
	q := strings.TrimSpace(raw)
	out := []Destination{}
	if q == "" {
		return out
	}

	parsed, dnErr := dn.Parse(q)
	rooted := dnErr == nil && len(parsed) > 1

	// A DN with more than one component is a DN. Nobody types
	// "uid=alice,ou=people,dc=alder,dc=test" meaning anything else.
	if dnErr == nil {
		out = append(out, Destination{
			Kind:  Entry,
			Label: "Open " + rdnLabel(parsed),
			Dn:    ptr(parsed.String()),
		})
	}

	// A filter is unambiguous: it is the only one of the three that starts with
	// a parenthesis.
	if strings.HasPrefix(q, "(") {
		if f, err := filter.Parse(q); err == nil {
			if rendered, rErr := f.Render(); rErr == nil {
				return append(out, Destination{
					Kind:   Search,
					Label:  "Search for " + rendered,
					Filter: ptr(rendered),
					Base:   ptrIfSet(base),
				})
			}
		}
		// An input that opens a parenthesis and does not parse is a filter the
		// operator is still typing, or a broken one. Either way it is not a
		// name to search for, and offering to search for the literal text
		// "(objectClass=" would be nonsense.
		return out
	}

	if rooted {
		return out
	}

	// A one-component DN is also a name, which is why both destinations are
	// offered rather than one being guessed. But the name is the RDN's *value*:
	// somebody typing "cn=platform" wants the thing called platform, not an
	// entry whose cn contains the literal text "cn=platform".
	if dnErr == nil {
		if f, ok := rdnFilter(parsed); ok {
			return append(out, Destination{
				Kind:   Search,
				Label:  "Search for " + rdnLabel(parsed),
				Filter: ptr(f),
				Base:   ptrIfSet(base),
			})
		}
		// A multi-valued RDN — "cn=a+ou=b" — is a DN and nothing else. There is
		// no single name in it to search for.
		return out
	}

	if f, ok := nameFilter(q); ok {
		out = append(out, Destination{
			Kind:   Search,
			Label:  "Search for " + q,
			Filter: ptr(f),
			Base:   ptrIfSet(base),
		})
	}
	return out
}

// rdnFilter searches for what a one-component DN names.
//
// Exact rather than a substring sweep: the operator named the attribute as well
// as the value, so honouring both is more precise than guessing which of four
// attributes they meant.
func rdnFilter(d dn.DN) (string, bool) {
	rdn := d.RDN()
	if len(rdn) != 1 {
		return "", false
	}
	rendered, err := filter.Equal(rdn[0].Type, rdn[0].Value).Render()
	if err != nil {
		return "", false
	}
	return rendered, true
}

// nameFilter builds the search for a bare name.
//
// Built with the filter package, never by pasting: the text comes from a box a
// user typed in, and "*)(objectClass=*" is a filter that returns the directory.
func nameFilter(text string) (string, bool) {
	f := filter.Or(
		filter.Contains("cn", text),
		filter.Contains("uid", text),
		filter.Contains("mail", text),
		filter.Contains("ou", text),
	)
	rendered, err := f.Render()
	if err != nil {
		return "", false
	}
	return rendered, true
}

// Resolve says what an input could be.
func (s *Server) Resolve(c *fiber.Ctx, params ResolveParams) error {
	sess := s.require(c)
	if sess == nil {
		return nil
	}

	// Where a search would start. The first naming context is the only honest
	// default: the input is a name, not a location.
	base := ""
	if contexts := sess.Conn.Capabilities().NamingContexts; len(contexts) > 0 {
		base = contexts[0]
	}

	return c.JSON(ResolveResult{
		Query:        params.Q,
		Destinations: resolveQuery(params.Q, base),
	})
}
