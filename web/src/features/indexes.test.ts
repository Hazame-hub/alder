import { describe, expect, it } from "vitest";
import { backendAt } from "./indexes";
import type { components } from "@/lib/api.gen";

type IndexReport = components["schemas"]["IndexReport"];

// Whether the entry in front of you is a backend, and so whether the header
// offers an Indexes button at all.
//
// Matched on the DN the server reported rather than on the shape of the DN,
// because the two servers keep a backend in places neither could guess from
// the other: `olcDatabase={1}mdb,cn=config` on OpenLDAP,
// `cn=userRoot,cn=ldbm database,cn=plugins,cn=config` on 389 Directory
// Server. A rule written from either one would be wrong on the other.

const report = (dns: string[]): IndexReport => ({
  provider: "openldap",
  backends: dns.map((dn, i) => ({ name: `backend${i}`, dn, indexes: [] })),
});

describe("which entry is a backend", () => {
  it("finds the backend whose DN this entry is", () => {
    const r = report(["olcDatabase={1}mdb,cn=config", "olcDatabase={0}config,cn=config"]);
    expect(backendAt(r, "olcDatabase={1}mdb,cn=config")?.name).toBe("backend0");
    expect(backendAt(r, "olcDatabase={0}config,cn=config")?.name).toBe("backend1");
  });

  it("finds 389 Directory Server's, which looks nothing like OpenLDAP's", () => {
    const r = report(["cn=userRoot,cn=ldbm database,cn=plugins,cn=config"]);
    expect(backendAt(r, "cn=userRoot,cn=ldbm database,cn=plugins,cn=config")).toBeDefined();
  });

  it("ignores case and surrounding space, because a DN is not a string", () => {
    const r = report(["olcDatabase={1}mdb,cn=config"]);
    expect(backendAt(r, "  olcDATABASE={1}mdb,CN=config ")).toBeDefined();
  });

  it("offers nothing on an ordinary entry", () => {
    const r = report(["olcDatabase={1}mdb,cn=config"]);
    expect(backendAt(r, "uid=alice,ou=people,dc=alder,dc=test")).toBeUndefined();
  });

  it("offers nothing before the report has answered", () => {
    // The button must not appear and then vanish, and must not appear on an
    // entry that is not a backend while the answer is still in flight.
    expect(backendAt(undefined, "olcDatabase={1}mdb,cn=config")).toBeUndefined();
  });
});

// Whether the panel asks the server anything at all.
//
// Reading the index report captures the whole configuration tree. The first
// version asked for it on every entry view -- including every ordinary
// person -- which cost a directory round trip per click, and on a session
// with no configuration identity put a 400 in the browser console every
// time. The gate is the same one the editor uses to decide whether an entry
// is configuration at all.

import { inConfigTree } from "@/lib/config-tree";
import type { SessionInfo } from "@/lib/api";

const session = (root: string): SessionInfo =>
  ({
    connected: true,
    capabilities: { config: { dn: root, readable: true, separateBind: true } },
  }) as SessionInfo;

describe("when the index report is asked for", () => {
  it("is asked for on an entry inside the configuration tree", () => {
    expect(inConfigTree(session("cn=config"), "olcDatabase={1}mdb,cn=config")).toBe(true);
    expect(inConfigTree(session("cn=config"), "cn=config")).toBe(true);
  });

  it("is not asked for on an ordinary entry", () => {
    // The one that cost a configuration capture per click.
    expect(inConfigTree(session("cn=config"), "uid=alice,ou=people,dc=alder,dc=test")).toBe(false);
    expect(inConfigTree(session("cn=config"), "dc=alder,dc=test")).toBe(false);
  });

  it("is not asked for when the server publishes no configuration tree", () => {
    expect(inConfigTree(undefined, "olcDatabase={1}mdb,cn=config")).toBe(false);
  });
});
