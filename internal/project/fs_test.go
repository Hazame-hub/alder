package project

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/hazame-hub/alder/internal/dn"
)

func memFiles(t *testing.T, files map[string]string) fs.FS {
	t.Helper()
	raw := make(map[string][]byte, len(files))
	for name, content := range files {
		raw[name] = []byte(content)
	}
	fsys, err := NewFS(raw)
	if err != nil {
		t.Fatalf("NewFS: %v", err)
	}
	return fsys
}

// The files a request carried are a filesystem fs.Glob and LoadFS can rely
// on: the standard library's own checks of one pass.
func TestTheMemoryFilesystemIsAFilesystem(t *testing.T) {
	fsys := memFiles(t, valid())
	if err := fstest.TestFS(fsys, "alder.yaml", "groups.ldif", "people/a.ldif", "people/b.ldif"); err != nil {
		t.Fatal(err)
	}
}

// One validator, two filesystems: a project read from memory loads to the
// same document as the same files read from disk.
func TestAProjectInMemoryLoadsAsItDoesFromDisk(t *testing.T) {
	onDisk, err := load(t, write(t, valid()))
	if err != nil {
		t.Fatalf("from disk: %v", problemsOf(err))
	}
	inMemory, err := LoadFS(memFiles(t, valid()))
	if err != nil {
		t.Fatalf("from memory: %v", problemsOf(err))
	}
	a, _ := onDisk.Document()
	b, _ := inMemory.Document()
	if a != b {
		t.Errorf("the documents differ:\n--- disk\n%s\n--- memory\n%s", a, b)
	}
	if inMemory.File != FileName || inMemory.Dir != "" {
		t.Errorf("File = %q, Dir = %q; a project in memory has no directory on disk", inMemory.File, inMemory.Dir)
	}
	if len(inMemory.Managed) != 2 || strings.Join(inMemory.Managed[0].Files, ",") != "people/a.ldif,people/b.ldif" {
		t.Errorf("managed = %+v", inMemory.Managed)
	}
}

// Every rule refuses the same way, problem for problem, read from either.
func TestEveryRuleIsEnforcedInMemoryInTheSameWords(t *testing.T) {
	for _, tc := range ruleCases {
		t.Run(tc.name, func(t *testing.T) {
			files := valid()
			tc.edit(files)
			_, diskErr := load(t, write(t, files))
			_, memErr := LoadFS(memFiles(t, files))
			var inv *Invalid
			if !errors.As(memErr, &inv) {
				t.Fatalf("want an invalid project, got %v", memErr)
			}
			if got, want := problemsOf(memErr), problemsOf(diskErr); got != want {
				t.Errorf("in memory:\n%s\non disk:\n%s", got, want)
			}
		})
	}
}

// A folder with no alder.yaml at its top is an error, not an empty project.
func TestAProjectInMemoryWithoutAlderYAMLIsAReadError(t *testing.T) {
	files := valid()
	delete(files, "alder.yaml")
	files["sub/alder.yaml"] = validYAML
	_, err := LoadFS(memFiles(t, files))
	var inv *Invalid
	if err == nil || errors.As(err, &inv) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want a not-found read error, got %v", err)
	}
}

func TestNewFSRefusesAPathThatIsNotACleanRelativeName(t *testing.T) {
	for _, bad := range []string{"", ".", "/alder.yaml", "../x.ldif", "a/../b.ldif", "a//b.ldif", "a/./b.ldif",
		"people\\a.ldif", "a/", "nul\x00.ldif"} {
		if _, err := NewFS(map[string][]byte{"alder.yaml": nil, bad: nil}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	// A name that is also a directory of another is two answers to one name.
	if _, err := NewFS(map[string][]byte{"people": nil, "people/a.ldif": nil}); err == nil {
		t.Error("a file and a directory of the same name were accepted")
	}
	many := map[string][]byte{}
	for i := 0; i <= MaxFiles; i++ {
		many[strings.Repeat("a", 1+i%7)+"/"+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".ldif"] = nil
	}
	if _, err := NewFS(many); err == nil {
		t.Errorf("%d files were accepted", len(many))
	}
}

// A glob in alder.yaml cannot reach past the files that were sent.
func TestAGlobInMemoryMatchesOnlyWhatWasSent(t *testing.T) {
	files := valid()
	files["alder.yaml"] = strings.Replace(validYAML, "groups.ldif", "../../etc/*", 1)
	_, err := LoadFS(memFiles(t, files))
	if text := problemsOf(err); !strings.Contains(text, "outside the project directory") {
		t.Errorf("problems = %s", text)
	}
}

func TestUnmanagedComparesByDNEquality(t *testing.T) {
	p, err := LoadFS(memFiles(t, valid()))
	if err != nil {
		t.Fatal(problemsOf(err))
	}
	parse := func(s string) dn.DN {
		d, err := dn.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	report := ListUnmanaged(p, func(base dn.DN) ([]dn.DN, error) {
		if strings.HasPrefix(base.String(), "ou=groups") {
			return nil, errors.New("too many entries")
		}
		return []dn.DN{
			parse("ou=people,dc=alder,dc=test"),
			// Described by people/a.ldif, spelled differently.
			parse("UID=Alice , OU=People,dc=alder,dc=test"),
			parse("uid=bob,ou=people,dc=alder,dc=test"),
			parse("uid=carol,ou=people,dc=alder,dc=test"),
		}, nil
	})
	if len(report) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if got := strings.Join(report[0].Entries, "|"); got != "ou=people,dc=alder,dc=test|uid=carol,ou=people,dc=alder,dc=test" {
		t.Errorf("unmanaged in people = %s", got)
	}
	if report[1].Error != "too many entries" || len(report[1].Entries) != 0 || report[1].Entries == nil {
		t.Errorf("an unreadable subtree = %+v; want its reason and an empty, non-nil list", report[1])
	}
}
