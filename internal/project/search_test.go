package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

const lockedSearch = `version: 1
title: Locked accounts
description: Accounts either server holds locked.
base: ou=people,dc=alder,dc=test
filter: (|(pwdAccountLockedTime=*)(nsAccountLock=true))
attributes: [uid, cn, mail, pwdAccountLockedTime]
columns: [uid, mail]
limit: 500
`

func withSearches(searches map[string]string) map[string]string {
	files := valid()
	for name, content := range searches {
		files[name] = content
	}
	return files
}

func TestASearchFileIsFoundByItsSuffixAndRead(t *testing.T) {
	files := withSearches(map[string]string{
		"people/locked.search.yaml": lockedSearch,
		"groups.search.yaml":        "version: 1\nbase: ou=groups,dc=alder,dc=test\nfilter: (objectClass=groupOfNames)\n",
		// Not a search: another suffix, and a dot-directory.
		"people/notes.yaml":            "version: 1\n",
		".github/ci.search.yaml":       "not: [even, valid",
		"people/.drafts/x.search.yaml": "not: [even, valid",
	})
	for _, from := range []string{"disk", "memory"} {
		t.Run(from, func(t *testing.T) {
			var p *Project
			var err error
			if from == "disk" {
				p, err = load(t, write(t, files))
			} else {
				p, err = LoadFS(memFiles(t, files))
			}
			if err != nil {
				t.Fatalf("%v", problemsOf(err))
			}
			if got := strings.Join(p.SearchNames(), ","); got != "groups,people/locked" {
				t.Fatalf("searches = %s", got)
			}
			s, ok := p.SearchNamed("people/locked")
			if !ok {
				t.Fatal("people/locked not found by name")
			}
			if s.File != "people/locked.search.yaml" || s.Title != "Locked accounts" || s.Scope != "sub" || s.Limit != 500 ||
				s.Base.String() != "ou=people,dc=alder,dc=test" ||
				s.Filter != "(|(pwdAccountLockedTime=*)(nsAccountLock=true))" ||
				strings.Join(s.Attributes, ",") != "uid,cn,mail,pwdAccountLockedTime" || strings.Join(s.Columns, ",") != "uid,mail" {
				t.Errorf("people/locked = %+v", s)
			}
			g, _ := p.SearchNamed("groups")
			if g.Limit != DefaultSearchLimit || len(g.Attributes) != 0 || len(g.Columns) != 0 {
				t.Errorf("defaults: %+v", g)
			}
		})
	}
}

// A search is not state: adding one changes nothing a plan, or a saved
// plan's digest, is made from.
func TestASearchFileDoesNotChangeTheDocument(t *testing.T) {
	without, err := load(t, write(t, valid()))
	if err != nil {
		t.Fatal(problemsOf(err))
	}
	with, err := load(t, write(t, withSearches(map[string]string{"people/locked.search.yaml": lockedSearch})))
	if err != nil {
		t.Fatal(problemsOf(err))
	}
	a, _ := without.Document()
	b, _ := with.Document()
	if a != b {
		t.Errorf("a search file changed the document:\n%s\n---\n%s", a, b)
	}
}

func TestTheDefaultsFillEachOtherIn(t *testing.T) {
	for _, tc := range []struct{ body, attrs, cols string }{
		{"", "", ""},
		{"columns: [uid, mail]\n", "uid,mail,objectClass", "uid,mail"},
		{"columns: [uid, objectclass]\n", "uid,objectclass", "uid,objectclass"},
		{"attributes: [uid, mail, uid]\n", "uid,mail", "uid,mail"},
	} {
		s, problems := ParseSearch("x.search.yaml",
			[]byte("version: 1\nbase: dc=alder,dc=test\nfilter: (uid=*)\n"+tc.body), nil)
		if len(problems) > 0 {
			t.Fatalf("%q: %v", tc.body, problems)
		}
		if strings.Join(s.Attributes, ",") != tc.attrs || strings.Join(s.Columns, ",") != tc.cols {
			t.Errorf("%q: attributes %v, columns %v", tc.body, s.Attributes, s.Columns)
		}
	}
}

func TestEverySearchRuleIsEnforced(t *testing.T) {
	head := "version: 1\nbase: ou=people,dc=alder,dc=test\nfilter: (uid=*)\n"
	for _, tc := range []struct {
		name, body, want string
	}{
		{"empty", "", "people/x.search.yaml: the file is empty"},
		{"unknown key", head + "colour: blue\n", "field colour not found"},
		{"key given twice", head + "limit: 5\nlimit: 6\n", `mapping key "limit" already defined`},
		{"keys read as written", "Version: 1\n", "field Version not found"},
		{"two documents", head + "---\n" + head, "more than one YAML document"},
		{"another version", "version: 2\nbase: dc=x\nfilter: (a=b)\n", "people/x.search.yaml:1: version must be 1, and is 2"},
		{"no version", "base: dc=x\nfilter: (a=b)\n", "version must be 1, and is 0"},
		{"no base", "version: 1\nfilter: (uid=*)\n", "base is required"},
		{"a base that is not a DN", "version: 1\nbase: people\nfilter: (uid=*)\n", `people/x.search.yaml:2: base "people" is not a DN`},
		{"no filter", "version: 1\nbase: dc=alder,dc=test\n", "filter is required"},
		{"a filter that does not parse", "version: 1\nbase: dc=alder,dc=test\nfilter: (uid=*\n",
			"people/x.search.yaml:3: filter is not a valid RFC 4515 filter"},
		{"a filter too long", "version: 1\nbase: dc=alder,dc=test\nfilter: (uid=" + strings.Repeat("a", 9000) + ")\n",
			"and a saved filter holds at most 8192"},
		{"a scope", head + "scope: deep\n", `scope must be base, one or sub, and is "deep"`},
		{"a limit too large", head + "limit: 10001\n", "people/x.search.yaml:4: limit must be between 1 and 10000, and is 10001"},
		{"a limit too small", head + "limit: 0\n", "limit must be between 1 and 10000, and is 0"},
		{"not a name", head + "attributes: [uid, \"(cn)\"]\n", `attributes: "(cn)" is not an attribute name`},
		{"an empty option", head + "attributes: [\"cn;\"]\n", `attributes: "cn;" is not an attribute name`},
		{"a secret", head + "attributes: [uid, userPassword]\n", "attributes: userPassword is a secret"},
		{"a secret by an option", head + "columns: [userPassword;binary]\n", "columns: userPassword;binary is a secret"},
		{"a column not asked for", head + "attributes: [uid]\ncolumns: [uid, mail]\n",
			"people/x.search.yaml:5: column mail is not in attributes"},
		{"too many attributes", head + "attributes: [" + strings.TrimSuffix(strings.Repeat("cn, ", 101), ", ") + "]\n",
			"attributes lists 101 attributes, and a search lists at most 100"},
		{"outside the suffix", "version: 1\nbase: ou=people,dc=elsewhere\nfilter: (uid=*)\n",
			"base ou=people,dc=elsewhere is outside the project's suffix dc=alder,dc=test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := withSearches(map[string]string{"people/x.search.yaml": tc.body})
			files["alder.yaml"] = "suffix: dc=alder,dc=test\n" + validYAML
			_, diskErr := load(t, write(t, files))
			_, memErr := LoadFS(memFiles(t, files))
			var inv *Invalid
			if !errors.As(memErr, &inv) {
				t.Fatalf("want an invalid project, got %v", memErr)
			}
			got := problemsOf(memErr)
			if !strings.Contains(got, tc.want) {
				t.Errorf("problems:\n%s\nwant one containing %q", got, tc.want)
			}
			if disk := problemsOf(diskErr); disk != got {
				t.Errorf("in memory:\n%s\non disk:\n%s", got, disk)
			}
		})
	}
}

func TestSearchNamesAreUniqueWithoutRegardToCase(t *testing.T) {
	body := "version: 1\nbase: dc=alder,dc=test\nfilter: (uid=*)\n"
	_, err := LoadFS(memFiles(t, withSearches(map[string]string{
		"people/locked.search.yaml": body,
		"People/Locked.search.yaml": body,
	})))
	if got := problemsOf(err); !strings.Contains(got, "people/locked.search.yaml: names the same search as People/Locked.search.yaml") {
		t.Errorf("problems:\n%s", got)
	}
}

func TestASearchFileHasANameAndABound(t *testing.T) {
	body := "version: 1\nbase: dc=alder,dc=test\nfilter: (uid=*)\n"
	_, err := LoadFS(memFiles(t, withSearches(map[string]string{
		".search.yaml":           body,
		"people/big.search.yaml": body + "description: " + strings.Repeat("a", maxSearchFileBytes) + "\n",
	})))
	got := problemsOf(err)
	for _, want := range []string{
		".search.yaml: has no name before .search.yaml",
		"people/big.search.yaml: is larger than 65536 bytes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("problems:\n%s\nwant %q", got, want)
		}
	}
}

func TestAProjectHoldsAtMostSoManySearches(t *testing.T) {
	searches := map[string]string{}
	for i := 0; i <= MaxSearches; i++ {
		searches[fmt.Sprintf("s/%03d.search.yaml", i)] = "version: 1\nbase: dc=alder,dc=test\nfilter: (uid=*)\n"
	}
	_, err := LoadFS(memFiles(t, withSearches(searches)))
	if got := problemsOf(err); !strings.Contains(got, "the project holds 201 .search.yaml files, and a project holds at most 200") {
		t.Errorf("problems:\n%s", got)
	}
}

func TestASearchFileLinkedFromOutsideIsRefused(t *testing.T) {
	dir := write(t, valid())
	outside := filepath.Join(t.TempDir(), "secret.search.yaml")
	if err := os.WriteFile(outside, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.search.yaml")); err != nil {
		t.Skipf("symlinks: %v", err)
	}
	_, err := load(t, dir)
	if got := problemsOf(err); !strings.Contains(got, "linked.search.yaml: resolves to a file outside the project directory") {
		t.Errorf("problems:\n%s", got)
	}
}

func TestAnEnvironmentMovesTheBaseAndNotTheFilter(t *testing.T) {
	files := withSearches(map[string]string{
		"people/managed.search.yaml": "version: 1\nbase: ou=people,dc=alder,dc=test\n" +
			"filter: (manager=uid=ana,ou=people,dc=alder,dc=test)\n",
		"people/plain.search.yaml": "version: 1\nbase: ou=people,dc=alder,dc=test\nfilter: (uid=*)\n",
	})
	files["alder.yaml"] = "suffix: dc=alder,dc=test\n" + strings.Replace(validYAML,
		"    bind-password-env: ALDER_DEV_BIND_PASSWORD\n",
		"    bind-password-env: ALDER_DEV_BIND_PASSWORD\n    suffix: dc=dev,dc=alder,dc=test\n", 1)
	p, err := LoadFS(memFiles(t, files))
	if err != nil {
		t.Fatal(problemsOf(err))
	}
	managed, _ := p.SearchNamed("people/managed")
	moved, left := p.SearchForEnvironment("dev", managed)
	if moved.Base.String() != "ou=people,dc=dev,dc=alder,dc=test" || moved.Filter != managed.Filter || !left {
		t.Errorf("dev: base %s, filter %s, left as written %v", moved.Base, moved.Filter, left)
	}
	if managed.Base.String() != "ou=people,dc=alder,dc=test" {
		t.Errorf("the project's own search was changed: %s", managed.Base)
	}
	plain, _ := p.SearchNamed("people/plain")
	if _, left := p.SearchForEnvironment("dev", plain); left {
		t.Error("a filter naming no DN was reported as left as written")
	}
	if same, left := p.SearchForEnvironment("nowhere", plain); !same.Base.Equal(plain.Base) || left {
		t.Errorf("an environment without a suffix moved the base to %s", same.Base)
	}
}

func TestIsAttributeDescription(t *testing.T) {
	for _, ok := range []string{"cn", "mail", "pwdAccountLockedTime", "2.5.4.3", "cn;lang-en", "userCertificate;binary"} {
		if !IsAttributeDescription(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "1cn", "cn=", "(cn)", "cn;", "cn;lang en", "2.5..4", "*"} {
		if IsAttributeDescription(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

// FuzzParseSearch holds the reader to never panicking and to never
// returning a search it found a problem in, on any input.
func FuzzParseSearch(f *testing.F) {
	f.Add([]byte(lockedSearch))
	f.Add([]byte("version: 1\nbase: dc=x\nfilter: (a=b)\ncolumns: [a]\n"))
	f.Add([]byte("version: 1\nlimit: -1\n---\nversion: 1\n"))
	f.Add([]byte("{version: 1, base: \"dc=x\", filter: \"(&)\", attributes: [\"a;b\"]}"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		s, problems := ParseSearch("f.search.yaml", raw, dn.DN{})
		if len(problems) > 0 {
			return
		}
		if s.Base.IsEmpty() || s.Filter == "" || s.Limit < 1 || s.Limit > MaxSearchLimit {
			t.Fatalf("accepted %+v from %q", s, raw)
		}
		for _, c := range s.Columns {
			if !containsFold(s.Attributes, c) {
				t.Fatalf("column %s not in %v", c, s.Attributes)
			}
		}
	})
}

// The web interface's Save as file writes this (web/src/lib/saved-search.ts,
// and its test): JSON-quoted scalars and flow lists. It has to read back as
// written, since validate is the only check of it.
func TestWhatTheBrowserSavesReadsBack(t *testing.T) {
	saved := strings.Join([]string{
		"# An Alder saved search. Commit it to a project, and check it with",
		"# alder project validate.",
		"version: 1",
		`title: "Quoted: \"title\""`,
		`base: "ou=people,dc=alder,dc=test"`,
		"scope: one",
		`filter: "(&(cn=a: \"b\")(description=#[x]))"`,
		`attributes: ["uid", "cn"]`,
		`columns: ["cn"]`,
		"limit: 50",
		"",
	}, "\n")
	s, problems := ParseSearch("saved.search.yaml", []byte(saved), nil)
	if len(problems) > 0 {
		t.Fatalf("%v", problems)
	}
	if s.Title != `Quoted: "title"` || s.Filter != `(&(cn=a: "b")(description=#[x]))` || s.Scope != "one" || s.Limit != 50 ||
		strings.Join(s.Attributes, ",") != "uid,cn" || strings.Join(s.Columns, ",") != "cn" {
		t.Errorf("read back as %+v", s)
	}
}
