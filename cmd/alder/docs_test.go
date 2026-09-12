package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Documentation that describes a version Alder is no longer at.
//
// SECURITY.md said "Pre-1.0" through 1.0, 1.1, 1.2 and 1.3, and
// docs/COMPATIBILITY.md named a release that a change had not landed in. Both
// are the same failure: a statement true when it was written, with nothing
// watching it. Prose cannot be checked automatically, but a version number in
// it can be, and a version number is what went wrong both times.

// releaseVersion is what release-please says this repository is at. It is the
// same file the release workflow reads, so this cannot disagree with a release.
func releaseVersion(t *testing.T) (major, minor int) {
	t.Helper()
	raw, err := os.ReadFile(repoFile(t, ".release-please-manifest.json"))
	if err != nil {
		t.Fatalf("reading the release manifest: %v", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parsing the release manifest: %v", err)
	}
	v, ok := manifest["."]
	if !ok {
		t.Fatalf("the release manifest has no root version: %s", raw)
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		t.Fatalf("the release version %q is not major.minor.patch", v)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("the major version in %q is not a number", v)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("the minor version in %q is not a number", v)
	}
	return major, minor
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	// The test runs in cmd/alder.
	return filepath.Join("..", "..", filepath.FromSlash(rel))
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(repoFile(t, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(raw)
}

// SECURITY.md's supported-versions section has to name the series that is
// actually shipping. It said "Pre-1.0" for four minor releases after 1.0.
func TestSecurityPolicyNamesTheCurrentSeries(t *testing.T) {
	major, _ := releaseVersion(t)
	doc := readRepoFile(t, "SECURITY.md")

	_, after, found := strings.Cut(doc, "## Supported versions")
	if !found {
		t.Fatal("SECURITY.md has no \"## Supported versions\" section")
	}
	section := after
	if next := strings.Index(after, "\n## "); next >= 0 {
		section = after[:next]
	}

	want := strconv.Itoa(major) + ".x"
	if !strings.Contains(section, want) {
		t.Errorf("SECURITY.md's supported versions do not mention %s, and the "+
			"release manifest says this is a %s release:\n%s", want, want, section)
	}
	if strings.Contains(strings.ToLower(section), "pre-1.0") && major >= 1 {
		t.Errorf("SECURITY.md still says pre-1.0 at version %d.x:\n%s", major, section)
	}
}

// A "before X.Y.Z" note in the compatibility document is a claim about a
// release that has happened. One naming a version this repository has not
// reached is either a typo or a note written against a release that was
// predicted and then scored differently -- which is how "before 1.4.0" came to
// describe a change that shipped in 1.3.1.
func TestCompatibilityDoesNotNameAnUnreleasedVersion(t *testing.T) {
	major, minor := releaseVersion(t)
	doc := readRepoFile(t, "docs/COMPATIBILITY.md")

	// Deliberately narrow: only versions in a sentence about what a release
	// did, not every number that looks like one. "1.0.x" as an illustration of
	// a patch series is not a claim.
	claim := regexp.MustCompile(`(?i)\b(?:before|since|in|as of)\s+(\d+)\.(\d+)\.(\d+)\b`)
	for _, m := range claim.FindAllStringSubmatch(doc, -1) {
		gotMajor, _ := strconv.Atoi(m[1])
		gotMinor, _ := strconv.Atoi(m[2])
		if gotMajor > major || (gotMajor == major && gotMinor > minor) {
			t.Errorf("docs/COMPATIBILITY.md says %q, but this repository is at %d.%d",
				strings.TrimSpace(m[0]), major, minor)
		}
	}
}

// The compatibility document promises that flags and their environment
// equivalents keep their meanings. That promise had no implementation for four
// releases. This is the check that it has one.
func TestTheCompatibilityPromiseAboutEnvironmentVariablesIsImplemented(t *testing.T) {
	doc := readRepoFile(t, "docs/COMPATIBILITY.md")
	if !strings.Contains(doc, "environment-variable") {
		t.Skip("the compatibility document no longer promises environment equivalents")
	}
	// One flag is enough to prove the mechanism exists; TestEveryServeFlagReadsAVariable
	// proves it covers all of them.
	if got := envName("addr"); got != "ALDER_ADDR" {
		t.Fatalf("the environment mechanism is not wired up: envName(\"addr\") = %q", got)
	}
}

// README claims Alder can be pointed at any directory unless restricted. Once
// the allowlist exists, the security policy has to say so: its "what Alder does
// not defend against" section is the place an operator looks to find out.
func TestSecurityPolicyMentionsTheTargetAllowlist(t *testing.T) {
	doc := readRepoFile(t, "SECURITY.md")
	if !strings.Contains(doc, "--allowed-targets") {
		t.Error("SECURITY.md does not mention --allowed-targets, so an operator " +
			"reading it cannot find out that the restriction exists")
	}
}
