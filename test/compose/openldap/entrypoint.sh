#!/bin/sh
# Entrypoint for the harness OpenLDAP container.
#
# Points cn=config at the certificates produced by the certs service, then
# hands over to slapd. slapmodify edits the config database offline, which
# avoids the chicken-and-egg problem of needing a running server to configure
# the TLS that server should have started with.
set -eu

if [ "${1:-}" = "slapd" ]; then
	if [ ! -r /certs/ca.crt ]; then
		echo "entrypoint: /certs/ca.crt is missing; the certs service must run first" >&2
		exit 1
	fi

	cat >/tmp/tls.ldif <<'LDIF'
dn: cn=config
changetype: modify
replace: olcTLSCACertificateFile
olcTLSCACertificateFile: /certs/ca.crt
-
replace: olcTLSCertificateFile
olcTLSCertificateFile: /certs/openldap.crt
-
replace: olcTLSCertificateKeyFile
olcTLSCertificateKeyFile: /certs/openldap.key
-
replace: olcTLSVerifyClient
olcTLSVerifyClient: never
LDIF

	slapmodify -n0 -F /etc/ldap/slapd.d -l /tmp/tls.ldif
	rm -f /tmp/tls.ldif

	mkdir -p /var/run/slapd
fi

exec "$@"
