package outline

import (
	"encoding/base64"
	"fmt"
	"io"
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
	var b strings.Builder
	_ = WriteYAML(&b, entries, opts)
	return b.String()
}

// WriteYAML draws the entries as nested YAML, straight into w.
//
// Bounded rather than streamed, for WriteTo's reason: a tree cannot be nested
// until its last entry has arrived. What it avoids is holding the finished
// document as well as the tree that produced it.
func WriteYAML(w io.Writer, entries []YAMLEntry, opts Options) error {
	b := newTreeBuilder(len(entries))
	for _, e := range entries {
		// The attributes ride on the node, so there is no second index from DN
		// back to the entry that carried them.
		b.add(e.DN, e.Structural, e.Attributes)
	}
	roots := b.finish()

	ew := &errWriter{w: w}
	writeYAMLHeader(ew, opts, countNodes(roots))
	_, _ = io.WriteString(ew, "tree:\n")
	for _, r := range roots {
		writeYAMLNode(ew, r, "  ")
	}
	return ew.err
}

// writeYAMLNode writes one node and everything below it.
//
// Written out with io.WriteString rather than Fprintf. That is more lines for
// the same document, and it is here rather than everywhere because this runs
// once per entry and once per value: passing a string to Fprintf puts it in an
// interface, which allocates, and at ten thousand entries that was a fifth of
// what the export allocated. The header, which runs once, still uses Fprintf.
func writeYAMLNode(b io.Writer, n *node, indent string) {
	_, _ = io.WriteString(b, indent)
	_, _ = io.WriteString(b, "- dn: ")
	_ = yamlenc.WriteScalar(b, n.label)
	_, _ = io.WriteString(b, "\n")

	// The RDN is repeated because it is what an editor shows on the folded
	// line, and "uid=alice" is a more useful summary of a collapsed subtree
	// than the full DN scrolled off the right.
	_, _ = io.WriteString(b, indent)
	_, _ = io.WriteString(b, "  rdn: ")
	_ = yamlenc.WriteScalar(b, n.rdn)
	_, _ = io.WriteString(b, "\n")

	if n.structural != "" {
		_, _ = io.WriteString(b, indent)
		_, _ = io.WriteString(b, "  class: ")
		_ = yamlenc.WriteScalar(b, n.structural)
		_, _ = io.WriteString(b, "\n")
	}
	if n.descendants > 0 {
		_, _ = fmt.Fprintf(b, "%s  below: %d\n", indent, n.descendants)
	}

	if len(n.attrs) > 0 {
		_, _ = io.WriteString(b, indent)
		_, _ = io.WriteString(b, "  attributes:\n")
		deeper := indent + "    "
		for _, a := range n.attrs {
			writeYAMLAttribute(b, a, deeper)
		}
	}

	if len(n.children) > 0 {
		_, _ = io.WriteString(b, indent)
		_, _ = io.WriteString(b, "  children:\n")
		deeper := indent + "    "
		for _, c := range n.children {
			writeYAMLNode(b, c, deeper)
		}
	}
}

func writeYAMLAttribute(b io.Writer, a YAMLAttribute, indent string) {
	_, _ = io.WriteString(b, indent)
	_ = yamlenc.WriteScalar(b, a.Name)
	_, _ = io.WriteString(b, ":\n")
	for _, v := range a.Values {
		if isPrintableUTF8(v) {
			_, _ = io.WriteString(b, indent)
			_, _ = io.WriteString(b, "  - ")
			// The bytes go straight in. string(v) here would escape to the
			// heap, which is one allocation per value written.
			_ = yamlenc.WriteScalarBytes(b, v)
			_, _ = io.WriteString(b, "\n")
			continue
		}
		// A value that is not text is base64, tagged as YAML's own binary type
		// rather than passed off as a string. An editor will not pretend it is
		// readable and a reader will not have to guess.
		_, _ = fmt.Fprintf(b, "%s  - !!binary %s\n", indent, yamlenc.Scalar(base64.StdEncoding.EncodeToString(v)))
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

func writeYAMLHeader(b io.Writer, opts Options, entries int) {
	_, _ = io.WriteString(b, "# The directory as Alder read it, nested so an editor can fold it.\n")
	_, _ = io.WriteString(b, "#\n")
	_, _ = io.WriteString(b, "# Every attribute is a list, even where the schema says one value: an LDAP\n")
	_, _ = io.WriteString(b, "# attribute holds a set, and a shape that changed with the data would make\n")
	_, _ = io.WriteString(b, "# a second value look like a type change in the diff.\n")
	_, _ = io.WriteString(b, "#\n")
	_, _ = io.WriteString(b, "# This is for reading. Alder imports LDIF, which is the format with a\n")
	_, _ = io.WriteString(b, "# specification and a changetype; nothing reads this back.\n")
	_, _ = io.WriteString(b, "#\n")
	if opts.Base != "" {
		_, _ = fmt.Fprintf(b, "# base:  %s\n", opts.Base)
	}
	if opts.Scope != "" {
		_, _ = fmt.Fprintf(b, "# scope: %s\n", opts.Scope)
	}
	if opts.Filter != "" {
		_, _ = fmt.Fprintf(b, "# filter: %s\n", opts.Filter)
	}
	if opts.Truncated {
		_, _ = fmt.Fprintf(b, "#\n# WARNING: the search stopped at %d entries, so this is part of the\n", opts.Limit)
		_, _ = io.WriteString(b, "# subtree and not the whole of it.\n")
	}
	_, _ = io.WriteString(b, "---\n")
	_, _ = fmt.Fprintf(b, "base: %s\n", yamlenc.Scalar(opts.Base))
	_, _ = fmt.Fprintf(b, "scope: %s\n", yamlenc.Scalar(opts.Scope))
	_, _ = fmt.Fprintf(b, "entries: %d\n", entries)
	_, _ = fmt.Fprintf(b, "truncated: %t\n", opts.Truncated)
}
