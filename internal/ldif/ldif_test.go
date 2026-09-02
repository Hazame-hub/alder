package ldif

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

func TestNeedsBase64(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"plain ASCII", "hello", false},
		{"empty", "", false},
		{"inner spaces", "Liddell, Alice", false},
		{"inner colon", "a:b", false},
		{"leading space", " leading", true},
		{"trailing space", "trailing ", true},
		{"leading colon", ":starts with a colon", true},
		{"leading less-than", "<starts with less-than", true},
		{"embedded newline", "two\nlines", true},
		{"embedded carriage return", "two\rlines", true},
		{"NUL byte", "a\x00b", true},
		{"non-ASCII", "Zweigstelle München", true},
		{"inner less-than", "a<b", false},
		{"inner hash", "a#b", false},
		{"leading hash is safe in LDIF", "#not-a-comment-here", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsBase64([]byte(tt.value)); got != tt.want {
				t.Errorf("NeedsBase64(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestWriteContentRecord(t *testing.T) {
	rec := &Record{
		DN: dn.MustParse("cn=alice,ou=people,dc=alder,dc=test"),
		Attrs: []Attribute{
			{Name: "objectClass", Values: bs("top", "person")},
			{Name: "cn", Values: bs("alice")},
			{Name: "sn", Values: bs("Anderson")},
		},
	}
	want := "dn: cn=alice,ou=people,dc=alder,dc=test\n" +
		"objectClass: top\n" +
		"objectClass: person\n" +
		"cn: alice\n" +
		"sn: Anderson\n"
	if got := rec.String(); got != want {
		t.Errorf("String() =\n%q\nwant\n%q", got, want)
	}
}

func TestWriteBase64Value(t *testing.T) {
	rec := &Record{
		DN:    dn.MustParse("cn=x,dc=alder,dc=test"),
		Attrs: []Attribute{{Name: "description", Values: bs(" leading space")}},
	}
	got := rec.String()
	if !strings.Contains(got, "description:: IGxlYWRpbmcgc3BhY2U=") {
		t.Errorf("String() = %q, want a base64 description line", got)
	}
}

func TestWriteFoldsAt76Columns(t *testing.T) {
	long := strings.Repeat("x", 200)
	rec := &Record{
		DN:    dn.MustParse("cn=x,dc=alder,dc=test"),
		Attrs: []Attribute{{Name: "description", Values: bs(long)}},
	}
	out := rec.String()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(line) > DefaultLineWidth {
			t.Errorf("line is %d columns, want at most %d: %q", len(line), DefaultLineWidth, line)
		}
	}
	// The continuation lines must each begin with exactly one space.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected the value to fold across several lines, got %d", len(lines))
	}
	for _, line := range lines[2:] {
		if !strings.HasPrefix(line, " ") || strings.HasPrefix(line, "  ") {
			t.Errorf("continuation line %q does not start with exactly one space", line)
		}
	}
}

func TestWriteFoldingIsReversible(t *testing.T) {
	// Folding that cannot be unfolded back to the same bytes is worse than no
	// folding at all, and off-by-one errors here are invisible by eye.
	for _, n := range []int{1, 60, 74, 75, 76, 77, 100, 151, 1000} {
		value := strings.Repeat("a", n)
		rec := &Record{
			DN:    dn.MustParse("cn=x,dc=alder,dc=test"),
			Attrs: []Attribute{{Name: "description", Values: [][]byte{[]byte(value)}}},
		}
		round, err := Unmarshal([]byte(rec.String()))
		if err != nil {
			t.Fatalf("n=%d: Unmarshal: %v", n, err)
		}
		got := round[0].AttrStrings("description")
		if len(got) != 1 || got[0] != value {
			t.Errorf("n=%d: round-trip lost the value (got %d bytes, want %d)", n, len(got[0]), n)
		}
	}
}

func TestWriteNegativeLineWidthDisablesFolding(t *testing.T) {
	var b strings.Builder
	w := NewWriter(&b)
	w.LineWidth = -1
	rec := &Record{
		DN:    dn.MustParse("cn=x,dc=alder,dc=test"),
		Attrs: []Attribute{{Name: "description", Values: bs(strings.Repeat("y", 300))}},
	}
	if err := w.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if strings.HasPrefix(line, " ") {
			t.Errorf("folding was disabled but %q is a continuation line", line)
		}
	}
}

func TestWriteChangeRecords(t *testing.T) {
	tests := []struct {
		name string
		rec  *Record
		want string
	}{
		{
			name: "add",
			rec: &Record{
				DN:     dn.MustParse("cn=new,dc=alder,dc=test"),
				Change: ChangeAdd,
				Attrs: []Attribute{
					{Name: "objectClass", Values: bs("top", "person")},
					{Name: "cn", Values: bs("new")},
					{Name: "sn", Values: bs("New")},
				},
			},
			want: "dn: cn=new,dc=alder,dc=test\nchangetype: add\n" +
				"objectClass: top\nobjectClass: person\ncn: new\nsn: New\n",
		},
		{
			name: "delete",
			rec: &Record{
				DN:     dn.MustParse("cn=old,dc=alder,dc=test"),
				Change: ChangeDelete,
			},
			want: "dn: cn=old,dc=alder,dc=test\nchangetype: delete\n",
		},
		{
			name: "modify with every operation",
			rec: &Record{
				DN:     dn.MustParse("cn=alice,dc=alder,dc=test"),
				Change: ChangeModify,
				Mods: []Mod{
					{Op: ModAdd, Name: "mail", Values: bs("alice@alder.test")},
					{Op: ModReplace, Name: "telephoneNumber", Values: bs("+1 555 0100")},
					{Op: ModDelete, Name: "description"},
					{Op: ModDelete, Name: "mail", Values: bs("old@alder.test")},
				},
			},
			want: "dn: cn=alice,dc=alder,dc=test\nchangetype: modify\n" +
				"add: mail\nmail: alice@alder.test\n-\n" +
				"replace: telephoneNumber\ntelephoneNumber: +1 555 0100\n-\n" +
				"delete: description\n-\n" +
				"delete: mail\nmail: old@alder.test\n-\n",
		},
		{
			name: "modrdn without a move",
			rec: &Record{
				DN:           dn.MustParse("cn=old,dc=alder,dc=test"),
				Change:       ChangeModRDN,
				NewRDN:       "cn=new",
				DeleteOldRDN: true,
			},
			want: "dn: cn=old,dc=alder,dc=test\nchangetype: modrdn\nnewrdn: cn=new\ndeleteoldrdn: 1\n",
		},
		{
			name: "modrdn with a move",
			rec: &Record{
				DN:           dn.MustParse("cn=old,ou=a,dc=alder,dc=test"),
				Change:       ChangeModRDN,
				NewRDN:       "cn=new",
				DeleteOldRDN: false,
				NewSuperior:  dn.MustParse("ou=b,dc=alder,dc=test"),
			},
			want: "dn: cn=old,ou=a,dc=alder,dc=test\nchangetype: modrdn\n" +
				"newrdn: cn=new\ndeleteoldrdn: 0\nnewsuperior: ou=b,dc=alder,dc=test\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rec.String(); got != tt.want {
				t.Errorf("String() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestMarshalWritesVersionAndBlankLines(t *testing.T) {
	recs := []*Record{
		{DN: dn.MustParse("cn=a,dc=alder,dc=test"), Attrs: []Attribute{{Name: "cn", Values: bs("a")}}},
		{DN: dn.MustParse("cn=b,dc=alder,dc=test"), Attrs: []Attribute{{Name: "cn", Values: bs("b")}}},
	}
	got, err := Marshal(recs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := "version: 1\n\ndn: cn=a,dc=alder,dc=test\ncn: a\n\ndn: cn=b,dc=alder,dc=test\ncn: b\n"
	if string(got) != want {
		t.Errorf("Marshal =\n%q\nwant\n%q", got, want)
	}
}

func TestReadContentRecord(t *testing.T) {
	in := `version: 1

# a comment, which is ignored
dn: cn=alice,ou=people,dc=alder,dc=test
objectClass: top
objectClass: person
cn: alice
sn: Anderson
description: a value that is folded across
  several physical lines to make it awkward
`
	recs, err := Unmarshal([]byte(in))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	r := recs[0]
	if !r.DN.Equal(dn.MustParse("cn=alice,ou=people,dc=alder,dc=test")) {
		t.Errorf("DN = %s", r.DN)
	}
	if got := r.AttrStrings("objectClass"); len(got) != 2 || got[0] != "top" || got[1] != "person" {
		t.Errorf("objectClass = %v, want the two values merged in order", got)
	}
	// The fold consumed exactly one space per continuation line.
	wantDesc := "a value that is folded across several physical lines to make it awkward"
	if got := r.AttrStrings("description"); len(got) != 1 || got[0] != wantDesc {
		t.Errorf("description = %q, want %q", got, wantDesc)
	}
	if got := r.Attr("CN"); len(got) != 1 {
		t.Error("Attr should match the attribute name case-insensitively")
	}
}

func TestReadBase64AndNonASCII(t *testing.T) {
	// Straight out of the harness seed data: a base64 DN with a non-ASCII RDN.
	in := "dn:: b3U9WndlaWdzdGVsbGUgTcO8bmNoZW4sb3U9c2VydmljZXMsZGM9YWxkZXIsZGM9dGVzdA==\n" +
		"objectClass: organizationalUnit\n" +
		"ou:: WndlaWdzdGVsbGUgTcO8bmNoZW4=\n"
	recs, err := Unmarshal([]byte(in))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := dn.MustParse("ou=Zweigstelle München,ou=services,dc=alder,dc=test")
	if !recs[0].DN.Equal(want) {
		t.Errorf("DN = %s, want %s", recs[0].DN, want)
	}
	if got := recs[0].AttrStrings("ou"); len(got) != 1 || got[0] != "Zweigstelle München" {
		t.Errorf("ou = %q", got)
	}
}

func TestReadPreservesBinaryValues(t *testing.T) {
	// A value with an embedded NUL and a high byte must survive as bytes.
	raw := []byte{0x00, 0xff, 0x41, 0x0a, 0x42}
	rec := &Record{
		DN:    dn.MustParse("cn=binary,dc=alder,dc=test"),
		Attrs: []Attribute{{Name: "alderNote", Values: [][]byte{raw}}},
	}
	round, err := Unmarshal([]byte(rec.String()))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got := round[0].Attr("alderNote")
	if len(got) != 1 || !bytes.Equal(got[0], raw) {
		t.Errorf("round-tripped %v, want %v", got, raw)
	}
}

func TestReadMultipleSpacesAfterColon(t *testing.T) {
	// FILL is *SPACE, so every space after the colon is separator. This is why
	// a value whose first character is a space has to be base64 encoded.
	recs, err := Unmarshal([]byte("dn: cn=x,dc=alder,dc=test\ncn:    spaced\n"))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got := recs[0].AttrStrings("cn"); got[0] != "spaced" {
		t.Errorf("cn = %q, want the FILL spaces consumed", got[0])
	}
}

func TestReadEmptyValue(t *testing.T) {
	recs, err := Unmarshal([]byte("dn: cn=x,dc=alder,dc=test\ndescription:\n"))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	got := recs[0].Attr("description")
	if len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("description = %v, want one empty value", got)
	}
}

func TestReadCommentWithContinuation(t *testing.T) {
	// The continuation belongs to the comment. Dropping comments before
	// unfolding would leave "sn: Wrong" looking like an attribute line.
	in := "dn: cn=x,dc=alder,dc=test\n" +
		"# this comment continues\n" +
		"  and its continuation is still a comment\n" +
		"cn: x\n"
	recs, err := Unmarshal([]byte(in))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(recs[0].Attrs) != 1 || recs[0].Attrs[0].Name != "cn" {
		t.Errorf("attrs = %+v, want only cn", recs[0].Attrs)
	}
}

func TestReadCRLF(t *testing.T) {
	in := "dn: cn=x,dc=alder,dc=test\r\ncn: x\r\n\r\ndn: cn=y,dc=alder,dc=test\r\ncn: y\r\n"
	recs, err := Unmarshal([]byte(in))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if got := recs[0].AttrStrings("cn"); got[0] != "x" {
		t.Errorf("cn = %q, want the CR stripped", got[0])
	}
}

func TestReadChangeRecords(t *testing.T) {
	in := `dn: cn=alice,dc=alder,dc=test
changetype: modify
add: mail
mail: alice@alder.test
mail: a.anderson@alder.test
-
delete: description
-
replace: telephoneNumber
telephoneNumber: +1 555 0100
-

dn: cn=old,dc=alder,dc=test
changetype: modrdn
newrdn: cn=new
deleteoldrdn: 1
newsuperior: ou=b,dc=alder,dc=test

dn: cn=gone,dc=alder,dc=test
changetype: delete
`
	recs, err := Unmarshal([]byte(in))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}

	mod := recs[0]
	if mod.Change != ChangeModify || len(mod.Mods) != 3 {
		t.Fatalf("modify record = %+v", mod)
	}
	if mod.Mods[0].Op != ModAdd || len(mod.Mods[0].Values) != 2 {
		t.Errorf("mod 0 = %+v, want an add with two values", mod.Mods[0])
	}
	if mod.Mods[1].Op != ModDelete || len(mod.Mods[1].Values) != 0 {
		t.Errorf("mod 1 = %+v, want a delete of the whole attribute", mod.Mods[1])
	}
	if mod.Mods[2].Op != ModReplace {
		t.Errorf("mod 2 = %+v, want a replace", mod.Mods[2])
	}

	rename := recs[1]
	if rename.Change != ChangeModRDN || rename.NewRDN != "cn=new" || !rename.DeleteOldRDN {
		t.Errorf("modrdn record = %+v", rename)
	}
	if !rename.NewSuperior.Equal(dn.MustParse("ou=b,dc=alder,dc=test")) {
		t.Errorf("newsuperior = %s", rename.NewSuperior)
	}

	if recs[2].Change != ChangeDelete {
		t.Errorf("delete record = %+v", recs[2])
	}
}

func TestRoundTripChangeRecords(t *testing.T) {
	original := []*Record{
		{
			DN:     dn.MustParse("cn=alice,ou=people,dc=alder,dc=test"),
			Change: ChangeModify,
			Mods: []Mod{
				{Op: ModAdd, Name: "mail", Values: bs("alice@alder.test")},
				{Op: ModDelete, Name: "description"},
				{Op: ModReplace, Name: "alderNote", Values: [][]byte{{0x00, 0xff}}},
			},
		},
		{
			DN:           dn.MustParse("cn=Liddell\\, Alice,ou=people,dc=alder,dc=test"),
			Change:       ChangeModRDN,
			NewRDN:       "cn=Alice Liddell",
			DeleteOldRDN: true,
		},
	}
	encoded, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := Unmarshal(encoded)
	if err != nil {
		t.Fatalf("Unmarshal: %v\ninput:\n%s", err, encoded)
	}
	if len(decoded) != len(original) {
		t.Fatalf("round-tripped %d records, want %d", len(decoded), len(original))
	}
	if !decoded[0].DN.Equal(original[0].DN) {
		t.Errorf("DN = %s, want %s", decoded[0].DN, original[0].DN)
	}
	if len(decoded[0].Mods) != 3 {
		t.Fatalf("mods = %+v", decoded[0].Mods)
	}
	if !bytes.Equal(decoded[0].Mods[2].Values[0], []byte{0x00, 0xff}) {
		t.Error("a binary value did not survive the round trip")
	}
	// A DN whose RDN contains an escaped comma is the classic parser trap.
	if !decoded[1].DN.Equal(original[1].DN) {
		t.Errorf("escaped-comma DN = %s, want %s", decoded[1].DN, original[1].DN)
	}
}

func TestReadRefusesURLReferences(t *testing.T) {
	// Following this would be arbitrary file disclosure from a process holding
	// a privileged bind. It is refused, by name, with the line number.
	in := "dn: cn=x,dc=alder,dc=test\njpegPhoto:< file:///etc/shadow\n"
	_, err := Unmarshal([]byte(in))
	if err == nil {
		t.Fatal("Unmarshal accepted a URL reference")
	}
	var urlErr *ErrURLReference
	if !errors.As(err, &urlErr) {
		t.Fatalf("error = %v, want an *ErrURLReference", err)
	}
	if urlErr.Attribute != "jpegPhoto" || urlErr.URL != "file:///etc/shadow" || urlErr.Line != 2 {
		t.Errorf("got %+v", urlErr)
	}
}

func TestReadRefusesControls(t *testing.T) {
	in := "dn: cn=x,dc=alder,dc=test\ncontrol: 1.2.840.113556.1.4.805 true\nchangetype: delete\n"
	_, err := Unmarshal([]byte(in))
	if err == nil {
		t.Fatal("Unmarshal accepted a control")
	}
	if !strings.Contains(err.Error(), "control") {
		t.Errorf("error = %v, want it to name the control", err)
	}
}

func TestReadSyntaxErrors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no dn", "cn: x\n", "must start with"},
		{"unparsable dn", "dn: not a dn\ncn: x\n", "dn:"},
		{"unknown changetype", "dn: cn=x,dc=a,dc=b\nchangetype: frobnicate\n", "changetype"},
		{"delete with attributes", "dn: cn=x,dc=a,dc=b\nchangetype: delete\ncn: x\n", "no attributes"},
		{"modify without a terminator", "dn: cn=x,dc=a,dc=b\nchangetype: modify\nadd: cn\ncn: x\n", "not terminated"},
		{"mod value names another attribute", "dn: cn=x,dc=a,dc=b\nchangetype: modify\nadd: cn\nsn: x\n-\n", "value line names"},
		{"add with no values", "dn: cn=x,dc=a,dc=b\nchangetype: modify\nadd: cn\n-\n", "at least one"},
		{"modrdn without newrdn", "dn: cn=x,dc=a,dc=b\nchangetype: modrdn\ndeleteoldrdn: 1\n", "newrdn"},
		{"modrdn without deleteoldrdn", "dn: cn=x,dc=a,dc=b\nchangetype: modrdn\nnewrdn: cn=y\n", "deleteoldrdn"},
		{"bad deleteoldrdn", "dn: cn=x,dc=a,dc=b\nchangetype: modrdn\nnewrdn: cn=y\ndeleteoldrdn: yes\n", "0 or 1"},
		{"bad base64", "dn: cn=x,dc=a,dc=b\ncn:: !!!not base64!!!\n", "base64"},
		{"bad attribute name", "dn: cn=x,dc=a,dc=b\nbad name: x\n", "attribute type"},
		{"unsupported version", "version: 2\n\ndn: cn=x,dc=a,dc=b\ncn: x\n", "version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Unmarshal([]byte(tt.in))
			if err == nil {
				t.Fatalf("Unmarshal(%q) = nil error", tt.in)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestReadReportsLineNumbers(t *testing.T) {
	in := "dn: cn=x,dc=alder,dc=test\ncn: x\n\ndn: cn=y,dc=alder,dc=test\nchangetype: frobnicate\n"
	_, err := Unmarshal([]byte(in))
	var syn *SyntaxError
	if !errors.As(err, &syn) {
		t.Fatalf("error = %v, want a *SyntaxError", err)
	}
	if syn.Line != 5 {
		t.Errorf("Line = %d, want 5", syn.Line)
	}
}

func TestReadEmptyInput(t *testing.T) {
	for _, in := range []string{"", "\n\n\n", "# just a comment\n", "version: 1\n"} {
		recs, err := Unmarshal([]byte(in))
		if err != nil {
			t.Errorf("Unmarshal(%q) = %v, want no error and no records", in, err)
		}
		if len(recs) != 0 {
			t.Errorf("Unmarshal(%q) returned %d records", in, len(recs))
		}
	}
}

func TestReadRootDSERecord(t *testing.T) {
	// The empty DN names the RootDSE and is legal in LDIF.
	recs, err := Unmarshal([]byte("dn:\nobjectClass: top\n"))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !recs[0].DN.IsEmpty() {
		t.Errorf("DN = %s, want the empty DN", recs[0].DN)
	}
}

func TestWriterComments(t *testing.T) {
	var b strings.Builder
	w := NewWriter(&b)
	w.Comments = true
	rec := &Record{
		DN:    dn.MustParse("cn=x,dc=alder,dc=test"),
		Attrs: []Attribute{{Name: "cn", Values: bs("x")}},
	}
	if err := w.WriteRecord(rec); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	if !strings.HasPrefix(b.String(), "# cn=x,dc=alder,dc=test\n") {
		t.Errorf("output = %q, want a leading comment", b.String())
	}
	// A commented document must still parse, since the comment is dropped.
	if _, err := Unmarshal([]byte(b.String())); err != nil {
		t.Errorf("a document with comments failed to parse: %v", err)
	}
}

// FuzzUnmarshal asserts the reader never panics and never hangs on arbitrary
// input. LDIF reaches this package from a file the user uploaded, so it is
// untrusted in the strongest sense.
func FuzzUnmarshal(f *testing.F) {
	seeds := []string{
		"dn: cn=x,dc=a,dc=b\ncn: x\n",
		"version: 1\n\ndn:\n\n",
		"dn: cn=x,dc=a,dc=b\nchangetype: modify\nadd: cn\ncn: x\n-\n",
		"dn:: " + strings.Repeat("A", 100) + "\n",
		"dn: cn=x,dc=a,dc=b\ncn:< file:///etc/passwd\n",
		"#\n \n \n",
		"dn: cn=x,dc=a,dc=b\ncn: " + strings.Repeat("y", 500) + "\n",
		"\x00\x01\x02",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		recs, err := Unmarshal([]byte(in))
		if err != nil {
			return
		}
		// Anything that parsed must render, and what it renders must parse back
		// to the same number of records. A reader and writer that disagree is
		// how an export silently loses an entry.
		out, err := Marshal(recs)
		if err != nil {
			t.Fatalf("Marshal of a parsed document failed: %v", err)
		}
		again, err := Unmarshal(out)
		if err != nil {
			t.Fatalf("re-reading our own output failed: %v\noutput:\n%s", err, out)
		}
		if len(again) != len(recs) {
			t.Fatalf("round trip changed the record count: %d then %d\ninput:\n%q\noutput:\n%s",
				len(recs), len(again), in, out)
		}
	})
}

func bs(values ...string) [][]byte {
	out := make([][]byte, len(values))
	for i, v := range values {
		out[i] = []byte(v)
	}
	return out
}
