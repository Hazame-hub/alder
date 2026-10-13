package project

import (
	"errors"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/dn"
)

// suffixYAML is validYAML written under dc=alder,dc=test, with a dev
// environment under dc=dev,dc=alder,dc=test and a prod environment that
// holds the files as written.
const suffixYAML = `version: 1
suffix: dc=alder,dc=test
managed:
  - base: ou=people,dc=alder,dc=test
    files: [people/*.ldif]
  - base: ou=groups,dc=alder,dc=test
    files: [groups.ldif]
environments:
  dev:
    api-url: https://alder.dev.example.com
    host: ldap.dev.example.com
    bind-dn: cn=alder,ou=services,dc=dev,dc=alder,dc=test
    bind-password-env: ALDER_DEV_BIND_PASSWORD
    suffix: dc=dev,dc=alder,dc=test
  prod:
    api-url: https://alder.example.com
    host: ldap.example.com
    bind-dn: cn=alder,ou=services,dc=alder,dc=test
    bind-password-env: ALDER_PROD_BIND_PASSWORD
`

// schemaKinds is a directory schema reduced to what ForEnvironment asks.
func schemaKinds(extra map[string]DNKind) DNKinds {
	known := map[string]DNKind{
		"objectclass": NotDN, "uid": NotDN, "cn": NotDN, "sn": NotDN, "description": NotDN, "ou": NotDN, "dc": NotDN,
		"member": DistinguishedName, "seealso": DistinguishedName, "manager": DistinguishedName,
		"uniquemember": NameAndOptionalUID,
	}
	for k, v := range extra {
		known[k] = v
	}
	return func(attr string) (DNKind, bool) {
		k, ok := known[strings.ToLower(attr)]
		return k, ok
	}
}

func suffixed(edit func(f map[string]string)) map[string]string {
	files := valid()
	files["alder.yaml"] = suffixYAML
	if edit != nil {
		edit(files)
	}
	return files
}

func TestAnEnvironmentSuffixRebasesEveryNameAndOnlyNames(t *testing.T) {
	files := suffixed(func(f map[string]string) {
		f["groups.ldif"] = `dn: cn=admins,ou=groups,dc=alder,dc=test
objectClass: groupOfNames
cn: admins
member: uid=alice,ou=people,dc=alder,dc=test
member: UID=Bob, OU=People, DC=Alder, DC=Test
member: cn=someone,dc=elsewhere,dc=test
seeAlso: not a DN at all
description: uid=alice,ou=people,dc=alder,dc=test

dn: cn=unique,ou=groups,dc=alder,dc=test
objectClass: groupOfUniqueNames
cn: unique
uniqueMember: uid=alice,ou=people,dc=alder,dc=test#'0101'B
uniqueMember: uid=bob,ou=people,dc=alder,dc=test
`
	})
	p, err := load(t, write(t, files))
	if err != nil {
		t.Fatalf("refused: %s", problemsOf(err))
	}
	before, _ := p.Document()

	q, rb, err := p.ForEnvironment("dev", schemaKinds(nil))
	if err != nil {
		t.Fatal(err)
	}
	if rb == nil || rb.From.String() != "dc=alder,dc=test" || rb.To.String() != "dc=dev,dc=alder,dc=test" {
		t.Fatalf("rebase = %+v", rb)
	}
	if rb.Entries != 4 || rb.Values != 4 {
		t.Errorf("moved %d entries and %d values, want 4 and 4", rb.Entries, rb.Values)
	}
	if len(rb.LeftAsWritten) != 1 || rb.LeftAsWritten[0].Attribute != "description" ||
		rb.LeftAsWritten[0].DN.String() != "cn=admins,ou=groups,dc=dev,dc=alder,dc=test" {
		t.Errorf("left as written = %+v, want the description of cn=admins", rb.LeftAsWritten)
	}
	if q.Managed[0].Base.String() != "ou=people,dc=dev,dc=alder,dc=test" ||
		q.Managed[1].Base.String() != "ou=groups,dc=dev,dc=alder,dc=test" {
		t.Errorf("bases = %s, %s", q.Managed[0].Base, q.Managed[1].Base)
	}

	doc, _ := q.Document()
	for _, want := range []string{
		"dn: uid=alice,ou=people,dc=dev,dc=alder,dc=test\n",
		"dn: cn=admins,ou=groups,dc=dev,dc=alder,dc=test\n",
		"member: uid=alice,ou=people,dc=dev,dc=alder,dc=test\n",
		// Rendered as the dn package renders a DN: the RDNs above the
		// suffix keep their values, the spelling is Alder's.
		"member: UID=Bob,OU=People,dc=dev,dc=alder,dc=test\n",
		// Outside the suffix, and not a DN: the directory's to judge.
		"member: cn=someone,dc=elsewhere,dc=test\n",
		"seeAlso: not a DN at all\n",
		// Not DN syntax, however much it looks like one.
		"description: uid=alice,ou=people,dc=alder,dc=test\n",
		"uniqueMember: uid=alice,ou=people,dc=dev,dc=alder,dc=test#'0101'B\n",
		"uniqueMember: uid=bob,ou=people,dc=dev,dc=alder,dc=test\n",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the rebased document lacks %q:\n%s", want, doc)
		}
	}

	// The project itself is not changed.
	if again, _ := p.Document(); again != before {
		t.Errorf("ForEnvironment changed the project it was given")
	}

	// An environment without a suffix plans the files as written.
	same, rb, err := p.ForEnvironment("prod", schemaKinds(nil))
	if err != nil || rb != nil || same != p {
		t.Errorf("prod: %v, %+v; want the project unchanged", err, rb)
	}
}

func TestASuffixEqualToTheProjectsIsNoRebase(t *testing.T) {
	files := suffixed(func(f map[string]string) {
		f["alder.yaml"] = strings.Replace(suffixYAML, "suffix: dc=dev,dc=alder,dc=test", "suffix: DC=Alder, DC=Test", 1)
	})
	p, err := load(t, write(t, files))
	if err != nil {
		t.Fatalf("refused: %s", problemsOf(err))
	}
	if _, _, ok := p.Rebasing("dev"); ok {
		t.Error("a suffix equal to the project's, spelled differently, is a rebase")
	}
}

// Whether a value is a name is the schema's answer; with no answer, nothing
// is guessed.
func TestRebasingRefusesWhatItCannotTell(t *testing.T) {
	p, err := load(t, write(t, suffixed(func(f map[string]string) {
		f["people/a.ldif"] = peopleA + "x-custom-ref: uid=bob,ou=people,dc=alder,dc=test\n"
	})))
	if err != nil {
		t.Fatalf("refused: %s", problemsOf(err))
	}
	_, _, err = p.ForEnvironment("dev", schemaKinds(nil))
	var un *Unrebasable
	if !errors.As(err, &un) || !strings.Contains(err.Error(), "does not define x-custom-ref") {
		t.Fatalf("want an attribute the schema does not define refused, got %v", err)
	}
	// Once the schema says it is a DN, it is moved like member.
	q, rb, err := p.ForEnvironment("dev", schemaKinds(map[string]DNKind{"x-custom-ref": DistinguishedName}))
	if err != nil || rb.Values != 2 {
		t.Fatalf("err = %v, rebase = %+v", err, rb)
	}
	if doc, _ := q.Document(); !strings.Contains(doc, "x-custom-ref: uid=bob,ou=people,dc=dev,dc=alder,dc=test\n") {
		t.Errorf("not moved:\n%s", doc)
	}
}

// An entry named by a DN-valued attribute below the suffix cannot keep both
// its name and its value once one of them moves.
func TestRebasingRefusesAnEntryNamedByADN(t *testing.T) {
	p, err := load(t, write(t, suffixed(func(f map[string]string) {
		f["groups.ldif"] = `dn: seeAlso=uid\=alice\,ou\=people\,dc\=alder\,dc\=test,ou=groups,dc=alder,dc=test
objectClass: extensibleObject
seeAlso: uid=alice,ou=people,dc=alder,dc=test
`
	})))
	if err != nil {
		t.Fatalf("refused: %s", problemsOf(err))
	}
	_, _, err = p.ForEnvironment("dev", schemaKinds(nil))
	if err == nil || !strings.Contains(err.Error(), "is named by seeAlso") {
		t.Fatalf("want the entry refused, got %v", err)
	}
}

func TestTheSuffixKeysAreChecked(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"an environment suffix with no project suffix",
			strings.Replace(suffixYAML, "suffix: dc=alder,dc=test\n", "", 1),
			"environments.dev: suffix replaces the project's suffix, and alder.yaml names none"},
		{"a project suffix that is not a DN",
			strings.Replace(suffixYAML, "suffix: dc=alder,dc=test\n", "suffix: alder.test\n", 1),
			`suffix "alder.test" is not a DN`},
		{"an environment suffix that is not a DN",
			strings.Replace(suffixYAML, "suffix: dc=dev,dc=alder,dc=test", "suffix: dev.alder.test", 1),
			`environments.dev: suffix "dev.alder.test" is not a DN`},
		{"a base outside the project suffix",
			strings.Replace(suffixYAML, "suffix: dc=alder,dc=test\n", "suffix: dc=example,dc=com\n", 1),
			"base ou=people,dc=alder,dc=test is outside the project's suffix dc=example,dc=com"},
		{"a misspelt key, still refused",
			strings.Replace(suffixYAML, "    suffix: dc=dev", "    sufix: dc=dev", 1),
			"not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, write(t, suffixed(func(f map[string]string) { f["alder.yaml"] = tc.yaml })))
			if text := problemsOf(err); !strings.Contains(text, tc.want) {
				t.Errorf("problems do not mention %q:\n%s", tc.want, text)
			}
		})
	}
}

// The suffix entry itself would change its RDN while keeping the value that
// names it, so a project that describes it cannot be rebased onto a suffix
// with another RDN -- but can onto one with the same RDN.
func TestTheSuffixEntryIsNotRenamed(t *testing.T) {
	yaml := strings.Replace(suffixYAML, "  - base: ou=groups,dc=alder,dc=test\n    files: [groups.ldif]\n",
		"  - base: ou=groups,dc=alder,dc=test\n    files: [groups.ldif]\n  - base: dc=alder,dc=test\n    files: [top.ldif]\n", 1)
	// Overlapping bases are refused on their own, so the suffix entry goes
	// in a project whose only base is the suffix.
	yaml = strings.Replace(yaml, "  - base: ou=people,dc=alder,dc=test\n    files: [people/*.ldif]\n  - base: ou=groups,dc=alder,dc=test\n    files: [groups.ldif]\n", "", 1)
	top := "dn: dc=alder,dc=test\nobjectClass: dcObject\nobjectClass: organization\ndc: alder\no: Alder\n"
	files := map[string]string{"alder.yaml": yaml, "top.ldif": top}

	_, err := load(t, write(t, files))
	if text := problemsOf(err); !strings.Contains(text, "environments.dev: suffix dc=dev,dc=alder,dc=test would rename dc=alder,dc=test") ||
		!strings.Contains(text, "its own dc value stays as written") {
		t.Errorf("problems:\n%s", text)
	}

	files["alder.yaml"] = strings.Replace(yaml, "suffix: dc=dev,dc=alder,dc=test", "suffix: dc=alder,dc=dev", 1)
	p, err := load(t, write(t, files))
	if err != nil {
		t.Fatalf("a suffix with the same RDN was refused: %s", problemsOf(err))
	}
	q, _, err := p.ForEnvironment("dev", schemaKinds(map[string]DNKind{"o": NotDN}))
	if err != nil || !q.Managed[0].Records[0].DN.Equal(dn.MustParse("dc=alder,dc=dev")) {
		t.Errorf("err = %v", err)
	}
}

func TestDNKindOfSyntax(t *testing.T) {
	for oid, want := range map[string]DNKind{
		SyntaxDN:                          DistinguishedName,
		" " + SyntaxNameAndOptionalUID:    NameAndOptionalUID,
		"1.3.6.1.4.1.1466.115.121.1.15":   NotDN, // Directory String
		"1.3.6.1.4.1.1466.115.121.1.12.1": NotDN,
	} {
		if got := DNKindOfSyntax(oid); got != want {
			t.Errorf("DNKindOfSyntax(%q) = %v, want %v", oid, got, want)
		}
	}
}
