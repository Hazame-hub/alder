// Package project reads an Alder project: an alder.yaml naming the subtrees a
// repository is responsible for, the LDIF files that describe them, and the
// environments it is planned against. docs/PROJECT.md is the specification.
//
// It reads files and nothing else. It holds no LDAP code and opens no
// connection: checking a project needs no server, so it can run in a
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

// Project is a loaded alder.yaml.
type Project struct {
	// File is the alder.yaml that was read, and Dir the directory paths in it
	// are relative to.
	File string
	Dir  string

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
}

func (e *Invalid) Error() string {
	if len(e.Problems) == 1 {
		return "the project is not valid: " + e.Problems[0].String()
	}
	return fmt.Sprintf("the project is not valid: %d problems", len(e.Problems))
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
	Managed      []managed              `yaml:"managed"`
	Environments map[string]Environment `yaml:"environments"`
}

type managed struct {
	Base  string   `yaml:"base"`
	Files []string `yaml:"files"`
}

// Load reads and checks a project. A project that was read but is not valid
// returns *Invalid listing every problem; any other error is a file that
// could not be read.
func Load(path string) (*Project, error) {
	raw, err := readLimited(path, maxProjectFileBytes)
	if err != nil {
		return nil, err
	}
	p := &Project{File: path, Dir: filepath.Dir(path)}
	var problems []Problem
	add := func(file string, line int, format string, a ...any) {
		problems = append(problems, Problem{File: file, Line: line, Message: fmt.Sprintf(format, a...)})
	}
	name := filepath.Base(path)

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
		return nil, &Invalid{Problems: problems}
	}
	if len(problems) > 0 {
		return nil, &Invalid{Problems: problems}
	}

	if f.Version != 1 {
		add(name, 0, "version must be 1, and is %d", f.Version)
		return nil, &Invalid{Problems: problems}
	}

	p.Environments = f.Environments
	problems = append(problems, checkEnvironments(name, f.Environments)...)

	if len(f.Managed) == 0 {
		add(name, 0, "managed names no subtree, so the project manages nothing")
	}
	claimed := map[string]string{} // file -> the base that claimed it
	for i, m := range f.Managed {
		where := fmt.Sprintf("managed[%d]", i)
		base, err := dn.Parse(m.Base)
		if err != nil || base.IsEmpty() {
			add(name, 0, "%s: base %q is not a DN", where, m.Base)
			continue
		}
		st := Subtree{Base: base}
		if len(m.Files) == 0 {
			add(name, 0, "%s (%s): files is empty", where, m.Base)
		}
		for _, pattern := range m.Files {
			matches, problem := expand(p.Dir, pattern)
			if problem != "" {
				add(name, 0, "%s (%s): %s", where, m.Base, problem)
				continue
			}
			for _, rel := range matches {
				if other, ok := claimed[rel]; ok {
					add(name, 0, "%s is matched by both %s and %s; a file belongs to one subtree", rel, other, m.Base)
					continue
				}
				claimed[rel] = m.Base
				st.Files = append(st.Files, rel)
			}
		}
		p.Managed = append(p.Managed, st)
	}

	// Overlapping subtrees would give an entry two owners.
	for i := range p.Managed {
		for j := i + 1; j < len(p.Managed); j++ {
			a, b := p.Managed[i].Base, p.Managed[j].Base
			if a.HasSuffix(b) || b.HasSuffix(a) {
				add(name, 0, "managed subtrees %s and %s overlap; each entry must belong to exactly one", a, b)
			}
		}
	}

	seen := map[string][]seenEntry{}
	for i := range p.Managed {
		st := &p.Managed[i]
		for _, rel := range st.Files {
			records, fileProblems, err := readRecords(p.Dir, rel, st.Base, seen)
			if err != nil {
				return nil, err
			}
			problems = append(problems, fileProblems...)
			st.Records = append(st.Records, records...)
		}
	}

	if len(problems) > 0 {
		return nil, &Invalid{Problems: problems}
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
func expand(dir, pattern string) ([]string, string) {
	clean := filepath.Clean(filepath.FromSlash(pattern))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Sprintf("%s is outside the project directory", pattern)
	}
	matches, err := filepath.Glob(filepath.Join(dir, clean))
	if err != nil {
		return nil, fmt.Sprintf("%s is not a valid pattern: %v", pattern, err)
	}
	var rel []string
	for _, m := range matches {
		info, statErr := os.Stat(m)
		if statErr != nil || info.IsDir() {
			continue
		}
		r, relErr := filepath.Rel(dir, m)
		if relErr != nil {
			continue
		}
		rel = append(rel, filepath.ToSlash(r))
	}
	if len(rel) == 0 {
		return nil, fmt.Sprintf("%s matches no file", pattern)
	}
	// Sorted, so the plan is the same on every machine.
	sort.Strings(rel)
	return rel, ""
}

type seenEntry struct {
	dn   dn.DN
	file string
	line int
}

func readRecords(dir, rel string, base dn.DN, seen map[string][]seenEntry) ([]*ldif.Record, []Problem, error) {
	raw, err := readLimited(filepath.Join(dir, filepath.FromSlash(rel)), maxLDIFFileBytes)
	if err != nil {
		return nil, nil, err
	}
	var problems []Problem
	add := func(line int, format string, a ...any) {
		problems = append(problems, Problem{File: rel, Line: line, Message: fmt.Sprintf(format, a...)})
	}
	records, err := ldif.Unmarshal(raw)
	if err != nil {
		var syn *ldif.SyntaxError
		if errors.As(err, &syn) {
			add(syn.Line, "%s", syn.Msg)
		} else {
			add(0, "%v", err)
		}
		return nil, problems, nil
	}
	for _, r := range records {
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
		key := bucket(r.DN)
		for _, prior := range seen[key] {
			if prior.dn.Equal(r.DN) {
				add(r.Line, "%s is already described at %s:%d", r.DN, prior.file, prior.line)
			}
		}
		seen[key] = append(seen[key], seenEntry{dn: r.DN, file: rel, line: r.Line})
	}
	return records, problems, nil
}

// bucket narrows the duplicate search: two equal DNs have the same depth and
// the same leaf attribute types. The types are sorted because a multi-valued
// RDN may list them in any order and still be the same name. Equality itself
// is dn.Equal.
func bucket(d dn.DN) string {
	types := make([]string, 0, len(d.RDN()))
	for _, ava := range d.RDN() {
		types = append(types, strings.ToLower(ava.Type))
	}
	sort.Strings(types)
	return fmt.Sprintf("%d|%s", len(d), strings.Join(types, "+"))
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

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a project file the operator named
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return data, nil
}
