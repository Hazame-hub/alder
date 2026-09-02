#!/bin/sh
# Seed both directory servers with identical data.
#
# The two servers share a suffix, so every data LDIF is applied byte-identically
# to both. Only the schema installation differs, and that difference is confined
# to the top of this script. If you find yourself adding a second vendor branch
# below this line, the harness has stopped testing what it is meant to test.
#
# Re-running is safe. Entries that already exist are skipped by ldapadd -c, and
# the verification step at the end is what actually decides success, so a
# partial previous run is repaired rather than reported as fine.
set -eu

SEED_DIR=${SEED_DIR:-/seed}
SCHEMA_DIR=${SCHEMA_DIR:-/ds389}
ADMIN_DN="cn=admin,dc=alder,dc=test"
ADMIN_PW=${OPENLDAP_ADMIN_PW:-alder-admin}
DM_DN="cn=Directory Manager"
DM_PW=${DS389_DM_PW:-alder-directory-manager}
SUFFIX="dc=alder,dc=test"

OPENLDAP_URI=${OPENLDAP_URI:-ldap://openldap:389}
DS389_URI=${DS389_URI:-ldap://ds389:3389}

# expected_entries is asserted after seeding. Update it when the seed data
# changes; a mismatch means the seed silently failed, which is the failure mode
# that costs a day of debugging later.
EXPECTED_MIN=${EXPECTED_MIN:-310}

log() { echo "seed: $*"; }

wait_for() {
	uri=$1
	name=$2
	i=0
	while [ "$i" -lt 60 ]; do
		if ldapsearch -x -H "$uri" -s base -b "" supportedLDAPVersion >/dev/null 2>&1; then
			log "$name is up at $uri"
			return 0
		fi
		i=$((i + 1))
		sleep 2
	done
	log "$name did not become reachable at $uri"
	return 1
}

# add_ldif applies a content LDIF, tolerating entries that already exist.
add_ldif() {
	uri=$1
	binddn=$2
	pw=$3
	file=$4
	log "applying $(basename "$file") to $uri"
	if ldapadd -x -c -H "$uri" -D "$binddn" -w "$pw" -f "$file" >/tmp/add.log 2>&1; then
		return 0
	fi
	# With -c, ldapadd continues past per-entry errors and exits non-zero if any
	# occurred. Only "Already exists" is acceptable, and only because re-running
	# the seed must be a no-op.
	if grep '^ldap_add: ' /tmp/add.log | grep -qv 'Already exists'; then
		cat /tmp/add.log >&2
		return 1
	fi
	log "  (entries already present, continuing)"
	return 0
}

count_entries() {
	uri=$1
	binddn=$2
	pw=$3
	ldapsearch -x -LLL -H "$uri" -D "$binddn" -w "$pw" \
		-b "$SUFFIX" -s sub "(objectClass=*)" dn 2>/dev/null |
		grep -c '^dn' || true
}

wait_for "$OPENLDAP_URI" OpenLDAP
wait_for "$DS389_URI" "389 DS"

# --- the only vendor-specific step -------------------------------------------
# OpenLDAP already carries the custom schema: it was compiled into cn=config
# when the image was built. 389 DS takes it over the wire.
log "creating the dc=alder,dc=test backend on 389 DS"
if ! ldapmodify -x -H "$DS389_URI" -D "$DM_DN" -w "$DM_PW" 	-f "$SCHEMA_DIR/backend.ldif" >/tmp/backend.log 2>&1; then
	if grep -q 'Already exists' /tmp/backend.log; then
		log "  (backend already present)"
	else
		cat /tmp/backend.log >&2
		exit 1
	fi
fi

log "installing the custom schema into 389 DS"
if ! ldapmodify -x -H "$DS389_URI" -D "$DM_DN" -w "$DM_PW" 	-f "$SCHEMA_DIR/alder-schema.ldif" >/tmp/schema.log 2>&1; then
	if grep -q 'Type or value exists' /tmp/schema.log; then
		log "  (schema already installed)"
	else
		cat /tmp/schema.log >&2
		exit 1
	fi
fi
# -----------------------------------------------------------------------------

for f in "$SEED_DIR"/*.ldif; do
	add_ldif "$OPENLDAP_URI" "$ADMIN_DN" "$ADMIN_PW" "$f"
	add_ldif "$DS389_URI" "$DM_DN" "$DM_PW" "$f"
done

ol_count=$(count_entries "$OPENLDAP_URI" "$ADMIN_DN" "$ADMIN_PW")
ds_count=$(count_entries "$DS389_URI" "$DM_DN" "$DM_PW")
log "OpenLDAP holds $ol_count entries below $SUFFIX"
log "389 DS holds $ds_count entries below $SUFFIX"

fail=0
if [ "$ol_count" -lt "$EXPECTED_MIN" ]; then
	log "OpenLDAP is short: expected at least $EXPECTED_MIN"
	fail=1
fi
if [ "$ds_count" -lt "$EXPECTED_MIN" ]; then
	log "389 DS is short: expected at least $EXPECTED_MIN"
	fail=1
fi
if [ "$fail" -ne 0 ]; then
	exit 1
fi

log "both servers seeded"
