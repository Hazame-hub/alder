import { describe, expect, it } from "vitest";
import { revealing } from "./tree";

const DN = "uid=alice,ou=people,dc=alder,dc=test";

describe("revealing a selected entry in the tree", () => {
  it("expands every ancestor, and not the entry itself", () => {
    const out = revealing(new Set(), DN);
    expect([...out].sort()).toEqual([
      "dc=test",
      "dc=alder,dc=test",
      "ou=people,dc=alder,dc=test",
    ].sort());
    expect(out.has(DN)).toBe(false);
  });

  it("keeps what was already expanded", () => {
    const out = revealing(new Set(["ou=groups,dc=alder,dc=test"]), DN);
    expect(out.has("ou=groups,dc=alder,dc=test")).toBe(true);
    expect(out.has("ou=people,dc=alder,dc=test")).toBe(true);
  });

  it("returns the very same set when there is nothing to add", () => {
    // Identity, not equality. A new Set here is a new state value, and the
    // whole tree re-renders to arrive where it already was.
    const already = revealing(new Set(), DN);
    expect(revealing(already, DN)).toBe(already);
  });

  it("still returns a new set when it does add something", () => {
    const empty = new Set<string>();
    expect(revealing(empty, DN)).not.toBe(empty);
  });
});
