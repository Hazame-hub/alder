package dn_test

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

func TestParseAndRender(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // canonical rendering; "" means same as in
	}{
		{name: "simple", in: "uid=alice,ou=people,dc=alder,dc=test"},
		{name: "spaces after commas", in: "uid=alice, ou=people, dc=alder", want: "uid=alice,ou=people,dc=alder"},
		{name: "value with inner space", in: "cn=Alice Liddell,dc=test"},
		{name: "escaped comma", in: `cn=Liddell\, Alice,dc=test`},
		{name: "escaped plus", in: `cn=A\+B,dc=test`},
		{name: "escaped leading space", in: `cn=\ leading,dc=test`},
		{name: "escaped trailing space", in: `cn=trailing\ ,dc=test`},
		{name: "escaped leading hash", in: `cn=\#notahex,dc=test`},
		{name: "hex pair escape", in: `cn=a\2Cb,dc=test`, want: `cn=a\,b,dc=test`},
		{name: "escaped equals renders bare", in: `cn=a\=b,dc=test`, want: `cn=a=b,dc=test`},
		{name: "bare equals in value", in: `cn=a=b,dc=test`},
		{name: "multi-valued rdn", in: "cn=Alice+uid=alice,dc=test"},
		{name: "numeric oid type", in: "2.5.4.3=Alice,dc=test"},
		{name: "attribute option", in: "cn;lang-en=Alice,dc=test"},
		{name: "non-ascii", in: "cn=Zoë Müller,ou=people,dc=alder,dc=test"},
		{name: "cjk", in: "cn=日本語,dc=test"},
		{name: "hexstring value", in: "cn=#48656c6c6f,dc=test"},
		{name: "utf8 escaped bytewise", in: `cn=\c3\a9,dc=test`, want: "cn=é,dc=test"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dn.Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) = error %v", tc.in, err)
			}
			want := tc.want
			if want == "" {
				want = tc.in
			}
			if got.String() != want {
				t.Fatalf("Parse(%q).String() = %q, want %q", tc.in, got.String(), want)
			}
			// Canonical form must be a fixed point.
			again, err := dn.Parse(got.String())
			if err != nil {
				t.Fatalf("reparse of %q: %v", got.String(), err)
			}
			if again.String() != got.String() {
				t.Fatalf("not idempotent: %q then %q", got.String(), again.String())
			}
			if !again.Equal(got) {
				t.Fatalf("reparsed DN not Equal to original")
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"nosuchassertion",
		"=value",   // missing type
		"cn=a,",    // trailing comma
		"cn=a+",    // trailing plus
		`cn=a\`,    // trailing backslash
		`cn=a\q`,   // invalid escape
		`cn=a"b`,   // unescaped quote
		"cn=a;b",   // unescaped semicolon
		"cn=a<b",   // unescaped less-than
		"cn=a>b",   // unescaped greater-than
		"1cn=a",    // type may not start with a digit unless it is an OID
		"c n=a",    // space inside type
		"cn=#4",    // odd-length hexstring
		"cn=#zz",   // non-hex hexstring
		"2.5.04=a", // leading zero in an OID arc
	}
	for _, s := range bad {
		t.Run(s, func(t *testing.T) {
			if got, err := dn.Parse(s); err == nil {
				t.Fatalf("Parse(%q) = %q, want error", s, got)
			}
		})
	}
}

func TestEmptyValueIsLegal(t *testing.T) {
	d, err := dn.Parse("cn=,dc=test")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := d.RDN()[0].Value; got != "" {
		t.Fatalf("value = %q, want empty", got)
	}
	if d.String() != "cn=,dc=test" {
		t.Fatalf("String() = %q", d.String())
	}
}

func TestEmptyDN(t *testing.T) {
	if _, err := dn.Parse(""); err == nil {
		t.Fatal("Parse(\"\") should be an error")
	}
	d, err := dn.ParseAllowEmpty("")
	if err != nil {
		t.Fatalf("ParseAllowEmpty: %v", err)
	}
	if !d.IsEmpty() {
		t.Fatal("want empty DN")
	}
	if d.String() != "" {
		t.Fatalf("String() = %q, want empty", d.String())
	}
	if !dn.MustParse("dc=test").HasSuffix(d) {
		t.Fatal("every DN has the empty DN as a suffix")
	}
}

func TestEqual(t *testing.T) {
	equal := [][2]string{
		{"uid=alice,dc=test", "UID=Alice,DC=Test"},
		{"cn=Alice Liddell,dc=test", "cn=alice   liddell,dc=test"},
		{"cn=  Alice ,dc=test", "cn=Alice,dc=test"},
		{"cn=a+uid=b,dc=test", "uid=b+cn=a,dc=test"},
		{`cn=a\,b,dc=test`, "cn=a\\2Cb,dc=test"},
	}
	for _, pair := range equal {
		if !dn.MustParse(pair[0]).Equal(dn.MustParse(pair[1])) {
			t.Errorf("%q should equal %q", pair[0], pair[1])
		}
	}
	different := [][2]string{
		{"uid=alice,dc=test", "uid=alice,dc=other"},
		{"uid=alice,dc=test", "cn=alice,dc=test"},
		{"uid=alice,dc=test", "uid=alice,ou=people,dc=test"},
		{"cn=a+uid=b,dc=test", "cn=a+uid=c,dc=test"},
		{"cn=#4869,dc=test", "cn=Hi,dc=test"}, // BER value never equals a string value
	}
	for _, pair := range different {
		if dn.MustParse(pair[0]).Equal(dn.MustParse(pair[1])) {
			t.Errorf("%q should not equal %q", pair[0], pair[1])
		}
	}
}

func TestHierarchy(t *testing.T) {
	base := dn.MustParse("dc=alder,dc=test")
	people := dn.MustParse("ou=people,dc=alder,dc=test")
	alice := dn.MustParse("uid=alice,ou=people,dc=alder,dc=test")

	if got := alice.Parent().String(); got != people.String() {
		t.Fatalf("Parent() = %q, want %q", got, people)
	}
	if !alice.HasSuffix(base) {
		t.Fatal("alice should be below base")
	}
	if !alice.IsChildOf(people) {
		t.Fatal("alice is an immediate child of people")
	}
	if alice.IsChildOf(base) {
		t.Fatal("alice is not an immediate child of base")
	}
	if base.HasSuffix(alice) {
		t.Fatal("base is not below alice")
	}
	if got := dn.MustParse("dc=test").Parent(); !got.IsEmpty() {
		t.Fatalf("Parent of a one-RDN DN should be empty, got %q", got)
	}
	if got := (dn.DN{}).Parent(); !got.IsEmpty() {
		t.Fatal("Parent of the empty DN should be empty")
	}
}

func TestChildDoesNotAliasParent(t *testing.T) {
	base := dn.MustParse("ou=people,dc=alder,dc=test")
	a, err := base.ChildAttr("uid", "alice")
	if err != nil {
		t.Fatal(err)
	}
	b, err := base.ChildAttr("uid", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if a.String() != "uid=alice,ou=people,dc=alder,dc=test" {
		t.Fatalf("a = %q", a)
	}
	if b.String() != "uid=bob,ou=people,dc=alder,dc=test" {
		t.Fatalf("b = %q", b)
	}
	if base.String() != "ou=people,dc=alder,dc=test" {
		t.Fatalf("base was mutated: %q", base)
	}
}

// TestInjectionIsEscaped is the reason this package exists. A value that tries
// to add its own RDN must survive as a single value.
func TestInjectionIsEscaped(t *testing.T) {
	hostile := []string{
		"alice,dc=evil,dc=test",
		"alice+uid=admin",
		`alice\`,
		"alice>evil",
		" alice ",
		"#hex",
		"alice\x00admin",
	}
	base := dn.MustParse("ou=people,dc=alder,dc=test")
	for _, v := range hostile {
		t.Run(v, func(t *testing.T) {
			d, err := base.ChildAttr("uid", v)
			if err != nil {
				t.Fatal(err)
			}
			round, err := dn.Parse(d.String())
			if err != nil {
				t.Fatalf("Parse(%q): %v", d, err)
			}
			if len(round) != 4 {
				t.Fatalf("value split the DN: %q parsed to %d RDNs", d, len(round))
			}
			if got := round.RDN()[0].Value; got != v {
				t.Fatalf("round trip changed value: got %q, want %q", got, v)
			}
		})
	}
}

func TestNewRejectsBadType(t *testing.T) {
	if _, err := dn.New("uid=x,dc=evil", "alice"); err == nil {
		t.Fatal("an attribute type containing '=' must be rejected")
	}
	if _, err := dn.New("uid"); err == nil {
		t.Fatal("odd argument count must be rejected")
	}
}

func TestEscapeValue(t *testing.T) {
	tests := map[string]string{
		"":         "",
		"plain":    "plain",
		" lead":    `\ lead`,
		"trail ":   `trail\ `,
		"#hash":    `\#hash`,
		"mid#hash": "mid#hash",
		"a,b":      `a\,b`,
		`a\b`:      `a\\b`,
		"a\x00b":   `a\00b`,
		" ":        `\ `,
	}
	for in, want := range tests {
		if got := dn.EscapeValue(in); got != want {
			t.Errorf("EscapeValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseNotUTF8(t *testing.T) {
	if _, err := dn.ParseAllowEmpty("cn=\xff\xfe"); err == nil {
		t.Fatal("invalid UTF-8 input must be rejected")
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"uid=alice,ou=people,dc=alder,dc=test",
		`cn=Liddell\, Alice`,
		"cn=#48656c6c6f",
		"cn=a+uid=b,dc=test",
		"cn=Zoë,dc=test",
		`cn=\ x\ ,dc=test`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := dn.ParseAllowEmpty(s)
		if err != nil {
			return
		}
		out := d.String()
		again, err := dn.ParseAllowEmpty(out)
		if err != nil {
			t.Fatalf("re-parsing our own output failed: input %q, output %q, error %v", s, out, err)
		}
		if again.String() != out {
			t.Fatalf("rendering is not idempotent: input %q, first %q, second %q", s, out, again.String())
		}
		if !again.Equal(d) {
			t.Fatalf("round trip changed the DN: input %q, output %q", s, out)
		}
		// A rendered DN never contains an unescaped separator inside a value,
		// so the number of RDNs is stable.
		if len(again) != len(d) {
			t.Fatalf("RDN count changed: %d then %d for %q", len(d), len(again), s)
		}
		if strings.Contains(out, "\x00") {
			t.Fatalf("NUL byte survived rendering of %q", s)
		}
	})
}
