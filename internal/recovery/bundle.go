package recovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/plan"
	"github.com/hazame-hub/alder/internal/schema"
)

// The format a recovery bundle is written in.
const (
	Format  = "alder-recovery"
	Version = 1
	// MaxSteps is the most steps a bundle holds: the most changes one apply
	// takes.
	MaxSteps = 2000
)

// Origin is what a bundle records about the directory it was made against. It
// is display, not proof: a server announces these, and two directories can
// announce the same ones. No host, port or bind DN is recorded.
type Origin struct {
	Vendor         string   `json:"vendor,omitempty"`
	VendorVersion  string   `json:"vendorVersion,omitempty"`
	NamingContexts []string `json:"namingContexts"`
}

// Bundle is a recovery bundle.
type Bundle struct {
	Format         string         `json:"format"`
	Version        int            `json:"version"`
	CreatedAt      string         `json:"createdAt"`
	Origin         Origin         `json:"origin"`
	Recoverability Recoverability `json:"recoverability"`
	// Steps are in the order their changes were applied. Their compensations
	// run in the reverse order; Changes returns them that way.
	Steps    []Step `json:"steps"`
	Checksum string `json:"checksum,omitempty"`
}

// New assembles a bundle from the steps of the changes that were applied.
func New(origin Origin, steps []Step, created time.Time) (*Bundle, error) {
	if len(steps) > MaxSteps {
		return nil, &Error{Code: CodeTooLarge, Detail: fmt.Sprintf("%d steps, and a bundle holds at most %d", len(steps), MaxSteps)}
	}
	if origin.NamingContexts == nil {
		origin.NamingContexts = []string{}
	}
	b := &Bundle{
		Format: Format, Version: Version, CreatedAt: created.UTC().Format(time.RFC3339),
		Origin: origin, Recoverability: Overall(steps), Steps: steps,
	}
	for i := range b.Steps {
		b.Steps[i].Reasons = sortedReasons(b.Steps[i].Reasons)
		if b.Steps[i].Compensation == nil {
			b.Steps[i].Compensation = []Change{}
		}
	}
	sum, err := b.contentChecksum()
	if err != nil {
		return nil, err
	}
	b.Checksum = sum
	return b, nil
}

// Changes are the compensating changes, in the order they must run: the
// compensation of the last applied change first.
//
// Compensations of the same entry are merged where that is safe, because a plan
// reads every change against the directory as it is now rather than as the
// changes before it would leave it. Two compensating modifications of one entry
// would otherwise plan as one that applies and one whose expectation -- the
// state between the two original changes -- does not hold yet, which a plan
// can only report as drift.
//
//   - Modifications of one entry within a run of modifications become one
//     modification, their mods in execution order: LDAP applies a
//     modification's mods in order, and modifications of different entries do
//     not depend on each other.
//   - A modification followed by the delete of the same entry, which is what
//     compensating an add and then an edit of it produces, becomes the delete.
//
// In both, the expectation of the compensation that runs first wins for every
// attribute it names: it describes the entry as it is now. Anything else is
// left as it is, in order.
func (b *Bundle) Changes() []Change {
	var ordered []Change
	for i := len(b.Steps) - 1; i >= 0; i-- {
		ordered = append(ordered, b.Steps[i].Compensation...)
	}
	return merge(ordered)
}

func merge(ordered []Change) []Change {
	var out []Change
	// run holds the positions in out of the modifications since the last change
	// that was not one, by folded DN.
	run := map[string]int{}
	for _, c := range ordered {
		key := foldedDN(c.DN)
		switch c.Type {
		case string(directory.ChangeModify):
			if at, ok := run[key]; ok {
				merged := out[at]
				merged.Mods = append(append([]Mod(nil), merged.Mods...), c.Mods...)
				merged.Expect = mergeExpect(merged.Expect, c.Expect)
				out[at] = merged
				continue
			}
			run[key] = len(out)
			out = append(out, c)
			continue
		case string(directory.ChangeDelete):
			if at, ok := run[key]; ok {
				c.Expect = mergeExpect(out[at].Expect, c.Expect)
				out = append(out[:at], out[at+1:]...)
			}
		}
		out = append(out, c)
		run = map[string]int{}
	}
	return out
}

// mergeExpect joins two expectations: first's attributes, then second's that
// first does not name. Exhaustive if either is.
func mergeExpect(first, second *Expect) *Expect {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	out := &Expect{Exhaustive: first.Exhaustive || second.Exhaustive}
	named := map[string]bool{}
	for _, a := range first.Attributes {
		named[strings.ToLower(schema.BaseName(a.Name))] = true
		out.Attributes = append(out.Attributes, a)
	}
	for _, a := range second.Attributes {
		if !named[strings.ToLower(schema.BaseName(a.Name))] {
			out.Attributes = append(out.Attributes, a)
		}
	}
	return out
}

func foldedDN(s string) string {
	if d, err := dn.Parse(s); err == nil {
		return strings.ToLower(d.String())
	}
	return s
}

// Encode writes a bundle as indented JSON.
func Encode(w io.Writer, b *Bundle) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(b)
}

func (b *Bundle) contentChecksum() (string, error) {
	content, err := json.Marshal(struct {
		Format         string         `json:"format"`
		Version        int            `json:"version"`
		Origin         Origin         `json:"origin"`
		Recoverability Recoverability `json:"recoverability"`
		Steps          []Step         `json:"steps"`
	}{b.Format, b.Version, b.Origin, b.Recoverability, b.Steps})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Integrity is what the checksum said.
type Integrity string

const (
	IntegrityVerified   Integrity = "verified"
	IntegrityUnverified Integrity = "unverified"
)

// Error codes for a bundle that cannot be used.
const (
	CodeNotBundle          = "not_recovery_bundle"
	CodeUnsupportedVersion = "unsupported_version"
	CodeInvalid            = "invalid"
	CodeChecksumMismatch   = "checksum_mismatch"
	CodeTooLarge           = "too_large"
)

// Error is a refused bundle.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return "recovery: " + e.Code + ": " + e.Detail }

// Decode reads a bundle, refusing anything that is not exactly a valid version
// 1 bundle. A bundle is untrusted input: it arrives from a file anyone could
// have edited, so nothing in it is believed until it has been checked, and a
// field this version does not know is refused rather than ignored.
func Decode(data []byte) (*Bundle, Integrity, error) {
	var probe struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil || probe.Format != Format {
		return nil, "", &Error{Code: CodeNotBundle, Detail: "this is not an Alder recovery bundle"}
	}
	if probe.Version != Version {
		return nil, "", &Error{Code: CodeUnsupportedVersion,
			Detail: fmt.Sprintf("recovery bundle version %d; this Alder reads version %d", probe.Version, Version)}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		return nil, "", &Error{Code: CodeInvalid, Detail: err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, "", &Error{Code: CodeInvalid, Detail: "there is more after the bundle"}
	}
	if err := b.validate(); err != nil {
		return nil, "", err
	}
	claimed := b.Checksum
	sum, err := b.contentChecksum()
	if err != nil {
		return nil, "", err
	}
	if claimed == "" {
		b.Checksum = sum
		return &b, IntegrityUnverified, nil
	}
	if claimed != sum {
		return nil, "", &Error{Code: CodeChecksumMismatch,
			Detail: "the content does not match its checksum: the bundle was corrupted or edited after it was written"}
	}
	return &b, IntegrityVerified, nil
}

func invalid(format string, a ...any) error {
	return &Error{Code: CodeInvalid, Detail: fmt.Sprintf(format, a...)}
}

func (b *Bundle) validate() error {
	if _, err := time.Parse(time.RFC3339, b.CreatedAt); err != nil {
		return invalid("createdAt %q is not an RFC 3339 time", b.CreatedAt)
	}
	if len(b.Steps) > MaxSteps {
		return &Error{Code: CodeTooLarge, Detail: fmt.Sprintf("%d steps, and a bundle holds at most %d", len(b.Steps), MaxSteps)}
	}
	if b.Origin.NamingContexts == nil {
		return invalid("origin.namingContexts is missing")
	}
	for i, s := range b.Steps {
		where := fmt.Sprintf("step %d", i)
		switch s.Kind {
		case KindData, KindSchema, KindConfig:
		default:
			return invalid("%s: kind %q", where, s.Kind)
		}
		switch s.Recoverability {
		case Exact, Partial, Unavailable:
		default:
			return invalid("%s: recoverability %q", where, s.Recoverability)
		}
		if s.Recoverability == Unavailable && len(s.Compensation) > 0 {
			return invalid("%s: an unavailable recovery carries a compensation", where)
		}
		if s.Recoverability != Exact && len(s.Reasons) == 0 {
			return invalid("%s: a %s recovery gives no reason", where, s.Recoverability)
		}
		for _, r := range s.Reasons {
			if !knownReasons[r.Code] {
				return invalid("%s: reason %q", where, r.Code)
			}
		}
		if s.Compensation == nil {
			return invalid("%s: compensation is missing", where)
		}
		if _, err := dn.Parse(s.Original.DN); err != nil {
			return invalid("%s: original dn: %v", where, err)
		}
		for j, c := range s.Compensation {
			if _, _, err := c.Record(); err != nil {
				return invalid("%s, compensation %d: %v", where, j, err)
			}
		}
	}
	if b.Recoverability != Overall(b.Steps) {
		return invalid("recoverability %q does not match its steps", b.Recoverability)
	}
	return nil
}

// Record turns a compensating change into the canonical operation and the
// expectation a plan checks.
func (c Change) Record() (directory.ChangeRecord, *plan.Expectation, error) {
	target, err := dn.Parse(c.DN)
	if err != nil {
		return directory.ChangeRecord{}, nil, fmt.Errorf("dn: %w", err)
	}
	rec := directory.ChangeRecord{DN: target}
	switch c.Type {
	case string(directory.ChangeAdd):
		rec.Type = directory.ChangeAdd
		if len(c.Attributes) == 0 || len(c.Mods) > 0 || c.NewRDN != "" {
			return rec, nil, errors.New("an add has attributes and nothing else")
		}
		for _, a := range c.Attributes {
			values, err := decodeValues(a.Name, a.Values)
			if err != nil {
				return rec, nil, err
			}
			rec.Attrs = append(rec.Attrs, directory.Attribute{Name: a.Name, Values: values})
		}
	case string(directory.ChangeModify):
		rec.Type = directory.ChangeModify
		if len(c.Mods) == 0 || len(c.Attributes) > 0 || c.NewRDN != "" {
			return rec, nil, errors.New("a modify has modifications and nothing else")
		}
		for _, m := range c.Mods {
			op := directory.ModOp(m.Op)
			if op != directory.ModAdd && op != directory.ModDelete && op != directory.ModReplace {
				return rec, nil, fmt.Errorf("modification %q", m.Op)
			}
			values, err := decodeValues(m.Name, m.Values)
			if err != nil {
				return rec, nil, err
			}
			rec.Mods = append(rec.Mods, directory.Mod{Op: op, Name: m.Name, Values: values})
		}
	case string(directory.ChangeDelete):
		rec.Type = directory.ChangeDelete
		if len(c.Attributes) > 0 || len(c.Mods) > 0 || c.NewRDN != "" {
			return rec, nil, errors.New("a delete names an entry and nothing else")
		}
	case string(directory.ChangeModRDN):
		rec.Type = directory.ChangeModRDN
		if c.NewRDN == "" || c.DeleteOldRDN == nil || len(c.Attributes) > 0 || len(c.Mods) > 0 {
			return rec, nil, errors.New("a rename has a new RDN, deleteOldRdn, and optionally a new superior")
		}
		rec.NewRDN = c.NewRDN
		rec.DeleteOldRDN = *c.DeleteOldRDN
		if c.NewSuperior != "" {
			sup, err := dn.Parse(c.NewSuperior)
			if err != nil {
				return rec, nil, fmt.Errorf("newSuperior: %w", err)
			}
			rec.NewSuperior = sup
		}
	default:
		// A password change can never be a compensation: there is no
		// password to put back.
		return rec, nil, fmt.Errorf("type %q cannot be a compensating change", c.Type)
	}
	if err := rec.Validate(); err != nil {
		return rec, nil, err
	}

	var expect *plan.Expectation
	if c.Expect != nil {
		expect = &plan.Expectation{Exhaustive: c.Expect.Exhaustive}
		for _, a := range c.Expect.Attributes {
			values, err := decodeValues(a.Name, a.Values)
			if err != nil {
				return rec, nil, fmt.Errorf("expect: %w", err)
			}
			expect.Attributes = append(expect.Attributes, directory.Attribute{Name: a.Name, Values: values})
		}
	}
	return rec, expect, nil
}

// decodeValues refuses a sensitive attribute with values outright: Alder never
// writes one into a bundle, so a bundle carrying one has been made by hand, and
// planning it would put a secret from a file into the directory.
func decodeValues(name string, values []Value) ([][]byte, error) {
	if err := dn.ValidateType(schema.BaseName(name)); err != nil {
		return nil, fmt.Errorf("attribute %q: %w", name, err)
	}
	if schema.IsSensitive(name) {
		return nil, fmt.Errorf("%s is a sensitive attribute, and a recovery bundle never holds one", name)
	}
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		raw, err := v.Bytes()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, raw)
	}
	return out, nil
}

// SameOrigin compares where a bundle was made with the directory it is being
// used against, as far as either can say.
func SameOrigin(a, b Origin) (bool, []string) {
	var differences []string
	if !strings.EqualFold(strings.TrimSpace(a.Vendor), strings.TrimSpace(b.Vendor)) {
		differences = append(differences, "vendor")
	}
	contexts := func(list []string) map[string]bool {
		out := map[string]bool{}
		for _, c := range list {
			if d, err := dn.Parse(c); err == nil {
				out[strings.ToLower(d.String())] = true
			}
		}
		return out
	}
	ca, cb := contexts(a.NamingContexts), contexts(b.NamingContexts)
	for c := range ca {
		if !cb[c] {
			differences = append(differences, "namingContexts")
			break
		}
	}
	return len(differences) == 0, differences
}
