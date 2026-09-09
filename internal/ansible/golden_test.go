package ansible

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
)

// Golden files for the Ansible a user actually receives.
//
// The tests alongside these assert behaviours -- that the attribute task is
// exact rather than present, that entries come out parent first. This file
// asserts the duller thing: that the exact bytes do not move. An exported
// playbook is committed to somebody's infrastructure repository and reviewed as
// a diff, so a change to indentation, key order or task naming shows up in
// every future review as noise nobody can attribute.
//
// Regenerate deliberately, never reflexively:
//
//	go test ./internal/ansible -update
//
// and read the resulting diff. A change here is a change to a promise.

var update = flag.Bool("update", false, "rewrite the golden files")

// assertGolden compares got against testdata/golden/<name>, or rewrites it
// under -update.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the golden directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(got))
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nrun \"go test ./internal/ansible -update\" to create it", path, err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Errorf("the rendering of %s changed.\n--- want ---\n%s\n--- got ---\n%s",
			name, want, got)
	}
}

func mustDN(t *testing.T, s string) dn.DN {
	t.Helper()
	parsed, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return parsed
}

func vals(values ...string) [][]byte {
	out := make([][]byte, 0, len(values))
	for _, v := range values {
		out = append(out, []byte(v))
	}
	return out
}

// One task per change type, which is the export offered on every pending change.
func TestGoldenTask(t *testing.T) {
	cases := []struct {
		file   string
		record func(t *testing.T) directory.ChangeRecord
	}{
		{
			file: "task-add.yml",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN:   mustDN(t, "uid=carol,ou=people,dc=alder,dc=test"),
					Type: directory.ChangeAdd,
					Attrs: []directory.Attribute{
						{Name: "objectClass", Values: vals("top", "inetOrgPerson")},
						{Name: "uid", Values: vals("carol")},
						{Name: "cn", Values: vals("Carol Adler")},
						{Name: "sn", Values: vals("Adler")},
						{Name: "mail", Values: vals("carol@alder.test", "c.adler@alder.test")},
					},
				}
			},
		},
		{
			// Every modification operation in one record, including the
			// distinction a delete with values makes against one without.
			file: "task-modify.yml",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN:   mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
					Type: directory.ChangeModify,
					Mods: []directory.Mod{
						{Op: directory.ModAdd, Name: "mail", Values: vals("alice@example.test")},
						{Op: directory.ModDelete, Name: "telephoneNumber"},
						{Op: directory.ModDelete, Name: "mail", Values: vals("old@alder.test")},
						{Op: directory.ModReplace, Name: "description", Values: vals("Replaced wholesale")},
					},
				}
			},
		},
		{
			file: "task-delete.yml",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN:   mustDN(t, "uid=departed,ou=people,dc=alder,dc=test"),
					Type: directory.ChangeDelete,
				}
			},
		},
		{
			file: "task-modrdn.yml",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN:           mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
					Type:         directory.ChangeModRDN,
					NewRDN:       "uid=alice.liddell",
					DeleteOldRDN: true,
				}
			},
		},
		{
			// Rule 6, frozen into a file. The rendering of a password change
			// must carry a placeholder and never the password, and a golden
			// file is the bluntest possible way to keep it that way.
			file: "task-setpassword.yml",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN:          mustDN(t, "uid=alice,ou=people,dc=alder,dc=test"),
					Type:        directory.ChangeSetPassword,
					NewPassword: "correct-horse-battery-staple",
				}
			},
		},
		{
			// A DN needing RFC 4514 escaping, and a value carrying the quote
			// and colon that YAML cares about.
			file: "task-quoting.yml",
			record: func(t *testing.T) directory.ChangeRecord {
				return directory.ChangeRecord{
					DN:   mustDN(t, `cn=Liddell\, Alice,ou=people,dc=alder,dc=test`),
					Type: directory.ChangeModify,
					Mods: []directory.Mod{
						{Op: directory.ModReplace, Name: "description", Values: vals(`He said "hello": then left`)},
						{Op: directory.ModReplace, Name: "title", Values: vals("Directrice général")},
					},
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, err := Task(tc.record(t))
			if err != nil {
				t.Fatalf("Task: %v", err)
			}
			assertGolden(t, tc.file, got)
		})
	}
}

// The playbook wrapper the export puts around one or more tasks.
func TestGoldenPlaybook(t *testing.T) {
	task, err := Task(directory.ChangeRecord{
		DN:   mustDN(t, "uid=carol,ou=people,dc=alder,dc=test"),
		Type: directory.ChangeAdd,
		Attrs: []directory.Attribute{
			{Name: "objectClass", Values: vals("top", "inetOrgPerson")},
			{Name: "cn", Values: vals("Carol Adler")},
			{Name: "sn", Values: vals("Adler")},
		},
	})
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	assertGolden(t, "playbook-wrapper.yml", Playbook(task))
}

// The playbook that enforces live content, which is the export offered on a
// search result and an object table.
func TestGoldenEnforceTasks(t *testing.T) {
	tree := func(t *testing.T) []*directory.Entry {
		t.Helper()
		// Deliberately not in tree order: a search returns the server's order,
		// and the renderer is what puts parents first.
		return []*directory.Entry{
			entry(t, "uid=alice,ou=people,dc=alder,dc=test",
				"objectClass", "top", "objectClass", "inetOrgPerson",
				"uid", "alice", "cn", "Alice Liddell", "sn", "Liddell"),
			entry(t, "dc=alder,dc=test",
				"objectClass", "top", "objectClass", "domain", "dc", "alder"),
			entry(t, "ou=people,dc=alder,dc=test",
				"objectClass", "top", "objectClass", "organizationalUnit", "ou", "people"),
		}
	}

	cases := []struct {
		file    string
		entries func(t *testing.T) []*directory.Entry
		opts    EnforceOptions
	}{
		{
			file:    "enforce-tree.yml",
			entries: tree,
			opts: EnforceOptions{
				Base:   "dc=alder,dc=test",
				Scope:  "sub",
				Filter: "(objectClass=*)",
			},
		},
		{
			// A partial playbook that does not say so reads later as the whole
			// of the subtree.
			file:    "enforce-truncated.yml",
			entries: tree,
			opts: EnforceOptions{
				Base:      "dc=alder,dc=test",
				Scope:     "sub",
				Filter:    "(objectClass=inetOrgPerson)",
				Truncated: true,
			},
		},
		{
			// Rule 6 again, on the other renderer: an entry holding a password
			// must produce a playbook that does not.
			file: "enforce-sensitive.yml",
			entries: func(t *testing.T) []*directory.Entry {
				return []*directory.Entry{
					entry(t, "uid=alice,ou=people,dc=alder,dc=test",
						"objectClass", "inetOrgPerson",
						"cn", "Alice Liddell",
						"sn", "Liddell",
						"userPassword", "{SSHA}bm90LWEtcmVhbC1oYXNo",
					),
				}
			},
			opts: EnforceOptions{Base: "dc=alder,dc=test", Scope: "sub"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got := EnforceTasks(tc.entries(t), nil, tc.opts)
			assertGolden(t, tc.file, got)
		})
	}
}

// Nothing in any golden file may carry secret material.
//
// The golden files above are what a reviewer sees, so this reads them back from
// disk rather than trusting the renderers a second time: if a future change
// ever writes a password into one, regenerating the goldens would otherwise
// bless it silently.
func TestNoGoldenFileCarriesASecret(t *testing.T) {
	forbidden := []string{
		"correct-horse-battery-staple", // the password in task-setpassword
		"bm90LWEtcmVhbC1oYXNo",         // the hash body in enforce-sensitive
		"{SSHA}",                       // any scheme-prefixed hash at all
	}

	entries, err := os.ReadDir(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Skipf("no golden directory yet: %v", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw, readErr := os.ReadFile(filepath.Join("testdata", "golden", e.Name()))
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, secret := range forbidden {
				if strings.Contains(string(raw), secret) {
					t.Errorf("%s contains %q, which is secret material", e.Name(), secret)
				}
			}
		})
	}
}
