package plan

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/schema"
)

// What the plan saw, in a form the apply can check it against.
//
// A plan is a statement about a directory at a moment. Applied later against a
// directory somebody else has changed, it does something other than what it
// described -- and the LDIF the operator confirmed is then a description of a
// change that did not happen. That is precisely the gap the whole "no write
// without a confirmed ChangeRecord" rule exists to close, reopened by the delay
// between planning and applying.
//
// So each planned change carries a fingerprint of the state it was planned
// against, and Apply recomputes it. Not a timestamp, and not a client's word
// for it: the server reads the entry again and compares.
//
// It is a fingerprint rather than a snapshot for three reasons. A snapshot of
// what the plan examined would be large, would have to travel to the browser
// and back, and -- the reason that settles it -- would carry attribute values
// the API withholds everywhere else.

// Baseline is an opaque token naming the state one change was planned against.
type Baseline string

// Fingerprinter computes baselines.
//
// The key is random and lives only in this process, so a baseline is
// meaningless anywhere else and cannot be brute-forced back into the values it
// covers. Restarting Alder invalidates every outstanding plan, which is the
// same rule as the session store: restarting logs everyone out, and a plan
// belongs to a session.
type Fingerprinter struct {
	key []byte
}

// NewFingerprinter returns one with a fresh key.
func NewFingerprinter() (*Fingerprinter, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("plan: generating a fingerprint key: %w", err)
	}
	return &Fingerprinter{key: key}, nil
}

// Of fingerprints the part of the directory one change depends on.
//
// Only that part. A change that replaces `mail` is not invalidated by somebody
// else editing `telephoneNumber`, and a baseline over the whole entry would
// refuse it -- which trains people to bypass the check, the way an editor that
// cried conflict on every save would.
//
// live is nil when the entry does not exist, and that absence is itself part of
// the fingerprint: a plan that decided "this is an add" is stale the moment the
// entry appears.
//
// The token carries the attribute names it covers, in front of the MAC. That is
// not decoration. A reconciled change is planned from one record -- an add
// naming cn and mail -- and applied as another -- a modify of mail alone -- so
// a verifier that re-derived the list from the record it was handed would
// fingerprint a different set and call every reconcile stale. It would also
// stop noticing that cn moved underneath, which is a change that would have
// altered the plan. The names are inside the MAC as well as in front of it, so
// editing the list invalidates the token rather than narrowing what it checks.
func (f *Fingerprinter) Of(record directory.ChangeRecord, live *directory.Entry) Baseline {
	return f.over(record.DN.String(), dependsOn(record), live)
}

// over is Of with the dependency list supplied, which is what verification
// does after reading it back out of a token.
func (f *Fingerprinter) over(target string, deps []string, live *directory.Entry) Baseline {
	mac := hmac.New(sha256.New, f.key)

	// The DN folded, because that is how the entry was found.
	_, _ = fmt.Fprintf(mac, "dn:%s\n", strings.ToLower(target))
	_, _ = fmt.Fprintf(mac, "deps:%s\n", strings.Join(deps, ","))
	if live == nil {
		_, _ = fmt.Fprint(mac, "absent\n")
	} else {
		_, _ = fmt.Fprint(mac, "present\n")
		for _, name := range deps {
			writeAttribute(mac, name, live.Get(name))
		}
	}

	return Baseline(base64.RawURLEncoding.EncodeToString([]byte(strings.Join(deps, ","))) +
		"." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

// Verify recomputes a baseline against the directory as it is now.
//
// The attribute list comes out of the token rather than out of the record, so
// that what is rechecked is what the plan actually looked at.
func (f *Fingerprinter) Verify(target string, claimed Baseline, live *directory.Entry) bool {
	encoded, _, ok := strings.Cut(string(claimed), ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	var deps []string
	if len(raw) > 0 {
		deps = strings.Split(string(raw), ",")
	}
	// Constant time, because this is a MAC comparison and there is no reason
	// for it not to be.
	return hmac.Equal([]byte(f.over(target, deps, live)), []byte(claimed))
}

// writeAttribute folds one attribute into the fingerprint.
//
// A sensitive attribute contributes its name, whether it is set, and how many
// values it has -- never the bytes. The fingerprint travels to the browser, and
// a keyed hash over a password hash is still a thing an attacker with the key
// could test guesses against; more simply, the rule everywhere else in Alder is
// that those bytes do not leave the server, and a hash of them leaving is a
// worse version of the same decision. What is lost is the ability to detect
// that somebody replaced a password with a different one of the same length,
// which is not what this is for.
func writeAttribute(mac interface{ Write([]byte) (int, error) }, name string, values [][]byte) {
	folded := strings.ToLower(schema.BaseName(name))
	if schema.IsSensitive(name) {
		_, _ = fmt.Fprintf(mac, "attr:%s:sensitive:%d\n", folded, len(values))
		return
	}
	// Sorted, because an attribute is a set and a server may return it in any
	// order. Ordering the fingerprint by the server's whim would report drift
	// on every other read.
	sorted := make([]string, 0, len(values))
	for _, v := range values {
		sorted = append(sorted, string(v))
	}
	sort.Strings(sorted)
	_, _ = fmt.Fprintf(mac, "attr:%s:%d\n", folded, len(sorted))
	for _, v := range sorted {
		_, _ = fmt.Fprintf(mac, "  %d:%s\n", len(v), v)
	}
}

// dependsOn is the set of attributes whose current values decided this change,
// folded and sorted so the fingerprint does not depend on the order they were
// written in.
//
// For a delete or a rename it is empty: what those depend on is the entry
// existing, which the present/absent marker already covers, and for a delete
// also that it has no children -- checked at plan time and reported as a
// conflict rather than folded in here, because a child appearing is a different
// failure with a different explanation.
func dependsOn(record directory.ChangeRecord) []string {
	seen := map[string]bool{}
	add := func(name string) {
		folded := strings.ToLower(schema.BaseName(name))
		seen[folded] = true
	}
	switch record.Type {
	case directory.ChangeAdd:
		for _, a := range record.Attrs {
			add(a.Name)
		}
	case directory.ChangeModify:
		for _, m := range record.Mods {
			add(m.Name)
		}
	case directory.ChangeSetPassword:
		// Nothing. A password change depends on the entry existing and on
		// nothing that can be read back, so there is nothing here to compare.
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Attributes is what a plan has to read to fingerprint this change, for a
// caller assembling one Read rather than several.
//
// objectClass rides along because the planner needs it to tell an add of a new
// entry from a reconcile of an existing one, and asking for it separately would
// be a second round trip per change.
func Attributes(record directory.ChangeRecord) []string {
	names := dependsOn(record)
	out := make([]string, 0, len(names)+1)
	out = append(out, "objectClass")
	for _, n := range names {
		if !strings.EqualFold(n, "objectclass") {
			out = append(out, n)
		}
	}
	return out
}
