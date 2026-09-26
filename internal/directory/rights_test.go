package directory

import "testing"

// The server's own answer, parsed. The strings here are what 389 Directory
// Server actually returned for the harness's svc-alder on an entry the acis
// restrict, copied from the wire rather than invented.

const harnessAttributeRights = "objectClass:rsc, aci:rsc, sn:rsc, cn:rsc, userPassword:none, " +
	"uid:rsc, alderTeam:none, alderOnCall:rsc"

func TestAttributeLevelRightsAreReadAndSorted(t *testing.T) {
	rights := ParseAttributeLevelRights(harnessAttributeRights)
	if len(rights) != 8 {
		t.Fatalf("eight attributes, got %d: %+v", len(rights), rights)
	}
	byName := map[string]string{}
	for _, r := range rights {
		byName[r.Name] = r.Rights
	}
	// The two the harness's acis take away, and one it leaves alone.
	if byName["userPassword"] != "none" || byName["alderTeam"] != "none" {
		t.Errorf("the denied attributes are not reported as denied: %+v", byName)
	}
	if byName["cn"] != "rsc" {
		t.Errorf("cn: %q", byName["cn"])
	}
	for i := 1; i < len(rights); i++ {
		if rights[i-1].Name > rights[i].Name && !equalFold(rights[i-1].Name, rights[i].Name) {
			// Sorted, so a person can find an attribute in a list of eighty.
			if lower(rights[i-1].Name) > lower(rights[i].Name) {
				t.Errorf("out of order: %q before %q", rights[i-1].Name, rights[i].Name)
			}
		}
	}
}

func TestRightsThatCannotBeSplitAreSkippedRatherThanGuessed(t *testing.T) {
	rights := ParseAttributeLevelRights("cn:rsc, something-with-no-colon, :rsc, mail:rsc")
	if len(rights) != 2 {
		t.Fatalf("only the two readable pairs: %+v", rights)
	}
}

func TestLettersAreGlossedAndNeverTranslatedAway(t *testing.T) {
	r := &EffectiveRights{Entry: "vadn"}
	words := r.EntryWords()
	if len(words) != 4 || words[0] != "view this entry" {
		t.Errorf("entry rights: %v", words)
	}
	if got := AttributeWords("rsc"); len(got) != 3 || got[0] != "read" {
		t.Errorf("attribute rights: %v", got)
	}
	// "none" is an answer, and it is not a letter.
	if got := AttributeWords("none"); len(got) != 0 {
		t.Errorf("none should gloss to nothing at all: %v", got)
	}
	// A letter this release has never seen is shown as it came, not dropped:
	// a right nobody here recognises still exists on the server.
	if got := AttributeWords("rz"); len(got) != 2 || got[1] != "z" {
		t.Errorf("unknown letters must survive: %v", got)
	}
}

func TestANumberWhereTheLettersGoIsNotAnAnswer(t *testing.T) {
	// 389 DS answers a request it cannot compute with a code in place of the
	// rights. "12" glossed as two unknown letters would read as a verdict.
	for _, code := range []string{"12", "1", "0"} {
		if !RightsErrorCode(code) {
			t.Errorf("%q is a code, not rights", code)
		}
	}
	for _, rights := range []string{"v", "rsc", "none", "", "rscwo"} {
		if RightsErrorCode(rights) {
			t.Errorf("%q is rights, not a code", rights)
		}
	}
}

func equalFold(a, b string) bool { return lower(a) == lower(b) }

func lower(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}
