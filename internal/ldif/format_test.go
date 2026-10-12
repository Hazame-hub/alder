package ldif

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

func TestFormat(t *testing.T) {
	long := strings.Repeat("y", 100)
	cases := []struct {
		name, in, want string
	}{
		{
			name: "already canonical",
			in:   "dn: cn=x,dc=a\nobjectClass: top\ncn: x\n",
			want: "dn: cn=x,dc=a\nobjectClass: top\ncn: x\n",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
		{
			name: "CRLF, extra blank lines, no final newline",
			in:   "\r\n\r\ndn: cn=x,dc=a\r\ncn: x\r\n\r\n\r\n\r\ndn: cn=y,dc=a\r\ncn: y",
			want: "dn: cn=x,dc=a\ncn: x\n\ndn: cn=y,dc=a\ncn: y\n",
		},
		{
			name: "spacing after the colon",
			in:   "DN:cn=x,dc=a\ncn:    x\ndescription:\n",
			want: "dn: cn=x,dc=a\ncn: x\ndescription: \n",
		},
		{
			name: "a DN as Alder writes one",
			in:   "dn: CN=x ,  dc=a\ncn: x\n",
			want: "dn: CN=x,dc=a\ncn: x\n",
		},
		{
			name: "folded at another width, unfolded and folded again",
			in:   "dn: cn=x,dc=a\ndescription: " + long[:40] + "\n " + long[40:] + "\n",
			want: "dn: cn=x,dc=a\ndescription: " + long[:63] + "\n " + long[63:] + "\n",
		},
		{
			name: "base64 that did not need to be",
			in:   "dn: cn=x,dc=a\ncn:: eA==\n",
			want: "dn: cn=x,dc=a\ncn: x\n",
		},
		{
			name: "a plain value with a trailing space shows it",
			in:   "dn: cn=x,dc=a\ncn: x \n",
			want: "dn: cn=x,dc=a\ncn:: eCA=\n",
		},
		{
			name: "non-ASCII stays base64",
			in:   "dn: cn=x,dc=a\ncn:: w6k=\n",
			want: "dn: cn=x,dc=a\ncn:: w6k=\n",
		},
		{
			name: "version gets a block of its own",
			in:   "VERSION:1\ndn: cn=x,dc=a\ncn: x\n",
			want: "version: 1\n\ndn: cn=x,dc=a\ncn: x\n",
		},
		{
			name: "comments are kept where they are, verbatim",
			in: "# The people of team A.\n#   indented, and long enough to have been folded by hand\n  continued\n" +
				"dn: cn=x,dc=a\n# why cn is what it is\ncn: x\n\n\n# a block on its own\n\ndn: cn=y,dc=a\ncn: y\n# trailing\n",
			want: "# The people of team A.\n#   indented, and long enough to have been folded by hand\n  continued\n" +
				"dn: cn=x,dc=a\n# why cn is what it is\ncn: x\n\n# a block on its own\n\ndn: cn=y,dc=a\ncn: y\n# trailing\n",
		},
		{
			name: "attribute order and spelling are kept",
			in:   "dn: cn=x,dc=a\nmail: b@x\nobjectclass: top\nmail: a@x\nuserCertificate;binary:: AAE=\n",
			want: "dn: cn=x,dc=a\nmail: b@x\nobjectclass: top\nmail: a@x\nuserCertificate;binary:: AAE=\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Format([]byte(tc.in))
			if err != nil {
				t.Fatalf("Format: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("Format =\n%q\nwant\n%q", got, tc.want)
			}
			again, err := Format(got)
			if err != nil || string(again) != string(got) {
				t.Errorf("Format is not idempotent: %q, %v", again, err)
			}
		})
	}
}

// The writer's own output is already canonical: formatting what Alder exports
// changes nothing, which is what makes the form canonical rather than merely
// a second one.
func TestFormatLeavesWriterOutputAlone(t *testing.T) {
	recs := []*Record{
		{DN: dn.MustParse(`cn=a\,b+uid=x,dc=alder,dc=test`), Attrs: []Attribute{
			{Name: "objectClass", Values: bs("top", "person")},
			{Name: "description", Values: bs(strings.Repeat("z", 300), " leading", "trailing ", "multi\nline", "é")},
			{Name: "jpegPhoto", Values: [][]byte{{0, 1, 2, 0xff}}},
		}},
		{DN: dn.MustParse("cn=b,dc=alder,dc=test"), Attrs: []Attribute{{Name: "cn", Values: bs("b")}}},
	}
	out, err := Marshal(recs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Format(out)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if string(got) != string(out) {
		t.Errorf("Format changed the writer's output:\n%s\nto\n%s", out, got)
	}
}

func TestFormatRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"a syntax error":       "dn: cn=x,dc=a\nnot an attribute\n",
		"a changetype record":  "dn: cn=x,dc=a\nchangetype: delete\n",
		"a URL reference":      "dn: cn=x,dc=a\ncn:< file:///etc/passwd\n",
		"a DN that is not one": "dn: not a dn\ncn: x\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := Format([]byte(in)); err == nil {
				t.Errorf("Format accepted it and returned %q", got)
			}
		})
	}
}

// FuzzFormat asserts that whatever Format accepts, it formats to a document
// that says the same thing and that formats to itself. Format checks the
// first property on every call; the fuzzer is what looks for the inputs
// where that check would fire.
func FuzzFormat(f *testing.F) {
	for _, s := range []string{
		"dn: cn=x,dc=a\ncn: x\n",
		"version: 1\ndn: cn=x,dc=a\n# c\n c\ncn: x\n\n\n# alone\n",
		" dn: cn=x,dc=a\n cn: x\n",
		"#\n \n \ndn: cn=x,dc=a\ncn:: IA==\n",
		"dn: cn=x,dc=a\ncn: " + strings.Repeat("y", 200) + "\r\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		before, err := Unmarshal([]byte(in))
		if err != nil {
			return
		}
		out, err := Format([]byte(in))
		if err != nil {
			for _, r := range before {
				if r.Change != ChangeNone {
					return
				}
			}
			t.Fatalf("Format refused a document of content records that parses: %v\ninput: %q", err, in)
		}
		again, err := Format(out)
		if err != nil {
			t.Fatalf("Format refused its own output: %v\noutput: %q", err, out)
		}
		if string(again) != string(out) {
			t.Fatalf("Format is not idempotent:\n%q\nthen\n%q", out, again)
		}
	})
}
