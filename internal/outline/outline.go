// Package outline renders a set of entries as the tree they form.
//
// It exists because an LDIF export is a flat list, and a flat list of three
// hundred records does not tell you the shape of what you exported. An operator
// testing Alder put it plainly: "having LDIF is quite flat when plaintext,
// maybe have something that outputs the file as a hierarchy".
//
// It is deliberately not LDIF, and could not be. RFC 2849 gives a leading space
// its own meaning -- it continues the line above -- so indenting records to show
// depth would produce a document that no longer parses as the thing it came
// from. Rather than a format that is almost LDIF and silently broken, this is
// plainly something else: a picture of the tree, which says so at the top and
// cannot be mistaken for something you apply.
package outline

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hazame-hub/alder/internal/dn"
)

// Entry is one entry's place in the tree.
//
// Only the DN is needed to build the shape; Structural is carried because
// "what kind of thing is this" is most of what a shape view is asked, and the
// caller has already read the object classes.
type Entry struct {
	DN         dn.DN
	Structural string
}

// Options describe the export the outline summarises, so the file says what it
// is a picture of.
type Options struct {
	Base   string
	Scope  string
	Filter string
	// Truncated reports that the search stopped early. A partial tree that does
	// not say so reads later as the whole of the subtree.
	Truncated bool
	// Limit is the bound the search stopped at, for the warning.
	Limit int
}

type node struct {
	label      string
	structural string
	children   []*node
	// descendants counts everything below, which is what makes a container
	// worth looking at in a shape view.
	descendants int
}

// Render draws the entries as a tree.
//
// Entries whose parent is not among them are roots: an export of a subtree
// contains its own base and nothing above it, and a filtered export may contain
// entries whose parents did not match. Both are shown at the top level rather
// than dropped, because an entry that is in the file has to appear in the
// picture of the file.
func Render(entries []Entry, opts Options) string {
	roots := build(entries)

	var b strings.Builder
	writeHeader(&b, opts, countNodes(roots))
	for i, r := range roots {
		if i > 0 {
			b.WriteString("\n")
		}
		// A root is drawn in full; everything below it is relative to it.
		fmt.Fprintf(&b, "%s%s\n", r.label, annotation(r))
		writeChildren(&b, r, "")
	}
	return b.String()
}

// parentKeyOf is the folded DN of the parent, or "" at the top.
//
// It goes through the dn package rather than cutting at the first comma: a
// comma inside an RDN is escaped, and cn=Liddell\, Alice is one component. The
// harness has such an entry precisely so that shortcuts like that are found.
func parentKeyOf(s string) string {
	parsed, err := dn.Parse(s)
	if err != nil || len(parsed) == 0 {
		return ""
	}
	return strings.ToLower(parsed.Parent().String())
}

func count(n *node) int {
	total := 0
	for _, c := range n.children {
		total += count(c) + 1
	}
	n.descendants = total
	return total
}

func sortNodes(nodes []*node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].label) < strings.ToLower(nodes[j].label)
	})
	for _, n := range nodes {
		sortNodes(n.children)
	}
}

// annotation is what follows a node: what it is, and how much is under it.
func annotation(n *node) string {
	var parts []string
	if n.structural != "" {
		parts = append(parts, n.structural)
	}
	switch {
	case n.descendants == 1:
		parts = append(parts, "1 below")
	case n.descendants > 1:
		parts = append(parts, fmt.Sprintf("%d below", n.descendants))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  [" + strings.Join(parts, ", ") + "]"
}

func writeChildren(b *strings.Builder, parent *node, prefix string) {
	for i, c := range parent.children {
		last := i == len(parent.children)-1
		branch, carry := "├── ", "│   "
		if last {
			branch, carry = "└── ", "    "
		}
		// Only the RDN: the rest of the DN is the path already drawn above it,
		// and repeating it is what made the flat list hard to read.
		fmt.Fprintf(b, "%s%s%s%s\n", prefix, branch, rdnOf(c.label), annotation(c))
		writeChildren(b, c, prefix+carry)
	}
}

func rdnOf(s string) string {
	parsed, err := dn.Parse(s)
	if err != nil || len(parsed) == 0 {
		return s
	}
	return parsed.RDN().String()
}

func writeHeader(b *strings.Builder, opts Options, entries int) {
	b.WriteString("# The shape of what was exported, as Alder read it.\n")
	b.WriteString("#\n")
	b.WriteString("# This is not LDIF and cannot be applied. LDIF gives a leading space its own\n")
	b.WriteString("# meaning -- it continues the line above -- so a tree drawn with indentation\n")
	b.WriteString("# could never also be a document you import. Export LDIF for that; this is\n")
	b.WriteString("# for reading, and for pasting into a ticket.\n")
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
	fmt.Fprintf(b, "# %d entries\n", entries)
	if opts.Truncated {
		fmt.Fprintf(b, "#\n# WARNING: the search stopped at %d entries, so this is part of the\n", opts.Limit)
		b.WriteString("# subtree and not the whole of it.\n")
	}
	b.WriteString("\n")
}

// countNodes is how many entries the tree holds, which is what the header
// reports. Counting the built tree rather than the input is what makes a
// duplicate DN count once.
func countNodes(roots []*node) int {
	total := 0
	for _, r := range roots {
		total += r.descendants + 1
	}
	return total
}
