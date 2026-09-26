import { describe, expect, it } from "vitest";
import { configPlace, inConfigTree } from "./config-tree";
import type { SessionInfo } from "@/lib/api";

const session = (dn?: string): SessionInfo =>
  ({ connected: true, capabilities: dn ? { config: { dn, readable: true } } : {} }) as SessionInfo;

describe("where an entry sits relative to the server's configuration", () => {
  const info = session("cn=config");

  it("knows the root from what is under it", () => {
    expect(configPlace(info, "cn=config")).toBe("root");
    expect(configPlace(info, "olcDatabase={1}mdb,cn=config")).toBe("inside");
    expect(configPlace(info, "CN=Config")).toBe("root");
    expect(configPlace(info, "olcOverlay={0}ppolicy,olcDatabase={1}mdb,CN=CONFIG")).toBe("inside");
  });

  it("does not mistake directory data for configuration", () => {
    expect(configPlace(info, "uid=alice,ou=people,dc=alder,dc=test")).toBeNull();
    // A suffix that merely ends in the same text is not under it.
    expect(configPlace(info, "cn=notconfig")).toBeNull();
    expect(inConfigTree(info, "dc=alder,dc=test")).toBe(false);
  });

  it("says nothing where the server announces no configuration tree", () => {
    expect(configPlace(session(), "cn=config")).toBeNull();
    expect(configPlace(undefined, "cn=config")).toBeNull();
  });

  it("works for a server that keeps its configuration elsewhere", () => {
    const ds = session("cn=config,cn=ldbm database,cn=plugins,cn=config");
    expect(configPlace(ds, "cn=config,cn=ldbm database,cn=plugins,cn=config")).toBe("root");
  });
});
