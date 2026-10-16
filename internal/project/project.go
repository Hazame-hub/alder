// Package project reads an Alder project: an alder.yaml naming the subtrees a
// repository is responsible for, the LDIF files that describe them, and the
// environments it is planned against. docs/PROJECT.md is the specification.
//
// It reads files and nothing else, through an fs.FS: the operating system's
// filesystem for the command-line client, the files a request carried for the
// server's POST /project/plan. It holds no LDAP code and opens no connection: checking a project needs no server, so it can run in a
// pre-commit hook, and planning one is the existing desired-state plan, asked
// of a running Alder server by the command-line client. LDIF, DNs and the
// sensitive-attribute list come from the server's own packages, so a project
// that validates here is read the same way when it is planned.
package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/ldif"
	"github.com/hazame-hub/alder/internal/schema"
)

// FileName is the project file's name.
const FileName = "alder.yaml"

// Limits on what is read. A project is text in a repository, so both are far
// above any real one; they exist so a mistaken glob cannot read a disk.
const (
	maxProjectFileBytes = 1 << 20
	maxLDIFFileBytes    = 64 << 20
)

// MaxRecords is how many entries one project may describe. A project is
// planned as one desired-state document, and one plan covers at most
// api.MaxChangesetChanges records, the same number; a project over it could
// never be planned, so it is refused when it is read, before its records are
// checked one against another.
const MaxRecords = 2000

// maxProblems is how many problems Invalid lists. A file can be written to
// hold any number -- one entry repeated is a problem per copy -- and listing
// a hundred is already more than anyone reads before fixing the first; the
// rest are counted.
const maxProblems = 100

// maxPatterns is how many file patterns a project may list across its
// subtrees, a pattern repeated in one subtree counted once. Each is a
// directory read and matched in full, so it is the patterns times the files
// that bounds the work, and a real project lists a handful.
const maxPatterns = 1000

// Project is a loaded alder.yaml.
type Project struct {
	// File is the alder.yaml that was read, and Dir the directory paths in it
	// are relative to.
	File string
	Dir  string

	// Suffix is the DN the project's files are written under, or empty when
	// alder.yaml names none. An environment may name the DN that replaces it
	// there; see ForEnvironment.
	Suffix dn.DN

	Managed      []Subtree
	Environments map[string]Environment
}

// Subtree is one managed subtree and the entries its files describe.
type Subtree struct {
	Base dn.DN
	// Files are the matched files, relative to the project directory, in the
	// order their records are planned.
	Files   []string
	Records []*ldif.Record
}

// Environment is a named set of connection settings. The keys are the
// client's connection flags, spelled the same way.
type Environment struct {
	APIURL    string `yaml:"api-url"`
	APICAFile string `yaml:"api-ca-file"`

	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	TLS        string `yaml:"tls"`
	CAFile     string `yaml:"ca-file"`
	ServerName string `yaml:"server-name"`

	BindDN           string `yaml:"bind-dn"`
	BindPasswordEnv  string `yaml:"bind-password-env"`
	BindPasswordFile string `yaml:"bind-password-file"`

	ConfigBindDN           string `yaml:"config-bind-dn"`
	ConfigBindPasswordEnv  string `yaml:"config-bind-password-env"`
	ConfigBindPasswordFile string `yaml:"config-bind-password-file"`

	// Suffix is the DN that replaces the project's suffix in this
	// environment. It is not a connection setting: it changes what is
	// planned, not where.
	Suffix string `yaml:"suffix"`
}

// Problem is one thing wrong with a project, located as precisely as it can
// be. Every problem is collected, not only the first, so one run of validate
// lists everything to fix.
type Problem struct {
	File    string
	Line    int
	Message string
}

func (p Problem) String() string {
	switch {
	case p.File != "" && p.Line > 0:
		return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Message)
	case p.File != "":
		return fmt.Sprintf("%s: %s", p.File, p.Message)
	}
	return p.Message
}

// Invalid is the error Load returns for a project that was read and is not
// valid. Any other error means it could not be read at all.
type Invalid struct {
	Problems []Problem
	// total is how many problems were found, when Problems lists only the
	// first of them; zero means it lists them all.
	total int
}

func (e *Invalid) Error() string {
	if e.total == 0 && len(e.Problems) == 1 {
		return "the project is not valid: " + e.Problems[0].String()
	}
	return fmt.Sprintf("the project is not valid: %d problems", max(e.total, len(e.Problems)))
}

// invalid lists problems as Invalid does: all of them up to maxProblems, and
// past that the first maxProblems and one more saying how many are left out.
func invalid(problems []Problem) *Invalid {
	if len(problems) <= maxProblems {
		return &Invalid{Problems: problems}
	}
	listed := append(problems[:maxProblems:maxProblems], Problem{Message: fmt.Sprintf(
		"and %d more problems, not listed; fix these and validate again", len(problems)-maxProblems)})
	return &Invalid{Problems: listed, total: len(problems)}
}

// TooLarge is the error for a project that describes more entries than one
// plan covers. It is not Invalid: nothing in the files is wrong, the project
// is only larger than Alder plans at once.
type TooLarge struct {
	Records int
}

func (e *TooLarge) Error() string {
	return fmt.Sprintf("the project describes %d entries, and one plan covers at most %d", e.Records, MaxRecords)
}

// Locate finds alder.yaml from what --project was given: nothing (the current
// directory), a directory, or the file itself. Parent directories are not
// searched; that is easy to add and hard to take back.
func Locate(path string) (string, error) {
	if path == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Join(path, FileName), nil
	}
	return path, nil
}

// file is alder.yaml as written. Unknown keys are refused by the decoder, so
// every key a version-1 file may use is here.
type file struct {
	Version      int                    `yaml:"version"`
	Suffix       string                 `yaml:"suffix"`
	Managed      []managed              `yaml:"managed"`
	Environments map[string]Environment `yaml:"environments"`
}

type managed struct {
	Base  string   `yaml:"base"`
	Files []string `yaml:"files"`
}

// source is where a project's files are read from. Every read goes through
// fsys; dir is set only when fsys is the operating system's filesystem rooted
// there, which is what makes a symlink something to check and what an error
// names a file by.
type source struct {
	fsys fs.FS
	dir  string
}

// display is how a file is named in an error that is not a Problem: the path
// the operating system would print, or the relative name of a file a request
// carried.
func (s source) display(rel string) string {
	if s.dir == "" {
		return rel
	}
	return filepath.Join(s.dir, filepath.FromSlash(rel))
}

// Load reads and checks a project from the operating system's filesystem:
// path is its alder.yaml, and the paths in it are relative to the directory
// that holds it. A project that was read but is not valid returns *Invalid
// listing every problem; any other error is a file that could not be read.
func Load(path string) (*Project, error) {
	dir := filepath.Dir(path)
	return loadFrom(source{fsys: os.DirFS(dir), dir: dir}, filepath.Base(path), path)
}

// LoadFS reads and checks a project held in fsys, with alder.yaml at its
// root. It is Load without the operating system: nothing outside fsys is
// read, so a project sent to the server reaches no file of the server's own.
// Project.File is then alder.yaml and Project.Dir is empty.
func LoadFS(fsys fs.FS) (*Project, error) {
	return loadFrom(source{fsys: fsys}, FileName, FileName)
}

// loadFrom reads the project file name from src; display is how it is named in
// Project.File and in errors.
func loadFrom(src source, name, display string) (*Project, error) {
	raw, err := readLimited(src.fsys, name, display, maxProjectFileBytes)
	if err != nil {
		return nil, err
	}
	p := &Project{File: display, Dir: src.dir}
	var problems []Problem
	add := func(file string, line int, format string, a ...any) {
		problems = append(problems, Problem{File: file, Line: line, Message: fmt.Sprintf(format, a...)})
	}

	// Said plainly before the decoder says "field not found": a password key
	// is far more likely to be a pasted secret than a typo.
	for _, k := range passwordKeys(raw) {
		add(name, k.line, "%q: a password is never written in %s; name where it comes from with %s-env or %s-file",
			k.key, FileName, k.key, k.key)
	}

	var f file
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			add(name, 0, "the file is empty")
		} else if len(problems) == 0 {
			add(name, 0, "%s", strings.TrimPrefix(err.Error(), "yaml: "))
		}
		return nil, invalid(problems)
	}
	// Decode reads one document. Anything after a "---" would be silently
	// ignored -- a second environments block, a pasted copy with a password
	// in it -- so a file with more than one is refused rather than half read.
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		add(name, 0, "the file holds more than one YAML document; everything after the first --- "+
			"would be ignored, so put the whole project in one document")
	}
	if len(problems) > 0 {
		return nil, invalid(problems)
	}

	if f.Version != 1 {
		add(name, 0, "version must be 1, and is %d", f.Version)
		return nil, invalid(problems)
	}

	if f.Suffix != "" {
		suffix, err := dn.Parse(f.Suffix)
		if err != nil {
			add(name, 0, "suffix %q is not a DN", f.Suffix)
		} else {
			p.Suffix = suffix
		}
	}

	p.Environments = f.Environments
	problems = append(problems, checkEnvironments(name, f.Environments)...)
	problems = append(problems, checkSuffixes(name, f.Suffix, f.Environments)...)

	if len(f.Managed) == 0 {
		add(name, 0, "managed names no subtree, so the project manages nothing")
	}
	patterns := 0
	for _, m := range f.Managed {
		distinct := map[string]bool{}
		for _, pattern := range m.Files {
			distinct[pattern] = true
		}
		patterns += len(distinct)
	}
	if patterns > maxPatterns {
		add(name, 0, "managed lists %d file patterns, and a project lists at most %d; "+
			"a pattern such as people/*.ldif matches every file in its directory", patterns, maxPatterns)
		return nil, invalid(problems)
	}
	// A file belongs to one subtree. Within a subtree, a pattern written
	// twice is read once and a file two patterns match is listed once; a file
	// two subtrees claim is reported once, however many claim it.
	claimed := map[string]string{} // file -> the base that claimed it
	reported := map[string]bool{}  // files reported as claimed twice
	for i, m := range f.Managed {
		where := fmt.Sprintf("managed[%d]", i)
		base, err := dn.Parse(m.Base)
		if err != nil || base.IsEmpty() {
			add(name, 0, "%s: base %q is not a DN", where, m.Base)
			continue
		}
		// A base outside the suffix would be planned under the same name in
		// every environment while the rest moved, which is never what a
		// project with a suffix means.
		if !p.Suffix.IsEmpty() && !base.HasSuffix(p.Suffix) {
			add(name, 0, "%s: base %s is outside the project's suffix %s", where, base, p.Suffix)
		}
		st := Subtree{Base: base}
		if len(m.Files) == 0 {
			add(name, 0, "%s (%s): files is empty", where, m.Base)
		}
		patterns := map[string]bool{}
		listed := map[string]bool{}
		for _, pattern := range m.Files {
			if patterns[pattern] {
				continue
			}
			patterns[pattern] = true
			matches, patternProblems := expand(src, pattern)
			for _, problem := range patternProblems {
				add(name, 0, "%s (%s): %s", where, m.Base, problem)
			}
			for _, rel := range matches {
				if listed[rel] {
					continue
				}
				listed[rel] = true
				if other, ok := claimed[rel]; ok {
					if !reported[rel] {
						reported[rel] = true
						add(name, 0, "%s is matched by both %s and %s; a file belongs to one subtree", rel, other, m.Base)
					}
					continue
				}
				claimed[rel] = m.Base
				st.Files = append(st.Files, rel)
			}
		}
		p.Managed = append(p.Managed, st)
	}

	for _, o := range overlaps(p.Managed) {
		add(name, 0, "managed subtrees %s and %s overlap; each entry must belong to exactly one",
			p.Managed[o[0]].Base, p.Managed[o[1]].Base)
	}

	// Every file is read before any record is checked, so a project too
	// large to plan is refused before the work that grows with its size.
	var read []readFile
	total := 0
	for i := range p.Managed {
		for _, rel := range p.Managed[i].Files {
			rf, err := readRecords(src, rel, i)
			if err != nil {
				return nil, err
			}
			total += len(rf.records)
			read = append(read, rf)
		}
	}
	if total > MaxRecords {
		return nil, &TooLarge{Records: total}
	}
	seen := map[string]seenEntry{}
	for _, rf := range read {
		st := &p.Managed[rf.subtree]
		problems = append(problems, rf.problems...)
		problems = append(problems, checkRecords(rf, st.Base, seen)...)
		st.Records = append(st.Records, rf.records...)
	}

	problems = append(problems, p.checkSuffixEntry(name)...)

	if len(problems) > 0 {
		return nil, invalid(problems)
	}
	return p, nil
}

// Document renders the project's records as the one desired-state LDIF
// document a plan is asked for: subtrees in the order they are declared,
// files in the order they matched, records in the order they were written.
func (p *Project) Document() (string, error) {
	var all []*ldif.Record
	for _, st := range p.Managed {
		all = append(all, st.Records...)
	}
	out, err := ldif.Marshal(all)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Entries is how many entries the project describes.
func (p *Project) Entries() int {
	n := 0
	for _, st := range p.Managed {
		n += len(st.Records)
	}
	return n
}

// EnvironmentNames lists the environments, sorted, for messages.
func (p *Project) EnvironmentNames() []string {
	names := make([]string, 0, len(p.Environments))
	for n := range p.Environments {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Path resolves a path written in alder.yaml against the project directory.
func (p *Project) Path(rel string) string {
	if rel == "" || filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(p.Dir, rel)
}

func checkEnvironments(name string, envs map[string]Environment) []Problem {
	var problems []Problem
	add := func(format string, a ...any) {
		problems = append(problems, Problem{File: name, Message: fmt.Sprintf(format, a...)})
	}
	if len(envs) == 0 {
		add("environments names none, so the project cannot be planned anywhere")
	}
	names := make([]string, 0, len(envs))
	for n := range envs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		e := envs[n]
		where := "environments." + n
		if strings.TrimSpace(n) == "" {
			add("an environment has an empty name")
		}
		if e.APIURL == "" {
			add("%s: api-url is required: the Alder server to plan through", where)
		}
		if e.Host == "" {
			add("%s: host is required: the directory the Alder server connects to", where)
		}
		switch e.TLS {
		case "", "ldaps", "starttls", "plaintext":
		default:
			add("%s: tls must be ldaps, starttls or plaintext, not %q", where, e.TLS)
		}
		if e.Port < 0 || e.Port > 65535 {
			add("%s: port must be between 1 and 65535", where)
		}
		problems = append(problems, checkIdentity(name, where, "bind", e.BindDN, e.BindPasswordEnv, e.BindPasswordFile)...)
		problems = append(problems, checkIdentity(name, where, "config-bind", e.ConfigBindDN, e.ConfigBindPasswordEnv, e.ConfigBindPasswordFile)...)
	}
	return problems
}

func checkIdentity(name, where, prefix, bindDN, fromEnv, fromFile string) []Problem {
	var problems []Problem
	add := func(format string, a ...any) {
		problems = append(problems, Problem{File: name, Message: fmt.Sprintf(format, a...)})
	}
	switch {
	case bindDN == "" && (fromEnv != "" || fromFile != ""):
		add("%s: a password source was given without %s-dn", where, prefix)
	case bindDN != "" && fromEnv == "" && fromFile == "":
		add("%s: %s-dn needs a password source: %s-password-env or %s-password-file", where, prefix, prefix, prefix)
	case fromEnv != "" && fromFile != "":
		add("%s: %s-password-env and %s-password-file are two answers to one question; give one", where, prefix, prefix)
	}
	if bindDN != "" {
		if _, err := dn.Parse(bindDN); err != nil {
			add("%s: %s-dn %q is not a DN", where, prefix, bindDN)
		}
	}
	return problems
}

// expand matches one files entry. A path must stay inside the project: a
// project is what is in its directory, and a file outside it is not reviewed
// with it.
//
// Inside means the real path, not the spelling. A pattern with ".." or an
// absolute path is refused as written; a symlink inside the project that
// points elsewhere on the machine is refused once resolved, so a repository
// cannot have validate or plan read, as LDIF, a file it does not contain -- a
// CI runner's secrets, say. (The files an environment names, such as a
// ca-file, are read where they are; what keeps their content from leaving is
// in the client, which sends a ca-file's certificates and nothing else.) A symlink that stays inside is fine. A refused file
// is named, never read, so nothing of its content can reach a message.
func expand(src source, pattern string) ([]string, []string) {
	clean := filepath.Clean(filepath.FromSlash(pattern))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, []string{fmt.Sprintf("%s is outside the project directory", pattern)}
	}
	// filepath.Glob has no "**": it would read it as "*", match one level,
	// and quietly leave out every deeper file. Refused rather than half
	// honoured, and no glob library is brought in to support it. fs.Glob,
	// which every read now goes through, is the same.
	if strings.Contains(pattern, "**") {
		return nil, []string{fmt.Sprintf("%s uses **, which a project pattern does not support; "+
			"list one pattern per directory, such as people/*.ldif and people/contractors/*.ldif", pattern)}
	}
	matches, err := fs.Glob(src.fsys, filepath.ToSlash(clean))
	if err != nil {
		return nil, []string{fmt.Sprintf("%s is not a valid pattern: %v", pattern, err)}
	}
	// Only a project on the operating system's filesystem has links to
	// follow. Files a request carried are the whole of their filesystem.
	var realDir string
	if src.dir != "" {
		realDir, err = realPath(src.dir)
		if err != nil {
			return nil, []string{fmt.Sprintf("the project directory cannot be resolved: %v", err)}
		}
	}
	var rel, problems []string
	for _, r := range matches {
		info, statErr := fs.Stat(src.fsys, r)
		if statErr != nil || info.IsDir() {
			continue
		}
		if src.dir != "" {
			real, realErr := realPath(src.display(r))
			if realErr != nil || !within(realDir, real) {
				problems = append(problems, fmt.Sprintf("%s resolves to a file outside the project directory; "+
					"a symlink in a project must point inside it", r))
				continue
			}
		}
		rel = append(rel, r)
	}
	if len(rel) == 0 && len(problems) == 0 {
		return nil, []string{fmt.Sprintf("%s matches no file", pattern)}
	}
	// Sorted, so the plan is the same on every machine.
	sort.Strings(rel)
	return rel, problems
}

// realPath is a path with every symlink resolved, made absolute.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// within reports whether path is dir or below it. Both are real paths.
func within(dir, path string) bool {
	r, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}

// overlaps finds the pairs of subtrees, as indexes into managed with the
// earlier first and in that order, whose bases are one at or below the other:
// they would give an entry two owners. A base written twice is paired with
// its first only. Each base is looked up with those of its suffixes as deep
// as some base, by dn.Key, rather than compared with every other base.
func overlaps(managed []Subtree) [][2]int {
	first := map[string]int{}
	depths := map[int]bool{}
	for i, st := range managed {
		if _, ok := first[st.Base.Key()]; !ok {
			first[st.Base.Key()] = i
		}
		depths[len(st.Base)] = true
	}
	var pairs [][2]int
	for j, st := range managed {
		if i := first[st.Base.Key()]; i != j {
			pairs = append(pairs, [2]int{i, j})
			continue
		}
		for n := 1; n < len(st.Base); n++ {
			if !depths[len(st.Base)-n] {
				continue
			}
			if i, ok := first[st.Base[n:].Key()]; ok {
				pairs = append(pairs, [2]int{min(i, j), max(i, j)})
			}
		}
	}
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a][0] != pairs[b][0] {
			return pairs[a][0] < pairs[b][0]
		}
		return pairs[a][1] < pairs[b][1]
	})
	return pairs
}

type seenEntry struct {
	file string
	line int
}

// readFile is one file's records, read but not yet checked, or the problem
// that kept it from being read as LDIF.
type readFile struct {
	rel      string
	subtree  int
	records  []*ldif.Record
	problems []Problem
}

func readRecords(src source, rel string, subtree int) (readFile, error) {
	rf := readFile{rel: rel, subtree: subtree}
	raw, err := readLimited(src.fsys, rel, src.display(rel), maxLDIFFileBytes)
	if err != nil {
		return rf, err
	}
	records, err := ldif.Unmarshal(raw)
	if err != nil {
		var syn *ldif.SyntaxError
		if errors.As(err, &syn) {
			rf.problems = append(rf.problems, Problem{File: rel, Line: syn.Line, Message: syn.Msg})
		} else {
			rf.problems = append(rf.problems, Problem{File: rel, Message: err.Error()})
		}
		return rf, nil
	}
	rf.records = records
	return rf, nil
}

// checkRecords checks one file's records against base and against every
// record checked before them; seen is keyed by dn.Key, so an entry described
// twice is found in one lookup and reported once, against where it was first
// described.
func checkRecords(rf readFile, base dn.DN, seen map[string]seenEntry) []Problem {
	var problems []Problem
	add := func(line int, format string, a ...any) {
		problems = append(problems, Problem{File: rf.rel, Line: line, Message: fmt.Sprintf(format, a...)})
	}
	for _, r := range rf.records {
		if r.Change != ldif.ChangeNone {
			add(r.Line, "%s is a changetype record; a project describes the state entries should be in, not operations", r.DN)
			continue
		}
		if !r.DN.HasSuffix(base) {
			add(r.Line, "%s is outside its managed subtree %s", r.DN, base)
		}
		// A project describes whole entries, and the plan may have to create
		// any of them, so the server reads every desired-state record as a
		// possible add and refuses one with no object class -- even for an
		// entry that exists. Said here, so a project that validates also
		// plans.
		if len(r.Attr("objectClass")) == 0 {
			add(r.Line, "%s has no objectClass; a project describes whole entries, "+
				"and the plan may have to create this one", r.DN)
		}
		for _, a := range r.Attrs {
			if schema.IsSensitive(a.Name) {
				add(r.Line, "%s carries %s, a secret; a project lives in a repository and holds none. "+
					"Set a password through Alder, as a change, never as declared state", r.DN, a.Name)
			}
		}
		key := r.DN.Key()
		if prior, ok := seen[key]; ok {
			add(r.Line, "%s is already described at %s:%d", r.DN, prior.file, prior.line)
			continue
		}
		seen[key] = seenEntry{file: rf.rel, line: r.Line}
	}
	return problems
}

type passwordKey struct {
	key  string
	line int
}

// passwordKeys finds bind-password and config-bind-password anywhere in the
// document, with their lines, so the refusal can say what it is refusing.
func passwordKeys(raw []byte) []passwordKey {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil
	}
	var found []passwordKey
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Value == "bind-password" || k.Value == "config-bind-password" {
					found = append(found, passwordKey{key: k.Value, line: k.Line})
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&root)
	return found
}

// readLimited reads name from fsys, refusing a file larger than limit. An
// error names the file as display, which for the operating system's
// filesystem is the path the operating system would print.
func readLimited(fsys fs.FS, name, display string, limit int64) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, renamed(err, display)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, renamed(err, display)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", display, limit)
	}
	return data, nil
}

// renamed puts display in place of the name an fs.FS reports a file by, so a
// file that cannot be read is named as it was before reads went through one.
func renamed(err error, display string) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		pathErr.Path = display
	}
	return err
}
