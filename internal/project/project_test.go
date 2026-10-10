package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/ldif"
)

const validYAML = `version: 1
managed:
  - base: ou=people,dc=alder,dc=test
    files: [people/*.ldif]
  - base: ou=groups,dc=alder,dc=test
    files: [groups.ldif]
environments:
  dev:
    api-url: https://alder.dev.example.com
    host: ldap.dev.example.com
    bind-dn: cn=alder,ou=services,dc=alder,dc=test
    bind-password-env: ALDER_DEV_BIND_PASSWORD
`

const peopleB = `dn: uid=bob,ou=people,dc=alder,dc=test
objectClass: inetOrgPerson
uid: bob
cn: Bob
sn: B
`

const peopleA = `dn: uid=alice,ou=people,dc=alder,dc=test
objectClass: inetOrgPerson
uid: alice
cn: Alice
sn: A
`

const groups = `dn: cn=admins,ou=groups,dc=alder,dc=test
objectClass: groupOfNames
cn: admins
member: uid=alice,ou=people,dc=alder,dc=test
`

// write lays out a project in a temporary directory and returns its root.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func valid() map[string]string {
	return map[string]string{
		"alder.yaml":    validYAML,
		"people/b.ldif": peopleB,
		"people/a.ldif": peopleA,
		"groups.ldif":   groups,
	}
}

func load(t *testing.T, dir string) (*Project, error) {
	t.Helper()
	path, err := Locate(dir)
	if err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestAValidProjectLoadsAndRendersInDeclaredOrder(t *testing.T) {
	p, err := load(t, write(t, valid()))
	if err != nil {
		t.Fatalf("a valid project was refused: %v", problemsOf(err))
	}
	if p.Entries() != 3 {
		t.Errorf("entries = %d, want 3", p.Entries())
	}
	doc, err := p.Document()
	if err != nil {
		t.Fatal(err)
	}
	// Subtrees in declared order; files sorted within a glob, so a.ldif
	// before b.ldif whatever order the disk lists them in.
	order := []string{"uid=alice", "uid=bob", "cn=admins"}
	last := -1
	for _, want := range order {
		at := strings.Index(doc, "dn: "+want)
		if at < 0 || at < last {
			t.Fatalf("document is not in declared order (%v):\n%s", order, doc)
		}
		last = at
	}
	// It is one LDIF document the server's reader accepts as content records.
	records, err := ldif.Unmarshal([]byte(doc))
	if err != nil || len(records) != 3 {
		t.Fatalf("rendered document does not parse back as three records: %v", err)
	}
}

func TestLocateTakesTheDirectoryOrTheFile(t *testing.T) {
	dir := write(t, valid())
	fromDir, err := Locate(dir)
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := Locate(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if fromDir != fromFile {
		t.Errorf("Locate(dir) = %s, Locate(file) = %s", fromDir, fromFile)
	}
}

// Each case breaks one rule from docs/PROJECT.md, and must be refused with a
// message that says which.
func TestEveryRuleIsEnforced(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(files map[string]string)
		want  string
		where string
	}{
		{
			name: "a password in alder.yaml",
			edit: func(f map[string]string) {
				f["alder.yaml"] = strings.Replace(validYAML, "bind-password-env: ALDER_DEV_BIND_PASSWORD", "bind-password: hunter2", 1)
			},
			want:  "a password is never written",
			where: "alder.yaml:",
		},
		{
			name: "an unknown key",
			edit: func(f map[string]string) {
				f["alder.yaml"] = strings.Replace(validYAML, "    host:", "    yes: true\n    host:", 1)
			},
			want: "not found",
		},
		{
			name: "a version other than 1",
			edit: func(f map[string]string) { f["alder.yaml"] = strings.Replace(validYAML, "version: 1", "version: 2", 1) },
			want: "version must be 1",
		},
		{
			name:  "a secret in a project file",
			edit:  func(f map[string]string) { f["people/a.ldif"] = peopleA + "userPassword: {SSHA}abc\n" },
			want:  "carries userPassword, a secret",
			where: "people/a.ldif:1:",
		},
		{
			name: "a changetype record",
			edit: func(f map[string]string) {
				f["people/a.ldif"] = "dn: uid=alice,ou=people,dc=alder,dc=test\nchangetype: delete\n"
			},
			want: "changetype record",
		},
		{
			name: "an entry with no objectClass",
			edit: func(f map[string]string) {
				f["people/a.ldif"] = "dn: uid=alice,ou=people,dc=alder,dc=test\ntitle: Engineer\n"
			},
			want:  "has no objectClass",
			where: "people/a.ldif:1:",
		},
		{
			name: "an entry outside its subtree",
			edit: func(f map[string]string) {
				f["people/a.ldif"] = strings.Replace(peopleA, "ou=people", "ou=elsewhere", 1)
			},
			want: "outside its managed subtree",
		},
		{
			name: "the same entry twice, spelled differently",
			edit: func(f map[string]string) {
				f["people/b.ldif"] = strings.Replace(peopleA, "uid=alice,ou=people", "UID=Alice , OU=People", 1)
			},
			want: "already described at",
		},
		{
			name: "overlapping subtrees",
			edit: func(f map[string]string) {
				f["alder.yaml"] = strings.Replace(validYAML, "base: ou=groups,dc=alder,dc=test", "base: dc=alder,dc=test", 1)
			},
			want: "overlap",
		},
		{
			name: "a glob that matches nothing",
			edit: func(f map[string]string) {
				f["alder.yaml"] = strings.Replace(validYAML, "groups.ldif", "missing.ldif", 1)
			},
			want: "matches no file",
		},
		{
			name: "a path outside the project",
			edit: func(f map[string]string) {
				f["alder.yaml"] = strings.Replace(validYAML, "groups.ldif", "../groups.ldif", 1)
			},
			want: "outside the project directory",
		},
		{
			name: "a bind DN with no password source",
			edit: func(f map[string]string) {
				f["alder.yaml"] = strings.Replace(validYAML, "    bind-password-env: ALDER_DEV_BIND_PASSWORD\n", "", 1)
			},
			want: "needs a password source",
		},
		{
			name: "no environments",
			edit: func(f map[string]string) { f["alder.yaml"] = validYAML[:strings.Index(validYAML, "environments:")] },
			want: "environments names none",
		},
		{
			name: "malformed LDIF",
			edit: func(f map[string]string) { f["groups.ldif"] = "dn: cn=admins,ou=groups,dc=alder,dc=test\nnot a line\n" },
			want: "groups.ldif:",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := valid()
			tc.edit(files)
			_, err := load(t, write(t, files))
			var inv *Invalid
			if !errors.As(err, &inv) {
				t.Fatalf("want an invalid project, got %v", err)
			}
			text := problemsOf(err)
			if !strings.Contains(text, tc.want) {
				t.Errorf("problems do not mention %q:\n%s", tc.want, text)
			}
			if tc.where != "" && !strings.Contains(text, tc.where) {
				t.Errorf("problems are not located at %q:\n%s", tc.where, text)
			}
		})
	}
}

// One run lists everything to fix, not only the first thing.
func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	files := valid()
	files["people/a.ldif"] = peopleA + "userPassword: x\n"
	files["groups.ldif"] = strings.Replace(groups, "ou=groups", "ou=elsewhere", 1)
	_, err := load(t, write(t, files))
	var inv *Invalid
	if !errors.As(err, &inv) || len(inv.Problems) < 2 {
		t.Fatalf("want at least two problems reported together, got %v", err)
	}
}

func TestAnUnreadableProjectIsNotInvalidButAnError(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), FileName))
	var inv *Invalid
	if err == nil || errors.As(err, &inv) {
		t.Fatalf("a missing file should be a read error, got %v", err)
	}
}

func problemsOf(err error) string {
	var inv *Invalid
	if !errors.As(err, &inv) {
		if err == nil {
			return ""
		}
		return err.Error()
	}
	lines := make([]string, len(inv.Problems))
	for i, p := range inv.Problems {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}
