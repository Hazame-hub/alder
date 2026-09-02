// Command gen writes the generated part of the conformance seed data.
//
// The output is committed, so bringing the harness up needs Docker and nothing
// else. Regenerate with "make seed" after changing anything here, and commit
// the result.
//
// Everything it emits is deterministic. There is no randomness and no clock:
// the same source always produces the same bytes, so a diff in the seed data
// is always a deliberate change.
//
// This file carries a small LDIF writer of its own. internal/ldif, which lands
// in M3, is the real one; duplicating a hundred lines here keeps the fixtures
// independent of the code under test, which is what you want from a fixture.
package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	suffix   = "dc=alder,dc=test"
	people   = "ou=people," + suffix
	groups   = "ou=groups," + suffix
	services = "ou=services," + suffix

	userCount = 300
)

var teams = []string{"platform", "network", "security", "support", "data", "release"}

// surnames and forenames are fixed lists; index arithmetic over them keeps the
// generator deterministic without a random source.
var forenames = []string{
	"Alice", "Bob", "Carol", "Dan", "Erin", "Frank", "Grace", "Heidi",
	"Ivan", "Judy", "Karl", "Lena", "Mallory", "Niaj", "Olivia", "Peggy",
	"Quentin", "Rupert", "Sybil", "Trent", "Uma", "Victor", "Wendy", "Xavier",
	"Yasmin", "Zach",
}

var surnames = []string{
	"Adler", "Birch", "Cedar", "Douglas", "Elm", "Fir", "Gum", "Hazel",
	"Ironwood", "Juniper", "Katsura", "Larch", "Maple", "Nutmeg", "Oak",
	"Poplar", "Quince", "Rowan", "Spruce", "Teak", "Umbrella", "Vine",
	"Willow", "Yew", "Zelkova",
}

func main() {
	out := flag.String("out", ".", "directory to write the generated LDIF into")
	flag.Parse()

	files := map[string]func() []byte{
		"10-users.ldif":      usersLDIF,
		"20-groups.ldif":     groupsLDIF,
		"30-edge-cases.ldif": edgeCasesLDIF,
	}
	for name, build := range files {
		path := filepath.Join(*out, name)
		if err := os.WriteFile(path, build(), 0o644); err != nil {
			log.Fatalf("writing %s: %v", path, err)
		}
		fmt.Println("wrote", path)
	}
}

func usersLDIF() []byte {
	var b bytes.Buffer
	header(&b, "Employee entries.",
		fmt.Sprintf("%d users spread evenly over %d teams, so that a filter on", userCount, len(teams)),
		"alderTeam returns a predictable count on both servers.")

	for i := 1; i <= userCount; i++ {
		uid := fmt.Sprintf("user%04d", i)
		given := forenames[i%len(forenames)]
		sur := surnames[(i/len(forenames))%len(surnames)]
		team := teams[i%len(teams)]

		e := newEntry("uid=" + uid + "," + people)
		e.add("objectClass", "top", "person", "organizationalPerson",
			"inetOrgPerson", "posixAccount", "alderEmployee")
		e.add("uid", uid)
		e.add("cn", given+" "+sur)
		e.add("sn", sur)
		e.add("givenName", given)
		e.add("mail", uid+"@alder.test")
		e.add("uidNumber", fmt.Sprint(10000+i))
		e.add("gidNumber", fmt.Sprint(20000+(i%len(teams))))
		e.add("homeDirectory", "/home/"+uid)
		e.add("loginShell", "/bin/bash")
		e.add("alderTeam", team)
		if i%7 == 0 {
			e.add("alderOnCall", "TRUE")
		}
		if i%11 == 0 {
			e.add("telephoneNumber", fmt.Sprintf("+49 30 %07d", i))
		}
		// A long value on some entries, to exercise LDIF line folding.
		if i%13 == 0 {
			e.add("description", "This description is deliberately longer than "+
				"the seventy-six column limit that RFC 2849 asks a writer to "+
				"fold at, so that any reader we write has to reassemble it.")
		}
		e.add("userPassword", uid+"-secret")
		e.writeTo(&b)
	}
	return b.Bytes()
}

func groupsLDIF() []byte {
	var b bytes.Buffer
	header(&b, "Groups, including nested ones.",
		"Each team is a groupOfNames. Two umbrella groups hold other groups as",
		"members, so that a naive member expansion recurses and a correct one",
		"terminates.")

	for gi, team := range teams {
		e := newEntry("cn=" + team + "," + groups)
		e.add("objectClass", "top", "groupOfNames")
		e.add("cn", team)
		e.add("description", "Members of the "+team+" team")
		for i := 1; i <= userCount; i++ {
			if i%len(teams) == gi {
				e.add("member", fmt.Sprintf("uid=user%04d,%s", i, people))
			}
		}
		e.writeTo(&b)
	}

	infra := newEntry("cn=infrastructure," + groups)
	infra.add("objectClass", "top", "groupOfNames")
	infra.add("cn", "infrastructure")
	infra.add("description", "A group whose members are groups")
	infra.add("member", "cn=platform,"+groups)
	infra.add("member", "cn=network,"+groups)
	infra.writeTo(&b)

	all := newEntry("cn=everyone," + groups)
	all.add("objectClass", "top", "groupOfNames")
	all.add("cn", "everyone")
	all.add("description", "Nested two levels deep, via cn=infrastructure")
	all.add("member", "cn=infrastructure,"+groups)
	all.add("member", "cn=security,"+groups)
	all.add("member", "cn=support,"+groups)
	all.add("member", "cn=data,"+groups)
	all.add("member", "cn=release,"+groups)
	all.writeTo(&b)

	return b.Bytes()
}

// edgeCasesLDIF holds the entries that exist to break a careless LDIF writer,
// a careless DN parser, or a UI that assumes ASCII.
func edgeCasesLDIF() []byte {
	var b bytes.Buffer
	header(&b, "Entries that are awkward on purpose.",
		"Non-ASCII RDNs, values that must be base64-encoded, a DN containing an",
		"escaped comma, and binary data. Every one of these has broken a real",
		"directory tool.")

	// A container whose RDN is not ASCII, so its DN line must be base64.
	ou := newEntry("ou=Zweigstelle München," + services)
	ou.add("objectClass", "top", "organizationalUnit")
	ou.add("ou", "Zweigstelle München")
	ou.add("description", "An RDN that is not ASCII")
	ou.writeTo(&b)

	cjk := newEntry("cn=日本語テスト," + people)
	cjk.add("objectClass", "top", "person", "organizationalPerson", "inetOrgPerson")
	cjk.add("cn", "日本語テスト")
	cjk.add("sn", "テスト")
	cjk.add("description", "A CJK RDN, three bytes per character")
	cjk.writeTo(&b)

	// An RDN containing a comma, escaped per RFC 4514. The escape belongs to
	// the DN syntax, so the value of cn has no backslash in it.
	comma := newEntry(`cn=Liddell\, Alice,` + people)
	comma.add("objectClass", "top", "person", "organizationalPerson", "inetOrgPerson")
	comma.add("cn", "Liddell, Alice")
	comma.add("sn", "Liddell")
	comma.add("description", "The RDN contains an escaped comma")
	comma.writeTo(&b)

	// Values that RFC 2849 forbids in plain form and that must be base64:
	// a leading space, a leading colon, a leading less-than, a trailing space,
	// an embedded newline, and non-ASCII bytes.
	awkward := newEntry("cn=awkward-values," + people)
	awkward.add("objectClass", "top", "person", "organizationalPerson",
		"inetOrgPerson", "alderEmployee")
	awkward.add("cn", "awkward-values")
	awkward.add("sn", "Awkward")
	awkward.add("alderTeam", "platform")
	awkward.add("alderNote", " value with a leading space")
	awkward.add("alderNote", "value with a trailing space ")
	awkward.add("alderNote", ":value that starts with a colon")
	awkward.add("alderNote", "<value that starts with a less-than")
	awkward.add("alderNote", "value with an\nembedded newline")
	awkward.add("alderNote", "vàlue with nön-ASCII bytes")
	awkward.add("description", strings.Repeat("long ", 40)+"end")
	awkward.writeTo(&b)

	// Binary data in an attribute whose syntax is not a directory string. The
	// bytes below are a one-pixel JPEG, kept short on purpose.
	photo := newEntry("cn=binary-values," + people)
	photo.add("objectClass", "top", "person", "organizationalPerson",
		"inetOrgPerson", "alderEmployee")
	photo.add("cn", "binary-values")
	photo.add("sn", "Binary")
	photo.add("alderTeam", "platform")
	photo.addRaw("alderBadgePhoto", tinyJPEG())
	photo.writeTo(&b)

	return b.Bytes()
}

// --- a very small LDIF writer ------------------------------------------------

type entry struct {
	dn    string
	attrs []attr
}

type attr struct {
	name   string
	values [][]byte
}

func newEntry(dn string) *entry { return &entry{dn: dn} }

func (e *entry) add(name string, values ...string) {
	a := attr{name: name}
	for _, v := range values {
		a.values = append(a.values, []byte(v))
	}
	e.attrs = append(e.attrs, a)
}

func (e *entry) addRaw(name string, value []byte) {
	e.attrs = append(e.attrs, attr{name: name, values: [][]byte{value}})
}

func (e *entry) writeTo(b *bytes.Buffer) {
	writeLine(b, "dn", []byte(e.dn))
	for _, a := range e.attrs {
		for _, v := range a.values {
			writeLine(b, a.name, v)
		}
	}
	b.WriteString("\n")
}

// writeLine emits one attribute line, base64-encoding when RFC 2849 requires
// it and folding at 76 columns.
func writeLine(b *bytes.Buffer, name string, value []byte) {
	var line string
	if needsBase64(value) {
		line = name + ":: " + base64.StdEncoding.EncodeToString(value)
	} else {
		line = name + ": " + string(value)
	}
	b.WriteString(fold(line))
	b.WriteString("\n")
}

// needsBase64 reports whether a value may not be written in plain form. The
// rules are RFC 2849's SAFE-STRING: no leading space, colon or less-than, no
// trailing space, no NUL, CR or LF, and nothing above US-ASCII.
func needsBase64(v []byte) bool {
	if len(v) == 0 {
		return false
	}
	switch v[0] {
	case ' ', ':', '<':
		return true
	}
	if v[len(v)-1] == ' ' {
		return true
	}
	for _, c := range v {
		if c == 0 || c == '\r' || c == '\n' || c > 0x7f {
			return true
		}
	}
	return false
}

// fold breaks a line at 76 columns, continuing with a single leading space.
// It folds on byte boundaries, which is what RFC 2849 specifies; a reader
// rejoins the bytes before decoding UTF-8, so splitting a multi-byte character
// is legal and is worth having in the fixtures.
func fold(line string) string {
	const width = 76
	if len(line) <= width {
		return line
	}
	var b strings.Builder
	b.WriteString(line[:width])
	for i := width; i < len(line); i += width - 1 {
		end := min(i+width-1, len(line))
		b.WriteString("\n ")
		b.WriteString(line[i:end])
	}
	return b.String()
}

func header(b *bytes.Buffer, lines ...string) {
	b.WriteString("# Generated by test/compose/seed/gen. Do not edit; run \"make seed\".\n#\n")
	for _, l := range lines {
		b.WriteString("# " + l + "\n")
	}
	b.WriteString("\n")
}

// tinyJPEG returns the smallest thing that is recognisably a JPEG: the SOI
// marker, a comment, and the EOI marker. It is not a valid image and is not
// meant to be; it is a run of bytes that must survive a round trip unchanged.
func tinyJPEG() []byte {
	b := []byte{0xff, 0xd8, 0xff, 0xfe, 0x00, 0x10}
	b = append(b, []byte("alder-test-badge")...)
	b = append(b, 0xff, 0xd9)
	return b
}
