# OpenLDAP for the Alder conformance harness.
#
# Built from the Debian package rather than a third-party LDAP image, because
# the harness has to still build in five years and every published OpenLDAP
# image has changed hands, licence or tag scheme at least once.
#
# The same image plays three roles in docker-compose.yml: it generates the test
# CA, it runs slapd, and it seeds both servers. That is why openssl and
# ldap-utils are installed here.
FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        slapd \
        ldap-utils \
        openssl \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY alder.schema /etc/ldap/schema/alder.schema
COPY slapd.conf /etc/ldap/slapd.conf
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

# Convert slapd.conf into a cn=config database at build time. Doing it here
# rather than at start-up means a broken configuration fails the build instead
# of producing a container that half works.
#
# TLS is deliberately absent from slapd.conf: slaptest resolves certificate
# paths, and the certificates do not exist until the certs service has run.
# entrypoint.sh writes them into cn=config offline, before slapd starts.
RUN set -eux; \
    rm -rf /etc/ldap/slapd.d; \
    mkdir -p /etc/ldap/slapd.d /var/lib/ldap /var/run/slapd; \
    slaptest -f /etc/ldap/slapd.conf -F /etc/ldap/slapd.d; \
    rm -f /etc/ldap/slapd.conf; \
    chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 389 636

HEALTHCHECK --interval=5s --timeout=5s --start-period=5s --retries=20 \
    CMD ldapsearch -x -H ldap://127.0.0.1:389 -s base -b "" supportedLDAPVersion >/dev/null 2>&1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["slapd", "-h", "ldap:/// ldaps:/// ldapi:///", "-d", "stats"]
