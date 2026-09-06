package api

import (
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/dn"
	"github.com/hazame-hub/alder/internal/filter"
)

func target() commandTarget {
	return commandTarget{
		Host: "ldap.example.test", Port: 636, TLS: "ldaps",
		BindDN: "cn=admin,dc=alder,dc=test",
	}
}

func req(t *testing.T, base, f string) directory.SearchRequest {
	t.Helper()
	b, err := dn.Parse(base)
	if err != nil {
		t.Fatalf("parsing base %q: %v", base, err)
	}
	parsed, err := filter.Parse(f)
	if err != nil {
		t.Fatalf("parsing filter %q: %v", f, err)
	}
	return directory.SearchRequest{
		BaseDN: b, Scope: directory.ScopeSubtree, Filter: parsed, Limit: 100,
	}
}

// The rule that matters most here: a bind password never leaves the session,
// and this is a new surface that could carry one out.
func TestTheCommandNeverCarriesAPassword(t *testing.T) {
	got := searchCommand(target(), req(t, "dc=alder,dc=test", "(objectClass=*)"))
	if !strings.Contains(got, " -W") {
		t.Error("no -W: ldapsearch would bind anonymously rather than prompt")
	}
	// -w is the flag that takes a password on the command line. Its presence
	// would mean somebody had wired a real one through.
	if strings.Contains(got, " -w ") || strings.Contains(got, " -w'") {
		t.Errorf("the command uses -w, which takes a password inline:\n%s", got)
	}
}

func TestTheCommandNamesTheConnection(t *testing.T) {
	got := searchCommand(target(), req(t, "ou=people,dc=alder,dc=test", "(uid=alice)"))
	for _, want := range []string{
		"ldapsearch -x",
		"ldaps://ldap.example.test:636",
		// No quotes: "=" and "," are not shell metacharacters, so quoting an
		// ordinary DN would be noise in something meant to be read.
		"-D cn=admin,dc=alder,dc=test",
		"-b ou=people,dc=alder,dc=test",
		"-s sub",
		"-z 100",
		"(uid=alice)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// StartTLS gets -ZZ, not -Z. The single form falls back to an unencrypted
// connection when the upgrade fails, which is not what the session did.
func TestStartTLSDemandsTheUpgrade(t *testing.T) {
	tgt := target()
	tgt.TLS = "starttls"
	tgt.Port = 389
	got := searchCommand(tgt, req(t, "dc=alder,dc=test", "(objectClass=*)"))
	if !strings.Contains(got, "-ZZ") {
		t.Errorf("StartTLS did not produce -ZZ:\n%s", got)
	}
	if !strings.Contains(got, "ldap://ldap.example.test:389") {
		t.Errorf("StartTLS should use the ldap:// scheme:\n%s", got)
	}
}

func TestPlaintextIsNotDressedUp(t *testing.T) {
	tgt := target()
	tgt.TLS = "plaintext"
	tgt.Port = 389
	got := searchCommand(tgt, req(t, "dc=alder,dc=test", "(objectClass=*)"))
	if strings.Contains(got, "ldaps://") || strings.Contains(got, "-ZZ") {
		t.Errorf("a plaintext session produced a command claiming TLS:\n%s", got)
	}
}

// The command has to reproduce what the session did, not what it should have
// done — otherwise it fails on a certificate the reader cannot see mentioned.
func TestAnUnverifiedSessionSaysSo(t *testing.T) {
	tgt := target()
	tgt.SkipVerify = true
	got := searchCommand(tgt, req(t, "dc=alder,dc=test", "(objectClass=*)"))
	if !strings.HasPrefix(got, "LDAPTLS_REQCERT=never") {
		t.Errorf("a session that skipped verification produced a verifying command:\n%s", got)
	}
}

func TestAPrivateCAIsMentionedBecauseItCannotBeInlined(t *testing.T) {
	tgt := target()
	tgt.CustomCA = true
	got := searchCommand(tgt, req(t, "dc=alder,dc=test", "(objectClass=*)"))
	if !strings.Contains(got, "LDAPTLS_CACERT") {
		t.Errorf("a private CA went unmentioned:\n%s", got)
	}
	// Saying "install the CA" and "skip verification" at once would be
	// contradictory advice.
	if strings.Contains(got, "REQCERT=never") {
		t.Errorf("a verifying session was told to skip verification:\n%s", got)
	}
}

func TestAnonymousOmitsTheBind(t *testing.T) {
	tgt := target()
	tgt.BindDN = ""
	got := searchCommand(tgt, req(t, "dc=alder,dc=test", "(objectClass=*)"))
	if strings.Contains(got, "-D ") || strings.Contains(got, " -W") {
		t.Errorf("an anonymous session produced a bind:\n%s", got)
	}
}

func TestScopeFollowsTheSearch(t *testing.T) {
	for scope, want := range map[directory.Scope]string{
		directory.ScopeBase:     "-s base",
		directory.ScopeOneLevel: "-s one",
		directory.ScopeSubtree:  "-s sub",
	} {
		r := req(t, "dc=alder,dc=test", "(objectClass=*)")
		r.Scope = scope
		if got := searchCommand(target(), r); !strings.Contains(got, want) {
			t.Errorf("scope %v did not produce %q:\n%s", scope, want, got)
		}
	}
}

// The command shows the filter the server would send, not the text that was
// typed. Normalising it is the point of parsing it, and a command built from
// the typed string would describe a different search from the one on screen.
func TestTheFilterIsTheParsedOneNotTheTypedOne(t *testing.T) {
	r := req(t, "dc=alder,dc=test", "(&(objectClass=person))")
	got := searchCommand(target(), r)
	rendered, err := r.Filter.Render()
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	if !strings.Contains(got, rendered) {
		t.Errorf("the command does not carry the filter the server would send (%q):\n%s",
			rendered, got)
	}
}

func TestRequestedAttributesAreListed(t *testing.T) {
	r := req(t, "dc=alder,dc=test", "(objectClass=*)")
	r.Attributes = []string{"cn", "mail", "employeeNumber"}
	got := searchCommand(target(), r)
	if !strings.Contains(got, "cn mail employeeNumber") {
		t.Errorf("requested attributes are missing:\n%s", got)
	}
}

// A DN or a filter pasted unquoted is the same injection this codebase escapes
// against everywhere else, aimed at a shell rather than at a directory.
func TestShellQuoting(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"a plain attribute name passes through", "objectClass", "objectClass"},
		{"a DN with spaces is quoted", "cn=Alice Liddell,dc=test", "'cn=Alice Liddell,dc=test'"},
		{"a filter's parentheses are quoted", "(uid=alice)", "'(uid=alice)'"},
		{"an ampersand cannot background the command", "(&(a=b)(c=d))", "'(&(a=b)(c=d))'"},
		{"a semicolon cannot start a second command", "a;rm -rf /", `'a;rm -rf /'`},
		{"a backtick is inert inside single quotes", "`id`", "'`id`'"},
		{"a dollar sign is inert inside single quotes", "$(id)", "'$(id)'"},
		{"empty becomes an explicit empty argument", "", "''"},
		// The one character single quotes cannot contain: close, escape, reopen.
		{"a single quote is broken out of the quoting", "O'Brien", `'O'\''Brien'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shellQuote(tc.in); got != tc.want {
				t.Errorf("shellQuote(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// A DN holding a quote reaches the command through shellQuote, so the whole
// argument stays one argument.
func TestADNWithAQuoteStaysOneArgument(t *testing.T) {
	// RFC 4514 has no escape for an apostrophe; it is written as itself, which
	// is exactly the character single-quoting cannot contain.
	got := searchCommand(target(), req(t, "cn=O'Brien,dc=alder,dc=test", "(objectClass=*)"))
	want := `-b 'cn=O'\''Brien,dc=alder,dc=test'`
	if !strings.Contains(got, want) {
		t.Errorf("a DN holding an apostrophe was not quoted into one argument:\nwant %s\nin\n%s",
			want, got)
	}
}
