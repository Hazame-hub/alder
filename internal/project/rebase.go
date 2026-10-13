package project

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/schema"
)

// An environment with a different suffix.
//
// A project's files are written under one suffix, the project's, and an
// environment may name the DN that replaces it there: dc=dev,dc=example,dc=com
// for files written under dc=example,dc=com. Planning for that environment
// moves every entry, every managed base and every value that names an entry
// from below the one to below the other, with the dn package, so that what
// is planned is the files' own content under the environment's names.
//
// Which values name an entry is the directory's schema's answer, not a guess
// from what a value looks like: a value is rebased when its attribute type has
// DN syntax, or Name And Optional UID, in the schema of the directory being
// planned, and never otherwise. That schema is read by the client, so this
// package takes it as a function and stays offline.

// DNKind says whether, and how, an attribute type's values name entries.
type DNKind int

const (
	// NotDN values are not names. They are never rebased, whatever they
	// look like.
	NotDN DNKind = iota
	// DistinguishedName values are each one DN (RFC 4517, 3.3.9): member,
	// manager, owner, seeAlso and the like.
	DistinguishedName
	// NameAndOptionalUID values are a DN, optionally followed by "#" and a
	// bit string (RFC 4517, 3.3.21): uniqueMember.
	NameAndOptionalUID
)

// The two syntaxes whose values name entries.
const (
	SyntaxDN                 = "1.3.6.1.4.1.1466.115.121.1.12"
	SyntaxNameAndOptionalUID = "1.3.6.1.4.1.1466.115.121.1.34"
)

// DNKindOfSyntax is the DNKind of an attribute type with the given syntax.
func DNKindOfSyntax(oid string) DNKind {
	switch strings.TrimSpace(oid) {
	case SyntaxDN:
		return DistinguishedName
	case SyntaxNameAndOptionalUID:
		return NameAndOptionalUID
	}
	return NotDN
}

// DNKinds answers DNKind for an attribute type, by name or OID, from the
// schema of the directory being planned. ok is false when that schema does
// not define the type.
type DNKinds func(attrType string) (kind DNKind, ok bool)

// Rebase is what ForEnvironment did, said beside the plan, because the plan
// is no longer byte for byte what the files hold.
type Rebase struct {
	From, To dn.DN
	// Entries is how many entries were moved, which is all of them.
	Entries int
	// Values is how many DN-valued attribute values were moved.
	Values int
	// LeftAsWritten are values that read as a DN at or below From but
	// belong to an attribute type without DN syntax. They are not names
	// to the directory, so they were not rewritten; they are said, because
	// one may be a name the schema does not know is one.
	LeftAsWritten []Untouched
}

// Untouched is one value ForEnvironment left as written.
type Untouched struct {
	DN        dn.DN
	Attribute string
}

// Unrebasable is the error ForEnvironment returns when it cannot tell which
// values to rewrite, or rewriting one would contradict the entry's own name.
// Nothing was rebased.
type Unrebasable struct {
	Problems []string
}

func (e *Unrebasable) Error() string {
	return "the project cannot be rebased onto this environment's suffix: " + strings.Join(e.Problems, "; ")
}

// Rebasing reports whether planning for the named environment rewrites the
// project's names, and from and to what. An environment with no suffix, or
// one equal to the project's, plans the files as written.
func (p *Project) Rebasing(env string) (from, to dn.DN, ok bool) {
	e, found := p.Environments[env]
	if !found || e.Suffix == "" || p.Suffix.IsEmpty() {
		return nil, nil, false
	}
	to, err := dn.Parse(e.Suffix)
	if err != nil || to.Equal(p.Suffix) {
		// Load refused a suffix that does not parse, so only equality
		// lands here.
		return nil, nil, false
	}
	return p.Suffix, to, true
}

// ForEnvironment is the project as the named environment holds it. Without
// a rebase it is p itself and the Rebase is nil. With one it is a copy, and
// p is not changed:
//
//   - every managed base and every entry DN is moved from the project's
//     suffix to the environment's. The RDNs above the suffix are kept as
//     written, values and all; only the suffix is replaced.
//   - every value of an attribute type kinds says has DN syntax, or Name And
//     Optional UID, that is at or below the project's suffix is moved the
//     same way, and rendered as the dn package renders a DN. A value of that
//     syntax outside the suffix, or one that does not parse as a DN, is left
//     as written for the directory to judge.
//   - every other value is left byte for byte as written, even one that
//     reads as a DN below the suffix; those are listed in LeftAsWritten.
//
// It refuses, with *Unrebasable, a project using an attribute type the
// schema does not define, since whether its values are names cannot be told,
// and an entry named by a DN-valued attribute below the suffix, since moving
// the value and not the name, or the name and not the value, would give the
// entry a name its own attributes contradict.
func (p *Project) ForEnvironment(env string, kinds DNKinds) (*Project, *Rebase, error) {
	from, to, ok := p.Rebasing(env)
	if !ok {
		return p, nil, nil
	}
	var problems []string
	unknown := map[string]bool{}
	kindOf := func(attr string) DNKind {
		k, known := kinds(schema.BaseName(attr))
		if !known {
			unknown[schema.BaseName(attr)] = true
		}
		return k
	}
	for _, st := range p.Managed {
		for _, r := range st.Records {
			for _, a := range r.Attrs {
				kindOf(a.Name)
			}
			for _, ava := range r.DN.RDN() {
				if kindOf(ava.Type) == NotDN || ava.Hex {
					continue
				}
				if name, err := dn.Parse(ava.Value); err == nil && name.HasSuffix(from) {
					problems = append(problems, fmt.Sprintf("%s is named by %s, whose values are DNs below the suffix; "+
						"its name and its value cannot both be kept", r.DN, ava.Type))
				}
			}
		}
	}
	if len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for n := range unknown {
			names = append(names, n)
		}
		sort.Strings(names)
		problems = append(problems, fmt.Sprintf("the directory's schema does not define %s, so whether its values "+
			"are DNs cannot be told", strings.Join(names, ", ")))
	}
	if len(problems) > 0 {
		return nil, nil, &Unrebasable{Problems: problems}
	}

	out := *p
	out.Suffix = to
	out.Managed = make([]Subtree, len(p.Managed))
	rb := &Rebase{From: from, To: to, LeftAsWritten: []Untouched{}}
	for i, st := range p.Managed {
		base, _ := st.Base.Rebase(from, to)
		moved := Subtree{Base: base, Files: st.Files, Records: make([]*ldif.Record, len(st.Records))}
		for j, r := range st.Records {
			moved.Records[j] = rebaseRecord(r, from, to, kindOf, rb)
		}
		out.Managed[i] = moved
	}
	return &out, rb, nil
}

func rebaseRecord(r *ldif.Record, from, to dn.DN, kindOf func(string) DNKind, rb *Rebase) *ldif.Record {
	moved := *r
	if d, ok := r.DN.Rebase(from, to); ok {
		moved.DN = d
		rb.Entries++
	}
	moved.Attrs = make([]ldif.Attribute, len(r.Attrs))
	for i, a := range r.Attrs {
		kind := kindOf(a.Name)
		values := make([][]byte, len(a.Values))
		noted := false
		for j, v := range a.Values {
			values[j] = v
			switch kind {
			case DistinguishedName, NameAndOptionalUID:
				if nv, ok := rebaseValue(v, kind, from, to); ok {
					values[j] = nv
					rb.Values++
				}
			default:
				if !noted && readsAsDNBelow(v, from) {
					rb.LeftAsWritten = append(rb.LeftAsWritten, Untouched{DN: moved.DN, Attribute: a.Name})
					noted = true
				}
			}
		}
		moved.Attrs[i] = ldif.Attribute{Name: a.Name, Values: values}
	}
	return &moved
}

// bitString is the optional UID of a Name And Optional UID value.
var bitString = regexp.MustCompile(`^'[01]*'B$`)

// rebaseValue moves one DN-valued value, reporting false for one that does
// not parse or lies outside from.
func rebaseValue(v []byte, kind DNKind, from, to dn.DN) ([]byte, bool) {
	text, uid := string(v), ""
	if kind == NameAndOptionalUID {
		// RFC 4517 reads a trailing "#'...'B" as the UID, and so does this.
		if i := strings.LastIndexByte(text, '#'); i >= 0 && bitString.MatchString(text[i+1:]) {
			text, uid = text[:i], text[i:]
		}
	}
	name, err := dn.Parse(text)
	if err != nil {
		return nil, false
	}
	moved, ok := name.Rebase(from, to)
	if !ok {
		return nil, false
	}
	return []byte(moved.String() + uid), true
}

func readsAsDNBelow(v []byte, from dn.DN) bool {
	if !utf8.Valid(v) || !strings.Contains(string(v), "=") {
		return false
	}
	name, err := dn.Parse(string(v))
	return err == nil && name.HasSuffix(from)
}

// checkSuffixes checks the suffix keys on their own: an environment's
// suffix replaces the project's, so it needs one to replace, and both are
// DNs.
func checkSuffixes(name, projectSuffix string, envs map[string]Environment) []Problem {
	var problems []Problem
	for _, n := range sortedNames(envs) {
		s := envs[n].Suffix
		if s == "" {
			continue
		}
		where := "environments." + n
		if projectSuffix == "" {
			problems = append(problems, Problem{File: name, Message: fmt.Sprintf("%s: suffix replaces the project's suffix, "+
				"and %s names none; add suffix at the top, the DN the project's files are written under", where, FileName)})
			continue
		}
		if d, err := dn.Parse(s); err != nil || d.IsEmpty() {
			problems = append(problems, Problem{File: name, Message: fmt.Sprintf("%s: suffix %q is not a DN", where, s)})
		}
	}
	return problems
}

// checkSuffixEntry refuses to rebase the entry at the suffix itself onto a
// suffix with another RDN. Its name would change while the attribute that
// names it -- dc: example -- stayed as the file wrote it, and rewriting that
// value would be a value owned by one environment, which a project does not
// express.
func (p *Project) checkSuffixEntry(name string) []Problem {
	if p.Suffix.IsEmpty() {
		return nil
	}
	var problems []Problem
	for _, n := range sortedNames(p.Environments) {
		to, err := dn.Parse(p.Environments[n].Suffix)
		if err != nil || to.RDN().Equal(p.Suffix.RDN()) {
			continue
		}
		for _, st := range p.Managed {
			for _, r := range st.Records {
				if r.DN.Equal(p.Suffix) {
					problems = append(problems, Problem{File: name, Message: fmt.Sprintf(
						"environments.%s: suffix %s would rename %s, which the project describes, to another RDN while "+
							"its own %s value stays as written; manage the entries below the suffix, not the suffix entry",
						n, to, r.DN, p.Suffix.RDN()[0].Type)})
				}
			}
		}
	}
	return problems
}

func sortedNames(envs map[string]Environment) []string {
	names := make([]string, 0, len(envs))
	for n := range envs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
