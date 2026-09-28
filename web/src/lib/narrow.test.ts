import { describe, expect, it } from "vitest";
import { narrowRows } from "./narrow";
import type { components } from "@/lib/api.gen";

type SearchResultEntry = components["schemas"]["SearchResultEntry"];

const value = (text: string) => ({ text });

const entry = (
  dn: string,
  attributes: { name: string; values: { text?: string }[] }[] = [],
): SearchResultEntry => ({ dn, attributes }) as SearchResultEntry;

const alice = entry("uid=alice,ou=people,dc=alder,dc=test", [
  { name: "cn", values: [value("Alice Fournier")] },
  { name: "mail", values: [value("alice@alder.test")] },
]);
const bob = entry("uid=bob,ou=people,dc=alder,dc=test", [
  { name: "cn", values: [value("Bob Marchand")] },
  { name: "nsAccountLock", values: [value("true")] },
]);
const carol = entry("uid=carol,ou=people,dc=alder,dc=test", [
  { name: "cn", values: [value("Carol Sen")] },
  { name: "pwdAccountLockedTime", values: [value("20260901120000Z")] },
]);

const dns = (rows: SearchResultEntry[]) => rows.map((r) => r.dn);

describe("narrowing the rows already fetched", () => {
  it("returns every row when nothing is typed", () => {
    expect(narrowRows([alice, bob, carol], "")).toHaveLength(3);
    expect(narrowRows([alice, bob, carol], "   ")).toHaveLength(3);
  });

  it("matches the DN", () => {
    expect(dns(narrowRows([alice, bob, carol], "bob"))).toEqual([bob.dn]);
  });

  it("matches a value the row is not sorted or keyed by", () => {
    expect(dns(narrowRows([alice, bob, carol], "fournier"))).toEqual([alice.dn]);
    expect(dns(narrowRows([alice, bob, carol], "@alder.test"))).toEqual([alice.dn]);
  });

  it("ignores case on both sides", () => {
    expect(dns(narrowRows([alice, bob, carol], "MARCHAND"))).toEqual([bob.dn]);
  });

  it("answers 'which of these cannot log in' for either server's spelling", () => {
    // The point of the whole exercise: nsAccountLock holds "true" and
    // pwdAccountLockedTime holds a timestamp, so neither is findable by
    // typing what the operator has in mind.
    expect(dns(narrowRows([alice, bob, carol], "locked"))).toEqual([bob.dn, carol.dn]);
  });

  it("does not call an unlocked account locked", () => {
    const unlocked = entry("uid=dan,dc=alder,dc=test", [
      { name: "nsAccountLock", values: [value("false")] },
    ]);
    expect(narrowRows([unlocked], "locked")).toEqual([]);
  });

  it("returns nothing rather than everything when no row matches", () => {
    expect(narrowRows([alice, bob, carol], "zzz")).toEqual([]);
  });

  it("survives a row with no attributes at all", () => {
    const bare = entry("uid=bare,dc=alder,dc=test");
    expect(dns(narrowRows([bare], "bare"))).toEqual([bare.dn]);
    expect(narrowRows([bare], "locked")).toEqual([]);
  });
});
