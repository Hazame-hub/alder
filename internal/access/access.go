// Package access reads the access control rules that bear on an entry, and
// reports them. It writes nothing, and it never claims to know what a bind may
// do.
//
// "Why can't I write this?" is the question an operator asks a directory at
// two in the morning, and until now Alder had nothing to say about it: the
// preflight report listed access control among the things it neither reads nor
// translates. Reading is what this package adds. Editing stays out of scope,
// deliberately and on the record -- an access rule is the one thing in a
// directory that can lock every administrator out of it, including the one
// making the change.
//
// The two servers do not express access control the same way, and this package
// does not pretend otherwise:
//
//   - OpenLDAP keeps an ordered list on the database entry in its configuration
//     tree: olcAccess, "{0}to <what> by <who> <level>", consulted in order,
//     first match wins.
//   - 389 Directory Server keeps aci attributes on the entries themselves, and
//     an aci on an ancestor bears on everything beneath it.
//
// There is no common model here for the same reason there is none for
// configuration: the two are different mechanisms with different evaluation
// rules, and a translation between them would be an invention. What is shared
// is the shape of a fact -- a rule, written somewhere, that names a target and
// grants or denies something to somebody -- and that is all this reports.
//
// Where the rules are found decides what is reported, not the server's name:
// aci attributes are looked for on the entry and its ancestors, olcAccess in
// the configuration tree, and whatever answers is what the report holds.
package access

import "strings"

// Styles of access control, named after the attribute that carries them.
const (
	StyleOpenLDAP = "olcAccess"
	StyleACI      = "aci"
)

// Whether a rule bears on the entry that was asked about.
const (
	// AppliesYes: the rule names this entry, or everything.
	AppliesYes = "yes"
	// AppliesMaybe: it may, and saying so would need evaluating something
	// Alder does not evaluate -- a regular expression, a filter, a group.
	AppliesMaybe = "maybe"
	// AppliesNo: its target is somewhere else.
	AppliesNo = "no"
)

// What a grant does, where the rule says so. OpenLDAP's levels are neither
// allow nor deny -- they are a level, and the first matching clause wins -- so
// they carry KindLevel.
const (
	KindAllow = "allow"
	KindDeny  = "deny"
	KindLevel = "level"
)

// Target is what a rule is written about.
type Target struct {
	// Scope is how the target names entries: base, subtree, one, children,
	// regex, filter, or "*" for everything. Empty when the rule does not say.
	Scope string `json:"scope,omitempty"`
	// DN is the entry or subtree named, as written.
	DN string `json:"dn,omitempty"`
	// Attributes narrows the rule to these attributes, as written. A leading
	// "!" is kept: an aci's targetattr may exclude rather than include.
	Attributes []string `json:"attributes,omitempty"`
	// Filter narrows it to entries matching an LDAP filter, as written.
	Filter string `json:"filter,omitempty"`
}

// Grant is one clause of a rule: somebody, and what they get.
type Grant struct {
	// Subject is who, in the rule's own words: self, users, anonymous, a DN,
	// a group, "*".
	Subject string `json:"subject"`
	// Access is what they get, as written: an OpenLDAP level (read, write,
	// auth, none) or a 389 DS rights list (read, search, compare, write).
	Access string `json:"access"`
	// Kind is allow, deny, or level.
	Kind string `json:"kind"`
}

// Rule is one access control rule as the server holds it.
type Rule struct {
	// Style is the mechanism it belongs to.
	Style string `json:"style"`
	// Source is the entry the rule is written on. For OpenLDAP that is a
	// database entry in the configuration tree; for 389 DS, an entry in the
	// data tree, which may be the one asked about or an ancestor of it.
	Source string `json:"source"`
	// Index is the rule's position where the server orders them. OpenLDAP
	// consults its rules in this order and stops at the first match.
	Index *int `json:"index,omitempty"`
	// Name is what the rule calls itself, where the syntax has a name for it.
	Name string `json:"name,omitempty"`
	// Raw is the rule exactly as the server holds it. It is always present,
	// whatever was parsed: this is the thing that is actually in force.
	Raw string `json:"raw"`
	// Parsed says whether the structure below was read with confidence. A rule
	// Alder could not take apart is reported whole rather than guessed at.
	Parsed bool    `json:"parsed"`
	Target *Target `json:"target,omitempty"`
	Grants []Grant `json:"grants,omitempty"`
	// Applies says whether the rule bears on the entry asked about.
	Applies string `json:"applies"`
	// Why explains that answer in a person's words.
	Why string `json:"why,omitempty"`
	// Inherited marks a rule written on an ancestor rather than on the entry.
	Inherited bool `json:"inherited,omitempty"`
}

// Effective is the server's own answer about one identity on this entry,
// where the server will answer at all.
//
// It outranks everything else in a report. The rules are what is written; this
// is what the directory will do, computed by the code that will do it.
type Effective struct {
	// Subject is the identity asked about, empty for the session's own.
	Subject string `json:"subject"`
	// Entry is the entry-level answer in the server's letters, with the gloss
	// beside it rather than instead of it.
	Entry      string           `json:"entry"`
	EntryWords []string         `json:"entryWords,omitempty"`
	Attributes []AttributeRight `json:"attributes,omitempty"`
}

// AttributeRight is what the identity may do with one attribute.
type AttributeRight struct {
	Name   string   `json:"name"`
	Rights string   `json:"rights"`
	Words  []string `json:"words,omitempty"`
}

// Report is what bears on one entry.
type Report struct {
	DN string `json:"dn"`
	// Styles found, in the order they were looked for.
	Styles []string `json:"styles"`
	Rules  []Rule   `json:"rules"`
	// Effective is the server's own verdict, where it gave one.
	Effective *Effective `json:"effective,omitempty"`
	// RightsNote says why there is no verdict, in a person's words. A server
	// that cannot answer and a server that declined are different facts, and
	// neither is "no rights".
	RightsNote string `json:"rightsNote,omitempty"`
	// Unread says a place rules could live could not be read, so the answer is
	// not the whole answer. The most common one by far: the configuration tree
	// needs its own identity, and this session has none.
	Unread []Unread `json:"unread,omitempty"`
}

// Unread is somewhere rules may be that Alder could not read.
type Unread struct {
	Where  string `json:"where"`
	Reason string `json:"reason"`
}

// Actionable is the honest summary of what a report is: the rules as written,
// in the order the server keeps them, and not an answer about this session's
// rights. It is here rather than in the interface so the command line, the API
// description and the web view all say the same thing.
const Disclaimer = "These are the rules the server holds, in its own order and its own words. " +
	"Alder does not evaluate them: what a particular bind may do is the directory's answer, " +
	"and the directory gives it when an operation is tried."

// counts how many rules bear on the entry, for a summary line.
func (r *Report) Applying() int {
	n := 0
	for _, rule := range r.Rules {
		if rule.Applies == AppliesYes {
			n++
		}
	}
	return n
}

// dnUnder reports whether child is at or under parent, comparing DN text the
// way the rest of the interface does: case-insensitively, component-wise.
//
// It is deliberately textual. Alder's dn package parses and escapes DNs for
// writing; an access rule's target is a string the administrator wrote, and
// comparing it as text is what the server's own matching does for the base and
// subtree forms this handles.
func dnUnder(child, parent string) bool {
	c := strings.ToLower(strings.TrimSpace(child))
	p := strings.ToLower(strings.TrimSpace(parent))
	if p == "" {
		return true
	}
	return c == p || strings.HasSuffix(c, ","+p)
}

// dnEqual compares two DNs as text, case-insensitively.
func dnEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
