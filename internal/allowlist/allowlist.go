// Package allowlist decides which directories Alder is permitted to connect to.
//
// Alder takes the host and port to connect to from the request body, which is
// what makes it a tool rather than a fixed deployment: an operator points it at
// whichever directory they are working on. SECURITY.md has always said the
// consequence out loud — anyone who can reach the HTTP endpoint can make Alder
// open a connection to a host of their choosing, which on a network where Alder
// can reach more than the caller can is a server-side request forgery with a
// friendly interface.
//
// An allowlist turns that from a property of the network into a decision the
// operator makes. It is off by default, because the tool is normally run beside
// the directory by the person who owns both and a mandatory allowlist would be
// a configuration step before the first useful screen. It is one variable to
// turn on.
//
// What it is not: a substitute for keeping Alder off the public internet. It
// bounds where a caller can send Alder, not who the caller is.
package allowlist

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// ErrNotAllowed is returned when a target is well formed and not permitted.
//
// Separate from a parse failure on purpose. "You typed this wrong" and "you may
// not go there" are different answers, they belong to different HTTP statuses,
// and collapsing them tells a caller probing for reachable hosts less than the
// distinction costs.
var ErrNotAllowed = errors.New("allowlist: not in the allowlist")

// Target is a directory Alder has been asked to connect to.
type Target struct {
	// Host is the host as given, with any brackets and trailing dot removed and
	// ASCII case folded. It is not resolved.
	Host string
	// Port is the TCP port. Always set; the caller supplies the default.
	Port int
	// IP is set when Host was a literal address rather than a name.
	IP netip.Addr
}

// String renders the target the way the allowlist is written.
func (t Target) String() string {
	if t.IP.IsValid() && t.IP.Is6() {
		return "[" + t.Host + "]:" + strconv.Itoa(t.Port)
	}
	return t.Host + ":" + strconv.Itoa(t.Port)
}

// Parse normalises a host and port into a Target.
//
// Normalisation is the whole of the security here. Two spellings of one
// endpoint must compare equal, or an allowlist is a list of the spellings
// somebody happened to think of; and a spelling that is not an endpoint at all
// must be refused rather than normalised into something that matches.
func ParseTarget(host string, port int) (Target, error) {
	h := strings.TrimSpace(host)
	if h == "" {
		return Target{}, errors.New("allowlist: the host is empty")
	}
	if port < 1 || port > 65535 {
		return Target{}, fmt.Errorf("allowlist: port %d is out of range", port)
	}

	// A bracketed literal is how an IPv6 address is written beside a port, and
	// it arrives that way from a URL. Bare is how it arrives from a form.
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	if strings.ContainsAny(h, "[]") {
		return Target{}, fmt.Errorf("allowlist: %q is not a host", host)
	}

	if addr, err := netip.ParseAddr(h); err == nil {
		// A zone makes two spellings of one address that are not the same
		// destination, and it is not something an allowlist entry can usefully
		// carry. Refused rather than silently dropped, which would let
		// fe80::1%evil match an entry for fe80::1.
		if addr.Zone() != "" {
			return Target{}, fmt.Errorf("allowlist: %q carries a zone, which is not supported", host)
		}
		// Unmap so that ::ffff:127.0.0.1 and 127.0.0.1 are one entry rather
		// than two, because they are one destination.
		addr = addr.Unmap()
		return Target{Host: addr.String(), Port: port, IP: addr}, nil
	}

	name, err := normaliseName(h)
	if err != nil {
		return Target{}, err
	}
	return Target{Host: name, Port: port}, nil
}

// ParseEndpoint parses one allowlist entry.
//
// It accepts "host", "host:port", "[::1]:636", and an ldap:// or ldaps:// URL,
// because those are the three ways an operator already writes a directory down
// and guessing which one they meant is worse than accepting all of them. A
// missing port means every port on that host.
func ParseEndpoint(s string) (Endpoint, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Endpoint{}, errors.New("allowlist: empty allowlist entry")
	}

	if i := strings.Index(raw, "://"); i >= 0 {
		return parseURLEndpoint(raw, raw[:i])
	}

	host, portText, err := splitHostPort(raw)
	if err != nil {
		return Endpoint{}, err
	}
	if portText == "" {
		t, perr := ParseTarget(host, 389) // any port, so the number is a placeholder
		if perr != nil {
			return Endpoint{}, perr
		}
		return Endpoint{Host: t.Host, AnyPort: true}, nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return Endpoint{}, fmt.Errorf("allowlist: %q has a port that is not a number", s)
	}
	t, err := ParseTarget(host, port)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Host: t.Host, Port: t.Port}, nil
}

// parseURLEndpoint handles the ldap:// and ldaps:// forms.
//
// Hand-split rather than net/url.Parse: a URL carries a userinfo section, and
// ldap://someone:hunter2@directory.example.test is a way to put a password in a
// configuration file. Refusing the whole shape is better than parsing it and
// throwing the credential away, because the credential would already have been
// written down.
func parseURLEndpoint(raw, scheme string) (Endpoint, error) {
	defaultPort := 0
	switch strings.ToLower(scheme) {
	case "ldap":
		defaultPort = 389
	case "ldaps":
		defaultPort = 636
	default:
		return Endpoint{}, fmt.Errorf("allowlist: %q is not an ldap:// or ldaps:// URL", raw)
	}

	rest := raw[len(scheme)+len("://"):]
	// Everything a URL can carry after the authority is meaningless to a
	// connection and is refused rather than ignored: an entry with a path is an
	// operator expecting something this does not do.
	if strings.ContainsAny(rest, "/?#") {
		return Endpoint{}, fmt.Errorf(
			"allowlist: %q has a path or query; an allowlist entry is a host and a port", raw)
	}
	if strings.Contains(rest, "@") {
		return Endpoint{}, fmt.Errorf(
			"allowlist: %q carries credentials; an allowlist entry is a host and a port", raw)
	}

	host, portText, err := splitHostPort(rest)
	if err != nil {
		return Endpoint{}, err
	}
	port := defaultPort
	if portText != "" {
		port, err = strconv.Atoi(portText)
		if err != nil {
			return Endpoint{}, fmt.Errorf("allowlist: %q has a port that is not a number", raw)
		}
	}
	t, err := ParseTarget(host, port)
	if err != nil {
		return Endpoint{}, err
	}
	return Endpoint{Host: t.Host, Port: t.Port}, nil
}

// splitHostPort separates a host from an optional port, keeping an IPv6
// literal's colons out of it.
func splitHostPort(s string) (host, port string, err error) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", fmt.Errorf("allowlist: %q has an unclosed bracket", s)
		}
		host = s[1:end]
		switch tail := s[end+1:]; {
		case tail == "":
			return host, "", nil
		case strings.HasPrefix(tail, ":"):
			return host, tail[1:], nil
		default:
			return "", "", fmt.Errorf("allowlist: %q is not a host and port", s)
		}
	}
	// A bare IPv6 literal has more than one colon and no port. Anything with
	// exactly one is host:port.
	if strings.Count(s, ":") > 1 {
		return s, "", nil
	}
	if h, p, found := strings.Cut(s, ":"); found {
		return h, p, nil
	}
	return s, "", nil
}

// normaliseName folds a DNS name to its comparable form.
func normaliseName(h string) (string, error) {
	// A trailing dot makes a name absolute and does not change where it points,
	// so "directory.example.test." must not slip past an entry written without
	// it.
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", errors.New("allowlist: the host is empty")
	}
	if len(h) > 253 {
		return "", errors.New("allowlist: the host is longer than a DNS name may be")
	}
	// ASCII case folding only. A name is compared byte for byte after this, and
	// Unicode folding would make two different names equal; an internationalised
	// name has to arrive already in its A-label form, which is what a resolver
	// wants anyway.
	h = strings.ToLower(h)
	for _, label := range strings.Split(h, ".") {
		if label == "" {
			return "", fmt.Errorf("allowlist: %q has an empty label", h)
		}
		if len(label) > 63 {
			return "", fmt.Errorf("allowlist: %q has a label longer than 63 characters", h)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			default:
				return "", fmt.Errorf("allowlist: %q is not a host name", h)
			}
		}
	}
	return h, nil
}

// Endpoint is one allowlist entry.
type Endpoint struct {
	// Host is normalised the way Parse normalises one.
	Host string
	// Port is the permitted port. Ignored when AnyPort.
	Port int
	// AnyPort is set by an entry written without a port, which permits the host
	// on any port. It is the honest reading of "ldap1.example.com": an operator
	// naming a host without a port means the directory there, not one of its
	// two ports.
	AnyPort bool
}

func (e Endpoint) String() string {
	if e.AnyPort {
		return e.Host
	}
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// matches reports whether this entry permits the target.
//
// Equality of the normalised host and the port, never a prefix or a suffix
// test. A suffix test on "example.com" would admit "notexample.com", and a
// prefix test on a name admits anything a caller can register a subdomain of.
func (e Endpoint) matches(t Target) bool {
	if e.Host != t.Host {
		return false
	}
	return e.AnyPort || e.Port == t.Port
}

// List is the set of endpoints Alder may connect to. A nil or empty list
// permits everything, which is the default.
type List struct {
	endpoints []Endpoint
}

// Parse reads a comma-separated list of endpoints.
//
// Empty means no restriction. Every entry must parse: an allowlist with a typo
// in it that quietly permits one host fewer than intended is the failure this
// refuses to start with.
func Parse(s string) (*List, error) {
	list := &List{}
	for _, part := range strings.Split(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		e, err := ParseEndpoint(part)
		if err != nil {
			return nil, err
		}
		list.endpoints = append(list.endpoints, e)
	}
	return list, nil
}

// Enabled reports whether the list restricts anything.
func (a *List) Enabled() bool { return a != nil && len(a.endpoints) > 0 }

// Endpoints is what the list permits, for logging at startup and for the
// message a refusal carries.
func (a *List) Endpoints() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.endpoints))
	for _, e := range a.endpoints {
		out = append(out, e.String())
	}
	return out
}

// Check reports whether Alder may connect to this host and port.
//
// A malformed target is an error whatever the list says, so that turning the
// allowlist on does not change which inputs are accepted, only which
// destinations are.
func (a *List) Check(host string, port int) error {
	t, err := ParseTarget(host, port)
	if err != nil {
		return err
	}
	if !a.Enabled() {
		return nil
	}
	for _, e := range a.endpoints {
		if e.matches(t) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotAllowed, t)
}
