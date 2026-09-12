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
	"io"
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
	// rdn and parentKey come from the parsed DN when the node is made, rather
	// than from parsing the label again while rendering. A DN arrives already
	// parsed, and reparsing every one of them twice -- once to find the parent,
	// once to find the RDN to print -- was most of the work at ten thousand
	// entries.
	rdn       string
	parentKey string
	children  []*node
	// descendants counts everything below, which is what makes a container
	// worth looking at in a shape view.
	descendants int
	// attrs is what the YAML rendering writes under this node. The outline
	// leaves it nil: it draws where entries sit, not what they hold. It lives
	// here rather than in a second map keyed by DN because indexing ten
	// thousand entries twice is the cost this field exists to avoid.
	attrs []YAMLAttribute
}

// errWriter latches the first write error.
//
// Both renderings are tree walks, and a walk that checked the error of every
// Fprintf would be mostly error handling. Once a write has failed the rest of
// the document is dropped on the floor and the error surfaces at the end, which
// is all a caller writing to a socket can act on anyway.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}

// WriteString passes strings through as strings.
//
// Without it io.WriteString finds only Write, converts to []byte on the way in,
// and that conversion copies -- once per fragment, and the renderers write in
// small fragments on purpose. Measured at ten thousand entries, its absence
// cost more allocations than writing fragments saved.
func (e *errWriter) WriteString(s string) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := io.WriteString(e.w, s)
	if err != nil {
		e.err = err
	}
	return n, err
}

// treeBuilder assembles entries into roots. Both renderings go through it, so
// they cannot disagree about the shape they are drawing.
type treeBuilder struct {
	byKey map[string]*node
	order []string
}

func newTreeBuilder(n int) *treeBuilder {
	return &treeBuilder{
		byKey: make(map[string]*node, n),
		order: make([]string, 0, n),
	}
}

// add records one entry. A DN already seen is ignored, which is what makes a
// duplicate count once.
//
// The parent comes from the parsed DN rather than from cutting the string at
// its first comma: a comma inside an RDN is escaped, and cn=Liddell\, Alice is
// one component. The harness holds such an entry precisely so that shortcuts
// like that are found.
func (b *treeBuilder) add(d dn.DN, structural string, attrs []YAMLAttribute) {
	label := d.String()
	// ToLower hands back the string it was given when there is nothing to fold,
	// so the ordinary all-lowercase DN costs nothing here.
	key := strings.ToLower(label)
	if _, seen := b.byKey[key]; seen {
		return
	}
	n := &node{label: label, structural: structural, attrs: attrs, rdn: label}
	if len(d) > 0 {
		n.rdn = d.RDN().String()
		n.parentKey = strings.ToLower(d.Parent().String())
	}
	b.byKey[key] = n
	b.order = append(b.order, key)
}

// finish links each node under its parent and returns the roots, counted and
// sorted.
func (b *treeBuilder) finish() []*node {
	var roots []*node
	for _, key := range b.order {
		n := b.byKey[key]
		if p, ok := b.byKey[n.parentKey]; ok && n.parentKey != key {
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

// Render draws the entries as a tree.
//
// Entries whose parent is not among them are roots: an export of a subtree
// contains its own base and nothing above it, and a filtered export may contain
// entries whose parents did not match. Both are shown at the top level rather
// than dropped, because an entry that is in the file has to appear in the
// picture of the file.
func Render(entries []Entry, opts Options) string {
	var b strings.Builder
	_ = WriteTo(&b, entries, opts)
	return b.String()
}

// WriteTo draws the entries as a tree, straight into w.
//
// The tree still has to be complete before the first line goes out -- the entry
// that settles whether a node is a leaf may be the last one to arrive -- so this
// does not stream the way the LDIF export does. What it drops is the second
// copy: the document is written as it is drawn, rather than assembled in a
// buffer the size of the whole of it and handed over afterwards.
func WriteTo(w io.Writer, entries []Entry, opts Options) error {
	b := newTreeBuilder(len(entries))
	for _, e := range entries {
		b.add(e.DN, e.Structural, nil)
	}
	roots := b.finish()

	ew := &errWriter{w: w}
	writeHeader(ew, opts, countNodes(roots))
	for i, r := range roots {
		if i > 0 {
			_, _ = io.WriteString(ew, "\n")
		}
		// A root is drawn in full; everything below it is relative to it.
		_, _ = fmt.Fprintf(ew, "%s%s\n", r.label, annotation(r))
		writeChildren(ew, r, "")
	}
	return ew.err
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

func writeChildren(b io.Writer, parent *node, prefix string) {
	for i, c := range parent.children {
		last := i == len(parent.children)-1
		branch, carry := "├── ", "│   "
		if last {
			branch, carry = "└── ", "    "
		}
		// Only the RDN: the rest of the DN is the path already drawn above it,
		// and repeating it is what made the flat list hard to read.
		_, _ = fmt.Fprintf(b, "%s%s%s%s\n", prefix, branch, c.rdn, annotation(c))
		writeChildren(b, c, prefix+carry)
	}
}

func writeHeader(b io.Writer, opts Options, entries int) {
	_, _ = io.WriteString(b, "# The shape of what was exported, as Alder read it.\n")
	_, _ = io.WriteString(b, "#\n")
	_, _ = io.WriteString(b, "# This is not LDIF and cannot be applied. LDIF gives a leading space its own\n")
	_, _ = io.WriteString(b, "# meaning -- it continues the line above -- so a tree drawn with indentation\n")
	_, _ = io.WriteString(b, "# could never also be a document you import. Export LDIF for that; this is\n")
	_, _ = io.WriteString(b, "# for reading, and for pasting into a ticket.\n")
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
	_, _ = fmt.Fprintf(b, "# %d entries\n", entries)
	if opts.Truncated {
		_, _ = fmt.Fprintf(b, "#\n# WARNING: the search stopped at %d entries, so this is part of the\n", opts.Limit)
		_, _ = io.WriteString(b, "# subtree and not the whole of it.\n")
	}
	_, _ = io.WriteString(b, "\n")
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
