import { describe, expect, it } from "vitest";
import {
  changeLine,
  hasWork,
  orderedItems,
  readyChanges,
  unmetAssumptions,
  type PackageValidation,
} from "./change-package";

const validation: PackageValidation = {
  packageId: "3f1b8a52-6a0e-4f1e-9a3e-77a4e1f0c111",
  integrity: "verified",
  target: { namingContexts: ["dc=alder,dc=test"], schemaWritable: true, vendor: "389 Project" },
  assumptions: [
    { kind: "namingContext", value: "dc=alder,dc=test", satisfied: true },
    { kind: "schemaOid", value: "1.2.3.4", satisfied: false, detail: "the schema defines nothing with that OID" },
  ],
  counts: {
    ready: 2,
    alreadySatisfied: 1,
    noOp: 0,
    conflict: 1,
    dependencyMissing: 0,
    unsupported: 0,
    targetIncompatible: 0,
    unknown: 0,
  },
  items: [
    {
      id: "entry",
      kind: "data",
      destructive: false,
      status: "ready",
      changes: [{ dn: "uid=pat,dc=alder,dc=test", type: "add" }],
    },
    // A refused change that nevertheless carries prepared changes. A server
    // would not send this; the client must not stage it if one does.
    {
      id: "gone",
      kind: "data",
      destructive: true,
      status: "conflict",
      changes: [{ dn: "uid=dave,dc=alder,dc=test", type: "delete" }],
    },
    { id: "attr", kind: "schema", destructive: false, status: "ready", changes: [{ dn: "cn=schema", type: "modify" }] },
    { id: "class", kind: "schema", destructive: false, status: "already_satisfied" },
  ],
  // The target's order: the attribute type before the entry that uses it.
  order: ["attr", "entry"],
};

describe("a validation", () => {
  it("stages the ready changes in the order the target gave", () => {
    expect(readyChanges(validation).map((r) => r.change.dn)).toEqual(["cn=schema", "uid=pat,dc=alder,dc=test"]);
    expect(hasWork(validation)).toBe(true);
  });

  it("stages nothing that is not ready, including a destructive conflict", () => {
    const ids = readyChanges(validation).map((r) => r.item.id);
    expect(ids).not.toContain("gone");
    expect(ids).not.toContain("class");
  });

  it("stages nothing at all when nothing is ready", () => {
    const nothing: PackageValidation = {
      ...validation,
      counts: { ...validation.counts, ready: 0 },
      items: validation.items.map((i) => ({ ...i, status: "already_satisfied", changes: undefined })),
      order: [],
    };
    expect(readyChanges(nothing)).toEqual([]);
    expect(hasWork(nothing)).toBe(false);
  });

  it("lists the work first, in order, then everything else", () => {
    expect(orderedItems(validation).map((i) => i.id)).toEqual(["attr", "entry", "class", "gone"]);
  });

  it("reports the assumptions this target does not satisfy", () => {
    expect(unmetAssumptions(validation).map((a) => a.value)).toEqual(["1.2.3.4"]);
  });
});

describe("changeLine", () => {
  it("says what a change does, to what", () => {
    expect(
      changeLine({
        id: "c1",
        kind: "schema",
        destructive: false,
        schema: { element: "attributeType", op: "add", oid: "1.2.3.4" },
      }),
    ).toBe("add attributeType 1.2.3.4");
    expect(
      changeLine({
        id: "c2",
        kind: "data",
        destructive: false,
        data: {
          dn: "uid=alice,dc=alder,dc=test",
          type: "modify",
          mods: [{ op: "replace", name: "title" }],
        },
      }),
    ).toBe("modify uid=alice,dc=alder,dc=test: replace title");
    expect(
      changeLine({
        id: "c3",
        kind: "data",
        destructive: false,
        data: { dn: "uid=bob,dc=alder,dc=test", type: "rename", newRdn: "uid=robert", newSuperior: "ou=staff,dc=alder,dc=test" },
      }),
    ).toBe("rename uid=bob,dc=alder,dc=test → uid=robert,ou=staff,dc=alder,dc=test");
  });
});

describe("what the client stages", () => {
  // A target would not put a change it refused in the order, but the client
  // does not take that on trust: what is staged is what is ready.
  const misleading: PackageValidation = {
    ...validation,
    order: ["attr", "gone", "class", "entry"],
  };

  it("stages only the ready changes, whatever the order lists", () => {
    expect(readyChanges(misleading).map((r) => r.item.id)).toEqual(["attr", "entry"]);
  });

  it("stages nothing for an item with no prepared changes", () => {
    const noChanges: PackageValidation = {
      ...validation,
      items: validation.items.map((i) => (i.id === "attr" ? { ...i, changes: undefined } : i)),
    };
    expect(readyChanges(noChanges).map((r) => r.item.id)).toEqual(["entry"]);
  });
});
