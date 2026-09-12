package outline

import (
	"strings"
	"testing"
)

func withAttrs(t *testing.T, d, structural string, pairs ...[]string) YAMLEntry {
	t.Helper()
	e := at(t, d, structural)
	out := YAMLEntry{DN: e.DN, Structural: e.Structural}
	for _, p := range pairs {
		vals := make([][]byte, 0, len(p)-1)
		for _, v := range p[1:] {
			vals = append(vals, []byte(v))
		}
		out.Attributes = append(out.Attributes, YAMLAttribute{Name: p[0], Values: vals})
	}
	return out
}

func yamlTree(t *testing.T) []YAMLEntry {
	t.Helper()
	return []YAMLEntry{
		withAttrs(t, "uid=alice,ou=people,"+suffix, "inetOrgPerson",
			[]string{"objectClass", "top", "inetOrgPerson"},
			[]string{"uid", "alice"},
			[]string{"cn", "Alice Liddell"},
			[]string{"mail", "alice@alder.test", "a.liddell@alder.test"}),
		withAttrs(t, suffix, "domain",
			[]string{"objectClass", "top", "domain"},
			[]string{"dc", "alder"}),
		withAttrs(t, "ou=people,"+suffix, "organizationalUnit",
			[]string{"objectClass", "top", "organizationalUnit"},
			[]string{"ou", "people"}),
	}
}

func TestGoldenYAML(t *testing.T) {
	got := RenderYAML(yamlTree(t), Options{Base: suffix, Scope: "sub", Filter: "(objectClass=*)"})
	assertGolden(t, "tree.yaml", got)
}

// The values that break hand-written YAML: a colon, a quote, a leading space,
// something that looks like a boolean, something that looks like a number, and
// bytes that are not text at all.
func TestGoldenYAMLQuotesTheAwkwardValues(t *testing.T) {
	e := withAttrs(t, "cn=awkward,"+suffix, "device",
		[]string{"colon", "host: value"},
		[]string{"quote", `he said "no"`},
		[]string{"leading", " indented"},
		[]string{"boolean", "no"},
		[]string{"numeric", "0755"},
		[]string{"empty", ""},
		[]string{"newline", "one\ntwo"},
	)
	// A JPEG's opening bytes, which are not text.
	e.Attributes = append(e.Attributes, YAMLAttribute{
		Name:   "photo",
		Values: [][]byte{{0xff, 0xd8, 0xff, 0x00, 0x01}},
	})

	got := RenderYAML([]YAMLEntry{e}, Options{Base: suffix, Scope: "base"})
	assertGolden(t, "awkward-values.yaml", got)

	// "no" is YAML 1.1's false and the reason unquoted YAML cannot be trusted.
	if !strings.Contains(got, `- "no"`) {
		t.Errorf("a value that reads as a boolean was not quoted:\n%s", got)
	}
	if !strings.Contains(got, `- "0755"`) {
		t.Errorf("a value that reads as a number was not quoted:\n%s", got)
	}
	if !strings.Contains(got, "!!binary") {
		t.Errorf("bytes that are not text were not tagged binary:\n%s", got)
	}
}

// Every attribute is a list, even the single-valued ones: a shape that changed
// with the data would make a second value look like a type change.
func TestEveryAttributeIsAList(t *testing.T) {
	got := RenderYAML(yamlTree(t), Options{Base: suffix})

	for _, single := range []string{`"uid":`, `"dc":`} {
		i := strings.Index(got, single)
		if i < 0 {
			t.Fatalf("%s missing:\n%s", single, got)
		}
		rest := got[i+len(single):]
		if !strings.HasPrefix(rest, "\n") {
			t.Errorf("%s has an inline value rather than a list: %q", single, rest[:20])
		}
	}
}

// The nesting is the point: an editor folds it, so a subtree collapses to one
// line.
func TestChildrenAreNestedUnderTheirParent(t *testing.T) {
	got := RenderYAML(yamlTree(t), Options{Base: suffix})

	base := strings.Index(got, `dn: "dc=alder,dc=test"`)
	people := strings.Index(got, `dn: "ou=people,dc=alder,dc=test"`)
	alice := strings.Index(got, `dn: "uid=alice,ou=people,dc=alder,dc=test"`)
	if base < 0 || people < 0 || alice < 0 {
		t.Fatalf("an entry is missing:\n%s", got)
	}
	if base >= people || people >= alice {
		t.Error("the entries are not nested parent before child")
	}
	if !strings.Contains(got, "children:") {
		t.Errorf("nothing is nested:\n%s", got)
	}
}

// It says what it is. A YAML file full of directory content looks like
// something you could apply, and nothing reads this back.
func TestTheYAMLSaysNothingReadsItBack(t *testing.T) {
	got := RenderYAML(yamlTree(t), Options{Base: suffix})
	if !strings.Contains(got, "nothing reads this back") {
		t.Errorf("the YAML does not say it is for reading:\n%s", got)
	}
}

// Stable whatever order the server returned, so two exports of an unchanged
// directory diff to nothing.
func TestTheYAMLIsStableAcrossServerOrder(t *testing.T) {
	forward := RenderYAML(yamlTree(t), Options{Base: suffix})
	reversed := yamlTree(t)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	if backward := RenderYAML(reversed, Options{Base: suffix}); backward != forward {
		t.Error("the order the server returned changed the document")
	}
}
