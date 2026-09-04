package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/session"
)

// Finding out where an added entry actually went.
//
// A directory does not have to store an entry under the name it was given. Where
// an entry's position among its siblings forms part of that name, the server
// assigns the position and rewrites the RDN: ask for cn=alder, get
// cn={5}alder. The add succeeds, and the DN the caller was told about resolves
// to nothing -- so the UI reports success and then navigates to a 404.
//
// Nothing in the protocol reports the stored DN back, so it is looked for. A
// base read of the requested DN settles the ordinary case in one round trip; the
// search below happens only when the entry is not where it was asked to go,
// which on most servers is never.
//
// The failure mode is benign by construction: when the entry cannot be located,
// the requested DN is reported along with a note saying it was not confirmed.
// Alder says less rather than something untrue.

// storedLocation reports where an added entry came to rest.
//
// It returns the DN to report, whether that differs from the requested one, and
// a note for the case where the entry could not be found at all.
func storedLocation(
	ctx context.Context,
	sess *session.Session,
	record directory.ChangeRecord,
) (actual string, moved bool, note string) {
	requested := record.DN.String()
	if record.Type != directory.ChangeAdd {
		return requested, false, ""
	}

	if _, err := sess.Conn.Read(ctx, record.DN, []string{"objectClass"}); err == nil {
		return requested, false, ""
	}

	parent := record.DN.Parent()
	if parent.IsEmpty() {
		return requested, false, notFoundNote(requested, "")
	}
	browser, ok := sess.Conn.(treeBrowser)
	if !ok {
		return requested, false, notFoundNote(requested, parent.String())
	}

	wantType, wantValue, ok := splitRDN(requested)
	if !ok {
		return requested, false, notFoundNote(requested, parent.String())
	}

	// One level of the parent is enough: an add places the entry directly under
	// it, whatever the server calls it.
	res, err := browser.Children(ctx, parent, []string{"objectClass"}, storedLookupLimit, nil)
	if err != nil {
		return requested, false, notFoundNote(requested, parent.String())
	}

	var matches []string
	for _, child := range res.Entries {
		gotType, gotValue, ok := splitRDN(child.DN.String())
		if !ok || !strings.EqualFold(gotType, wantType) {
			continue
		}
		if strings.EqualFold(stripPositionPrefix(gotValue), wantValue) {
			matches = append(matches, child.DN.String())
		}
	}
	// Exactly one, or nothing is claimed. Two entries whose names differ only by
	// a position prefix would make any choice a guess.
	if len(matches) != 1 {
		return requested, false, notFoundNote(requested, parent.String())
	}
	return matches[0], true, ""
}

// storedLookupLimit bounds the one search this ever performs. A parent with
// more children than this is not somewhere a freshly added entry gets lost.
const storedLookupLimit = 500

func notFoundNote(requested, parent string) string {
	if parent == "" {
		return fmt.Sprintf(
			"The directory accepted this, but no entry could be read at %s afterwards.", requested)
	}
	return fmt.Sprintf(
		"The directory accepted this, but no entry could be read at %s afterwards, and Alder "+
			"could not identify where it went. Look under %s.", requested, parent)
}

// splitRDN returns the attribute and value of a DN's first RDN.
//
// It works on the string rather than through the dn package because what is
// wanted here is the RDN exactly as the server spells it, prefix and all.
func splitRDN(d string) (attr, value string, ok bool) {
	rdn := d
	if i := indexUnescaped(d, ','); i >= 0 {
		rdn = d[:i]
	}
	eq := strings.IndexByte(rdn, '=')
	if eq <= 0 || eq == len(rdn)-1 {
		return "", "", false
	}
	return strings.TrimSpace(rdn[:eq]), strings.TrimSpace(rdn[eq+1:]), true
}

// indexUnescaped finds the first occurrence of c that is not backslash-escaped.
func indexUnescaped(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == c {
			return i
		}
	}
	return -1
}

// stripPositionPrefix removes a leading {n} from an RDN value.
//
// That prefix is the whole reason this file exists: it is how a server records
// an entry's position in its name, and it is the difference between the name
// asked for and the name stored.
func stripPositionPrefix(v string) string {
	if !strings.HasPrefix(v, "{") {
		return v
	}
	end := strings.IndexByte(v, '}')
	// end == 1 is "{}", which carries no position and is part of the name.
	if end < 2 {
		return v
	}
	for _, r := range v[1:end] {
		if r < '0' || r > '9' {
			return v
		}
	}
	return v[end+1:]
}
