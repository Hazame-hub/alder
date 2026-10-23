package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
	"github.com/hazame-hub/alder/internal/schema"
)

// Saved searches (docs/proposals/saved-searches.md, accepted 2026-10-22).
//
// A search is a file of its own beside the LDIF, named NAME.search.yaml
// anywhere below alder.yaml outside dot-directories, and found by that
// suffix: alder.yaml does not list it, so an older release reads a project
// holding search files exactly as before. A search is not intended state.
// It writes nothing, it is never planned, and it is not part of the document
// a saved plan's digest covers.

// SearchSuffix is how a search file is named. Only this spelling: a project
// moves between filesystems that do and do not fold case, and a suffix that
// matched on one and not the other would be a search that appears and
// disappears with the checkout.
const SearchSuffix = ".search.yaml"

// Bounds on what is read. A search is a few lines; these exist so a project
// cannot make validate, or the server reading files a request carried, do
// unbounded work.
const (
	// MaxSearches is how many search files a project may hold.
	MaxSearches = 200
	// MaxSearchLimit and DefaultSearchLimit are POST /search's.
	MaxSearchLimit     = 10000
	DefaultSearchLimit = 100

	maxSearchFileBytes   = 64 << 10
	maxSearchFilterBytes = 8 << 10
	maxSearchAttributes  = 100
	// maxSearchWalk is how many files and directories are looked at to find
	// the search files. A project directory holds a handful; alder.yaml at
	// the top of a large repository is the case this bounds.
	maxSearchWalk = 100000
)

// Search is one saved search, as its file describes it.
type Search struct {
	// Name is the file's path below the project without the suffix:
	// people/locked-accounts. File is the path with it.
	Name string
	File string

	Title       string
	Description string

	Base   dn.DN
	Scope  string
	Filter string
	// Attributes are asked for; empty asks for every user attribute, as a
	// search with none does. Columns are which of them a table shows; empty
	// leaves the choice to the reader.
	Attributes []string
	Columns    []string
	Limit      int
}

// searchFile is a search file as written. Unknown keys are refused by the
// decoder, so every key a version-1 file may use is here.
type searchFile struct {
	Version     int      `yaml:"version"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Base        string   `yaml:"base"`
	Scope       string   `yaml:"scope"`
	Filter      string   `yaml:"filter"`
	Attributes  []string `yaml:"attributes"`
	Columns     []string `yaml:"columns"`
	Limit       *int     `yaml:"limit"`
}

// findSearches lists the search files below the project's directory, sorted.
// Dot-directories are not entered, as the browser does not send them. On the
// operating system's filesystem a file that resolves outside the project is
// refused, by name, as a managed file is.
func findSearches(src source) ([]string, []Problem, error) {
	var found []string
	var problems []Problem
	var realDir string
	if src.dir != "" {
		var err error
		if realDir, err = realPath(src.dir); err != nil {
			return nil, nil, fmt.Errorf("the project directory cannot be resolved: %w", err)
		}
	}
	visited := 0
	errStop := errors.New("stop")
	err := fs.WalkDir(src.fsys, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxSearchWalk {
			problems = append(problems, Problem{Message: fmt.Sprintf("the project directory holds more than %d files and "+
				"directories outside dot-directories, and is not searched further for %s files; "+
				"put alder.yaml in a directory of its own", maxSearchWalk, SearchSuffix)})
			return errStop
		}
		if d.IsDir() {
			if rel != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, SearchSuffix) {
			return nil
		}
		if !isFile(src.fsys, rel) {
			// A link to a directory, or one that leads nowhere: not a file
			// to read, and the walk does not follow it.
			return nil
		}
		if src.dir != "" && !resolvesWithin(realDir, src.display(rel)) {
			problems = append(problems, Problem{File: rel, Message: "resolves to a file outside the project directory; " +
				"a symlink in a project must point inside it"})
			return nil
		}
		found = append(found, rel)
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, nil, err
	}
	sort.Strings(found)
	return found, problems, nil
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// resolvesWithin reports whether path, every link in it followed, is at or
// below realDir. A path that cannot be resolved is not.
func resolvesWithin(realDir, path string) bool {
	real, err := realPath(path)
	return err == nil && within(realDir, real)
}

// loadSearches reads and checks every search file. suffix is the project's,
// empty when it names none.
func loadSearches(src source, suffix dn.DN) ([]Search, []Problem, error) {
	files, problems, err := findSearches(src)
	if err != nil {
		return nil, nil, err
	}
	if len(files) > MaxSearches {
		problems = append(problems, Problem{Message: fmt.Sprintf("the project holds %d %s files, and a project holds at most %d",
			len(files), SearchSuffix, MaxSearches)})
		return nil, problems, nil
	}
	var searches []Search
	names := map[string]string{} // folded name -> the file that has it
	for _, rel := range files {
		name := strings.TrimSuffix(rel, SearchSuffix)
		if name == "" || strings.HasSuffix(name, "/") {
			problems = append(problems, Problem{File: rel, Message: "has no name before " + SearchSuffix})
			continue
		}
		folded := strings.ToLower(name)
		if other, ok := names[folded]; ok {
			problems = append(problems, Problem{File: rel, Message: fmt.Sprintf("names the same search as %s, but for case; "+
				"on a filesystem that folds case they are one file", other)})
			continue
		}
		names[folded] = rel
		raw, err := readSearchFile(src, rel)
		if err != nil {
			return nil, nil, err
		}
		if raw == nil {
			problems = append(problems, Problem{File: rel, Message: fmt.Sprintf("is larger than %d bytes; a search is a few lines",
				maxSearchFileBytes)})
			continue
		}
		s, fileProblems := ParseSearch(rel, raw, suffix)
		problems = append(problems, fileProblems...)
		if len(fileProblems) == 0 {
			searches = append(searches, s)
		}
	}
	return searches, problems, nil
}

// readSearchFile reads one search file, or answers nil for one over the
// bound. Any other error is a file that could not be read.
func readSearchFile(src source, rel string) ([]byte, error) {
	f, err := src.fsys.Open(rel)
	if err != nil {
		return nil, renamed(err, src.display(rel))
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxSearchFileBytes+1))
	if err != nil {
		return nil, renamed(err, src.display(rel))
	}
	if len(data) > maxSearchFileBytes {
		return nil, nil
	}
	return data, nil
}

// ParseSearch reads one search file's content: rel is its path below the
// project, and suffix the project's, empty for none. Every problem is
// returned, each located at the line of the key it is about when there is
// one. The Search is meaningful only when there are none.
func ParseSearch(rel string, raw []byte, suffix dn.DN) (Search, []Problem) {
	s := Search{Name: strings.TrimSuffix(rel, SearchSuffix), File: rel}
	var problems []Problem
	add := func(line int, format string, a ...any) {
		problems = append(problems, Problem{File: rel, Line: line, Message: fmt.Sprintf(format, a...)})
	}

	var f searchFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			add(0, "the file is empty")
		} else {
			add(0, "%s", strings.TrimPrefix(err.Error(), "yaml: "))
		}
		return s, problems
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		add(0, "the file holds more than one YAML document; a search file is one search")
		return s, problems
	}
	lines := keyLines(raw)

	if f.Version != 1 {
		add(lines["version"], "version must be 1, and is %d", f.Version)
		return s, problems
	}
	s.Title, s.Description = f.Title, f.Description

	switch base, err := dn.Parse(f.Base); {
	case strings.TrimSpace(f.Base) == "":
		add(lines["base"], "base is required: the entry the search starts from")
	case err != nil || base.IsEmpty():
		add(lines["base"], "base %q is not a DN", f.Base)
	case !suffix.IsEmpty() && !base.HasSuffix(suffix):
		// The base is what an environment's suffix moves; one outside the
		// project's suffix would stay put while everything else moved.
		add(lines["base"], "base %s is outside the project's suffix %s", base, suffix)
	default:
		s.Base = base
	}

	switch s.Scope = f.Scope; s.Scope {
	case "":
		s.Scope = "sub"
	case "base", "one", "sub":
	default:
		add(lines["scope"], "scope must be base, one or sub, and is %q", f.Scope)
	}

	// Parsed and thrown away: what is kept, and what runs, is the text as
	// written, which the server parses again. A filter is literal; there is
	// nothing in it to fill in.
	s.Filter = strings.TrimSpace(f.Filter)
	switch {
	case s.Filter == "":
		add(lines["filter"], "filter is required: an RFC 4515 filter such as (objectClass=inetOrgPerson)")
	case len(s.Filter) > maxSearchFilterBytes:
		add(lines["filter"], "filter is %d bytes, and a saved filter holds at most %d", len(s.Filter), maxSearchFilterBytes)
	default:
		if _, err := filter.Parse(s.Filter); err != nil {
			add(lines["filter"], "filter is not a valid RFC 4515 filter: %v", err)
		}
	}

	attrs, attrProblems := attributeList("attributes", f.Attributes)
	cols, colProblems := attributeList("columns", f.Columns)
	for _, p := range append(attrProblems, colProblems...) {
		key := "attributes"
		if strings.HasPrefix(p, "columns") {
			key = "columns"
		}
		add(lines[key], "%s", p)
	}
	switch {
	case len(attrs) == 0 && len(cols) > 0:
		// What is shown has to have been asked for, and objectClass is what
		// tells a reader what kind of entry each row is.
		attrs = cols
		if !containsFold(cols, "objectClass") {
			attrs = append(append([]string{}, cols...), "objectClass")
		}
	case len(cols) == 0:
		cols = attrs
	default:
		for _, c := range cols {
			if !containsFold(attrs, c) {
				add(lines["columns"], "column %s is not in attributes, so it would never have a value to show", c)
			}
		}
	}
	s.Attributes, s.Columns = attrs, cols

	s.Limit = DefaultSearchLimit
	if f.Limit != nil {
		if *f.Limit < 1 || *f.Limit > MaxSearchLimit {
			add(lines["limit"], "limit must be between 1 and %d, and is %d", MaxSearchLimit, *f.Limit)
		}
		s.Limit = *f.Limit
	}
	return s, problems
}

// attributeList checks a list of attribute descriptions and drops the ones
// written twice. Whether the directory defines them is not checked: that is
// the search's to answer, as it is today.
func attributeList(key string, names []string) ([]string, []string) {
	var problems []string
	if len(names) > maxSearchAttributes {
		return nil, []string{fmt.Sprintf("%s lists %d attributes, and a search lists at most %d", key, len(names), maxSearchAttributes)}
	}
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if !IsAttributeDescription(n) {
			problems = append(problems, fmt.Sprintf("%s: %q is not an attribute name", key, n))
			continue
		}
		// Refused by name rather than left for the server to withhold: a
		// file that asks for a password reads as if it could have one.
		if schema.IsSensitive(n) {
			problems = append(problems, fmt.Sprintf("%s: %s is a secret, whose values Alder never shows", key, n))
			continue
		}
		if !containsFold(out, n) {
			out = append(out, n)
		}
	}
	return out, problems
}

// IsAttributeDescription reports whether a is a descriptor or a numeric OID,
// with any options: what a search may ask for by name.
func IsAttributeDescription(a string) bool {
	if a == "" || dn.ValidateType(schema.BaseName(a)) != nil {
		return false
	}
	for _, opt := range strings.Split(a, ";")[1:] {
		if opt == "" {
			return false
		}
		for _, c := range opt {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// keyLines is the line of each top-level key, so a problem about a value
// points at it. A file that did not decode never gets here.
func keyLines(raw []byte) map[string]int {
	lines := map[string]int{}
	var root yaml.Node
	if yaml.Unmarshal(raw, &root) != nil || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return lines
	}
	m := root.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		lines[m.Content[i].Value] = m.Content[i].Line
	}
	return lines
}

// SearchNamed is the project's search by its name, exactly as written.
func (p *Project) SearchNamed(name string) (Search, bool) {
	for _, s := range p.Searches {
		if s.Name == name {
			return s, true
		}
	}
	return Search{}, false
}

// SearchNames lists the searches' names, in the order they were found.
func (p *Project) SearchNames() []string {
	names := make([]string, 0, len(p.Searches))
	for _, s := range p.Searches {
		names = append(names, s.Name)
	}
	return names
}

// SearchForEnvironment is s as the named environment runs it: its base moved
// onto the environment's suffix, as every managed base is. The filter is not
// rewritten -- the search the reviewer read is the search that runs -- so
// leftAsWritten says when it holds a DN below the project's suffix, which
// still names the files' entries and not the environment's.
func (p *Project) SearchForEnvironment(env string, s Search) (out Search, leftAsWritten bool) {
	from, to, ok := p.Rebasing(env)
	if !ok {
		return s, false
	}
	out = s
	if moved, ok := s.Base.Rebase(from, to); ok {
		out.Base = moved
	}
	return out, containsDNBelow([]byte(s.Filter), suffixPattern(from))
}
