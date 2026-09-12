package outline

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/yamlenc"
)

// YAMLEntry is one entry and what it holds.
//
// The outline shows where entries sit; this shows that and what is in them,
// which is what makes the YAML worth having next to the LDIF: an editor folds
// it, so a subtree can be collapsed to one line and opened where it matters.
type YAMLEntry struct {
	DN         dn.DN
	Structural string
	Attributes []YAMLAttribute
}

// YAMLAttribute is one attribute and its values, in the order the server
// returned them.
type YAMLAttribute struct {
	Name   string
	Values [][]byte
}

// RenderYAML draws the entries as nested YAML.
//
// Every value is a list, even where the schema says an attribute is
// single-valued. An LDAP attribute holds a set of values, and a renderer that
// collapsed the common case would produce a document whose shape depended on
// the data in it -- so anything reading it would need both paths, and anyone
// diffing two of them would see a type change where a second value appeared.
//
// It is not LDIF, and it is not something to apply. Alder imports LDIF, which
// is the format with a specification and a changetype; this is for looking at.
func RenderYAML(entries []YAMLEntry, opts Options) string {
	nodes := make([]Entry, 0, len(entries))
	byKey := make(map[string]YAMLEntry, len(entries))
	for _, e := range entries {
		nodes = append(nodes, Entry{DN: e.DN, Structural: e.Structural})
		byKey[strings.ToLower(e.DN.String())] = e
	}
	roots := build(nodes)

	var b strings.Builder
	writeYAMLHeader(&b, opts, len(byKey))
	b.WriteString("tree:\n")
	for _, r := range roots {
		writeYAMLNode(&b, r, byKey, "  ")
	}
	return b.String()
}

func writeYAMLNode(b *strings.Builder, n *node, byKey map[string]YAMLEntry, indent string) {
	entry := byKey[strings.ToLower(n.label)]

	fmt.Fprintf(b, "%s- dn: %s\n", indent, yamlenc.Scalar(n.label))
	// The RDN is repeated because it is what an editor shows on the folded
	// line, and "uid=alice" is a more useful summary of a collapsed subtree
	// than the full DN scrolled off the right.
	fmt.Fprintf(b, "%s  rdn: %s\n", indent, yamlenc.Scalar(rdnOf(n.label)))
	if n.structural != "" {
		fmt.Fprintf(b, "%s  class: %s\n", indent, yamlenc.Scalar(n.structural))
	}
	if n.descendants > 0 {
		fmt.Fprintf(b, "%s  below: %d\n", indent, n.descendants)
	}

	if len(entry.Attributes) > 0 {
		fmt.Fprintf(b, "%s  attributes:\n", indent)
		for _, a := range entry.Attributes {
			writeYAMLAttribute(b, a, indent+"    ")
		}
	}

	if len(n.children) > 0 {
		fmt.Fprintf(b, "%s  children:\n", indent)
		for _, c := range n.children {
			writeYAMLNode(b, c, byKey, indent+"    ")
		}
	}
}

func writeYAMLAttribute(b *strings.Builder, a YAMLAttribute, indent string) {
	fmt.Fprintf(b, "%s%s:\n", indent, yamlenc.Scalar(a.Name))
	for _, v := range a.Values {
		if isPrintableUTF8(v) {
			fmt.Fprintf(b, "%s  - %s\n", indent, yamlenc.Scalar(string(v)))
			continue
		}
		// A value that is not text is base64, tagged as YAML's own binary type
		// rather than passed off as a string. An editor will not pretend it is
		// readable and a reader will not have to guess.
		fmt.Fprintf(b, "%s  - !!binary %s\n", indent, yamlenc.Scalar(base64.StdEncoding.EncodeToString(v)))
	}
}

// isPrintableUTF8 reports whether a value can be written as a YAML string
// without losing what it is.
func isPrintableUTF8(v []byte) bool {
	if !utf8.Valid(v) {
		return false
	}
	for _, r := range string(v) {
		// The escapes cover tab, newline and carriage return; anything else in
		// the control range is a sign the value is not text at all.
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
}

// build turns a flat set of entries into roots with children, counted and
// sorted. Shared with the outline, so the two renderings never disagree about
// the shape they are drawing.
func build(entries []Entry) []*node {
	byKey := make(map[string]*node, len(entries))
	order := make([]string, 0, len(entries))
	for _, e := range entries {
		key := strings.ToLower(e.DN.String())
		if _, seen := byKey[key]; seen {
			continue
		}
		byKey[key] = &node{label: e.DN.String(), structural: e.Structural}
		order = append(order, key)
	}

	var roots []*node
	for _, key := range order {
		n := byKey[key]
		parent := parentKeyOf(n.label)
		if p, ok := byKey[parent]; ok && parent != key {
			p.children = append(p.children, n)
			continue
		}
		roots = append(roots, n)
	}
	for _, r := range roots {
		count(r)
	}
	sortNodes(roots)
	return roots
}

func writeYAMLHeader(b *strings.Builder, opts Options, entries int) {
	b.WriteString("# The directory as Alder read it, nested so an editor can fold it.\n")
	b.WriteString("#\n")
	b.WriteString("# Every attribute is a list, even where the schema says one value: an LDAP\n")
	b.WriteString("# attribute holds a set, and a shape that changed with the data would make\n")
	b.WriteString("# a second value look like a type change in the diff.\n")
	b.WriteString("#\n")
	b.WriteString("# This is for reading. Alder imports LDIF, which is the format with a\n")
	b.WriteString("# specification and a changetype; nothing reads this back.\n")
	b.WriteString("#\n")
	if opts.Base != "" {
		fmt.Fprintf(b, "# base:  %s\n", opts.Base)
	}
	if opts.Scope != "" {
		fmt.Fprintf(b, "# scope: %s\n", opts.Scope)
	}
	if opts.Filter != "" {
		fmt.Fprintf(b, "# filter: %s\n", opts.Filter)
	}
	if opts.Truncated {
		fmt.Fprintf(b, "#\n# WARNING: the search stopped at %d entries, so this is part of the\n", opts.Limit)
		b.WriteString("# subtree and not the whole of it.\n")
	}
	b.WriteString("---\n")
	fmt.Fprintf(b, "base: %s\n", yamlenc.Scalar(opts.Base))
	fmt.Fprintf(b, "scope: %s\n", yamlenc.Scalar(opts.Scope))
	fmt.Fprintf(b, "entries: %d\n", entries)
	fmt.Fprintf(b, "truncated: %t\n", opts.Truncated)
}
