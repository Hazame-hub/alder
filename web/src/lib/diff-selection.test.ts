import { describe, expect, it } from "vitest";
import {
  filterItems,
  nonDestructive,
  selectable,
  selectedChangeItems,
  selectedChanges,
  type DiffItem,
} from "./diff-selection";

const modify: DiffItem = {
  kind: "modified",
  sourceDn: "uid=alice,ou=people,dc=alder,dc=test",
  targetDn: "uid=alice,ou=people,dc=alder,dc=test",
  attributes: [{ name: "title", kind: "modified" }],
  candidate: {
    destructive: false,
    changes: [{ dn: "uid=alice,ou=people,dc=alder,dc=test", type: "modify", mods: [] }],
  },
};
const add: DiffItem = {
  kind: "added",
  targetDn: "uid=bob,ou=people,dc=alder,dc=test",
  candidate: { destructive: false, changes: [{ dn: "uid=bob,ou=people,dc=alder,dc=test", type: "add" }] },
};
const remove: DiffItem = {
  kind: "removed",
  sourceDn: "uid=dave,ou=people,dc=alder,dc=test",
  candidate: { destructive: true, changes: [{ dn: "uid=dave,ou=people,dc=alder,dc=test", type: "delete" }] },
};
const blockedDelete: DiffItem = {
  kind: "removed",
  sourceDn: "uid=eve,ou=people,dc=alder,dc=test",
  candidate: { destructive: true, changes: [], blocked: "incomplete_comparison" },
};
const unknown: DiffItem = { kind: "unknown", sourceDn: "uid=zed,ou=people,dc=alder,dc=test", reason: "search_limit_reached" };
const items = [modify, add, remove, blockedDelete, unknown];

describe("filterItems", () => {
  it("keeps everything with no filter", () => {
    expect(filterItems(items, { kinds: new Set(), dn: "", attribute: "" })).toEqual([0, 1, 2, 3, 4]);
  });
  it("filters by kind, DN substring and attribute name", () => {
    expect(filterItems(items, { kinds: new Set(["removed"]), dn: "", attribute: "" })).toEqual([2, 3]);
    expect(filterItems(items, { kinds: new Set(), dn: "BOB", attribute: "" })).toEqual([1]);
    expect(filterItems(items, { kinds: new Set(), dn: "", attribute: "tit" })).toEqual([0]);
  });
});

describe("selection", () => {
  it("never offers a blocked or candidate-less item", () => {
    expect(selectable(blockedDelete)).toBe(false);
    expect(selectable(unknown)).toBe(false);
    expect(selectable(modify)).toBe(true);
  });

  it("select all never selects a deletion", () => {
    expect(nonDestructive(items, [0, 1, 2, 3, 4])).toEqual([0, 1]);
  });

  it("stages a deletion only when chosen as a deletion", () => {
    const all = new Set([0, 1, 2, 3, 4]);
    expect(selectedChanges(items, all, new Set()).map((c) => c.type)).toEqual(["modify", "add"]);
    expect(selectedChanges(items, new Set([0]), new Set([2])).map((c) => c.type)).toEqual(["modify", "delete"]);
  });

  it("a blocked deletion stays out even when chosen", () => {
    expect(selectedChanges(items, new Set(), new Set([3]))).toEqual([]);
  });

  it("names each change with the item it came from, and agrees with the plain list", () => {
    // The comparison screens label every staged change, and a second
    // traversal written beside this one is how a label ends up on the wrong
    // change.
    const picked = selectedChangeItems(items, new Set([0, 1]), new Set([2]));
    expect(picked.map((p) => p.change.type)).toEqual(selectedChanges(items, new Set([0, 1]), new Set([2])).map((c) => c.type));
    expect(picked.map((p) => p.item.kind)).toEqual(["modified", "added", "removed"]);
  });
});
