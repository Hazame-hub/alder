package outline

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// assertGolden compares got against testdata/golden/<name>, or rewrites it
// under -update.
//
// The outline is a thing people paste into tickets, so its exact shape is worth
// pinning for the same reason the LDIF and the Ansible are.
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
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\nrun \"go test ./internal/outline -update\" to create it", path, err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Errorf("the outline changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func at(t *testing.T, s, structural string) Entry {
	t.Helper()
	parsed, err := dn.Parse(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return Entry{DN: parsed, Structural: structural}
}

const suffix = "dc=alder,dc=test"

func tree(t *testing.T) []Entry {
	t.Helper()
	// Deliberately not in tree order: a search returns the server's order, and
	// the outline is what makes a shape out of it.
	return []Entry{
		at(t, "uid=bob,ou=people,"+suffix, "inetOrgPerson"),
		at(t, suffix, "domain"),
		at(t, "ou=people,"+suffix, "organizationalUnit"),
		at(t, "uid=alice,ou=people,"+suffix, "inetOrgPerson"),
		at(t, "ou=groups,"+suffix, "organizationalUnit"),
		at(t, "cn=platform,ou=groups,"+suffix, "groupOfNames"),
	}
}

func TestGoldenOutline(t *testing.T) {
	got := Render(tree(t), Options{Base: suffix, Scope: "sub", Filter: "(objectClass=*)"})
	assertGolden(t, "subtree.txt", got)
}

// A DN with an escaped comma is one component, not two. Cutting at the first
// comma would file this entry under a parent that does not exist.
func TestGoldenOutlineWithAnEscapedComma(t *testing.T) {
	entries := []Entry{
		at(t, suffix, "domain"),
		at(t, "ou=people,"+suffix, "organizationalUnit"),
		at(t, `cn=Liddell\, Alice,ou=people,`+suffix, "inetOrgPerson"),
	}
	got := Render(entries, Options{Base: suffix, Scope: "sub"})
	assertGolden(t, "escaped-comma.txt", got)

	if !strings.Contains(got, `└── cn=Liddell\, Alice`) {
		t.Errorf("the escaped RDN is not a single child:\n%s", got)
	}
}

// A truncated tree that does not say so reads later as the whole subtree.
func TestGoldenOutlineSaysWhenItIsPartial(t *testing.T) {
	got := Render(tree(t), Options{Base: suffix, Scope: "sub", Truncated: true, Limit: 6})
	assertGolden(t, "truncated.txt", got)
}

// An entry whose parent did not match the filter still has to appear: it is in
// the file, so it is in the picture of the file.
func TestAnEntryWithNoParentInTheSetIsARoot(t *testing.T) {
	entries := []Entry{
		at(t, "uid=alice,ou=people,"+suffix, "inetOrgPerson"),
		at(t, "uid=bob,ou=elsewhere,"+suffix, "inetOrgPerson"),
	}
	got := Render(entries, Options{Base: suffix, Scope: "sub", Filter: "(uid=*)"})

	for _, want := range []string{
		"uid=alice,ou=people," + suffix,
		"uid=bob,ou=elsewhere," + suffix,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%s is missing from the outline:\n%s", want, got)
		}
	}
}

// The count is what makes a container worth looking at.
func TestContainersReportWhatIsBelowThem(t *testing.T) {
	got := Render(tree(t), Options{Base: suffix})

	if !strings.Contains(got, "5 below") {
		t.Errorf("the base does not report its five descendants:\n%s", got)
	}
	if !strings.Contains(got, "ou=people  [organizationalUnit, 2 below]") {
		t.Errorf("ou=people does not report its two:\n%s", got)
	}
	if strings.Contains(got, "0 below") {
		t.Errorf("a leaf claims a count:\n%s", got)
	}
}

// It says what it is, because a file that looks like an export and is not one
// is worse than no file.
func TestTheOutlineSaysItIsNotLdif(t *testing.T) {
	got := Render(tree(t), Options{Base: suffix})
	if !strings.Contains(got, "not LDIF") {
		t.Errorf("the outline does not say it is not LDIF:\n%s", got)
	}
}

// Rendering is stable: siblings are sorted, so two exports of an unchanged
// directory diff to nothing whatever order the server returned.
func TestSiblingsAreSortedSoTheOutlineIsStable(t *testing.T) {
	forward := Render(tree(t), Options{Base: suffix})

	reversed := tree(t)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if backward := Render(reversed, Options{Base: suffix}); backward != forward {
		t.Errorf("the order the server returned changed the outline:\n%s\nvs\n%s",
			forward, backward)
	}
}

func TestAnEmptySetRendersAHeaderAndNothingElse(t *testing.T) {
	got := Render(nil, Options{Base: suffix})
	if !strings.Contains(got, "# 0 entries") {
		t.Errorf("an empty outline does not say so:\n%s", got)
	}
}
