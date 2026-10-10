/**
 * The directory servers a journey runs against.
 *
 * The same two the conformance suite holds to identical behaviour, with the
 * same identities, from test/conformance/conformance_test.go. Every journey
 * runs once per server, as a Playwright project, because "it works on
 * OpenLDAP" is half a result here: both servers working equally well is the
 * quality bar in section 3 of the charter, and an end-to-end run that only
 * tried one would be quietly lowering it.
 */
export interface DirectoryServer {
  /** The project name, and the word that appears in test titles. */
  name: "openldap" | "389ds";
  /** The LDAPS port the harness publishes on the host. */
  ldapsPort: number;
  bindDN: string;
  bindPassword: string;
  /**
   * The compose service, and the plain LDAP port inside its container.
   *
   * Ground truth is read through these, with the server's own command-line
   * tools on the container's loopback -- never through Alder. A journey that
   * checked its result by asking the application it was testing would only
   * prove the application agrees with itself.
   */
  service: "openldap" | "ds389";
  innerLdapPort: number;
}

export const servers: readonly DirectoryServer[] = [
  {
    name: "openldap",
    ldapsPort: 10636,
    bindDN: "cn=admin,dc=alder,dc=test",
    bindPassword: "alder-admin",
    service: "openldap",
    innerLdapPort: 389,
  },
  {
    name: "389ds",
    ldapsPort: 11636,
    bindDN: "cn=Directory Manager",
    bindPassword: "alder-directory-manager",
    service: "ds389",
    innerLdapPort: 3389,
  },
];

export const suffix = "dc=alder,dc=test";
