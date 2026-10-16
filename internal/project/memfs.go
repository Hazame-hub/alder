package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

// MaxFiles is how many files one project sent as files may hold. A real
// project is a handful; this exists so a request cannot make the server
// build an arbitrarily large filesystem before anything is checked.
const MaxFiles = 1000

// maxPathBytes and maxNameBytes bound a path a request carries as common
// filesystems do (PATH_MAX, NAME_MAX), so matching a pattern against a name
// stays small: path.Match costs the one's length times the other's.
const (
	maxPathBytes = 4096
	maxNameBytes = 255
)

// NewFS holds a project's files in memory, keyed by their path relative to
// the project's directory, for LoadFS. It is how the server reads a project
// a request carried: nothing outside the map can be named, and there are no
// links to follow.
//
// Every path must be a clean relative name, slash-separated: no leading
// slash, no "." or ".." element, no empty element, no backslash, which
// Windows would read as a separator. A path that is also the directory of
// another is refused. The error names the first path refused.
func NewFS(files map[string][]byte) (fs.FS, error) {
	if len(files) > MaxFiles {
		return nil, fmt.Errorf("%d files, and a project sent as files holds at most %d", len(files), MaxFiles)
	}
	m := &memFS{files: map[string][]byte{}, dirs: map[string][]string{".": nil}}
	listed := map[string]bool{}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := CheckPath(name); err != nil {
			return nil, err
		}
		m.files[name] = files[name]
		// Every directory above the file exists, and lists its child once.
		// Walked by slicing the name, and stopped at the first ancestor
		// already listed (its own ancestors are then listed too), so the
		// work is linear in the paths, however deep.
		for end := len(name); end > 0; {
			child := name[:end]
			if listed[child] {
				break
			}
			listed[child] = true
			parent, base := ".", child
			if i := strings.LastIndexByte(child, '/'); i >= 0 {
				parent, base = child[:i], child[i+1:]
			}
			m.dirs[parent] = append(m.dirs[parent], base)
			end = len(parent)
			if parent == "." {
				end = 0
			}
		}
	}
	for d := range m.dirs {
		if _, clash := m.files[d]; clash {
			return nil, fmt.Errorf("%s is both a file and the directory of another file", d)
		}
		sort.Strings(m.dirs[d])
	}
	return m, nil
}

// CheckPath says whether name is a path NewFS accepts.
func CheckPath(name string) error {
	switch {
	case name == "" || name == ".":
		return fmt.Errorf("a file has an empty path")
	case strings.ContainsAny(name, "\\\x00"):
		return fmt.Errorf("%q is not a slash-separated relative path", name)
	case !fs.ValidPath(name):
		return fmt.Errorf("%q is not a clean relative path: no leading slash, no . or .. and no empty part", name)
	case len(name) > maxPathBytes:
		return fmt.Errorf("%.64q... is a path of %d bytes, and one holds at most %d", name, len(name), maxPathBytes)
	}
	for _, part := range strings.Split(name, "/") {
		if len(part) > maxNameBytes {
			return fmt.Errorf("%.64q... has a name of %d bytes, and one holds at most %d", name, len(part), maxNameBytes)
		}
	}
	return nil
}

// memFS is the filesystem NewFS returns. It answers Open, Stat and ReadDir,
// which is everything fs.Glob and LoadFS ask.
type memFS struct {
	files map[string][]byte
	// dirs lists each directory's children by name, sorted.
	dirs map[string][]string
}

func (m *memFS) Open(name string) (fs.File, error) {
	info, err := m.Stat(name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errorOf(err)}
	}
	if info.IsDir() {
		entries, _ := m.ReadDir(name)
		return &memDir{info: info, entries: entries}, nil
	}
	return &memFile{info: info, r: bytes.NewReader(m.files[name])}, nil
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	if data, ok := m.files[name]; ok {
		return memInfo{name: path.Base(name), size: int64(len(data))}, nil
	}
	if _, ok := m.dirs[name]; ok {
		return memInfo{name: path.Base(name), dir: true}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	children, ok := m.dirs[name]
	if !ok {
		if _, isFile := m.files[name]; isFile {
			return nil, &fs.PathError{Op: "readdir", Path: name, Err: fmt.Errorf("not a directory")}
		}
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	out := make([]fs.DirEntry, 0, len(children))
	for _, c := range children {
		info, err := m.Stat(path.Join(name, c))
		if err != nil {
			return nil, err
		}
		out = append(out, fs.FileInfoToDirEntry(info))
	}
	return out, nil
}

// errorOf is the reason inside a *fs.PathError, so Open can say "open" of
// what Stat found.
func errorOf(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

type memInfo struct {
	name string
	size int64
	dir  bool
}

func (i memInfo) Name() string { return i.name }
func (i memInfo) Size() int64  { return i.size }
func (i memInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return i.dir }
func (i memInfo) Sys() any           { return nil }

type memFile struct {
	info fs.FileInfo
	r    *bytes.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memFile) Read(b []byte) (int, error) { return f.r.Read(b) }
func (f *memFile) Close() error               { return nil }

type memDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	offset  int
}

func (d *memDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *memDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.Name(), Err: fmt.Errorf("is a directory")}
}
func (d *memDir) Close() error { return nil }

func (d *memDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.offset:]
	if n <= 0 {
		d.offset = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.offset += n
	return rest[:n], nil
}
