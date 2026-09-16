// Package changepkg reads and writes Alder change packages.
//
// A change package is a portable description of intended directory and schema
// changes: what an operator means to change, not the operations Alder once
// produced somewhere else. It travels between environments as bytes, and each
// target validates it against its own current state and makes its own plan.
//
// So a package holds no plan token, no baseline, no session, no credential and
// no secret. A baseline is a MAC under a key that exists only in one process,
// and an expectation about an entry's current values describes the environment
// it was written in; neither means anything in the next one. What survives the
// journey is the intent: this DN gains this attribute, this schema definition
// should exist, this entry goes away.
//
// Schema intent is declarative for the same reason. The modification that
// installs an attribute type is `attributeTypes` on `cn=schema` on one server
// and `olcAttributeTypes` on a configuration collection on another, so a
// package carries the element, the operation and the definition, and each
// target builds the modification it needs through the ordinary schema write
// path.
package changepkg

import (
	"bytes"
	"crypto/rand"
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

	"crypto/sha256"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/schema"
)

// The format a change package is written in.
const (
	Format  = "alder-change-package"
	Version = 1

	// MaxChanges is the most items a package holds. The same bound as a
	// changeset and a plan, and for the same reason: the whole set is read,
	// validated and planned in one request.
	MaxChanges = 2000
	// MaxDependencyEdges bounds the whole graph, so a package cannot ask for
	// unbounded work by naming every item from every other.
	MaxDependencyEdges = 20000
	// MaxDependenciesPerItem bounds one item's list.
	MaxDependenciesPerItem = 64
	// MaxTextBytes bounds one string a package carries: a definition, a DN, a
	// title, one attribute value.
	MaxTextBytes = 64 << 10
	// MaxIDBytes bounds a change identifier.
	MaxIDBytes = 128
)

// Item kinds.
const (
	KindData   = "data"
	KindSchema = "schema"
)

// Data operations. They are the ordinary change types, less the password
// change, which cannot be packaged without its secret.
const (
	OpAdd    = "add"
	OpModify = "modify"
	OpDelete = "delete"
	OpRename = "rename"
)

// Schema operations, as the schema write path names them.
const (
	SchemaAdd     = "add"
	SchemaReplace = "replace"
	SchemaDelete  = "delete"
)

// Schema element kinds, as the schema comparison names them.
const (
	ElementAttributeType = "attributeType"
	ElementObjectClass   = "objectClass"
)

// How an item is read when it is planned.
const (
	// IntentExact plans the operation as written.
	IntentExact = "exact"
	// IntentDesired reconciles an add against the entry that is there,
	// changing only the attributes it names. Only an add may ask for it.
	IntentDesired = "desired"
)

// How a package was made.
const (
	MethodChangeset  = "changeset"
	MethodDataDiff   = "data_diff"
	MethodSchemaDiff = "schema_diff"
	MethodCLI        = "cli"
)

// Why an intended change is not in the package. Machine-readable, because the
// alternative is dropping it silently.
const (
	// OmittedSecret: the change carries or sets a secret. A package never
	// holds one, and a placeholder that could replay a password is worse than
	// an omission.
	OmittedSecret = "secret_not_portable"
	// OmittedUnsupported: the change is of a kind a package does not carry.
	OmittedUnsupported = "unsupported_change"
	// OmittedServerSpecific: the change is tied to one server's arrangement
	// and could not be revalidated safely elsewhere.
	OmittedServerSpecific = "server_specific"
)

// Error codes, the same identifiers the other Alder artifacts use.
const (
	CodeNotPackage         = "not_a_package"
	CodeUnsupportedVersion = "unsupported_version"
	CodeInvalid            = "invalid"
	CodeChecksumMismatch   = "checksum_mismatch"
	CodeTooLarge           = "too_large"
)

// Error is a refused package.
type Error struct {
	Code   string
	Detail string
}

func (e *Error) Error() string { return "change package: " + e.Code + ": " + e.Detail }

func invalid(format string, a ...any) *Error {
	return &Error{Code: CodeInvalid, Detail: fmt.Sprintf(format, a...)}
}

// Integrity is what the checksum said on decoding.
type Integrity string

const (
	IntegrityVerified   Integrity = "verified"
	IntegrityUnverified Integrity = "unverified"
)

// Value is one attribute value: exactly one of Text or Base64, as everywhere
// else in Alder.
type Value struct {
	Text   string `json:"text,omitempty"`
	Base64 string `json:"base64,omitempty"`
}

// Attribute is an attribute description and its values, in order.
type Attribute struct {
	Name   string  `json:"name"`
	Values []Value `json:"values"`
}

// Mod is one modification. A delete with no values removes the attribute.
type Mod struct {
	Op     string  `json:"op"`
	Name   string  `json:"name"`
	Values []Value `json:"values,omitempty"`
}

// DataChange is an intended change to an ordinary entry.
type DataChange struct {
	DN   string `json:"dn"`
	Type string `json:"type"`
	// Attributes applies to add.
	Attributes []Attribute `json:"attributes,omitempty"`
	// Mods applies to modify.
	Mods []Mod `json:"mods,omitempty"`
	// NewRDN, DeleteOldRDN and NewSuperior apply to rename, which covers a
	// rename, a move, or both.
	NewRDN       string `json:"newRdn,omitempty"`
	DeleteOldRDN bool   `json:"deleteOldRdn,omitempty"`
	NewSuperior  string `json:"newSuperior,omitempty"`
}

// SchemaChange is an intended change to a schema definition.
//
// It names what the definition is, not where the server keeps it: the entry
// and attribute that hold it differ between products, and the target works
// that out when the package is validated.
type SchemaChange struct {
	Element string `json:"element"`
	Op      string `json:"op"`
	OID     string `json:"oid"`
	// Definition is the RFC 4512 text, canonicalised. Required to add or
	// replace; a delete names the OID only.
	Definition string `json:"definition,omitempty"`
}

// Item is one intended change.
type Item struct {
	// ID identifies the item inside this package. Dependencies name it.
	ID string `json:"id"`
	// Label is what the operator was doing when the change was made, for a
	// person reading the package. It carries no meaning.
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind"`
	// Intent applies to a data add. Absent means exact.
	Intent string `json:"intent,omitempty"`
	// Destructive is written out rather than inferred, so a reader sees what
	// removes something without interpreting the operation.
	Destructive bool          `json:"destructive"`
	Data        *DataChange   `json:"data,omitempty"`
	Schema      *SchemaChange `json:"schema,omitempty"`
	// DependsOn are the items that must be applied before this one.
	DependsOn []string `json:"dependsOn,omitempty"`
}

// Omitted is an intended change the package deliberately does not carry.
type Omitted struct {
	// Subject is what was left out, named without its values: a DN, or an OID.
	Subject string `json:"subject,omitempty"`
	Kind    string `json:"kind,omitempty"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail,omitempty"`
}

// Provenance is what a package records about where it came from. It is
// informational: a package proves nothing about its origin, and nothing here
// grants any authority.
type Provenance struct {
	AlderVersion   string   `json:"alderVersion,omitempty"`
	Method         string   `json:"method,omitempty"`
	Vendor         string   `json:"vendor,omitempty"`
	VendorVersion  string   `json:"vendorVersion,omitempty"`
	NamingContexts []string `json:"namingContexts,omitempty"`
	// SnapshotChecksum and SchemaSnapshotChecksum name the snapshots a
	// comparison-made package came from.
	SnapshotChecksum       string `json:"snapshotChecksum,omitempty"`
	SchemaSnapshotChecksum string `json:"schemaSnapshotChecksum,omitempty"`
}

// Assumptions are what the package expects of a target. They are checked at
// validation and are about directory semantics, never about a host.
type Assumptions struct {
	// NamingContexts the target must hold, as DNs.
	NamingContexts []string `json:"namingContexts,omitempty"`
	// SchemaOIDs the target's schema must already define.
	SchemaOIDs []string `json:"schemaOids,omitempty"`
	// ObjectClasses the target's schema must already define, by name.
	ObjectClasses []string `json:"objectClasses,omitempty"`
}

// Counts summarise a package. They are recomputed when it is read, and a
// document whose counts disagree with its content is refused.
type Counts struct {
	Changes     int `json:"changes"`
	Data        int `json:"data"`
	Schema      int `json:"schema"`
	Destructive int `json:"destructive"`
	Omitted     int `json:"omitted"`
}

// Package is a change package.
type Package struct {
	Format      string `json:"format"`
	Version     int    `json:"version"`
	ID          string `json:"id"`
	CreatedAt   string `json:"createdAt"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	Source      Provenance  `json:"source"`
	Assumptions Assumptions `json:"assumptions"`
	Counts      Counts      `json:"counts"`

	// Changes are in the package's canonical order: a dependency before what
	// depends on it, ties broken by identifier. The order a target applies
	// them in comes from the graph, not from this array.
	Changes []Item    `json:"changes"`
	Omitted []Omitted `json:"omitted"`

	Checksum string `json:"checksum,omitempty"`
}

// NewID returns an opaque identifier for a package: a random UUID (version 4).
// It is provenance, never authorisation.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("change package: generating an identifier: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// Build assembles a package, canonicalises it and computes its checksum.
//
// Items may arrive without identifiers, in which case they are numbered in the
// order given; a dependency naming an item is resolved against the identifiers
// as they end up. Nothing is dropped: a change that cannot be packaged must
// already be in Omitted, with a reason.
func Build(p *Package, created time.Time) (*Package, error) {
	out := *p
	out.Format, out.Version = Format, Version
	out.CreatedAt = created.UTC().Format(time.RFC3339)
	if out.ID == "" {
		id, err := NewID()
		if err != nil {
			return nil, err
		}
		out.ID = id
	}
	out.Changes = append([]Item(nil), p.Changes...)
	for i := range out.Changes {
		if out.Changes[i].ID == "" {
			out.Changes[i].ID = fmt.Sprintf("c%d", i+1)
		}
	}
	if err := out.canonicalise(); err != nil {
		return nil, err
	}
	if err := out.validate(); err != nil {
		return nil, err
	}
	sum, err := out.contentChecksum()
	if err != nil {
		return nil, err
	}
	out.Checksum = sum
	return &out, nil
}

// canonicalise puts the document in its canonical form: derived fields
// recomputed, lists deduplicated and sorted where they are sets, schema
// definitions rendered by the schema writer, and changes in dependency order.
func (p *Package) canonicalise() error {
	if p.Changes == nil {
		p.Changes = []Item{}
	}
	if p.Omitted == nil {
		p.Omitted = []Omitted{}
	}
	p.Source.NamingContexts = sortedUnique(p.Source.NamingContexts)
	p.Assumptions.NamingContexts = sortedUnique(p.Assumptions.NamingContexts)
	p.Assumptions.SchemaOIDs = sortedUnique(p.Assumptions.SchemaOIDs)
	p.Assumptions.ObjectClasses = sortedUnique(p.Assumptions.ObjectClasses)

	for i := range p.Changes {
		item := &p.Changes[i]
		item.DependsOn = sortedUnique(item.DependsOn)
		item.Destructive = destructive(*item)
		if item.Kind == KindData && item.Data != nil && item.Intent == IntentExact {
			item.Intent = ""
		}
		if item.Kind == KindSchema && item.Schema != nil && item.Schema.Definition != "" {
			canonical, oid, err := canonicalDefinition(item.Schema.Element, item.Schema.Definition)
			if err != nil {
				return invalid("change %s: %v", item.ID, err)
			}
			item.Schema.Definition = canonical
			if item.Schema.OID == "" {
				item.Schema.OID = oid
			}
		}
	}
	order, err := p.order()
	if err != nil {
		return err
	}
	byID := make(map[string]Item, len(p.Changes))
	for _, item := range p.Changes {
		byID[item.ID] = item
	}
	ordered := make([]Item, 0, len(order))
	for _, id := range order {
		ordered = append(ordered, byID[id])
	}
	p.Changes = ordered
	p.Counts = p.counts()
	return nil
}

func (p *Package) counts() Counts {
	c := Counts{Changes: len(p.Changes), Omitted: len(p.Omitted)}
	for _, item := range p.Changes {
		switch item.Kind {
		case KindData:
			c.Data++
		case KindSchema:
			c.Schema++
		}
		if item.Destructive {
			c.Destructive++
		}
	}
	return c
}

// destructive reports whether an item removes something.
func destructive(item Item) bool {
	switch {
	case item.Kind == KindData && item.Data != nil:
		return item.Data.Type == OpDelete
	case item.Kind == KindSchema && item.Schema != nil:
		return item.Schema.Op == SchemaDelete
	}
	return false
}

// Order returns the identifiers in the order a target should apply them: every
// dependency before what depends on it, ties broken by identifier so the same
// graph always produces the same order.
func (p *Package) Order() ([]string, error) { return p.order() }

func (p *Package) order() ([]string, error) {
	edges := map[string][]string{}
	indegree := map[string]int{}
	known := map[string]bool{}
	for _, item := range p.Changes {
		if known[item.ID] {
			return nil, invalid("two changes have the identifier %q", item.ID)
		}
		known[item.ID] = true
		indegree[item.ID] = 0
	}
	total := 0
	for _, item := range p.Changes {
		for _, needs := range item.DependsOn {
			if needs == item.ID {
				return nil, invalid("change %s depends on itself", item.ID)
			}
			if !known[needs] {
				return nil, invalid("change %s depends on %q, which this package does not contain", item.ID, needs)
			}
			edges[needs] = append(edges[needs], item.ID)
			indegree[item.ID]++
			total++
		}
	}
	if total > MaxDependencyEdges {
		return nil, &Error{Code: CodeTooLarge,
			Detail: fmt.Sprintf("%d dependencies, and a package holds at most %d", total, MaxDependencyEdges)}
	}

	var ready []string
	for id, d := range indegree {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	out := make([]string, 0, len(p.Changes))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)
		for _, next := range edges[id] {
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
			}
		}
		sort.Strings(ready)
	}
	if len(out) != len(p.Changes) {
		var stuck []string
		for _, item := range p.Changes {
			if indegree[item.ID] > 0 {
				stuck = append(stuck, item.ID)
			}
		}
		sort.Strings(stuck)
		return nil, invalid("the dependencies cycle, so there is no order to apply them in: %s", strings.Join(stuck, ", "))
	}
	return out, nil
}

// canonicalDefinition parses a definition and renders it the way the schema
// writer does, so the same definition written two ways is the same bytes.
// Provenance extensions are dropped: they say where a definition came from on
// one server, and the next one records its own.
func canonicalDefinition(element, definition string) (string, string, error) {
	switch element {
	case ElementAttributeType:
		at, err := schema.ParseAttributeType(definition)
		if err != nil {
			return "", "", fmt.Errorf("the attribute type does not parse: %w", err)
		}
		at.Extensions = withoutProvenance(at.Extensions)
		text, err := at.Definition()
		if err != nil {
			return "", "", fmt.Errorf("the attribute type cannot be written out: %w", err)
		}
		return text, at.OID, nil
	case ElementObjectClass:
		oc, err := schema.ParseObjectClass(definition)
		if err != nil {
			return "", "", fmt.Errorf("the object class does not parse: %w", err)
		}
		oc.Extensions = withoutProvenance(oc.Extensions)
		text, err := oc.Definition()
		if err != nil {
			return "", "", fmt.Errorf("the object class cannot be written out: %w", err)
		}
		return text, oc.OID, nil
	}
	return "", "", fmt.Errorf("%q is not a kind of schema definition", element)
}

var provenanceExtensions = map[string]bool{"X-ORIGIN": true, "X-SCHEMA-FILE": true}

func withoutProvenance(ext schema.Extensions) schema.Extensions {
	if len(ext) == 0 {
		return nil
	}
	out := schema.Extensions{}
	for k, v := range ext {
		if provenanceExtensions[strings.ToUpper(k)] {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// canonicalContent is what the checksum covers: the document without its
// createdAt and without the checksum itself, so the same intent captured twice
// has the same checksum.
func (p *Package) canonicalContent() ([]byte, error) {
	content := *p
	content.CreatedAt = ""
	content.Checksum = ""
	return json.Marshal(content)
}

func (p *Package) contentChecksum() (string, error) {
	content, err := p.canonicalContent()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Encode writes the package as its canonical document: indented JSON, so a
// package committed to a repository diffs readably.
func Encode(w io.Writer, p *Package) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

// Decode reads and validates a package document. It executes nothing: a
// package is untrusted input, read exactly as strictly as a snapshot.
func Decode(data []byte) (*Package, Integrity, error) {
	var probe struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil || probe.Format != Format {
		return nil, "", &Error{Code: CodeNotPackage,
			Detail: "the document is not an Alder change package (no \"format\": \"" + Format + "\")"}
	}
	if probe.Version > Version {
		return nil, "", &Error{Code: CodeUnsupportedVersion,
			Detail: fmt.Sprintf("package version %d is newer than this Alder reads (%d); use a newer Alder", probe.Version, Version)}
	}
	if probe.Version < 1 {
		return nil, "", &Error{Code: CodeUnsupportedVersion, Detail: fmt.Sprintf("package version %d does not exist", probe.Version)}
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var p Package
	if err := dec.Decode(&p); err != nil {
		return nil, "", &Error{Code: CodeInvalid, Detail: err.Error()}
	}
	if dec.More() {
		return nil, "", &Error{Code: CodeInvalid, Detail: "the document continues after the package"}
	}

	claimedCounts, claimed := p.Counts, p.Checksum
	if err := p.validate(); err != nil {
		return nil, "", err
	}
	if err := p.canonicalise(); err != nil {
		return nil, "", err
	}
	if p.Counts != claimedCounts {
		return nil, "", invalid("the counts do not match what the package holds")
	}
	sum, err := p.contentChecksum()
	if err != nil {
		return nil, "", err
	}
	p.Checksum = sum
	if claimed == "" {
		return &p, IntegrityUnverified, nil
	}
	if claimed != sum {
		return nil, "", &Error{Code: CodeChecksumMismatch,
			Detail: "the content does not match its checksum: the file was corrupted or edited after it was made"}
	}
	return &p, IntegrityVerified, nil
}

// validate checks everything about a package that can be checked without a
// directory: its shape, its identifiers, its dependency graph, its DNs and
// definitions, and that it carries no secret.
func (p *Package) validate() error {
	if p.Format != Format {
		return invalid("format is %q, not %q", p.Format, Format)
	}
	if p.Version != Version {
		return &Error{Code: CodeUnsupportedVersion, Detail: fmt.Sprintf("version %d is not version %d", p.Version, Version)}
	}
	if p.ID == "" {
		return invalid("the package has no identifier")
	}
	if err := text("id", p.ID, MaxIDBytes); err != nil {
		return err
	}
	if p.CreatedAt != "" {
		if _, err := time.Parse(time.RFC3339, p.CreatedAt); err != nil {
			return invalid("createdAt %q is not an RFC 3339 time", p.CreatedAt)
		}
	}
	for name, value := range map[string]string{"title": p.Title, "description": p.Description} {
		if err := text(name, value, MaxTextBytes); err != nil {
			return err
		}
	}
	if len(p.Changes) > MaxChanges {
		return &Error{Code: CodeTooLarge,
			Detail: fmt.Sprintf("%d changes, and a package holds at most %d", len(p.Changes), MaxChanges)}
	}
	if err := p.validateSource(); err != nil {
		return err
	}
	for _, o := range p.Omitted {
		if o.Reason == "" {
			return invalid("an omitted change has no reason")
		}
		for name, value := range map[string]string{"subject": o.Subject, "reason": o.Reason, "detail": o.Detail} {
			if err := text("omitted "+name, value, MaxTextBytes); err != nil {
				return err
			}
		}
	}

	seen := map[string]bool{}
	for _, item := range p.Changes {
		if err := validateItem(item); err != nil {
			return err
		}
		if seen[item.ID] {
			return invalid("two changes have the identifier %q", item.ID)
		}
		seen[item.ID] = true
		if len(item.DependsOn) > MaxDependenciesPerItem {
			return &Error{Code: CodeTooLarge,
				Detail: fmt.Sprintf("change %s names %d dependencies, and one change may name at most %d",
					item.ID, len(item.DependsOn), MaxDependenciesPerItem)}
		}
	}
	// The graph itself: missing identifiers, self-dependencies and cycles.
	if _, err := p.order(); err != nil {
		return err
	}
	return nil
}

func (p *Package) validateSource() error {
	for _, list := range [][]string{p.Source.NamingContexts, p.Assumptions.NamingContexts} {
		for _, context := range list {
			if _, err := dn.Parse(context); err != nil {
				return invalid("naming context %q is not a DN: %v", context, err)
			}
		}
	}
	for _, oid := range p.Assumptions.SchemaOIDs {
		if err := text("assumed OID", oid, MaxTextBytes); err != nil {
			return err
		}
	}
	for _, name := range p.Assumptions.ObjectClasses {
		if err := dn.ValidateType(name); err != nil {
			return invalid("assumed object class %q is not a name: %v", name, err)
		}
	}
	switch p.Source.Method {
	case "", MethodChangeset, MethodDataDiff, MethodSchemaDiff, MethodCLI:
	default:
		return invalid("%q is not a way a package is made", p.Source.Method)
	}
	return nil
}

func validateItem(item Item) error {
	if item.ID == "" {
		return invalid("a change has no identifier")
	}
	if err := text("change id", item.ID, MaxIDBytes); err != nil {
		return err
	}
	if err := text("label", item.Label, MaxTextBytes); err != nil {
		return err
	}
	switch item.Kind {
	case KindData:
		if item.Data == nil || item.Schema != nil {
			return invalid("change %s is a data change and must carry exactly one data change", item.ID)
		}
		if err := validateData(item); err != nil {
			return err
		}
	case KindSchema:
		if item.Schema == nil || item.Data != nil {
			return invalid("change %s is a schema change and must carry exactly one schema change", item.ID)
		}
		if err := validateSchema(item); err != nil {
			return err
		}
	default:
		return invalid("change %s is of kind %q, which is not data or schema", item.ID, item.Kind)
	}
	if item.Destructive != destructive(item) {
		return invalid("change %s is marked destructive %v, and it is not what the operation does", item.ID, item.Destructive)
	}
	return nil
}

func validateData(item Item) error {
	d := item.Data
	if _, err := dn.Parse(d.DN); err != nil {
		return invalid("change %s: dn %q is not a DN: %v", item.ID, d.DN, err)
	}
	if err := text("dn", d.DN, MaxTextBytes); err != nil {
		return err
	}
	switch d.Type {
	case OpAdd:
		if len(d.Attributes) == 0 {
			return invalid("change %s adds an entry and names no attributes", item.ID)
		}
		if len(d.Mods) > 0 || d.NewRDN != "" || d.NewSuperior != "" {
			return invalid("change %s adds an entry and carries fields of another operation", item.ID)
		}
		for _, a := range d.Attributes {
			if err := validateAttribute(item.ID, a.Name, a.Values); err != nil {
				return err
			}
		}
	case OpModify:
		if len(d.Mods) == 0 {
			return invalid("change %s modifies an entry and names no modifications", item.ID)
		}
		if len(d.Attributes) > 0 || d.NewRDN != "" || d.NewSuperior != "" {
			return invalid("change %s modifies an entry and carries fields of another operation", item.ID)
		}
		for _, m := range d.Mods {
			switch m.Op {
			case string(directory.ModAdd), string(directory.ModDelete), string(directory.ModReplace):
			default:
				return invalid("change %s: %q is not a modification operation", item.ID, m.Op)
			}
			if err := validateAttribute(item.ID, m.Name, m.Values); err != nil {
				return err
			}
		}
	case OpDelete:
		if len(d.Attributes) > 0 || len(d.Mods) > 0 || d.NewRDN != "" || d.NewSuperior != "" {
			return invalid("change %s deletes an entry and carries fields of another operation", item.ID)
		}
	case OpRename:
		if d.NewRDN == "" && d.NewSuperior == "" {
			return invalid("change %s renames an entry and names neither a new RDN nor a new parent", item.ID)
		}
		if len(d.Attributes) > 0 || len(d.Mods) > 0 {
			return invalid("change %s renames an entry and carries fields of another operation", item.ID)
		}
		if d.NewRDN != "" {
			if _, err := dn.Parse(d.NewRDN); err != nil {
				return invalid("change %s: newRdn %q is not an RDN: %v", item.ID, d.NewRDN, err)
			}
		}
		if d.NewSuperior != "" {
			if _, err := dn.Parse(d.NewSuperior); err != nil {
				return invalid("change %s: newSuperior %q is not a DN: %v", item.ID, d.NewSuperior, err)
			}
		}
	default:
		return invalid("change %s is of type %q, which a package does not carry", item.ID, d.Type)
	}
	switch item.Intent {
	case "", IntentExact:
	case IntentDesired:
		if d.Type != OpAdd {
			return invalid("change %s asks for desired state, which only an add may do", item.ID)
		}
	default:
		return invalid("change %s: %q is not an intent", item.ID, item.Intent)
	}
	return nil
}

// validateAttribute checks one attribute's name and values, and refuses a
// secret. A package that carried a password -- in any form, including a hash
// an operator pasted in -- would replay it into every environment it reached.
func validateAttribute(id, name string, values []Value) error {
	if err := dn.ValidateType(name); err != nil {
		return invalid("change %s: %q is not an attribute description: %v", id, name, err)
	}
	if schema.IsSensitive(name) {
		return invalid("change %s names %s, and a package never carries a secret", id, name)
	}
	for _, v := range values {
		switch {
		case v.Text != "" && v.Base64 != "":
			return invalid("change %s: a value of %s is both text and base64", id, name)
		case v.Base64 != "":
			raw, err := base64.StdEncoding.DecodeString(v.Base64)
			if err != nil {
				return invalid("change %s: a value of %s is not base64: %v", id, name, err)
			}
			if len(raw) > MaxTextBytes {
				return invalid("change %s: a value of %s is longer than %d bytes", id, name, MaxTextBytes)
			}
		default:
			if err := text("a value of "+name, v.Text, MaxTextBytes); err != nil {
				return invalid("change %s: %v", id, err)
			}
		}
	}
	return nil
}

func validateSchema(item Item) error {
	s := item.Schema
	switch s.Element {
	case ElementAttributeType, ElementObjectClass:
	default:
		return invalid("change %s: %q is not a kind of schema definition", item.ID, s.Element)
	}
	switch s.Op {
	case SchemaAdd, SchemaReplace:
		if s.Definition == "" {
			return invalid("change %s %ss a definition and carries none", item.ID, s.Op)
		}
	case SchemaDelete:
		if s.Definition != "" {
			return invalid("change %s deletes a definition and carries one", item.ID)
		}
	default:
		return invalid("change %s: %q is not a schema operation", item.ID, s.Op)
	}
	if s.OID == "" {
		return invalid("change %s names no OID", item.ID)
	}
	if err := text("oid", s.OID, MaxTextBytes); err != nil {
		return err
	}
	if err := text("definition", s.Definition, MaxTextBytes); err != nil {
		return err
	}
	if s.Definition != "" {
		_, oid, err := canonicalDefinition(s.Element, s.Definition)
		if err != nil {
			return invalid("change %s: %v", item.ID, err)
		}
		if !strings.EqualFold(oid, s.OID) {
			return invalid("change %s names OID %s and its definition is of %s", item.ID, s.OID, oid)
		}
	}
	return nil
}

// text bounds a string and requires it to be UTF-8. Control characters are
// left as they are: they are directory content, and every place that shows one
// escapes it. Refusing them here would refuse definitions a server published.
func text(what, value string, max int) error {
	if len(value) > max {
		return invalid("%s is longer than %d bytes", what, max)
	}
	if !utf8.ValidString(value) {
		return invalid("%s is not valid UTF-8", what)
	}
	return nil
}

// ErrNotPortable reports a change that cannot be carried in a package.
var ErrNotPortable = errors.New("change package: this change cannot be packaged")
