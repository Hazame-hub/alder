#!/bin/sh
# Turn the two 389 Directory Server instances into a supplier and a consumer.
#
# OpenLDAP needs nothing here: a consumer's syncrepl is a line in its own
# configuration, so openldap-replica starts replicating the moment it starts.
# 389 DS is the other way round -- replication is enabled per suffix on each
# instance and the agreement is created on the supplier, all of it over LDAP,
# all of it after both servers are up. Hence a script, and hence its place in
# docker-compose.yml after the seed: the agreement is created with --init, so
# the consumer is filled from whatever the supplier holds at that moment, and
# that moment should be after the fixtures are in.
#
# Runs to completion on every "up", so everything it does is idempotent: an
# instance that already has replication enabled, or an agreement that already
# exists, is left as it is rather than treated as a failure.
set -eu

DM="cn=Directory Manager"
DM_PW=alder-directory-manager
SUFFIX="dc=alder,dc=test"
SUPPLIER=ldap://ds389:3389
CONSUMER=ldap://ds389-replica:3389

# The identity the supplier binds as when it pushes changes. It lives in
# cn=config on the consumer rather than in the data, which is 389 DS's own
# arrangement: a replication manager is not a directory user.
REPL_DN="cn=replication manager,cn=config"
REPL_PW=alder-replication

dsconf_do() {
	# dsconf reports "already exists" and similar as a failure. That is the
	# right default for a person and the wrong one for a script that runs on
	# every "up", so the output is kept and shown only when it is not that.
	target=$1
	shift
	if out=$(dsconf -D "$DM" -w "$DM_PW" "$target" "$@" 2>&1); then
		echo "$out"
		return 0
	fi
	case $out in
	*"already enabled"* | *"already exists"* | *"already configured"* | *"Already"*)
		echo "replicate: already done: $*"
		return 0
		;;
	esac
	echo "$out" >&2
	return 1
}

ldapmodify_ok() {
	# The same shape as seed.sh's: applying a file twice is an ordinary
	# outcome here, and only the messages that mean "already there" are it.
	target=$1
	file=$2
	if out=$(ldapmodify -x -H "$target" -D "$DM" -w "$DM_PW" -f "$file" 2>&1); then
		return 0
	fi
	case $out in
	*"Already exists"* | *"Type or value exists"*)
		echo "replicate: already applied: $file"
		return 0
		;;
	esac
	echo "$out" >&2
	return 1
}

# The consumer starts as an instance with no database at all: DS_SUFFIX_DN
# names a suffix and creates nothing, which is the same thing seed.sh works
# around on the supplier. Replication cannot be enabled on a suffix that has
# no backend, so the same file is applied here.
echo "replicate: creating the backend on the consumer"
ldapmodify_ok "$CONSUMER" /ds389/backend.ldif

# And the custom schema. 389 DS does replicate schema from the supplier, but
# only once the agreement is running; the total update that fills the consumer
# would otherwise be checked against a schema that has never heard of
# alderTeam.
echo "replicate: installing the custom schema on the consumer"
ldapmodify_ok "$CONSUMER" /ds389/alder-schema.ldif

# Lockout state has to be allowed on the consumer, or replication stops.
#
# When binds fail, the supplier writes accountUnlockTime, passwordRetryCount
# and retryCountResetTime on the account and replicates them like any other
# change. The consumer REFUSES a replicated password-policy operation unless
# passwordIsGlobalPolicy is on:
#
#   ERR - do_modify - Rejecting replicated password policy operation ...
#   To allow these changes to be accepted, set passwordIsGlobalPolicy to 'on'
#
# and the agreement then falls into "Error (16) ... connection error. Backing
# off, will retry update later" -- which stalls the WHOLE agreement, not just
# those attributes. One locked-out account silently stops replication for
# everything.
#
# It goes here rather than in policy.ldif because cn=config does not
# replicate: the setting has to be put on the server that does the rejecting.
# The harness found this the first time a conformance case locked an account
# out by failing binds instead of by writing the attribute, which is the
# reason that case does it the hard way.
echo "replicate: allowing replicated lockout state on the consumer"
cat >/tmp/globalpolicy.ldif <<'LDIF'
dn: cn=config
changetype: modify
replace: passwordIsGlobalPolicy
passwordIsGlobalPolicy: on
LDIF
ldapmodify_ok "$CONSUMER" /tmp/globalpolicy.ldif
rm -f /tmp/globalpolicy.ldif

echo "replicate: enabling replication on the consumer"
dsconf_do "$CONSUMER" replication enable \
	--suffix "$SUFFIX" --role consumer \
	--bind-dn "$REPL_DN" --bind-passwd "$REPL_PW"

echo "replicate: enabling replication on the supplier"
dsconf_do "$SUPPLIER" replication enable \
	--suffix "$SUFFIX" --role supplier --replica-id 1

# Created without --init, deliberately. The total update is asked for below,
# in one place, from the only condition that actually decides it: the consumer
# does not have the data. Doing it here too means a fresh harness starts two
# overlapping total updates, because the first has not finished by the time
# that condition is evaluated.
echo "replicate: creating the agreement"
dsconf_do "$SUPPLIER" repl-agmt create \
	--suffix "$SUFFIX" \
	--host ds389-replica --port 3389 --conn-protocol LDAP \
	--bind-dn "$REPL_DN" --bind-passwd "$REPL_PW" --bind-method SIMPLE \
	to-ds389-replica

has_data() {
	ldapsearch -x -H "$1" -D "$2" -w "$3" -LLL \
		-b "uid=user0001,ou=people,$SUFFIX" -s base dn >/dev/null 2>&1
}

# A consumer that exists, has an agreement pointing at it, and holds nothing.
#
# This is what a recreated container looks like: the supplier still has the
# agreement from last time, so "repl-agmt create" reports that it already
# exists and nothing initialises the new, empty consumer. It then sits at
# "Not in Synchronization ... consumer (Unavailable)" for ever, which reads
# like a broken agreement rather than one that was simply never started.
# Incremental replication does not fill an empty consumer; only a total update
# does, so ask for one.
if ! has_data "$CONSUMER" "$DM" "$DM_PW"; then
	echo "replicate: the consumer is empty, sending a total update"
	dsconf_do "$SUPPLIER" repl-agmt init --suffix "$SUFFIX" to-ds389-replica
fi

# Wait for the total update to finish and the consumer to hold the data.
#
# An agreement created with --init reports success as soon as the task is
# accepted, so a suite that ran straight after it would be asserting against a
# directory mid-copy. The check is the thing that actually matters -- the
# consumer answers for an entry the supplier has -- rather than the agreement's
# own status, which says "Total update succeeded" a moment before the last
# entry is searchable.
echo "replicate: waiting for the consumer to catch up"
i=0
while [ "$i" -lt 60 ]; do
	if has_data "$CONSUMER" "$DM" "$DM_PW"; then
		echo "replicate: the consumer has the supplier's data"
		break
	fi
	i=$((i + 1))
	sleep 2
done
if [ "$i" -ge 60 ]; then
	echo "replicate: the consumer never caught up; the agreement says:" >&2
	dsconf -D "$DM" -w "$DM_PW" "$SUPPLIER" repl-agmt status --suffix "$SUFFIX" to-ds389-replica >&2 || true
	exit 1
fi

# And the OpenLDAP side, which configures itself but still has to be waited
# for: syncrepl retries every five seconds, so a freshly started consumer is
# briefly an empty directory rather than a replica.
echo "replicate: waiting for openldap-replica to catch up"
i=0
while [ "$i" -lt 60 ]; do
	if has_data ldap://openldap-replica:389 "cn=admin,$SUFFIX" alder-admin; then
		echo "replicate: openldap-replica has the supplier's data"
		break
	fi
	i=$((i + 1))
	sleep 2
done
if [ "$i" -ge 60 ]; then
	echo "replicate: openldap-replica never caught up" >&2
	exit 1
fi

echo "replicate: both replicas are in step"
