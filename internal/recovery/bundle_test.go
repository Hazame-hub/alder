package recovery

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hazame-hub/alder/internal/directory"
)

var fixedTime = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

// A changeset of three: add a parent, add a child under it, modify the child.
func threeSteps(t *testing.T) []Step {
	t.Helper()
	sch := testSchema(t)
	parent := "ou=projects,dc=alder,dc=test"
	child := "cn=alder,ou=projects,dc=alder,dc=test"
	addParent := directory.ChangeRecord{DN: mustDN(t, parent), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("organizationalUnit")}},
	}}
	addChild := directory.ChangeRecord{DN: mustDN(t, child), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("person")}},
		{Name: "sn", Values: [][]byte{[]byte("Tree")}},
	}}
	modifyChild := directory.ChangeRecord{DN: mustDN(t, child), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModAdd, "description", "grown")}}
	childAfterAdd := entry(t, child, []string{"objectClass", "person"}, []string{"sn", "Tree"}, []string{"cn", "alder"})
	return []Step{
		Derive(0, addParent, nil, sch, KindData),
		Derive(1, addChild, nil, sch, KindData),
		Derive(2, modifyChild, childAfterAdd, sch, KindData),
	}
}

func encoded(t *testing.T, b *Bundle) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, b); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestBundleRoundTripsAndCompensatesInReverse(t *testing.T) {
	b, err := New(Origin{Vendor: "OpenLDAP", NamingContexts: []string{"dc=alder,dc=test"}}, threeSteps(t), fixedTime)
	if err != nil {
		t.Fatal(err)
	}
	if b.Format != Format || b.Version != Version || b.Recoverability != Exact || !strings.HasPrefix(b.Checksum, "sha256:") {
		t.Fatalf("bundle header %+v", b)
	}

	decoded, integrity, err := Decode(encoded(t, b))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if integrity != IntegrityVerified || decoded.Checksum != b.Checksum {
		t.Errorf("integrity %s", integrity)
	}

	// The child removed -- its edit folded into that delete's expectation --
	// then the parent: the order the changes ran, reversed, not sorted by DN.
	changes := decoded.Changes()
	var got []string
	for _, c := range changes {
		got = append(got, c.Type+" "+c.DN)
	}
	want := []string{
		"delete cn=alder,ou=projects,dc=alder,dc=test",
		"delete ou=projects,dc=alder,dc=test",
	}
	if !equalStrings(got, want) {
		t.Errorf("compensation order\n got %v\nwant %v", got, want)
	}
	for i, c := range changes {
		if _, _, err := c.Record(); err != nil {
			t.Errorf("change %d does not convert: %v", i, err)
		}
	}
}

func TestTheChecksumIgnoresOnlyCreatedAt(t *testing.T) {
	a, _ := New(Origin{NamingContexts: []string{}}, threeSteps(t), fixedTime)
	b, _ := New(Origin{NamingContexts: []string{}}, threeSteps(t), fixedTime.Add(time.Hour))
	if a.Checksum != b.Checksum {
		t.Error("two bundles differing only in createdAt have different checksums")
	}
}

func TestDecodeRefuses(t *testing.T) {
	good, err := New(Origin{NamingContexts: []string{"dc=alder,dc=test"}}, threeSteps(t), fixedTime)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(encoded(t, good))

	// resealed builds a bundle whose checksum is right for bad content, so the
	// refusal is the validation's and not the checksum's.
	resealed := func(edit func(b *Bundle)) string {
		copyOf, _, err := Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		edit(copyOf)
		copyOf.Checksum = ""
		sum, err := copyOf.contentChecksum()
		if err != nil {
			t.Fatal(err)
		}
		copyOf.Checksum = sum
		return string(encoded(t, copyOf))
	}
	sensitiveAdd := resealed(func(b *Bundle) {
		b.Steps[0].Compensation = []Change{{DN: "cn=x,dc=alder,dc=test", Type: "add", Attributes: []Attribute{
			{Name: "objectClass", Values: encode([][]byte{[]byte("person")})},
			{Name: "userPassword", Values: encode([][]byte{[]byte(secret)})},
		}}}
	})

	cases := []struct {
		name  string
		input string
		code  string
	}{
		{"not JSON", "hello", CodeNotBundle},
		{"a snapshot", `{"format":"alder-snapshot","version":1}`, CodeNotBundle},
		{"a later version", strings.Replace(raw, `"version": 1`, `"version": 2`, 1), CodeUnsupportedVersion},
		{"an unknown field", strings.Replace(raw, `"format": "alder-recovery",`, `"format": "alder-recovery", "execute": true,`, 1), CodeInvalid},
		{"trailing content", raw + `{"format":"alder-recovery"}`, CodeInvalid},
		{"a tampered value", strings.Replace(raw, `"grown"`, `"planted"`, 1), CodeChecksumMismatch},
		{"a tampered DN", strings.Replace(raw, `"ou=projects,dc=alder,dc=test"`, `"dc=alder,dc=test"`, 1), CodeChecksumMismatch},
		{"a sensitive value, resealed", sensitiveAdd, CodeInvalid},
		{"a password change as compensation", resealed(func(b *Bundle) {
			b.Steps[0].Compensation = []Change{{DN: "cn=x,dc=alder,dc=test", Type: "setpassword"}}
		}), CodeInvalid},
		{"an unavailable step with a compensation", resealed(func(b *Bundle) {
			b.Steps[0].Recoverability = Unavailable
			b.Steps[0].Reasons = []Reason{{Code: ReasonPasswordNotCaptured}}
			b.Recoverability = Partial
		}), CodeInvalid},
		{"a recoverability its steps do not support", resealed(func(b *Bundle) { b.Recoverability = Unavailable }), CodeInvalid},
		{"a partial step without a reason", resealed(func(b *Bundle) {
			b.Steps[0].Recoverability = Partial
			b.Recoverability = Partial
		}), CodeInvalid},
		{"an unknown reason", resealed(func(b *Bundle) {
			b.Steps[0].Recoverability = Partial
			b.Steps[0].Reasons = []Reason{{Code: "trust_me"}}
			b.Recoverability = Partial
		}), CodeInvalid},
		{"an unknown kind", resealed(func(b *Bundle) { b.Steps[0].Kind = "everything" }), CodeInvalid},
		{"a malformed DN", resealed(func(b *Bundle) { b.Steps[0].Compensation[0].DN = "not a dn" }), CodeInvalid},
		{"an unknown modification", resealed(func(b *Bundle) { b.Steps[2].Compensation[0].Mods[0].Op = "increment" }), CodeInvalid},
		{"an attribute name that is not one", resealed(func(b *Bundle) { b.Steps[2].Compensation[0].Mods[0].Name = "desc ription" }), CodeInvalid},
		{"a bad createdAt", resealed(func(b *Bundle) { b.CreatedAt = "yesterday" }), CodeInvalid},
		{"a missing origin", resealed(func(b *Bundle) { b.Origin.NamingContexts = nil }), CodeInvalid},
		{"too many steps", resealed(func(b *Bundle) {
			for len(b.Steps) <= MaxSteps {
				b.Steps = append(b.Steps, b.Steps[2])
			}
		}), CodeTooLarge},
		{"a value that is both text and base64", strings.Replace(raw, `"text": "grown"`, `"text": "grown", "base64": "Z3Jvd24="`, 1), CodeInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.input == raw {
				t.Fatal("the case did not change the bundle")
			}
			_, _, err := Decode([]byte(tc.input))
			if err == nil {
				t.Fatal("accepted")
			}
			if got := codeOf(err); got != tc.code {
				t.Errorf("code %q (%v), want %q", got, err, tc.code)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the refusal repeats a secret: %v", err)
			}
		})
	}
}

func TestDecodeWithoutAChecksumIsUnverified(t *testing.T) {
	b, _ := New(Origin{NamingContexts: []string{}}, threeSteps(t), fixedTime)
	b.Checksum = ""
	_, integrity, err := Decode(encoded(t, b))
	if err != nil || integrity != IntegrityUnverified {
		t.Errorf("integrity %s, err %v", integrity, err)
	}
}

func TestSameOrigin(t *testing.T) {
	a := Origin{Vendor: "389 Project", NamingContexts: []string{"dc=alder,dc=test"}}
	if same, _ := SameOrigin(a, Origin{Vendor: "389 project", NamingContexts: []string{"DC=alder, DC=test", "cn=changelog"}}); !same {
		t.Error("the same directory, spelled differently, is a different origin")
	}
	same, differences := SameOrigin(a, Origin{Vendor: "OpenLDAP", NamingContexts: []string{"dc=example,dc=com"}})
	if same || len(differences) != 2 {
		t.Errorf("a different directory: same %v, differences %v", same, differences)
	}
}

func TestChangesMergeCompensationsOfOneEntry(t *testing.T) {
	sch := testSchema(t)
	bob := "cn=bob,ou=people,dc=alder,dc=test"
	pre := entry(t, alice, []string{"description", "zero"})
	first := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModReplace, "description", "one")}}
	other := directory.ChangeRecord{DN: mustDN(t, bob), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModAdd, "mail", "bob@alder.test")}}
	second := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify,
		Mods: []directory.Mod{mod(directory.ModReplace, "description", "two")}}
	steps := []Step{
		Derive(0, first, pre, sch, KindData),
		Derive(1, other, entry(t, bob), sch, KindData),
		Derive(2, second, entry(t, alice, []string{"description", "one"}), sch, KindData),
	}
	b, err := New(Origin{NamingContexts: []string{}}, steps, fixedTime)
	if err != nil {
		t.Fatal(err)
	}
	changes := b.Changes()
	if len(changes) != 2 || changes[0].DN != alice || changes[1].DN != bob {
		t.Fatalf("changes %+v, want alice's two merged, then bob", changes)
	}
	merged := changes[0]
	var ops []string
	for _, m := range merged.Mods {
		ops = append(ops, m.Op+" "+strings.Join(texts(t, m.Values), ","))
	}
	if strings.Join(ops, " | ") != "delete two | add one | delete one | add zero" {
		t.Errorf("merged mods %v, want the later change's compensation first", ops)
	}
	if len(merged.Expect.Attributes) != 1 || !equalStrings(texts(t, merged.Expect.Attributes[0].Values), []string{"two"}) {
		t.Errorf("merged expectation %+v, want description = two, the state now", merged.Expect)
	}
}

func TestChangesFoldAnEditIntoTheDeleteOfAnAddedEntry(t *testing.T) {
	sch := testSchema(t)
	add := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeAdd, Attrs: []directory.Attribute{
		{Name: "objectClass", Values: [][]byte{[]byte("person")}},
		{Name: "sn", Values: [][]byte{[]byte("Liddell")}},
	}}
	edit := directory.ChangeRecord{DN: mustDN(t, alice), Type: directory.ChangeModify, Mods: []directory.Mod{
		mod(directory.ModReplace, "sn", "Edited"), mod(directory.ModAdd, "mail", "a@alder.test"),
	}}
	afterAdd := entry(t, alice, []string{"objectClass", "person"}, []string{"sn", "Liddell"}, []string{"cn", "alice"})
	b, _ := New(Origin{NamingContexts: []string{}}, []Step{
		Derive(0, add, nil, sch, KindData),
		Derive(1, edit, afterAdd, sch, KindData),
	}, fixedTime)
	changes := b.Changes()
	if len(changes) != 1 || changes[0].Type != "delete" || !changes[0].Expect.Exhaustive {
		t.Fatalf("changes %+v, want one exhaustive delete", changes)
	}
	got := map[string][]string{}
	for _, a := range changes[0].Expect.Attributes {
		got[a.Name] = texts(t, a.Values)
	}
	if !equalStrings(got["sn"], []string{"Edited"}) || !equalStrings(got["mail"], []string{"a@alder.test"}) ||
		!equalStrings(got["cn"], []string{"alice"}) {
		t.Errorf("expectation %v, want the entry as the edit left it", got)
	}
}

func TestChangesDoNotMergeAcrossAnotherKindOfChange(t *testing.T) {
	yes := true
	ordered := []Change{
		{DN: alice, Type: "modify", Mods: []Mod{{Op: "add", Name: "description"}}},
		{DN: "cn=x,dc=alder,dc=test", Type: "modrdn", NewRDN: "cn=y", DeleteOldRDN: &yes},
		{DN: alice, Type: "modify", Mods: []Mod{{Op: "add", Name: "mail"}}},
	}
	if got := merge(ordered); len(got) != 3 {
		t.Errorf("merged across a rename: %+v", got)
	}
}
