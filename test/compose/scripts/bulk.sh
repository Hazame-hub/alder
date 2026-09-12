#!/bin/sh
# Load the bulk filler into 389 DS, then check that both servers agree on how
# much of it arrived.
#
# This is the opt-in scale path, not part of seeding. Nothing in the conformance
# suite may depend on anything this script produces; see test/compose/README.md.
#
# Only half the work is here. OpenLDAP's half runs in openldap/entrypoint.sh,
# because slapadd needs the server stopped and the only moment a container's
# slapd is stopped is before it starts. 389 DS is the other way round: its
# import runs *inside* the running server as a task, so it belongs in a script
# that can talk LDAP.
#
# The import is driven by adding an entry under cn=import,cn=tasks,cn=config
# rather than by shelling out to dsconf. dsconf lives in the 389 DS container
# and this script does not, and the task entry is what dsconf creates anyway.
# It also keeps the harness's habit of doing 389 DS configuration over LDAP as
# cn=Directory Manager, which is how an operator without dsconf to hand would.
set -eu

BULK_LDIF=${ALDER_BULK_LDIF:-/bulk/bulk.ldif}
SCHEMA_DIR=${SCHEMA_DIR:-/ds389}
ADMIN_DN="cn=admin,dc=alder,dc=test"
ADMIN_PW=${OPENLDAP_ADMIN_PW:-alder-admin}
DM_DN="cn=Directory Manager"
DM_PW=${DS389_DM_PW:-alder-directory-manager}
SUFFIX="dc=alder,dc=test"
BULK_DN="ou=bulk,dc=alder,dc=test"

OPENLDAP_URI=${OPENLDAP_URI:-ldap://openldap:389}
DS389_URI=${DS389_URI:-ldap://ds389:3389}

log() { echo "bulk: $*"; }

ds_search() {
	ldapsearch -x -LLL -H "$DS389_URI" -D "$DM_DN" -w "$DM_PW" "$@"
}

count_below() {
	uri=$1
	binddn=$2
	pw=$3
	base=$4
	ldapsearch -x -LLL -H "$uri" -D "$binddn" -w "$pw" \
		-b "$base" -s sub "(objectClass=*)" dn 2>/dev/null |
		grep -c '^dn' || true
}

if [ ! -r "$BULK_LDIF" ]; then
	log "$BULK_LDIF is not readable inside this container"
	log "generate it first: task compose:bulk -- <count>"
	exit 1
fi

# --- 389 DS ------------------------------------------------------------------

log "creating the $BULK_DN backend on 389 DS"
if ! ldapmodify -x -H "$DS389_URI" -D "$DM_DN" -w "$DM_PW" \
	-f "$SCHEMA_DIR/bulk-backend.ldif" >/tmp/bulk-backend.log 2>&1; then
	if grep -q 'Already exists' /tmp/bulk-backend.log; then
		log "  (backend already present)"
	else
		cat /tmp/bulk-backend.log >&2
		exit 1
	fi
fi

# The task entry's RDN has to be new on every run: 389 DS keeps finished task
# entries around for a while, and adding one that already exists fails rather
# than starting a second import.
task_cn="alder-bulk-$(date +%s)"
task_dn="cn=$task_cn,cn=import,cn=tasks,cn=config"

log "starting the import task for $BULK_LDIF into bulkRoot"
start=$(date +%s)
ldapadd -x -H "$DS389_URI" -D "$DM_DN" -w "$DM_PW" >/dev/null <<LDIF
dn: $task_dn
objectClass: top
objectClass: extensibleObject
cn: $task_cn
nsFilename: $BULK_LDIF
nsInstance: bulkRoot
LDIF

# The add returns as soon as the task is queued, so the exit code above says
# nothing about whether the import worked. nsTaskExitCode appears only when it
# has finished, and it is the only thing that does say.
i=0
code=""
while [ "$i" -lt 1800 ]; do
	code=$(ds_search -b "$task_dn" -s base "(objectClass=*)" nsTaskExitCode |
		sed -n 's/^nsTaskExitCode: //p')
	if [ -n "$code" ]; then
		break
	fi
	i=$((i + 1))
	sleep 1
done
elapsed=$(($(date +%s) - start))

if [ -z "$code" ]; then
	log "the import task did not finish within $i seconds"
	ds_search -b "$task_dn" -s base "(objectClass=*)" nsTaskStatus >&2 || true
	exit 1
fi
if [ "$code" != "0" ]; then
	log "the import task failed with nsTaskExitCode $code"
	ds_search -b "$task_dn" -s base "(objectClass=*)" nsTaskStatus >&2 || true
	exit 1
fi
log "389 DS imported in ${elapsed}s"

# --- what actually landed ----------------------------------------------------
#
# Reported for both servers and compared, because "the loader exited 0" and
# "the directory holds what the file said" are different claims, and only the
# second one is worth anything to whoever is about to measure against this.

ol_bulk=$(count_below "$OPENLDAP_URI" "$ADMIN_DN" "$ADMIN_PW" "$BULK_DN")
ds_bulk=$(count_below "$DS389_URI" "$DM_DN" "$DM_PW" "$BULK_DN")
ol_all=$(count_below "$OPENLDAP_URI" "$ADMIN_DN" "$ADMIN_PW" "$SUFFIX")
ds_all=$(count_below "$DS389_URI" "$DM_DN" "$DM_PW" "$SUFFIX")

log "OpenLDAP: $ol_bulk entries below $BULK_DN, $ol_all below $SUFFIX"
log "389 DS:   $ds_bulk entries below $BULK_DN, $ds_all below $SUFFIX"

if [ "$ol_bulk" -eq 0 ]; then
	log "OpenLDAP has no bulk data: was ALDER_BULK_LDIF set when its container was created?"
	exit 1
fi
if [ "$ol_bulk" -ne "$ds_bulk" ] || [ "$ol_all" -ne "$ds_all" ]; then
	log "the two servers disagree about how much data they hold"
	exit 1
fi

log "both servers loaded"
