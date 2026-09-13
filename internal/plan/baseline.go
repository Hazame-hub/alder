package plan

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"slices"
	"sort"
	"strconv"
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

// Of fingerprints a planned change: the operation it is, and the part of the
// directory it depends on.
//
// Two MACs, not one, because apply has two different questions to ask and the
// answers mean different things to the person reading them.
//
// The first covers the operation itself -- type, DN, every modification and
// value, the rename target. It is what makes "what the plan showed is what
// Apply runs" something the server enforces rather than a convention two code
// paths happen to follow. Without it a baseline was a statement about an
// entry's state that could be attached to any change touching the same
// attributes of that entry, and the server would accept a change nobody had
// planned as though somebody had. A mismatch here is not the directory moving;
// it is the request not being the plan.
//
// The second covers the state the decision depended on, and only that part. A
// change that replaces `mail` is not invalidated by somebody else editing
// `telephoneNumber`, and a baseline over the whole entry would refuse it --
// which trains people to bypass the check, the way an editor that cried
// conflict on every save would. A mismatch here is the directory moving.
//
// live is nil when the entry does not exist, and that absence is itself part of
// the state: a plan that decided "this is an add" is stale the moment the entry
// appears.
//
// The token carries the attribute names the state covers, in front of both
// MACs. A reconciled change is planned from one record -- an add naming cn and
// mail -- and applied as another -- a modify of mail alone -- so a verifier
// that re-derived the list from the record it was handed would fingerprint a
// different set and call every reconcile stale. The names are inside the state
// MAC as well as in front of it, so editing the list invalidates the token
// rather than narrowing what it checks.
func (f *Fingerprinter) Of(record directory.ChangeRecord, live *directory.Entry) Baseline {
	return f.Token(dependsOn(record), record, live)
}

// Token is Of with the dependency list chosen by the caller.
//
// The planner uses it for a reconciled change, where the state that decided the
// outcome is the one the proposal named but the operation is the one that
// will run.
func (f *Fingerprinter) Token(deps []string, operation directory.ChangeRecord, live *directory.Entry) Baseline {
	return Baseline(base64.RawURLEncoding.EncodeToString([]byte(strings.Join(deps, ","))) +
		"." + base64.RawURLEncoding.EncodeToString(f.operationMAC(operation)) +
		"." + base64.RawURLEncoding.EncodeToString(f.stateMAC(operation.DN.String(), deps, live)))
}

// Attributes is the attribute names a token's state covers, read out of the
// token itself. Verification has to read these back from the directory, and
// they are not always the attributes the applied operation touches.
func (b Baseline) Attributes() []string {
	encoded, _, ok := strings.Cut(string(b), ".")
	if !ok {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return nil
	}
	return strings.Split(string(raw), ",")
}

// Verdict is what checking a token against a request and a directory found.
type Verdict int

const (
	// VerdictCurrent means the request is the planned operation and the
	// directory still looks the way the plan assumed.
	VerdictCurrent Verdict = iota
	// VerdictMismatch means the request is not the operation that was planned.
	VerdictMismatch
	// VerdictStale means the request is the planned operation and the directory
	// has moved underneath it.
	VerdictStale
)

// Check verifies a token against the operation being applied and the directory
// as it is now.
//
// The operation is checked before the state, because a request that is not the
// plan tells nobody anything by also being stale. A token that cannot be read at
// all is reported stale rather than mismatched: it names no operation, and the
// honest thing to tell whoever sent it is to plan again.
func (f *Fingerprinter) Check(claimed Baseline, operation directory.ChangeRecord, live *directory.Entry) Verdict {
	parts := strings.Split(string(claimed), ".")
	if len(parts) != 3 {
		return VerdictStale
	}
	rawDeps, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return VerdictStale
	}
	opMAC, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return VerdictStale
	}
	stateMAC, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return VerdictStale
	}

	var deps []string
	if len(rawDeps) > 0 {
		deps = strings.Split(string(rawDeps), ",")
	}
	// Constant time, because these are MAC comparisons and there is no reason
	// for them not to be. Both are computed before either is acted on.
	opOK := hmac.Equal(opMAC, f.operationMAC(operation))
	stateOK := hmac.Equal(stateMAC, f.stateMAC(operation.DN.String(), deps, live))

	switch {
	case opOK && stateOK:
		return VerdictCurrent
	case !opOK && stateOK:
		// The token is genuine -- its state still verifies under this key, for
		// this entry -- and the operation beside it is not the one it was
		// issued for. That, and only that, is a mismatch.
		return VerdictMismatch
	default:
		// Either the directory moved, or neither half verifies: a token from
		// another Alder process, one from before a restart, or one that was
		// never a token. None of those names an operation this server issued,
		// so the only honest instruction is the same -- plan again.
		return VerdictStale
	}
}

// Verify is Check reduced to "is this still good", for callers that only care
// about the state. Kept because the planner's own tests ask exactly that.
func (f *Fingerprinter) Verify(target string, claimed Baseline, live *directory.Entry) bool {
	parts := strings.Split(string(claimed), ".")
	if len(parts) != 3 {
		return false
	}
	rawDeps, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	stateMAC, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	var deps []string
	if len(rawDeps) > 0 {
		deps = strings.Split(string(rawDeps), ",")
	}
	return hmac.Equal(stateMAC, f.stateMAC(target, deps, live))
}

func (f *Fingerprinter) stateMAC(target string, deps []string, live *directory.Entry) []byte {
	mac := hmac.New(sha256.New, f.key)
	// The DN folded, because that is how the entry was found.
	_, _ = fmt.Fprintf(mac, "state\ndn:%s\n", strings.ToLower(target))
	_, _ = fmt.Fprintf(mac, "deps:%s\n", strings.Join(deps, ","))
	if live == nil {
		_, _ = fmt.Fprint(mac, "absent\n")
	} else {
		_, _ = fmt.Fprint(mac, "present\n")
		for _, name := range deps {
			writeAttribute(mac, name, live.Get(name))
		}
	}
	return mac.Sum(nil)
}

// operationMAC covers everything that decides what the directory is asked to
// do.
//
// Every field is length-prefixed, so no two different operations can be framed
// into the same byte stream. Attribute and value order are preserved rather than
// sorted: this is not "the same set of changes", it is "the change that was
// planned", and a modify is a sequence.
//
// A password change contributes its type and DN and never the password. The
// plan never returned the password to begin with -- it is write-only on the way
// in -- so the client supplies it again on apply, and what the token can
// honestly bind is that this entry's password is being set, not to what.
//
// A sensitive attribute in an add or a modify is bound the same way the state
// MAC binds one: by name, operation and how many values, never the bytes. The
// plan withholds those values from its response, so no client can send back
// bytes it was never shown; and a keyed hash over a password hash would be the
// one place in Alder a password's bytes influenced something that reaches the
// browser. What is given up is detecting a substituted password of the same
// shape, which is exactly what set_password has always given up too.
func (f *Fingerprinter) operationMAC(op directory.ChangeRecord) []byte {
	mac := hmac.New(sha256.New, f.key)
	w := framer{w: mac}
	field := func(s string) { w.field("", []byte(s)) }
	bytesField := func(b []byte) { w.field("", b) }

	field("operation")
	field(string(op.Type))
	field(strings.ToLower(op.DN.String()))
	switch op.Type {
	case directory.ChangeAdd:
		field(strconv.Itoa(len(op.Attrs)))
		for _, a := range op.Attrs {
			field(strings.ToLower(a.Name))
			field(strconv.Itoa(len(a.Values)))
			if schema.IsSensitive(a.Name) {
				field("withheld")
				continue
			}
			for _, v := range a.Values {
				bytesField(v)
			}
		}
	case directory.ChangeModify:
		field(strconv.Itoa(len(op.Mods)))
		for _, m := range op.Mods {
			field(string(m.Op))
			field(strings.ToLower(m.Name))
			field(strconv.Itoa(len(m.Values)))
			if schema.IsSensitive(m.Name) {
				field("withheld")
				continue
			}
			for _, v := range m.Values {
				bytesField(v)
			}
		}
	case directory.ChangeModRDN:
		field(op.NewRDN)
		field(strconv.FormatBool(op.DeleteOldRDN))
		field(strings.ToLower(op.NewSuperior.String()))
	case directory.ChangeDelete, directory.ChangeSetPassword:
		// The type and the DN are the whole of it.
	}
	return mac.Sum(nil)
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
	// on every other read. The slice headers are sorted, not copies of the
	// values: a group of a hundred thousand members is hashed on every plan
	// and every apply of a change to it.
	sorted := slices.Clone(values)
	slices.SortFunc(sorted, bytes.Compare)
	_, _ = fmt.Fprintf(mac, "attr:%s:%d\n", folded, len(sorted))
	w := framer{w: mac}
	for _, v := range sorted {
		w.field("  ", v)
	}
}

// framer writes length-prefixed fields straight into a hash.
//
// The byte stream is the one fmt.Fprintf("%d:%s\n") produced; what changes is
// that framing a value no longer formats it. Tokens are computed over every
// value a change depends on, and for a large group formatting was most of the
// cost of planning.
type framer struct {
	w   interface{ Write([]byte) (int, error) }
	buf []byte
}

func (f *framer) field(indent string, value []byte) {
	f.buf = append(f.buf[:0], indent...)
	f.buf = strconv.AppendInt(f.buf, int64(len(value)), 10)
	f.buf = append(f.buf, ':')
	_, _ = f.w.Write(f.buf)
	_, _ = f.w.Write(value)
	_, _ = f.w.Write(newline)
}

var newline = []byte{'\n'}

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
