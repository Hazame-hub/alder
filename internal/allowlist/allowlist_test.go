package allowlist

import (
	"errors"
	"testing"
)

func TestParseNormalises(t *testing.T) {
	cases := []struct {
		name string
		host string
		port int
		want string
	}{
		{"a plain name", "directory.example.test", 389, "directory.example.test:389"},
		{"case is folded", "Directory.Example.TEST", 389, "directory.example.test:389"},
		{"a trailing dot is the same name", "directory.example.test.", 389, "directory.example.test:389"},
		{"surrounding space", "  directory.example.test  ", 636, "directory.example.test:636"},
		{"an underscore, which SRV names carry", "_ldap._tcp.example.test", 389, "_ldap._tcp.example.test:389"},
		{"an IPv4 literal", "10.0.0.5", 636, "10.0.0.5:636"},
		{"an IPv6 literal", "2001:db8::1", 636, "[2001:db8::1]:636"},
		{"a bracketed IPv6 literal", "[2001:db8::1]", 636, "[2001:db8::1]:636"},
		{"IPv6 case is folded by the parser", "2001:DB8::1", 636, "[2001:db8::1]:636"},
		{"an IPv4-mapped address is the IPv4 address", "::ffff:10.0.0.5", 389, "10.0.0.5:389"},
		{"loopback", "127.0.0.1", 389, "127.0.0.1:389"},
		{"IPv6 loopback", "::1", 389, "[::1]:389"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTarget(tc.host, tc.port)
			if err != nil {
				t.Fatalf("Parse(%q, %d): %v", tc.host, tc.port, err)
			}
			if got.String() != tc.want {
				t.Errorf("Parse(%q, %d) = %s, want %s", tc.host, tc.port, got, tc.want)
			}
		})
	}
}

func TestParseRefusesMalformed(t *testing.T) {
	cases := []struct {
		name string
		host string
		port int
	}{
		{"empty", "", 389},
		{"only space", "   ", 389},
		{"only a dot", ".", 389},
		{"port zero", "directory.example.test", 0},
		{"port too high", "directory.example.test", 65536},
		{"negative port", "directory.example.test", -1},
		{"an empty label", "directory..example.test", 389},
		{"a leading dot", ".example.test", 389},
		{"a stray bracket", "directory.example.test]", 389},
		{"a scheme is not a host", "ldap://directory.example.test", 389},
		{"a path is not a host", "directory.example.test/dc=x", 389},
		{"a port inside the host", "directory.example.test:389", 389},
		{"credentials", "user:pass@directory.example.test", 389},
		{"a space inside", "directory example.test", 389},
		{"a zone", "fe80::1%eth0", 389},
		{"a null byte", "directory.example.test\x00.evil.test", 389},
		{"a newline", "directory.example.test\nevil.test", 389},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseTarget(tc.host, tc.port); err == nil {
				t.Errorf("Parse(%q, %d) = %s, want an error", tc.host, tc.port, got)
			}
		})
	}
}

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"host and port", "ldap1.example.com:389", "ldap1.example.com:389"},
		{"host alone means any port", "ldap1.example.com", "ldap1.example.com"},
		{"an ldap URL defaults to 389", "ldap://ldap1.example.com", "ldap1.example.com:389"},
		{"an ldaps URL defaults to 636", "ldaps://ldap1.example.com", "ldap1.example.com:636"},
		{"a URL with an explicit port", "ldaps://ldap1.example.com:1636", "ldap1.example.com:1636"},
		{"the scheme is case-insensitive", "LDAPS://LDAP1.EXAMPLE.COM", "ldap1.example.com:636"},
		{"an IPv6 endpoint", "[2001:db8::1]:636", "[2001:db8::1]:636"},
		{"a bare IPv6 literal means any port", "2001:db8::1", "2001:db8::1"},
		{"an IPv6 URL", "ldaps://[2001:db8::1]", "[2001:db8::1]:636"},
		{"an IPv4 endpoint", "10.0.0.5:389", "10.0.0.5:389"},
		{"surrounding space", "  ldap1.example.com:389  ", "ldap1.example.com:389"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseEndpoint(tc.in)
			if err != nil {
				t.Fatalf("ParseEndpoint(%q): %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Errorf("ParseEndpoint(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseEndpointRefusesMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"a scheme that is not LDAP", "https://ldap1.example.com"},
		{"a scheme that is not LDAP at all", "file:///etc/passwd"},
		{"no host", "ldaps://"},
		{"a path", "ldap://ldap1.example.com/dc=example,dc=com"},
		{"a query", "ldap://ldap1.example.com?x=1"},
		{"a fragment", "ldap://ldap1.example.com#x"},
		// A URL is where a password ends up in a configuration file. Refusing
		// the shape is what stops one being written down at all.
		{"credentials in a URL", "ldap://someone:hunter2@ldap1.example.com"},
		{"an empty userinfo", "ldap://@ldap1.example.com"},
		{"a port that is not a number", "ldap1.example.com:ldap"},
		{"a port out of range", "ldap1.example.com:70000"},
		{"an unclosed bracket", "[2001:db8::1:636"},
		{"a bracket with a trailing mess", "[2001:db8::1]636"},
		{"an empty label", "ldap1..example.com:389"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ParseEndpoint(tc.in); err == nil {
				t.Errorf("ParseEndpoint(%q) = %s, want an error", tc.in, got)
			}
		})
	}
}

// An allowlist nobody configured permits everything, which is what keeps the
// first run of Alder a single command.
func TestAnEmptyAllowlistPermitsEverything(t *testing.T) {
	for _, s := range []string{"", "   ", ",", " , "} {
		list, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if list.Enabled() {
			t.Errorf("Parse(%q) is enabled", s)
		}
		if err := list.Check("anything.example.test", 389); err != nil {
			t.Errorf("Parse(%q) refused a target: %v", s, err)
		}
	}
}

// A malformed target is refused whether or not the list is on, so switching the
// allowlist on changes which destinations are reachable and not which inputs
// parse.
func TestAnUnconfiguredListStillRefusesMalformedTargets(t *testing.T) {
	list, err := Parse("")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := list.Check("ldap://x.example.test", 389); err == nil {
		t.Error("an unconfigured allowlist accepted a URL as a host")
	}
	if errors.Is(err, ErrNotAllowed) {
		t.Error("a malformed host was reported as not allowed rather than as malformed")
	}
}

func TestParseRefusesABadEntry(t *testing.T) {
	// One typo must stop the server rather than quietly permit one host fewer
	// than the operator wrote down.
	if _, err := Parse("good.example.test:389,https://bad.example.test"); err == nil {
		t.Error("Parse accepted a list with an unusable entry")
	}
}

func TestAllowlistMatching(t *testing.T) {
	const list = "ldap1.example.com:389, ldaps://ldap2.example.com, 10.0.0.5, [2001:db8::1]:636"

	allowed := []struct {
		name string
		host string
		port int
	}{
		{"the exact entry", "ldap1.example.com", 389},
		{"case folded", "LDAP1.Example.COM", 389},
		{"a trailing dot", "ldap1.example.com.", 389},
		{"the ldaps URL's default port", "ldap2.example.com", 636},
		{"a host without a port, on any port", "10.0.0.5", 389},
		{"the same host on another port", "10.0.0.5", 636},
		{"an IPv4-mapped spelling of it", "::ffff:10.0.0.5", 389},
		{"an IPv6 entry", "2001:db8::1", 636},
		{"the same address bracketed", "[2001:db8::1]", 636},
		{"the same address in upper case", "2001:DB8::1", 636},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			a, err := Parse(list)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if err := a.Check(tc.host, tc.port); err != nil {
				t.Errorf("Check(%q, %d) refused: %v", tc.host, tc.port, err)
			}
		})
	}

	denied := []struct {
		name string
		host string
		port int
	}{
		{"a port that was not listed", "ldap1.example.com", 636},
		{"a host that was not listed", "ldap3.example.com", 389},
		// The two that a substring test gets wrong, which is why there is not
		// one anywhere in this package.
		{"a longer name ending in an entry", "evil-ldap1.example.com", 389},
		{"a subdomain of an entry", "ldap1.example.com.evil.test", 389},
		{"an entry that is a prefix of the target", "ldap1.example.common", 389},
		{"a different address in the same range", "10.0.0.6", 389},
		{"the IPv6 entry on another port", "2001:db8::1", 389},
		{"a different IPv6 address", "2001:db8::2", 636},
		// Neither of these is reachable through an entry for a name. Alder does
		// not resolve, so an address is only ever matched against an address.
		{"loopback", "127.0.0.1", 389},
		{"link-local metadata", "169.254.169.254", 389},
	}
	for _, tc := range denied {
		t.Run("denied/"+tc.name, func(t *testing.T) {
			a, err := Parse(list)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			err = a.Check(tc.host, tc.port)
			if err == nil {
				t.Fatalf("Check(%q, %d) was permitted", tc.host, tc.port)
			}
			if !errors.Is(err, ErrNotAllowed) {
				t.Errorf("Check(%q, %d) = %v, want ErrNotAllowed", tc.host, tc.port, err)
			}
		})
	}
}

// The refusal names the target so an operator can see what to add, and nothing
// else: it is rendered from the parsed target rather than from the input, so
// there is nothing of the caller's to echo back.
func TestARefusalNamesTheNormalisedTarget(t *testing.T) {
	a, err := Parse("ldap1.example.com:389")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = a.Check("Other.Example.COM.", 636)
	if err == nil {
		t.Fatal("permitted")
	}
	if got, want := err.Error(), "other.example.com:636"; !contains(got, want) {
		t.Errorf("the refusal %q does not name %q", got, want)
	}
}

func TestEndpointsAreReportedForLogging(t *testing.T) {
	a, err := Parse("ldap1.example.com:389,ldaps://ldap2.example.com,10.0.0.5")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := a.Endpoints()
	want := []string{"ldap1.example.com:389", "ldap2.example.com:636", "10.0.0.5"}
	if len(got) != len(want) {
		t.Fatalf("Endpoints() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Endpoints()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A nil list is what a Server built without one holds, and it must behave like
// an unconfigured one rather than panicking.
func TestANilAllowlistPermitsEverything(t *testing.T) {
	var a *List
	if a.Enabled() {
		t.Error("a nil allowlist is enabled")
	}
	if err := a.Check("directory.example.test", 389); err != nil {
		t.Errorf("a nil allowlist refused: %v", err)
	}
	if got := a.Endpoints(); got != nil {
		t.Errorf("a nil allowlist reported endpoints: %v", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
