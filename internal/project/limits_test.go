package project

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

// Projects arrive over the network (POST /project/plan), so what a file can
// make the reader do is bounded by the file's size, never by the square of
// it. These tests count problems rather than time them: each repetition
// below used to be a problem for every pair.

func person(uid string) string {
	return fmt.Sprintf("dn: uid=%s,ou=people,dc=alder,dc=test\nobjectClass: inetOrgPerson\nuid: %s\ncn: %s\nsn: %s\n\n",
		uid, uid, uid, uid)
}

func invalidOf(t *testing.T, err error) *Invalid {
	t.Helper()
	var inv *Invalid
	if !errors.As(err, &inv) {
		t.Fatalf("want an invalid project, got %v", err)
	}
	return inv
}

// An entry written three times is two problems, each pointing at the first.
func TestAnEntryDescribedAgainIsReportedOnceAgainstTheFirst(t *testing.T) {
	files := valid()
	files["people/a.ldif"] = peopleA + "\n" + peopleA + "\n" + strings.Replace(peopleA, "uid=alice", "UID=Alice", 1)
	inv := invalidOf(t, func() error { _, err := LoadFS(memFiles(t, files)); return err }())
	want := []string{
		"people/a.ldif:7: uid=alice,ou=people,dc=alder,dc=test is already described at people/a.ldif:1",
		"people/a.ldif:13: UID=Alice,ou=people,dc=alder,dc=test is already described at people/a.ldif:1",
	}
	if got := problemsOf(inv); got != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

// Equal is the rule for "the same entry": spellings it equates are one entry,
// and names it tells apart are not reported, however alike they look.
func TestDuplicatesAreFoundByDNEquality(t *testing.T) {
	files := valid()
	entry := func(d string) string { return "dn: " + d + "\nobjectClass: person\n\n" }
	files["people/a.ldif"] = entry(`cn=Liddell\, Alice,ou=people,dc=alder,dc=test`) +
		entry(`cn=Liddell\2C Alice,ou=people,dc=alder,dc=test`) + // escaped differently
		entry("cn=x+uid=y,ou=people,dc=alder,dc=test") +
		entry("UID=y+CN=x,ou=people,dc=alder,dc=test") + // the RDN's assertions the other way round
		entry("cn=x+uid=z,ou=people,dc=alder,dc=test") + // differs
		entry("cn=#4869,ou=people,dc=alder,dc=test") + // a BER value
		entry("cn=Hi,ou=people,dc=alder,dc=test") // never equals a string value
	files["people/b.ldif"] = ""
	text := problemsOf(func() error { _, err := LoadFS(memFiles(t, files)); return err }())
	want := []string{
		`people/a.ldif:4: cn=Liddell\, Alice,ou=people,dc=alder,dc=test is already described at people/a.ldif:1`,
		`people/a.ldif:10: UID=y+CN=x,ou=people,dc=alder,dc=test is already described at people/a.ldif:7`,
	}
	if text != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", text, strings.Join(want, "\n"))
	}
}

// Past maxProblems, the rest are counted in one more problem, and the error
// says how many there are in all.
func TestProblemsAreListedUpToALimitAndTheRestCounted(t *testing.T) {
	files := valid()
	files["people/a.ldif"] = strings.Repeat(person("alice"), 300)
	inv := invalidOf(t, func() error { _, err := LoadFS(memFiles(t, files)); return err }())
	if len(inv.Problems) != maxProblems+1 {
		t.Fatalf("%d problems listed, want %d and one counting the rest", len(inv.Problems), maxProblems+1)
	}
	if last := inv.Problems[maxProblems]; last.File != "" || last.Message != "and 199 more problems, not listed; fix these and validate again" {
		t.Errorf("last problem = %+v", last)
	}
	if inv.Error() != "the project is not valid: 299 problems" {
		t.Errorf("error = %q", inv.Error())
	}
}

// A pattern written twice in a subtree is read once, and a file two patterns
// match there is one file: neither is a problem. A file two subtrees claim
// is, once, however many patterns claim it.
func TestAPatternRepeatedIsReadOnceAndAFileClaimedTwiceIsReportedOnce(t *testing.T) {
	files := valid()
	files["alder.yaml"] = strings.Replace(validYAML, "files: [people/*.ldif]",
		"files: ["+strings.Repeat("people/*.ldif, people/a.ldif, ", 50)+"people/*.ldif]", 1)
	p, err := LoadFS(memFiles(t, files))
	if err != nil {
		t.Fatalf("a repeated pattern was refused:\n%s", problemsOf(err))
	}
	if got := strings.Join(p.Managed[0].Files, ","); got != "people/a.ldif,people/b.ldif" || p.Entries() != 3 {
		t.Errorf("files = %s, entries = %d; want each file once", got, p.Entries())
	}

	files["alder.yaml"] = strings.Replace(files["alder.yaml"], "files: [groups.ldif]",
		"files: [groups.ldif, "+strings.Repeat("people/*.ldif, ", 50)+"people/*.ldif]", 1)
	text := problemsOf(func() error { _, err := LoadFS(memFiles(t, files)); return err }())
	want := []string{
		"alder.yaml: people/a.ldif is matched by both ou=people,dc=alder,dc=test and ou=groups,dc=alder,dc=test; a file belongs to one subtree",
		"alder.yaml: people/b.ldif is matched by both ou=people,dc=alder,dc=test and ou=groups,dc=alder,dc=test; a file belongs to one subtree",
	}
	if text != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", text, strings.Join(want, "\n"))
	}
}

// A subtree written again overlaps its first and nothing else; distinct
// subtrees one below another are still each reported, earlier first.
func TestOverlappingSubtreesAreReportedOncePerPair(t *testing.T) {
	sub := func(base string) string { return "  - base: " + base + "\n    files: [groups.ldif]\n" }
	yaml := "version: 1\nmanaged:\n" +
		sub("ou=people,dc=alder,dc=test") +
		sub("dc=alder,dc=test") +
		sub("OU=People, DC=Alder,dc=test") +
		sub("ou=a,ou=people,dc=alder,dc=test") +
		sub("ou=people,dc=alder,dc=test") +
		sub("ou=groups,dc=other") +
		validYAML[strings.Index(validYAML, "environments:"):]
	files := valid()
	files["alder.yaml"] = yaml
	var got []string
	for _, line := range strings.Split(problemsOf(func() error { _, err := LoadFS(memFiles(t, files)); return err }()), "\n") {
		if strings.Contains(line, "overlap") {
			got = append(got, strings.TrimSuffix(strings.TrimPrefix(line, "alder.yaml: managed subtrees "),
				" overlap; each entry must belong to exactly one"))
		}
	}
	want := []string{
		"ou=people,dc=alder,dc=test and dc=alder,dc=test",
		"ou=people,dc=alder,dc=test and OU=People,DC=Alder,dc=test",
		"ou=people,dc=alder,dc=test and ou=a,ou=people,dc=alder,dc=test",
		"ou=people,dc=alder,dc=test and ou=people,dc=alder,dc=test",
		"dc=alder,dc=test and ou=a,ou=people,dc=alder,dc=test",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("overlaps:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A project larger than one plan is refused as it is read, before its
// records are compared: a thousand copies of one entry past the limit is the
// size, not a thousand problems.
func TestAProjectLargerThanOnePlanIsRefusedBeforeItsRecordsAreChecked(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxRecords-3; i++ { // and alice, bob and admins
		b.WriteString(person(fmt.Sprintf("u%d", i)))
	}
	files := valid()
	files["people/c.ldif"] = b.String()
	if _, err := LoadFS(memFiles(t, files)); err != nil {
		t.Fatalf("a project of exactly %d entries was refused: %v", MaxRecords, problemsOf(err))
	}

	files["people/c.ldif"] += person("u0")
	_, err := LoadFS(memFiles(t, files))
	var tooLarge *TooLarge
	if !errors.As(err, &tooLarge) || tooLarge.Records != MaxRecords+1 {
		t.Fatalf("want TooLarge with %d records, got %v", MaxRecords+1, err)
	}
	if err.Error() != "the project describes 2001 entries, and one plan covers at most 2000" {
		t.Errorf("error = %q", err.Error())
	}
}

// The suffix entry is found once, not once per environment and copy.
func TestTheSuffixEntryIsReportedOncePerEnvironment(t *testing.T) {
	yaml := "version: 1\nsuffix: dc=alder,dc=test\nmanaged:\n  - base: dc=alder,dc=test\n    files: [top.ldif]\nenvironments:\n"
	for _, n := range []string{"a", "b", "c"} {
		yaml += "  " + n + ":\n    api-url: https://x.example.com\n    host: x.example.com\n    suffix: dc=" + n + ",dc=example\n"
	}
	top := "dn: dc=alder,dc=test\nobjectClass: dcObject\ndc: alder\n\n"
	text := problemsOf(func() error {
		_, err := LoadFS(memFiles(t, map[string]string{"alder.yaml": yaml, "top.ldif": top + top + top}))
		return err
	}())
	if got := strings.Count(text, "already described"); got != 2 {
		t.Errorf("%d duplicates reported, want 2:\n%s", got, text)
	}
	if got := strings.Count(text, "would rename"); got != 3 {
		t.Errorf("%d renames reported, want one per environment:\n%s", got, text)
	}
}

// Unmanaged entries are found by the same equality, spellings and all.
func TestUnmanagedMembershipIsDNEquality(t *testing.T) {
	set := dnSet{}
	set.add(dn.MustParse(`cn=Liddell\, Alice+uid=a,ou=people,dc=alder,dc=test`))
	for in, want := range map[string]bool{
		`UID=a+CN=liddell\2c  alice,OU=People,dc=alder,dc=test`: true,
		`cn=Liddell\, Alice,ou=people,dc=alder,dc=test`:         false,
		`cn=Liddell\, Alice+uid=b,ou=people,dc=alder,dc=test`:   false,
	} {
		if got := set.has(dn.MustParse(in)); got != want {
			t.Errorf("has(%s) = %v, want %v", in, got, want)
		}
	}
}

// Every pattern is a directory read and matched in full, so how many a
// project lists is bounded, and nothing is matched past the bound.
func TestAProjectListsABoundedNumberOfPatterns(t *testing.T) {
	patterns := func(n int) string {
		list := make([]string, n)
		for i := range list {
			list[i] = fmt.Sprintf("'people/*[^%d].ldif'", i) // distinct, and each matches a.ldif and b.ldif
		}
		return "files: [" + strings.Join(list, ", ") + "]"
	}
	files := valid()
	// With groups.ldif, exactly the bound.
	files["alder.yaml"] = strings.Replace(validYAML, "files: [people/*.ldif]", patterns(maxPatterns-1), 1)
	if _, err := LoadFS(memFiles(t, files)); err != nil {
		t.Fatalf("%d patterns were refused: %s", maxPatterns, problemsOf(err))
	}

	files["alder.yaml"] = strings.Replace(validYAML, "files: [people/*.ldif]", patterns(maxPatterns), 1)
	inv := invalidOf(t, func() error { _, err := LoadFS(memFiles(t, files)); return err }())
	want := fmt.Sprintf("alder.yaml: managed lists %d file patterns, and a project lists at most %d; "+
		"a pattern such as people/*.ldif matches every file in its directory", maxPatterns+1, maxPatterns)
	if got := problemsOf(inv); got != want {
		t.Errorf("problems:\n%s\nwant:\n%s", got, want)
	}
}

// A project over the size with other problems lists them all, the size among
// them, rather than hiding the rest behind it.
func TestAProjectTooLargeStillListsItsOtherProblems(t *testing.T) {
	var b strings.Builder
	for i := 0; i <= MaxRecords; i++ {
		b.WriteString(person(fmt.Sprintf("u%d", i)))
	}
	files := valid()
	files["people/c.ldif"] = b.String()
	files["people/d.ldif"] = "this is not LDIF\n"
	inv := invalidOf(t, func() error { _, err := LoadFS(memFiles(t, files)); return err }())
	got := problemsOf(inv)
	if !strings.Contains(got, "people/d.ldif") || !strings.Contains(got, "one plan covers at most 2000") {
		t.Errorf("problems:\n%s", got)
	}
}

// path.Match costs a pattern's length times a name's: both are bounded, so
// one long pattern against one long name is not seconds of work.
func TestPatternsAndPathsAreBoundedInLength(t *testing.T) {
	files := valid()
	files["alder.yaml"] = strings.Replace(validYAML, "files: [people/*.ldif]",
		"files: ['people/*"+strings.Repeat("a", maxPatternBytes)+"b']", 1)
	inv := invalidOf(t, func() error { _, err := LoadFS(memFiles(t, files)); return err }())
	if got := problemsOf(inv); !strings.Contains(got, "a pattern with a wildcard holds at most 256") {
		t.Errorf("problems:\n%s", got)
	}
	for _, name := range []string{strings.Repeat("a", 256), strings.Repeat("a/", 2049) + "f"} {
		if err := CheckPath(name); err == nil {
			t.Errorf("CheckPath accepted a path of %d bytes", len(name))
		}
	}
	if err := CheckPath(strings.Repeat("a/", 1900) + strings.Repeat("b", 255)); err != nil {
		t.Errorf("CheckPath refused a deep path within the bounds: %v", err)
	}
}

// NewFS lists every directory above every file, once, however the paths
// share their ancestors.
func TestNewFSListsEachDirectoryOnce(t *testing.T) {
	deep := strings.Repeat("d/", 1500)
	fsys, err := NewFS(map[string][]byte{
		deep + "a.ldif": nil, deep + "b.ldif": nil, "d/x.ldif": nil, "top.ldif": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		".": "d top.ldif", "d": "d x.ldif", strings.TrimSuffix(deep, "/"): "a.ldif b.ldif",
	} {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			t.Fatalf("ReadDir(%.20s): %v", dir, err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if got := strings.Join(names, " "); got != want {
			t.Errorf("ReadDir(%.20s) = %q, want %q", dir, got, want)
		}
	}
}
