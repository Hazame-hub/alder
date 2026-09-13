// Package snapshot is Alder's versioned record of directory state.
//
// A snapshot is not LDIF. LDIF is the interchange format -- it imports, it
// exports, it round-trips through other tools -- and it has nowhere to say what
// a capture covered, what it left out on purpose, or how its values compare. A
// snapshot says all three, so that comparing it later is a statement about the
// directory rather than about the file.
//
// Three properties are the point of the format:
//
//   - It is versioned from the first byte. A reader rejects a version it does
//     not know rather than guessing what a newer writer meant.
//   - Its content is canonical. Entries, attribute names and values are in a
//     defined order, so two captures of an unchanged directory differ only in
//     their creation time, and the content checksum is identical.
//   - It holds no secrets. A sensitive attribute is recorded as how many values
//     it has, never as a value or anything derived from one.
package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

const (
	// Format names the document so that a snapshot is never mistaken for some
	// other JSON, and the other way round.
	Format = "alder-snapshot"
	// Version is the snapshot format version this build writes and the newest
	// it reads. See docs/COMPATIBILITY.md.
	Version = 1
	// KindData is the only kind 1.7 captures: entries of a naming context.
	// Schema and configuration are deliberately not snapshot kinds yet.
	KindData = "data"
	// Complete is the only completeness a snapshot may have. A capture that
	// could not finish is an error, never a snapshot.
	Complete = "complete"

	// MaxEntries bounds a snapshot. A capture has to hold every entry to put
	// them in canonical order, so this is a bound on memory, and a capture
	// that would exceed it fails rather than returning part of the tree.
	MaxEntries = 50000
)

// IdentityAttributes are read with every capture, whether or not operational
// attributes are included, because they are how a rename is recognised. They
// are recorded as the entry's id, not as attributes.
var IdentityAttributes = []string{"entryUUID", "nsUniqueId"}

// Value is one attribute value: exactly one of Text or Base64.
type Value struct {
	Text   *string `json:"text,omitempty"`
	Base64 *string `json:"base64,omitempty"`
}

// Bytes returns the value's bytes.
func (v Value) Bytes() ([]byte, error) {
	switch {
	case v.Text != nil && v.Base64 == nil:
		return []byte(*v.Text), nil
	case v.Base64 != nil && v.Text == nil:
		return base64.StdEncoding.DecodeString(*v.Base64)
	}
	return nil, errors.New("a value must carry exactly one of text or base64")
}

// Attribute is one attribute of an entry. A sensitive attribute has no values
// and a Withheld count instead.
type Attribute struct {
	Name     string  `json:"name"`
	Values   []Value `json:"values,omitempty"`
	Withheld int     `json:"withheld,omitempty"`
}

// Entry is one captured entry.
type Entry struct {
	DN string `json:"dn"`
	// ID is a stable identity the server assigned, as "attribute=value" --
	// "entryUUID=..." or "nsUniqueId=..." -- and empty when the server offered
	// none. Only equal ids on both sides of a comparison make a rename.
	ID         string      `json:"id,omitempty"`
	Attributes []Attribute `json:"attributes"`
}

// AttributeInfo is what the capturing server's schema said about an attribute
// the snapshot holds, recorded so a later comparison does not depend on having
// that server's schema to hand.
type AttributeInfo struct {
	Name        string `json:"name"`
	Equality    string `json:"equality,omitempty"`
	Syntax      string `json:"syntax,omitempty"`
	SingleValue bool   `json:"singleValue,omitempty"`
	Operational bool   `json:"operational,omitempty"`
	Sensitive   bool   `json:"sensitive,omitempty"`
}

// Source is where and what was captured.
type Source struct {
	Vendor        string `json:"vendor,omitempty"`
	VendorVersion string `json:"vendorVersion,omitempty"`
	Base          string `json:"base"`
	Scope         string `json:"scope"`
	Filter        string `json:"filter"`
}

// Snapshot is the whole document.
type Snapshot struct {
	Format    string `json:"format"`
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"createdAt"`
	Source    Source `json:"source"`
	// OperationalAttributes reports whether operational attributes were
	// captured. Off by default: they change on every write and are not state
	// anybody sets.
	OperationalAttributes bool `json:"operationalAttributes"`
	// SchemaAvailable reports whether the capturing session could read a
	// schema. Without one, values compare byte for byte.
	SchemaAvailable bool `json:"schemaAvailable"`
	// Excluded names what was left out on purpose, as stable identifiers:
	// "operational-attributes" and "sensitive-values".
	Excluded     []string        `json:"excluded"`
	Completeness string          `json:"completeness"`
	EntryCount   int             `json:"entryCount"`
	Attributes   []AttributeInfo `json:"attributes"`
	// Checksum is "sha256:<hex>" over the canonical content -- everything but
	// CreatedAt and Checksum. A change to the entries or the covered metadata
	// invalidates it; a change to CreatedAt does not. It is an integrity check
	// against corruption, not authentication or a signature.
	Checksum string  `json:"checksum,omitempty"`
	Entries  []Entry `json:"entries"`

	index *index
}

// Excluded categories.
const (
	ExcludedOperational = "operational-attributes"
	ExcludedSensitive   = "sensitive-values"
)

// Capture describes a capture for Build.
type Capture struct {
	Base          dn.DN
	Scope         string
	Filter        string
	Vendor        string
	VendorVersion string
	Operational   bool
	CreatedAt     time.Time
}

// Build turns captured entries into a canonical snapshot.
//
// The entries are expected to have been read with attributes "*" plus the
// identity attributes, and "+" when operational attributes are wanted. What is
// kept is decided here, from the schema when there is one, so that the same
// rule holds whichever way the entries were obtained.
func Build(c Capture, sch *schema.Schema, entries []*directory.Entry) (*Snapshot, error) {
	if len(entries) > MaxEntries {
		return nil, &Error{Code: CodeTooLarge, Detail: fmt.Sprintf("%d entries, and a snapshot holds at most %d", len(entries), MaxEntries)}
	}
	schemaAvailable := sch != nil && !sch.IsEmpty()
	s := &Snapshot{
		Format:                Format,
		Version:               Version,
		Kind:                  KindData,
		CreatedAt:             c.CreatedAt.UTC().Format(time.RFC3339),
		Source:                Source{Vendor: c.Vendor, VendorVersion: c.VendorVersion, Base: c.Base.String(), Scope: c.Scope, Filter: c.Filter},
		OperationalAttributes: c.Operational,
		SchemaAvailable:       schemaAvailable,
		Completeness:          Complete,
		Entries:               make([]Entry, 0, len(entries)),
	}
	s.Excluded = []string{ExcludedSensitive}
	if !c.Operational {
		s.Excluded = []string{ExcludedOperational, ExcludedSensitive}
	}

	infos := map[string]AttributeInfo{}
	identity := map[string]bool{}
	for _, name := range IdentityAttributes {
		identity[strings.ToLower(name)] = true
	}

	for _, e := range entries {
		out := Entry{DN: e.DN.String()}
		for _, name := range IdentityAttributes {
			if v := e.Get(name); len(v) > 0 {
				out.ID = name + "=" + string(v[0])
				break
			}
		}
		for _, name := range e.Order {
			base := strings.ToLower(schema.BaseName(name))
			values := e.Attributes[name]
			var at *schema.AttributeType
			if schemaAvailable {
				at = sch.AttributeType(schema.BaseName(name))
			}
			operational := at != nil && (sch.EffectiveUsage(at).Operational() || sch.EffectiveNoUserModification(at))
			if identity[base] && !c.Operational {
				// Read for the id; not state unless operational attributes were
				// asked for.
				continue
			}
			if operational && !c.Operational {
				continue
			}
			canonical := name
			if schemaAvailable {
				canonical = sch.CanonicalAttrName(name)
			}
			sensitive := schema.IsSensitive(name)
			attr := Attribute{Name: canonical}
			if sensitive {
				attr.Withheld = len(values)
			} else {
				attr.Values = encodeValues(values)
			}
			out.Attributes = append(out.Attributes, attr)

			key := strings.ToLower(canonical)
			if _, seen := infos[key]; !seen {
				info := AttributeInfo{Name: canonical, Operational: operational, Sensitive: sensitive}
				if at != nil {
					info.Equality = sch.EffectiveEquality(at)
					info.Syntax = sch.EffectiveSyntax(at)
					info.SingleValue = sch.EffectiveSingleValue(at)
				}
				infos[key] = info
			}
		}
		s.Entries = append(s.Entries, out)
	}

	s.Attributes = make([]AttributeInfo, 0, len(infos))
	for _, info := range infos {
		s.Attributes = append(s.Attributes, info)
	}
	if err := s.canonicalise(); err != nil {
		return nil, err
	}
	s.EntryCount = len(s.Entries)
	sum, err := s.contentChecksum()
	if err != nil {
		return nil, err
	}
	s.Checksum = sum
	return s, nil
}

func encodeValues(values [][]byte) []Value {
	out := make([]Value, 0, len(values))
	for _, v := range values {
		if utf8.Valid(v) && !hasControlBytes(v) {
			text := string(v)
			out = append(out, Value{Text: &text})
			continue
		}
		b := base64.StdEncoding.EncodeToString(v)
		out = append(out, Value{Base64: &b})
	}
	return out
}

func hasControlBytes(v []byte) bool {
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// canonicalise puts the snapshot in its one defined order: entries parent
// before child and then by name, objectClass first and then attributes by
// name, and values by their comparison key. Value order carries no meaning in
// LDAP for the data a 1.7 snapshot holds; the configuration trees where it
// does (X-ORDERED) are not a snapshot kind.
func (s *Snapshot) canonicalise() error {
	keys := make([]string, len(s.Entries))
	for i := range s.Entries {
		d, err := dn.Parse(s.Entries[i].DN)
		if err != nil {
			return &Error{Code: CodeInvalid, Detail: fmt.Sprintf("entry %d: %q is not a DN: %v", i, s.Entries[i].DN, err)}
		}
		s.Entries[i].DN = d.String()
		keys[i] = hierarchyKey(d)
	}
	order := make([]int, len(s.Entries))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return keys[order[a]] < keys[order[b]] })
	sorted := make([]Entry, len(s.Entries))
	for i, j := range order {
		sorted[i] = s.Entries[j]
		if i > 0 && keys[order[i-1]] == keys[j] {
			return &Error{Code: CodeInvalid, Detail: fmt.Sprintf("%q appears twice", s.Entries[j].DN)}
		}
	}
	s.Entries = sorted

	for i := range s.Entries {
		attrs := s.Entries[i].Attributes
		sort.SliceStable(attrs, func(a, b int) bool { return attributeLess(attrs[a].Name, attrs[b].Name) })
		for j := range attrs {
			if j > 0 && strings.EqualFold(attrs[j-1].Name, attrs[j].Name) {
				return &Error{Code: CodeInvalid, Detail: fmt.Sprintf("%q holds %q twice", s.Entries[i].DN, attrs[j].Name)}
			}
			keyer := s.KeyerFor(attrs[j].Name)
			sortValues(attrs[j].Values, keyer)
		}
	}
	sort.SliceStable(s.Attributes, func(a, b int) bool { return attributeLess(s.Attributes[a].Name, s.Attributes[b].Name) })
	sort.Strings(s.Excluded)
	s.index = nil
	return nil
}

func attributeLess(a, b string) bool {
	ao, bo := strings.EqualFold(a, "objectClass"), strings.EqualFold(b, "objectClass")
	if ao != bo {
		return ao
	}
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

func sortValues(values []Value, keyer Keyer) {
	type keyed struct {
		key string
		raw string
		v   Value
	}
	items := make([]keyed, len(values))
	for i, v := range values {
		raw, _ := v.Bytes()
		items[i] = keyed{key: keyer(raw), raw: string(raw), v: v}
	}
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].key != items[b].key {
			return items[a].key < items[b].key
		}
		return items[a].raw < items[b].raw
	})
	for i := range items {
		values[i] = items[i].v
	}
}

// hierarchyKey orders a DN after its parent and before its parent's next
// sibling: the RDNs from the root down, folded, joined by a byte that sorts
// below every character a rendered RDN contains.
func hierarchyKey(d dn.DN) string {
	parts := make([]string, 0, len(d))
	for i := len(d) - 1; i >= 0; i-- {
		parts = append(parts, strings.ToLower(d[i].String()))
	}
	return strings.Join(parts, "\x00")
}

// DNKey is the comparison key of a DN: rendered through the parser and folded,
// the same allowance the rest of Alder makes when it compares DNs.
func DNKey(d dn.DN) string { return strings.ToLower(d.String()) }

// canonicalContent is the checksummed part of the document.
func (s *Snapshot) canonicalContent() ([]byte, error) {
	content := struct {
		Format                string          `json:"format"`
		Version               int             `json:"version"`
		Kind                  string          `json:"kind"`
		Source                Source          `json:"source"`
		OperationalAttributes bool            `json:"operationalAttributes"`
		SchemaAvailable       bool            `json:"schemaAvailable"`
		Excluded              []string        `json:"excluded"`
		Completeness          string          `json:"completeness"`
		EntryCount            int             `json:"entryCount"`
		Attributes            []AttributeInfo `json:"attributes"`
		Entries               []Entry         `json:"entries"`
	}{s.Format, s.Version, s.Kind, s.Source, s.OperationalAttributes, s.SchemaAvailable,
		s.Excluded, s.Completeness, s.EntryCount, s.Attributes, s.Entries}
	return json.Marshal(content)
}

func (s *Snapshot) contentChecksum() (string, error) {
	content, err := s.canonicalContent()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Encode writes the snapshot as its canonical document: indented JSON, one
// value per line, so a snapshot committed to a repository diffs readably.
func Encode(w io.Writer, s *Snapshot) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// Integrity is what the checksum said on decoding.
type Integrity string

const (
	IntegrityVerified   Integrity = "verified"
	IntegrityUnverified Integrity = "unverified"
)

// Error codes are stable identifiers for why a document is not a usable
// snapshot.
const (
	CodeNotSnapshot        = "not_a_snapshot"
	CodeUnsupportedVersion = "unsupported_version"
	CodeInvalid            = "invalid"
	CodeChecksumMismatch   = "checksum_mismatch"
	CodeTooLarge           = "too_large"
)

// Error is a refused snapshot.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return "snapshot: " + e.Code + ": " + e.Detail }

// Decode reads and validates a snapshot document.
//
// Everything a snapshot claims is checked before it is used: the format and
// version, every DN, every value's encoding, that no sensitive attribute
// carries a value, that entries lie inside the captured scope. Unknown fields
// are refused rather than ignored: version 1 has a fixed set, and a field a
// newer writer added may change what the others mean. The document is put in
// canonical order, and a checksum that is present must match; one that is
// absent leaves the integrity unverified, which the caller reports.
func Decode(data []byte) (*Snapshot, Integrity, error) {
	var probe struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	// Only the first value is probed, so a document with something after the
	// snapshot is reported as the invalid snapshot it is, not as foreign.
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil || probe.Format != Format {
		return nil, "", &Error{Code: CodeNotSnapshot, Detail: "the document is not an Alder snapshot (no \"format\": \"alder-snapshot\")"}
	}
	if probe.Version > Version {
		return nil, "", &Error{Code: CodeUnsupportedVersion,
			Detail: fmt.Sprintf("snapshot version %d is newer than this Alder reads (%d); use a newer Alder", probe.Version, Version)}
	}
	if probe.Version < 1 {
		return nil, "", &Error{Code: CodeUnsupportedVersion, Detail: fmt.Sprintf("snapshot version %d does not exist", probe.Version)}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Snapshot
	if err := dec.Decode(&s); err != nil {
		return nil, "", &Error{Code: CodeInvalid, Detail: err.Error()}
	}
	if dec.More() {
		return nil, "", &Error{Code: CodeInvalid, Detail: "the document continues after the snapshot"}
	}
	if err := s.validate(); err != nil {
		return nil, "", err
	}
	claimed := s.Checksum
	if err := s.canonicalise(); err != nil {
		return nil, "", err
	}
	sum, err := s.contentChecksum()
	if err != nil {
		return nil, "", err
	}
	s.Checksum = sum
	if claimed == "" {
		return &s, IntegrityUnverified, nil
	}
	if claimed != sum {
		return nil, "", &Error{Code: CodeChecksumMismatch,
			Detail: "the content does not match its checksum: the file was corrupted or edited after capture"}
	}
	return &s, IntegrityVerified, nil
}

func (s *Snapshot) validate() error {
	invalid := func(format string, args ...any) error {
		return &Error{Code: CodeInvalid, Detail: fmt.Sprintf(format, args...)}
	}
	if s.Kind != KindData {
		return invalid("kind %q is not supported; Alder 1.x snapshots are kind %q", s.Kind, KindData)
	}
	if s.Completeness != Complete {
		return invalid("completeness %q: only a complete capture is a snapshot", s.Completeness)
	}
	if _, err := time.Parse(time.RFC3339, s.CreatedAt); err != nil {
		return invalid("createdAt %q is not an RFC 3339 time", s.CreatedAt)
	}
	base, err := dn.Parse(s.Source.Base)
	if err != nil {
		return invalid("source.base %q is not a DN: %v", s.Source.Base, err)
	}
	switch s.Source.Scope {
	case "base", "one", "sub":
	default:
		return invalid("source.scope %q is not base, one or sub", s.Source.Scope)
	}
	if strings.TrimSpace(s.Source.Filter) != "" {
		if _, err := filter.Parse(s.Source.Filter); err != nil {
			return invalid("source.filter is not a valid filter: %v", err)
		}
	}
	for _, x := range s.Excluded {
		if x != ExcludedOperational && x != ExcludedSensitive {
			return invalid("excluded category %q is not known", x)
		}
	}
	if len(s.Entries) > MaxEntries {
		return &Error{Code: CodeTooLarge, Detail: fmt.Sprintf("%d entries, and a snapshot holds at most %d", len(s.Entries), MaxEntries)}
	}
	if s.EntryCount != len(s.Entries) {
		return invalid("entryCount says %d and the document holds %d", s.EntryCount, len(s.Entries))
	}
	seenInfo := map[string]bool{}
	for _, a := range s.Attributes {
		if err := validateAttributeName(a.Name); err != nil {
			return invalid("attributes: %v", err)
		}
		key := strings.ToLower(a.Name)
		if seenInfo[key] {
			return invalid("attributes: %q is described twice", a.Name)
		}
		seenInfo[key] = true
	}
	for i, e := range s.Entries {
		d, err := dn.Parse(e.DN)
		if err != nil {
			return invalid("entry %d: %q is not a DN: %v", i, e.DN, err)
		}
		if !inScope(d, base, s.Source.Scope) {
			return invalid("entry %q lies outside the captured scope (%s of %s)", e.DN, s.Source.Scope, s.Source.Base)
		}
		if e.ID != "" {
			name, _, ok := strings.Cut(e.ID, "=")
			if !ok || !isIdentityAttribute(name) {
				return invalid("entry %q: id %q is not attribute=value for a known identity attribute", e.DN, e.ID)
			}
		}
		for _, a := range e.Attributes {
			if err := validateAttributeName(a.Name); err != nil {
				return invalid("entry %q: %v", e.DN, err)
			}
			if a.Withheld < 0 {
				return invalid("entry %q: %s has a negative withheld count", e.DN, a.Name)
			}
			if schema.IsSensitive(a.Name) && len(a.Values) > 0 {
				return invalid("entry %q: %s is a sensitive attribute and carries values; a snapshot never holds them", e.DN, a.Name)
			}
			if a.Withheld > 0 && len(a.Values) > 0 {
				return invalid("entry %q: %s has both values and a withheld count", e.DN, a.Name)
			}
			if a.Withheld == 0 && len(a.Values) == 0 {
				return invalid("entry %q: %s has no values", e.DN, a.Name)
			}
			for _, v := range a.Values {
				if _, err := v.Bytes(); err != nil {
					return invalid("entry %q: %s: %v", e.DN, a.Name, err)
				}
			}
		}
	}
	return nil
}

func isIdentityAttribute(name string) bool {
	for _, a := range IdentityAttributes {
		if strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

func validateAttributeName(name string) error {
	base := schema.BaseName(name)
	if err := dn.ValidateType(base); err != nil {
		return fmt.Errorf("%q is not an attribute description: %w", name, err)
	}
	return nil
}

func inScope(d, base dn.DN, scope string) bool {
	switch scope {
	case "base":
		return d.Equal(base)
	case "one":
		return d.IsChildOf(base)
	default:
		return d.HasSuffix(base)
	}
}

// InScope reports whether a DN lies within this snapshot's base and scope. The
// filter cannot be evaluated against a DN alone and is not considered.
func (s *Snapshot) InScope(d dn.DN) bool {
	base, err := dn.Parse(s.Source.Base)
	if err != nil {
		return false
	}
	return inScope(d, base, s.Source.Scope)
}

// index is the lookup a comparison needs, built once per snapshot.
type index struct {
	byKey map[string]int
	byID  map[string]int
	dns   []dn.DN
	info  map[string]AttributeInfo
}

func (s *Snapshot) lookup() *index {
	if s.index != nil {
		return s.index
	}
	ix := &index{
		byKey: make(map[string]int, len(s.Entries)),
		byID:  make(map[string]int, len(s.Entries)),
		dns:   make([]dn.DN, len(s.Entries)),
		info:  make(map[string]AttributeInfo, len(s.Attributes)),
	}
	dupIDs := map[string]bool{}
	for i, e := range s.Entries {
		d, _ := dn.Parse(e.DN)
		ix.dns[i] = d
		ix.byKey[DNKey(d)] = i
		if e.ID != "" {
			if _, dup := ix.byID[e.ID]; dup {
				dupIDs[e.ID] = true
			}
			ix.byID[e.ID] = i
		}
	}
	// An id that is not unique identifies nothing.
	for id := range dupIDs {
		delete(ix.byID, id)
	}
	for _, a := range s.Attributes {
		ix.info[strings.ToLower(schema.BaseName(a.Name))] = a
	}
	s.index = ix
	return ix
}

// EntryByKey returns the entry with this DN key, if the snapshot holds one.
func (s *Snapshot) EntryByKey(key string) (*Entry, dn.DN, bool) {
	ix := s.lookup()
	i, ok := ix.byKey[key]
	if !ok {
		return nil, nil, false
	}
	return &s.Entries[i], ix.dns[i], true
}

// EntryByID returns the entry with this identity, if exactly one has it.
func (s *Snapshot) EntryByID(id string) (*Entry, dn.DN, bool) {
	if id == "" {
		return nil, nil, false
	}
	ix := s.lookup()
	i, ok := ix.byID[id]
	if !ok {
		return nil, nil, false
	}
	return &s.Entries[i], ix.dns[i], true
}

// DNAt returns the parsed DN of the i-th entry.
func (s *Snapshot) DNAt(i int) dn.DN { return s.lookup().dns[i] }

// Info returns what the snapshot recorded about an attribute.
func (s *Snapshot) Info(name string) (AttributeInfo, bool) {
	info, ok := s.lookup().info[strings.ToLower(schema.BaseName(name))]
	return info, ok
}

// KeyerFor returns the comparison key function for an attribute, from the
// equality rule this snapshot recorded for it.
func (s *Snapshot) KeyerFor(name string) Keyer {
	if info, ok := s.Info(name); ok {
		if k, known := KeyerForRule(info.Equality); known {
			return k
		}
	}
	return ExactKey
}
