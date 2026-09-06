import { describe, expect, it } from "vitest";
import { depthOf, orderDeepestFirst } from "./subtree";

/**
 * Getting this order wrong does not fail cleanly.
 *
 * A directory refuses to delete an entry that still has children, so a subtree
 * staged parent-first stops partway through: the leaves it reached are gone and
 * every container is still standing. That is a worse state than not having
 * started, and it is why the ordering is tested rather than assumed.
 */

const base = "dc=alder,dc=test";

describe("orderDeepestFirst", () => {
  it("puts every child ahead of its parent", () => {
    const ordered = orderDeepestFirst([
      `ou=people,${base}`,
      `uid=alice,ou=people,${base}`,
      base,
      `cn=nested,uid=alice,ou=people,${base}`,
    ]);
    expect(ordered).toEqual([
      `cn=nested,uid=alice,ou=people,${base}`,
      `uid=alice,ou=people,${base}`,
      `ou=people,${base}`,
      base,
    ]);
  });

  it("is already correct when the server returned it deepest-first", () => {
    const already = [`uid=alice,ou=people,${base}`, `ou=people,${base}`];
    expect(orderDeepestFirst(already)).toEqual(already);
  });

  it("keeps the server's order between entries at the same depth", () => {
    // Neither is the other's parent, so their relative order does not matter
    // for correctness — but it should not be shuffled either.
    const siblings = [
      `uid=carol,ou=people,${base}`,
      `uid=alice,ou=people,${base}`,
      `uid=bob,ou=people,${base}`,
    ];
    expect(orderDeepestFirst(siblings)).toEqual(siblings);
  });

  it("orders across branches without inventing a relationship", () => {
    const ordered = orderDeepestFirst([
      `ou=groups,${base}`,
      `ou=people,${base}`,
      `cn=deep,cn=mid,ou=groups,${base}`,
      `uid=alice,ou=people,${base}`,
    ]);
    // The deepest first, whichever branch it is in; the two containers last.
    expect(ordered[0]).toBe(`cn=deep,cn=mid,ou=groups,${base}`);
    expect(ordered.slice(2)).toEqual([`ou=groups,${base}`, `ou=people,${base}`]);
  });

  it("handles an RDN containing an escaped comma", () => {
    // The harness holds cn=Liddell\, Alice precisely because tools split on a
    // bare comma and get this wrong. Counted naively it looks one level deeper
    // than it is, which would order it ahead of its own children.
    const person = `cn=Liddell\\, Alice,ou=people,${base}`;
    const child = `cn=child,${person}`;
    expect(depthOf(person)).toBe(4);
    expect(depthOf(child)).toBe(5);
    expect(orderDeepestFirst([person, child])).toEqual([child, person]);
  });

  it("handles a non-ASCII RDN", () => {
    const branch = `ou=Zweigstelle München,ou=services,${base}`;
    expect(depthOf(branch)).toBe(4);
    expect(orderDeepestFirst([branch, `ou=services,${base}`])).toEqual([
      branch,
      `ou=services,${base}`,
    ]);
  });

  it("copes with nothing and with one", () => {
    expect(orderDeepestFirst([])).toEqual([]);
    expect(orderDeepestFirst([base])).toEqual([base]);
  });
});
