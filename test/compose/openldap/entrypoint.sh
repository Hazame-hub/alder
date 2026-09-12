#!/bin/sh
# Entrypoint for the harness OpenLDAP container.
#
# Points cn=config at the certificates produced by the certs service, then
# hands over to slapd. slapmodify edits the config database offline, which
# avoids the chicken-and-egg problem of needing a running server to configure
# the TLS that server should have started with.
#
# It also carries the offline half of the bulk loader, which exists for the
# same reason: some things can only be done to slapd while it is not running,
# and in a container the only such moment is before the exec.
set -eu

# The mdb map size while bulk loading.
#
# slapd.conf's 256 MB is the right size for the 320 seeded entries. Measured, it
# also just barely holds 100,000 filler entries: 230 MB of a 256 MB map, so the
# next round number would fail with MDB_MAP_FULL partway through and leave a
# half-loaded directory that nothing announces. Bulk mode exists to be asked for
# arbitrary sizes, so it does not run 10% from the edge.
#
# The map is a virtual reservation. The file is created at its full length but
# stays sparse -- 8 GB apparent, 230 MB on disk after a hundred thousand
# entries -- which is why this is generous rather than measured to fit. It is
# applied only in bulk mode, so the default harness keeps the map size its
# configuration actually states.
BULK_MAXSIZE=${ALDER_BULK_MAXSIZE:-8589934592}

# The suffix's parent entries. slapadd refuses an entry whose parent is absent,
# and the bulk file starts at ou=bulk, so dc=alder,dc=test has to be there
# first. It is read from the seed file rather than written out again here: a
# second copy of the suffix entry is a second thing to keep in step, and the
# one that drifts is always the copy.
BULK_BASE=${ALDER_BULK_BASE:-/seed/00-base.ldif}

# bulk_load slapadds the filler into the still-stopped mdb database.
#
# Only the filler. The seeded fixtures are still added over LDAP by seed.sh
# afterwards, exactly as they are without bulk mode -- loading those offline too
# would be faster and would quietly change what the fixtures are, because
# slapadd does not run the overlays or the access rules that an LDAP write goes
# through. Bulk mode is meant to add volume beside the fixtures, not to produce
# a different set of them.
bulk_load() {
	if [ ! -r "$ALDER_BULK_LDIF" ]; then
		echo "entrypoint: ALDER_BULK_LDIF=$ALDER_BULK_LDIF is not readable" >&2
		echo "entrypoint: generate it with: task compose:bulk -- <count>" >&2
		exit 1
	fi
	if [ ! -r "$BULK_BASE" ]; then
		echo "entrypoint: $BULK_BASE is not readable; the seed directory must be mounted" >&2
		exit 1
	fi

	# A restarted container keeps its filesystem, so without this marker a
	# "docker compose restart" would slapadd the same file onto a database that
	# already holds it and fail on the first duplicate.
	if [ -e /var/lib/ldap/.bulk-loaded ]; then
		echo "entrypoint: bulk data is already in this database, skipping the load"
		return 0
	fi

	cat >/tmp/maxsize.ldif <<LDIF
dn: olcDatabase={1}mdb,cn=config
changetype: modify
replace: olcDbMaxSize
olcDbMaxSize: $BULK_MAXSIZE
LDIF
	slapmodify -n0 -F /etc/ldap/slapd.d -l /tmp/maxsize.ldif
	rm -f /tmp/maxsize.ldif

	echo "entrypoint: slapadd of $BULK_BASE and $ALDER_BULK_LDIF, offline"
	start=$(date +%s)
	# The echo between the two files is the record separator. 00-base.ldif ends
	# with an attribute line and no trailing blank, so a plain cat would run its
	# last entry into the first line of the bulk file.
	#
	# -q trades the consistency checks and the fsyncs for speed, which is the
	# whole point of the offline path. This is a throwaway directory that gets
	# rebuilt from the LDIF the moment anyone doubts it.
	{
		cat "$BULK_BASE"
		echo
		cat "$ALDER_BULK_LDIF"
	} | slapadd -q -F /etc/ldap/slapd.d -b "dc=alder,dc=test"
	echo "entrypoint: slapadd finished in $(($(date +%s) - start))s"

	touch /var/lib/ldap/.bulk-loaded
}

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

	# Optional offline bulk load, for scale work only. Unset by default, and
	# the default harness never reaches this block.
	#
	# It has to happen here, before slapd is exec'd, because slapadd writes
	# straight into the mdb files and requires the server to be stopped. There
	# is no other moment in the container's life when that is true: slapd is
	# PID 1, so stopping it later stops the container.
	if [ -n "${ALDER_BULK_LDIF:-}" ]; then
		bulk_load
	fi

	mkdir -p /var/run/slapd
fi

exec "$@"
