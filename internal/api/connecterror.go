package api

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/hazame-hub/alder/internal/directory"
	"github.com/hazame-hub/alder/internal/directory/ldapdriver"
)

// What a caller is told when a connection fails before it is authenticated.
//
// Opening a session needs nothing beforehand -- POST /session is how a person
// authenticates -- so the reason a connection failed went back to anyone who
// could reach Alder. Without --allowed-targets that made Alder a probe of its
// own network: "connection refused" from one address, "i/o timeout" from
// another, a DNS failure, the names in a certificate an internal host
// presents, or the bytes a service that is not LDAP answers with, each for an
// address of the caller's choosing.
//
// So without a list, a failure is reported in one of three fixed sentences,
// and the reason itself goes only to this server's log, where the operator
// reads it. With a list, every target was put there by the operator, there is
// nothing to probe, and the reason is repeated as it was.
//
// This narrows the oracle and does not close it. Whether a directory answered
// at all is visible in which sentence comes back, and in how long it took; a
// connection that succeeds is the plainest answer of all. Only the allowlist
// closes it, and SECURITY.md says so.
const (
	connectUnreachable = "Alder could not reach a directory at that address, or nothing there answered as one. " +
		"The reason is in this Alder's log. It is not repeated here, because this instance has no " +
		"--allowed-targets list, and the reason would describe the network behind it to anyone who asked."
	connectUntrusted = "The server at that address presented a TLS certificate Alder does not trust for that name. " +
		"Give the CA certificate that issued it, or a server name the certificate covers. " +
		"The certificate's details are in this Alder's log."
)

// connectFailureDetail is the detail of the error a failed connection is
// reported with.
func (s *Server) connectFailureDetail(cfg directory.ConnConfig, err error) string {
	if s.cfg.AllowedTargets.Enabled() || connectFailureIsTheCallers(cfg, err) {
		return err.Error()
	}
	if certificateRejected(err) {
		return connectUntrusted
	}
	return connectUnreachable
}

// connectFailureIsTheCallers reports a failure that says something about the
// request rather than about the network: settings that are not usable, a
// plaintext connection this Alder refuses, or a directory that answered and
// refused the bind. The last does reveal that a directory is there, but so
// does the success the same request would have had with the right password.
func connectFailureIsTheCallers(cfg directory.ConnConfig, err error) bool {
	if cfg.Validate() != nil || errors.Is(err, ldapdriver.ErrPlaintextRefused) {
		return true
	}
	var ldapErr *ldapdriver.Error
	return errors.As(err, &ldapErr) && !ldapErr.IsClientSide()
}

// certificateRejected reports a TLS handshake that failed on the certificate,
// which is the one network failure a person connecting to their own directory
// needs named to fix it.
func certificateRejected(err error) bool {
	var (
		verification *tls.CertificateVerificationError
		unknown      x509.UnknownAuthorityError
		hostname     x509.HostnameError
		invalid      x509.CertificateInvalidError
	)
	return errors.As(err, &verification) || errors.As(err, &unknown) ||
		errors.As(err, &hostname) || errors.As(err, &invalid)
}

// A failed connection is reported under a headline that names what went wrong,
// because the person reading it acts on the headline. "Could not connect"
// above every failure sent someone who had mistyped a password off to check
// firewalls and ports, when the directory had answered and refused the bind.
//
// The headline never says more than the detail beside it already does: a
// network failure without --allowed-targets still reads as one sentence, and
// only a refused bind -- which proves no more than the success the right
// password would have had -- or a certificate is named.
const (
	connectHeadlineCredentials = "Wrong bind DN or password."
	connectHeadlineRefusedBind = "The directory answered but refused this bind."
	connectHeadlineCertificate = "The directory's certificate is not trusted."
	connectHeadlinePlaintext   = "This Alder refuses unencrypted connections."
	connectHeadlineSettings    = "The connection settings are not usable."
	connectHeadlineUnreachable = "Could not reach the directory."
)

// connectFailureHeadline is the message, and connectFailureHint the next step,
// that a failed connection is reported with.
func connectFailureHeadline(cfg directory.ConnConfig, err error) string {
	if ldapErr, ok := directoryRefusal(err); ok {
		if ldapErr.Code == rcInvalidCredentials {
			return connectHeadlineCredentials
		}
		return connectHeadlineRefusedBind
	}
	switch {
	case cfg.Validate() != nil:
		return connectHeadlineSettings
	case errors.Is(err, ldapdriver.ErrPlaintextRefused):
		return connectHeadlinePlaintext
	case certificateRejected(err):
		return connectHeadlineCertificate
	}
	return connectHeadlineUnreachable
}

func connectFailureHint(cfg directory.ConnConfig, err error) string {
	if ldapErr, ok := directoryRefusal(err); ok {
		switch ldapErr.Code {
		case rcInvalidCredentials:
			return "The directory answered and refused these credentials. " +
				"Check the bind DN (a full DN such as cn=admin,dc=example,dc=org, not a user name) and retype the password."
		case rcInappropriateAuth, rcStrongAuthRequired, rcConfidentialityRequired:
			return "The directory does not accept a simple bind like this one. It may want an encrypted " +
				"connection (ldaps or StartTLS), or this account may not be allowed to bind with a password."
		case rcUnwillingToPerform, rcInsufficientAccess:
			return "The directory refused the bind itself. The account may be locked or disabled, " +
				"or the directory may not allow binds for it."
		}
		return ""
	}
	switch {
	case cfg.Validate() != nil:
		return ""
	case errors.Is(err, ldapdriver.ErrPlaintextRefused):
		return "Choose ldaps or StartTLS. An operator can allow plaintext with --i-know-this-is-insecure."
	case certificateRejected(err):
		return "Paste the CA certificate that issued it under Certificate options, " +
			"or set a server name the certificate covers."
	}
	return "Check the host, the port and the transport: ldaps is usually port 636, " +
		"ldap and StartTLS port 389."
}

// directoryRefusal is the directory's own answer to the bind, when it gave one.
func directoryRefusal(err error) (*ldapdriver.Error, bool) {
	var ldapErr *ldapdriver.Error
	if errors.As(err, &ldapErr) && !ldapErr.IsClientSide() {
		return ldapErr, true
	}
	return nil, false
}

// writeConnectFailure answers a failed connection: still 502 upstream, which
// clients rely on, with a headline and a next step that name the failure, and
// the result code when the directory sent one.
func (s *Server) writeConnectFailure(c *fiber.Ctx, cfg directory.ConnConfig, err error) error {
	detail := s.connectFailureDetail(cfg, err)
	body := Error{Error: ErrorErrorUpstream, Message: connectFailureHeadline(cfg, err), Detail: &detail}
	if hint := connectFailureHint(cfg, err); hint != "" {
		body.Hint = &hint
	}
	if ldapErr, ok := directoryRefusal(err); ok {
		n := int(ldapErr.Code)
		body.LdapCode = &n
	}
	return c.Status(fiber.StatusBadGateway).JSON(body)
}
