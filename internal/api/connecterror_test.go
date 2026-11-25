package api

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/hazame-hub/alder/internal/allowlist"
	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
)

// failingDriver fails every connection with the error a test gives it.
type failingDriver struct{ err error }

func (d failingDriver) Connect(context.Context, directory.ConnConfig) (directory.Session, error) {
	return nil, d.err
}

// What a connection failure tells a caller who has not authenticated. The
// network errors are the ones the driver really produces, wrapped as it wraps
// them; the address in each is one a caller could not otherwise have learned
// anything about.
func TestAFailedConnectionDoesNotDescribeTheNetworkWithoutAnAllowlist(t *testing.T) {
	refused := fmt.Errorf("directory: connecting to ldaps://10.1.2.3:22: %w",
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})
	untrusted := fmt.Errorf("directory: connecting to ldaps://intranet:636: %w",
		x509.HostnameError{Certificate: &x509.Certificate{DNSNames: []string{"payroll.internal.example"}}, Host: "intranet"})
	bindRefused := fmt.Errorf("directory: bind as cn=admin failed: %w",
		&ldapdriver.Error{Code: 49, Message: "Invalid Credentials"})
	lostDuringBind := fmt.Errorf("directory: bind as cn=admin failed: %w",
		&ldapdriver.Error{Code: 200, Message: "connection reset by peer from 10.1.2.3"})

	cases := []struct {
		name     string
		err      error
		list     string
		want     string
		mustHide string
	}{
		{name: "a refused dial", err: refused, want: connectUnreachable, mustHide: "10.1.2.3"},
		{name: "a certificate for another name", err: untrusted, want: connectUntrusted, mustHide: "payroll"},
		{name: "a connection lost during the bind", err: lostDuringBind, want: connectUnreachable, mustHide: "10.1.2.3"},
		// The directory answered. Saying so reveals nothing a successful
		// bind with the right password would not.
		{name: "a refused bind", err: bindRefused, want: bindRefused.Error()},
		// About the request, not the network.
		{name: "plaintext refused", err: ldapdriver.ErrPlaintextRefused, want: ldapdriver.ErrPlaintextRefused.Error()},
		// Every target is one the operator named, so there is nothing to
		// probe and the reason is the useful answer.
		{name: "a refused dial to an allowed target", err: refused, list: "ldap.example.test:636", want: refused.Error()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := allowlist.Parse(tc.list)
			if err != nil {
				t.Fatal(err)
			}
			rig := newRig(t, Config{AllowedTargets: parsed}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
			rig.server.driver = failingDriver{err: tc.err}

			res := connect(t, rig, "ldap.example.test", 636)

			if res.Status != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502\nbody: %s", res.Status, res.Body)
			}
			body := decode[Error](t, res)
			if body.Detail == nil || *body.Detail != tc.want {
				t.Errorf("detail = %v\nwant     %q", body.Detail, tc.want)
			}
			if tc.mustHide != "" && strings.Contains(res.Body, tc.mustHide) {
				t.Errorf("the response repeats %q from the network error: %s", tc.mustHide, res.Body)
			}
			if strings.Contains(res.Body, allowlistTestPassword) {
				t.Errorf("the response carries the bind password: %s", res.Body)
			}
		})
	}
}

// The headline names the failure, so a mistyped password is not reported in
// the same words as a firewall. The status and the code stay what clients
// already handle.
func TestAFailedConnectionHasAHeadlineThatNamesTheFailure(t *testing.T) {
	refused := fmt.Errorf("directory: connecting to ldaps://10.1.2.3:22: %w",
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})
	untrusted := fmt.Errorf("directory: connecting to ldaps://intranet:636: %w",
		x509.UnknownAuthorityError{})
	wrongPassword := fmt.Errorf("directory: bind as cn=admin failed: %w",
		&ldapdriver.Error{Code: 49, Message: "Invalid Credentials"})
	locked := fmt.Errorf("directory: bind as cn=admin failed: %w",
		&ldapdriver.Error{Code: 53, Message: "Unwilling To Perform"})
	lost := fmt.Errorf("directory: bind as cn=admin failed: %w",
		&ldapdriver.Error{Code: 200, Message: "connection reset"})

	cases := []struct {
		name     string
		err      error
		headline string
		ldapCode int
		hint     string
	}{
		{"a wrong password", wrongPassword, connectHeadlineCredentials, 49, "bind DN"},
		{"a refused bind", locked, connectHeadlineRefusedBind, 53, "locked"},
		{"an untrusted certificate", untrusted, connectHeadlineCertificate, 0, "CA certificate"},
		{"plaintext refused", ldapdriver.ErrPlaintextRefused, connectHeadlinePlaintext, 0, "StartTLS"},
		{"a refused dial", refused, connectHeadlineUnreachable, 0, "port"},
		// go-ldap's own numbers are not the directory's and are never shown.
		{"a connection lost during the bind", lost, connectHeadlineUnreachable, 0, "port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newRig(t, Config{}, &fakeSession{caps: defaultCaps(), sch: testSchema(t)})
			rig.server.driver = failingDriver{err: tc.err}

			res := connect(t, rig, "ldap.example.test", 636)

			if res.Status != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502\nbody: %s", res.Status, res.Body)
			}
			body := decode[Error](t, res)
			if body.Error != ErrorErrorUpstream {
				t.Errorf("code = %q, want upstream", body.Error)
			}
			if body.Message != tc.headline {
				t.Errorf("message = %q\nwant      %q", body.Message, tc.headline)
			}
			if body.Hint == nil || !strings.Contains(*body.Hint, tc.hint) {
				t.Errorf("hint = %v, want it to mention %q", body.Hint, tc.hint)
			}
			switch {
			case tc.ldapCode == 0 && body.LdapCode != nil:
				t.Errorf("ldapCode = %d, want none", *body.LdapCode)
			case tc.ldapCode != 0 && (body.LdapCode == nil || *body.LdapCode != tc.ldapCode):
				t.Errorf("ldapCode = %v, want %d", body.LdapCode, tc.ldapCode)
			}
			if strings.Contains(res.Body, "10.1.2.3") {
				t.Errorf("the response repeats the address: %s", res.Body)
			}
		})
	}
}
