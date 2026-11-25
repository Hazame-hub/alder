// Package entrytemplate holds entry templates: the shape of a new user or
// group, written down once, so that creating one is filling in a name rather
// than knowing which object classes and attributes a person needs.
//
// A template never writes. Rendering one produces an ordinary add change,
// which goes through the plan, the exact LDIF and the confirmation every other
// write goes through. The one thing a template adds that the creation form
// cannot is a number taken from the directory: the next free uidNumber or
// gidNumber, read by a search when the template is rendered and shown in the
// plan like any other value. It is a suggestion computed at that moment, not a
// reservation; DECISIONS.md (2026-11-25) says why that is enough and where it
// is not.
//
// Templates are configuration. The built-in ones ship with Alder, an operator
// adds more with `alder serve --templates DIR`, and nothing is stored: a
// template file is read at start and never written.
package entrytemplate

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The views a template can be offered from, as GET /views names them.
const (
	ViewUsers               = "users"
	ViewGroups              = "groups"
	ViewOrganizationalUnits = "organizationalUnits"
)

// FieldKind says what a field accepts.
type FieldKind string

const (
	// FieldText is any text.
	FieldText FieldKind = "text"
	// FieldNumber is a non-negative integer, as uidNumber and gidNumber are.
	FieldNumber FieldKind = "number"
	// FieldDN is a distinguished name; the form offers the entry picker.
	FieldDN FieldKind = "dn"
)

// Field is something the person creating the entry types.
//
// A field is a variable, not an attribute: it reaches the entry only through
// the attributes that name it, as {name}. Writing that down is what lets a
// reviewer of the template see exactly what each typed value becomes.
type Field struct {
	Name     string    `yaml:"name"`
	Label    string    `yaml:"label"`
	Help     string    `yaml:"help"`
	Required bool      `yaml:"required"`
	Default  string    `yaml:"default"`
	Kind     FieldKind `yaml:"kind"`
}

// Range bounds an allocated number. To is inclusive.
type Range struct {
	From int64 `yaml:"from"`
	To   int64 `yaml:"to"`
	// Base is where to look for numbers already in use. Empty means every
	// naming context the server publishes, which is right for uidNumber and
	// gidNumber: they are unique across the directory, not per branch.
	Base string `yaml:"base"`
}

// Template is one shape of entry.
type Template struct {
	Name        string `yaml:"name"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	// View is where "New ..." offers it: users, groups or organizationalUnits.
	View string `yaml:"view"`
	// Parent is the entry new ones are created under by default. Empty asks.
	Parent        string   `yaml:"parent"`
	RDN           string   `yaml:"rdn"`
	ObjectClasses []string `yaml:"objectClasses"`
	Fields        []Field  `yaml:"fields"`
	// Attributes are the values the entry gets, each of which may name fields
	// and allocated numbers as {name}. A value that comes out empty -- an
	// optional field left blank -- is left out, and so is an attribute left
	// with no values.
	Attributes map[string][]string `yaml:"attributes"`
	// Allocate names attributes whose value is the next free number in a
	// range, found when the template is rendered.
	Allocate map[string]Range `yaml:"allocate"`

	// Source is where the template came from: "built-in", or a file path.
	Source string `yaml:"-"`
	// Hidden marks a built-in that is offered only where the server's schema
	// accepts it, so a server gets the variant that fits it and not the other.
	Hidden bool `yaml:"-"`
}

var (
	namePattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	variablePattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
	placeholderPattern = regexp.MustCompile(`\{([^{}]*)\}`)
)

// maxAllocated is the highest number a range may reach: uidNumber and
// gidNumber are signed 32-bit on every system that reads them.
const maxAllocated = math.MaxInt32

// Validate checks a template on its own, without a directory: what can be
// wrong with the file whatever server it is used against.
func (t *Template) Validate() error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if !namePattern.MatchString(t.Name) {
		add("name %q must be lower-case letters, digits and dashes", t.Name)
	}
	switch t.View {
	case ViewUsers, ViewGroups, ViewOrganizationalUnits:
	default:
		add("view %q must be users, groups or organizationalUnits", t.View)
	}
	if strings.TrimSpace(t.Title) == "" {
		add("title is required")
	}
	if len(t.ObjectClasses) == 0 {
		add("objectClasses is required")
	}
	if !variablePattern.MatchString(t.RDN) {
		add("rdn %q must be an attribute name", t.RDN)
	}

	variables := map[string]bool{}
	for i, f := range t.Fields {
		if !variablePattern.MatchString(f.Name) {
			add("field %d: name %q must be letters, digits and dashes", i+1, f.Name)
			continue
		}
		if variables[strings.ToLower(f.Name)] {
			add("field %q is listed twice", f.Name)
		}
		variables[strings.ToLower(f.Name)] = true
		switch f.Kind {
		case "", FieldText, FieldNumber, FieldDN:
		default:
			add("field %q: kind %q must be text, number or dn", f.Name, f.Kind)
		}
		if f.Kind == FieldNumber && f.Default != "" {
			if _, err := strconv.ParseUint(f.Default, 10, 31); err != nil {
				add("field %q: default %q is not a number", f.Name, f.Default)
			}
		}
	}
	for attr, r := range t.Allocate {
		if !variablePattern.MatchString(attr) {
			add("allocate: %q must be an attribute name", attr)
			continue
		}
		if variables[strings.ToLower(attr)] {
			add("allocate: %q is also a field", attr)
		}
		variables[strings.ToLower(attr)] = true
		if r.From < 0 || r.To < r.From || r.To > maxAllocated {
			add("allocate: %s needs 0 <= from <= to <= %d", attr, maxAllocated)
		}
		if _, set := lookupFold(t.Attributes, attr); set {
			add("allocate: %s is also under attributes; an allocated number is set by allocation alone", attr)
		}
	}

	rdnSet := false
	for attr, values := range t.Attributes {
		if !variablePattern.MatchString(attr) {
			add("attributes: %q must be an attribute name", attr)
		}
		if strings.EqualFold(attr, "objectClass") {
			add("attributes: objectClass is set by objectClasses")
		}
		if strings.EqualFold(attr, t.RDN) {
			rdnSet = true
		}
		for _, v := range values {
			for _, m := range placeholderPattern.FindAllStringSubmatch(v, -1) {
				if !variables[strings.ToLower(m[1])] {
					add("attributes: %s names {%s}, which is not a field or an allocated number", attr, m[1])
				}
			}
		}
	}
	if _, allocated := lookupFold(t.Allocate, t.RDN); allocated {
		rdnSet = true
	}
	if !rdnSet && variablePattern.MatchString(t.RDN) {
		add("rdn %s is not among the attributes, so a new entry would have no name", t.RDN)
	}

	if len(problems) > 0 {
		return fmt.Errorf("template %q: %s", t.Name, strings.Join(problems, "; "))
	}
	return nil
}

// FieldKindOf is a field's kind, text when it does not say.
func (f Field) FieldKindOf() FieldKind {
	if f.Kind == "" {
		return FieldText
	}
	return f.Kind
}

// AllocatedNames are the allocated attributes, sorted.
func (t *Template) AllocatedNames() []string {
	names := make([]string, 0, len(t.Allocate))
	for n := range t.Allocate {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// AttributeNames are the attributes the template sets, allocated ones
// included, sorted.
func (t *Template) AttributeNames() []string {
	names := make([]string, 0, len(t.Attributes)+len(t.Allocate))
	for n := range t.Attributes {
		names = append(names, n)
	}
	names = append(names, t.AllocatedNames()...)
	sort.Strings(names)
	return names
}

// ErrInput is a value the person typed that the template cannot use.
var ErrInput = errors.New("entrytemplate: unusable input")

// Value is one attribute of a rendered entry.
type Value struct {
	Name   string
	Values []string
}

// Rendered is a template filled in: the new entry's RDN attribute and value,
// and every attribute in a stable order (objectClass first).
type Rendered struct {
	RDNType    string
	RDNValue   string
	Attributes []Value
}

// Render fills a template with what was typed and what was allocated.
//
// input holds the typed fields; allocated the numbers already found for each
// allocated attribute. Nothing here reads the directory.
func (t *Template) Render(input map[string]string, allocated map[string]int64) (*Rendered, error) {
	vars := map[string]string{}
	var problems []string
	for _, f := range t.Fields {
		v := strings.TrimSpace(lookupInput(input, f.Name))
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			problems = append(problems, fmt.Sprintf("%s is required", f.labelOrName()))
		}
		if v != "" && f.FieldKindOf() == FieldNumber {
			if _, err := strconv.ParseUint(v, 10, 31); err != nil {
				problems = append(problems, fmt.Sprintf("%s must be a whole number", f.labelOrName()))
			}
		}
		if strings.ContainsAny(v, "\x00\r\n") {
			problems = append(problems, fmt.Sprintf("%s must be one line", f.labelOrName()))
		}
		vars[strings.ToLower(f.Name)] = v
	}
	for k := range input {
		if !t.hasField(k) {
			problems = append(problems, fmt.Sprintf("%q is not a field of this template", k))
		}
	}
	for _, name := range t.AllocatedNames() {
		n, ok := allocated[name]
		if !ok {
			return nil, fmt.Errorf("entrytemplate: %s was not allocated", name)
		}
		vars[strings.ToLower(name)] = strconv.FormatInt(n, 10)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInput, strings.Join(problems, "; "))
	}

	out := &Rendered{RDNType: t.RDN}
	classes := []string{"top"}
	for _, c := range t.ObjectClasses {
		if !containsFold(classes, c) {
			classes = append(classes, c)
		}
	}
	out.Attributes = append(out.Attributes, Value{Name: "objectClass", Values: classes})
	for _, name := range t.AttributeNames() {
		var values []string
		if src, ok := t.Attributes[name]; ok {
			for _, raw := range src {
				v := strings.TrimSpace(placeholderPattern.ReplaceAllStringFunc(raw, func(m string) string {
					return vars[strings.ToLower(m[1:len(m)-1])]
				}))
				// "{givenName} {sn}" with no given name is "Doe", not " Doe".
				v = strings.Join(strings.Fields(v), " ")
				if v != "" && !containsFold(values, v) {
					values = append(values, v)
				}
			}
		} else {
			values = []string{vars[strings.ToLower(name)]}
		}
		if len(values) == 0 {
			continue
		}
		out.Attributes = append(out.Attributes, Value{Name: name, Values: values})
		if strings.EqualFold(name, t.RDN) {
			out.RDNValue = values[0]
		}
	}
	if out.RDNValue == "" {
		return nil, fmt.Errorf("%w: the new entry's name (%s) came out empty", ErrInput, t.RDN)
	}
	return out, nil
}

func (t *Template) hasField(name string) bool {
	for _, f := range t.Fields {
		if strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}

func (f Field) labelOrName() string {
	if f.Label != "" {
		return f.Label
	}
	return f.Name
}

func lookupInput(input map[string]string, name string) string {
	for k, v := range input {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func lookupFold[V any](m map[string]V, key string) (V, bool) {
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	var zero V
	return zero, false
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
