package filter_test

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/filter"
)

func mustRender(t *testing.T, f filter.Filter) string {
	t.Helper()
	s, err := f.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return s
}

func TestBuilders(t *testing.T) {
	tests := []struct {
		name string
		f    filter.Filter
		want string
	}{
		{"equality", filter.Equal("uid", "alice"), "(uid=alice)"},
		{"present", filter.Present("objectClass"), "(objectClass=*)"},
		{"approx", filter.Approx("sn", "liddel"), "(sn~=liddel)"},
		{"ge", filter.GreaterOrEqual("uidNumber", "1000"), "(uidNumber>=1000)"},
		{"le", filter.LessOrEqual("uidNumber", "2000"), "(uidNumber<=2000)"},
		{"contains", filter.Contains("cn", "ali"), "(cn=*ali*)"},
		{"prefix", filter.HasPrefix("cn", "ali"), "(cn=ali*)"},
		{"suffix", filter.HasSuffix("mail", "@alder.test"), "(mail=*@alder.test)"},
		{
			"substrings all parts",
			filter.Substrings("cn", "a", []string{"b", "c"}, "d"),
			"(cn=a*b*c*d)",
		},
		{
			"and",
			filter.And(filter.Equal("objectClass", "person"), filter.Equal("uid", "alice")),
			"(&(objectClass=person)(uid=alice))",
		},
		{
			"or",
			filter.Or(filter.Equal("uid", "alice"), filter.Equal("uid", "bob")),
			"(|(uid=alice)(uid=bob))",
		},
		{"not", filter.Not(filter.Present("mail")), "(!(mail=*))"},
		{
			"nested",
			filter.And(
				filter.Equal("objectClass", "inetOrgPerson"),
				filter.Or(filter.Contains("cn", "ali"), filter.Contains("sn", "ali")),
				filter.Not(filter.Equal("nsAccountLock", "true")),
			),
			"(&(objectClass=inetOrgPerson)(|(cn=*ali*)(sn=*ali*))(!(nsAccountLock=true)))",
		},
		{"and of one collapses", filter.And(filter.Equal("uid", "alice")), "(uid=alice)"},
		{
			"extensible with rule",
			filter.Extensible("cn", "caseExactMatch", "Alice", false),
			"(cn:caseExactMatch:=Alice)",
		},
		{
			"extensible with dn attrs",
			filter.Extensible("", "2.5.13.2", "Alice", true),
			"(:dn:2.5.13.2:=Alice)",
		},
		{
			"extensible bare",
			filter.Extensible("cn", "", "Alice", false),
			"(cn:=Alice)",
		},
		{"non-ascii value", filter.Equal("cn", "Zoë"), "(cn=Zoë)"},
		{"attribute option", filter.Present("userCertificate;binary"), "(userCertificate;binary=*)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRender(t, tc.f); got != tc.want {
				t.Fatalf("Render() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEscaping covers RFC 4515 section 3 and the section 4 examples.
func TestEscaping(t *testing.T) {
	tests := map[string]string{
		"Parens R Us (from the RFC)": `Parens R Us \28from the RFC\29`,
		"*":                          `\2a`,
		`C:\MyFile`:                  `C:\5cMyFile`,
		"a\x00b":                     `a\00b`,
		"nothing special":            "nothing special",
		"tab\there":                  `tab\09here`,
	}
	for in, want := range tests {
		if got := filter.EscapeValue(in); got != want {
			t.Errorf("EscapeValue(%q) = %q, want %q", in, got, want)
		}
	}
	// The classic RFC 4515 section 4 example, end to end.
	got := mustRender(t, filter.Equal("o", "Parens R Us (from the RFC)"))
	if want := `(o=Parens R Us \28from the RFC\29)`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestInjection is why this package exists: a value must never be able to
// change the shape of the filter it lands in.
func TestInjection(t *testing.T) {
	hostile := []string{
		"*",
		"*)(uid=*",
		"alice)(|(uid=admin",
		`\`,
		"a)(objectClass=*",
		"()",
		"\x00",
	}
	for _, v := range hostile {
		t.Run(v, func(t *testing.T) {
			f := filter.And(filter.Equal("objectClass", "person"), filter.Equal("uid", v))
			rendered := mustRender(t, f)

			round, err := filter.Parse(rendered)
			if err != nil {
				t.Fatalf("Parse(%q): %v", rendered, err)
			}
			if len(round.Subs) != 2 {
				t.Fatalf("value changed the filter shape: %q has %d operands", rendered, len(round.Subs))
			}
			if round.Subs[1].Kind != filter.KindEquality {
				t.Fatalf("value changed the node kind to %s", round.Subs[1].Kind)
			}
			if round.Subs[1].Value != v {
				t.Fatalf("round trip changed the value: got %q, want %q", round.Subs[1].Value, v)
			}
		})
	}
}

func TestInvalidAttributeIsRejected(t *testing.T) {
	bad := []string{
		"",
		"uid=alice)(objectClass",
		"uid )",
		"uid*",
		"(uid",
		"1uid",
		"2.5.04.3",
		"uid;",
	}
	for _, attr := range bad {
		t.Run(attr, func(t *testing.T) {
			f := filter.Equal(attr, "x")
			if f.Err() == nil {
				t.Fatalf("Equal(%q, ...) should carry an error", attr)
			}
			if _, err := f.Render(); err == nil {
				t.Fatalf("Render of a filter on %q should fail", attr)
			}
			// The error must also survive composition.
			if filter.And(filter.Present("cn"), f).Err() == nil {
				t.Fatal("an error inside a junction must propagate")
			}
		})
	}
}

func TestEmptyJunctionIsRejected(t *testing.T) {
	if filter.And().Err() == nil {
		t.Fatal("And() with no operands must be an error")
	}
	if filter.Or().Err() == nil {
		t.Fatal("Or() with no operands must be an error")
	}
}

func TestZeroFilterRendersNothing(t *testing.T) {
	var f filter.Filter
	if _, err := f.Render(); err == nil {
		t.Fatal("the zero Filter must not render")
	}
	if !strings.HasPrefix(f.String(), "<invalid filter") {
		t.Fatalf("String() = %q, want a placeholder", f.String())
	}
}

func TestSubstringsRules(t *testing.T) {
	if filter.Substrings("cn", "", nil, "").Err() == nil {
		t.Fatal("a substrings filter with no components must be an error")
	}
	if filter.Substrings("cn", "a", []string{""}, "b").Err() == nil {
		t.Fatal("an empty any-component must be an error")
	}
}

func TestSubstringsDoesNotAliasCaller(t *testing.T) {
	any := []string{"b"}
	f := filter.Substrings("cn", "a", any, "c")
	any[0] = "MUTATED"
	if got := mustRender(t, f); got != "(cn=a*b*c)" {
		t.Fatalf("caller mutated the filter: %q", got)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want string // canonical rendering; "" means unchanged
	}{
		{in: "(uid=alice)"},
		{in: "(objectClass=*)"},
		{in: "(cn=*ali*)"},
		{in: "(cn=a*b*c*d)"},
		{in: "(uidNumber>=1000)"},
		{in: "(uidNumber<=2000)"},
		{in: "(sn~=liddel)"},
		{in: "(&(objectClass=person)(uid=alice))"},
		{in: "(|(uid=alice)(uid=bob))"},
		{in: "(!(mail=*))"},
		{in: "(&(a=1)(|(b=2)(!(c=3))))"},
		{in: "(cn:caseExactMatch:=Alice)"},
		{in: "(cn:dn:caseExactMatch:=Alice)"},
		{in: "(:dn:2.5.13.2:=Alice)"},
		{in: "(cn:=Alice)"},
		{in: `(o=Parens R Us \28from the RFC\29)`},
		{in: `(cn=*\2a*)`},
		{in: `(filename=C:\5cMyFile)`},
		{in: "(cn=Zoë)"},
		{in: `(cn=\c3\a9)`, want: "(cn=é)"},
		{in: "  (uid=alice)  ", want: "(uid=alice)"},
		{in: `(cn=\2A)`, want: `(cn=\2a)`},
		{in: "(&(uid=alice))"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			f, err := filter.Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			want := tc.want
			if want == "" {
				want = tc.in
			}
			got := mustRender(t, f)
			if got != want {
				t.Fatalf("Parse(%q) rendered %q, want %q", tc.in, got, want)
			}
			again, err := filter.Parse(got)
			if err != nil {
				t.Fatalf("re-parsing %q: %v", got, err)
			}
			if !again.Equal(f) {
				t.Fatalf("round trip changed the tree for %q", tc.in)
			}
		})
	}
}

func TestParseKinds(t *testing.T) {
	f, err := filter.Parse("(cn=*)")
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind != filter.KindPresent {
		t.Fatalf("(cn=*) parsed as %s, want present", f.Kind)
	}
	f, err = filter.Parse("(cn=*a*)")
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind != filter.KindSubstrings || len(f.Any) != 1 || f.Any[0] != "a" {
		t.Fatalf("(cn=*a*) parsed as %+v", f)
	}
	f, err = filter.Parse(`(cn=\2a)`)
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind != filter.KindEquality || f.Value != "*" {
		t.Fatalf(`(cn=\2a) should be an equality match on a literal asterisk, got %+v`, f)
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"",
		"uid=alice",           // unparenthesised
		"(uid=alice",          // unbalanced
		"uid=alice)",          // unbalanced
		"((uid=alice))",       // a filter is not a filterlist
		"(&)",                 // empty junction
		"(|)",                 // empty junction
		"(!)",                 // not needs an operand
		"(!(a=1)(b=2))",       // not takes exactly one
		"(uid=alice)(cn=bob)", // trailing input
		"(uid)",               // no operator
		"(=alice)",            // no attribute
		"(uid==alice)",        // '=' is not valid in an unescaped value? it is; see below
		`(uid=\)`,             // truncated escape
		`(uid=\q1)`,           // not a hex pair
		"(uid=a(b)",           // unescaped paren in a value
		"(uid:=)(x=1)",        // trailing input
		"(uid:dn:dn:=x)",      // duplicate dn
		"(uid:a:b:=x)",        // two matching rules
		"(1uid=x)",            // invalid attribute
		"(uid>alice)",         // '>' without '='
	}
	for _, s := range bad {
		if s == "(uid==alice)" {
			continue // '=' inside a value is legal; covered in TestParseEqualsInValue
		}
		t.Run(s, func(t *testing.T) {
			if f, err := filter.Parse(s); err == nil {
				t.Fatalf("Parse(%q) = %q, want error", s, f)
			}
		})
	}
}

func TestParseEqualsInValue(t *testing.T) {
	f, err := filter.Parse("(description=a=b)")
	if err != nil {
		t.Fatal(err)
	}
	if f.Value != "a=b" {
		t.Fatalf("value = %q", f.Value)
	}
}

func TestParseDepthLimit(t *testing.T) {
	deep := strings.Repeat("(&", 500) + "(a=1)" + strings.Repeat(")", 500)
	if _, err := filter.Parse(deep); err == nil {
		t.Fatal("a deeply nested filter must be rejected, not stack-overflow")
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"(uid=alice)",
		"(&(objectClass=person)(|(cn=*a*)(!(sn=b))))",
		`(o=Parens R Us \28from the RFC\29)`,
		"(cn:dn:caseExactMatch:=Alice)",
		"(cn=*)",
		"(cn=a*b*c)",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		parsed, err := filter.Parse(s)
		if err != nil {
			return
		}
		out, err := parsed.Render()
		if err != nil {
			t.Fatalf("parsed %q but could not render it: %v", s, err)
		}
		again, err := filter.Parse(out)
		if err != nil {
			t.Fatalf("could not re-parse our own output: input %q, output %q, error %v", s, out, err)
		}
		out2, err := again.Render()
		if err != nil {
			t.Fatalf("re-render failed for %q: %v", out, err)
		}
		if out != out2 {
			t.Fatalf("rendering is not idempotent: %q then %q (input %q)", out, out2, s)
		}
		if !again.Equal(parsed) {
			t.Fatalf("round trip changed the tree: input %q, output %q", s, out)
		}
		if strings.Count(out, "(") != strings.Count(out, ")") {
			t.Fatalf("unbalanced parentheses in output %q for input %q", out, s)
		}
	})
}
