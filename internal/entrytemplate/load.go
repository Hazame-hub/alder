package entrytemplate

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
)

// FileSuffix names a template file in a --templates directory.
const FileSuffix = ".template.yaml"

// maxFileSize bounds one template file. A template is a few dozen lines.
const maxFileSize = 64 << 10

// Set is the templates a server offers, by name.
type Set struct {
	list []*Template
}

// Load returns the built-in templates and, when dir is not empty, every
// *.template.yaml file directly in dir. A file template replaces a built-in
// of the same name. Any file that is not a valid template stops the load with
// the file named: a server that started with some templates silently missing
// would offer a "New user" that is not the one the operator wrote.
func Load(dir string) (*Set, error) {
	byName := map[string]*Template{}
	for _, t := range Builtins() {
		byName[t.Name] = t
	}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("entrytemplate: reading --templates: %w", err)
		}
		fromFiles := map[string]string{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), FileSuffix) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			t, err := ReadFile(path)
			if err != nil {
				return nil, err
			}
			if other, dup := fromFiles[t.Name]; dup {
				return nil, fmt.Errorf("entrytemplate: %s and %s both define template %q", other, path, t.Name)
			}
			fromFiles[t.Name] = path
			byName[t.Name] = t
		}
	}
	s := &Set{}
	for _, t := range byName {
		s.list = append(s.list, t)
	}
	sort.Slice(s.list, func(i, j int) bool {
		if s.list[i].View != s.list[j].View {
			return s.list[i].View < s.list[j].View
		}
		return s.list[i].Name < s.list[j].Name
	})
	return s, nil
}

// ReadFile reads and validates one template file.
func ReadFile(path string) (*Template, error) {
	f, err := os.Open(path) // #nosec G304 -- a template file in the directory the operator named with --templates
	if err != nil {
		return nil, fmt.Errorf("entrytemplate: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("entrytemplate: %s: %w", path, err)
	}
	if len(raw) > maxFileSize {
		return nil, fmt.Errorf("entrytemplate: %s is larger than %d bytes", path, maxFileSize)
	}
	t, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("entrytemplate: %s: %w", path, err)
	}
	t.Source = path
	return t, nil
}

// Parse decodes and validates one template document. Unknown keys are
// refused, so a misspelt key is an error and not a template that
// quietly sets nothing.
func Parse(raw []byte) (*Template, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var t Template
	if err := dec.Decode(&t); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the file is empty")
		}
		return nil, err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("one template per file")
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

// All lists every template, in a stable order.
func (s *Set) All() []*Template {
	if s == nil {
		return nil
	}
	return s.list
}

// Get finds a template by name.
func (s *Set) Get(name string) *Template {
	for _, t := range s.All() {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// NewSet makes a set from templates already validated, for a caller that
// assembles its own. A later template replaces an earlier one of the same name.
func NewSet(templates ...*Template) *Set {
	s := &Set{}
	for _, t := range templates {
		replaced := false
		for i, have := range s.list {
			if have.Name == t.Name {
				s.list[i], replaced = t, true
			}
		}
		if !replaced {
			s.list = append(s.list, t)
		}
	}
	return s
}
