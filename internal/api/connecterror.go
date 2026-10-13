package api

import (
	"crypto/tls"
	"crypto/x509"
	"errors"

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
