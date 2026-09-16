import { describe, expect, it } from "vitest";
import {
  filterSchemaItems,
  missingDependencies,
  nonDestructiveKeys,
  orderedSchemaChanges,
  schemaSelectable,
  type SchemaDiff,
  type SchemaDiffItem,
  type SchemaElementKind,
  type DiffKind,
} from "./schema-diff";

const change = (text: string) => ({
  dn: "cn=schema",
  type: "modify" as const,
  mods: [{ op: "add" as const, name: "attributeTypes", values: [{ text }] }],
});

const at: SchemaDiffItem = {
  key: "attributeType:1.3.6.1.4.1.99999.1.3",
  element: "attributeType",
  oid: "1.3.6.1.4.1.99999.1.3",
  names: ["alderSiteCode"],
  kind: "added",
  candidate: { destructive: false, changes: [change("at")] },
};
const oc: SchemaDiffItem = {
  key: "objectClass:1.3.6.1.4.1.99999.2.2",
  element: "objectClass",
  oid: "1.3.6.1.4.1.99999.2.2",
  names: ["alderSite"],
  kind: "added",
  candidate: {
    destructive: false,
    changes: [change("oc")],
    requires: ["attributeType:1.3.6.1.4.1.99999.1.3"],
  },
};
const gone: SchemaDiffItem = {
  key: "attributeType:1.3.6.1.4.1.99999.1.9",
  element: "attributeType",
  oid: "1.3.6.1.4.1.99999.1.9",
  names: ["alderGone"],
  kind: "removed",
  candidate: {
    destructive: true,
    changes: [change("gone")],
    impact: ["usage_unknown"],
  },
};
const meta: SchemaDiffItem = {
  key: "attributeType:2.5.4.3",
  element: "attributeType",
  oid: "2.5.4.3",
  names: ["cn", "commonName"],
  kind: "metadata_only",
  candidate: { destructive: false, changes: [], blocked: "metadata_only" },
};
const unknown: SchemaDiffItem = {
  key: "objectClass:nsHost-oid",
  element: "objectClass",
  oid: "nsHost-oid",
  kind: "unknown",
  problems: ["non_numeric_oid"],
};
const items = [gone, meta, oc, unknown, at];
const diff: SchemaDiff = {
  attributeTypes: {
    compared: 3,
    added: 1,
    removed: 1,
    modified: 0,
    metadataOnly: 1,
    unchanged: 0,
    unknown: 0,
  },
  objectClasses: {
    compared: 2,
    added: 1,
    removed: 0,
    modified: 0,
    metadataOnly: 0,
    unchanged: 0,
    unknown: 1,
  },
  items,
  order: [at.key, oc.key, gone.key],
};
const none = new Set<string>();
const all = {
  text: "",
  elements: new Set<SchemaElementKind>(),
  kinds: new Set<DiffKind>(),
  actionable: "all",
} as const;

describe("filterSchemaItems", () => {
  it("keeps everything with no filter", () => {
    expect(filterSchemaItems(items, all)).toEqual([0, 1, 2, 3, 4]);
  });
  it("finds by part of an OID or of any NAME", () => {
    expect(filterSchemaItems(items, { ...all, text: "COMMONNAME" })).toEqual([1]);
    expect(filterSchemaItems(items, { ...all, text: "99999.2" })).toEqual([2]);
  });
  it("filters by element, change and whether a change is offered", () => {
    expect(filterSchemaItems(items, { ...all, elements: new Set(["objectClass"]) })).toEqual([2, 3]);
    expect(
      filterSchemaItems(items, {
        ...all,
        kinds: new Set(["metadata_only", "unknown"]),
      }),
    ).toEqual([1, 3]);
    expect(filterSchemaItems(items, { ...all, actionable: "actionable" })).toEqual([0, 2, 4]);
    expect(filterSchemaItems(items, { ...all, actionable: "not_actionable" })).toEqual([1, 3]);
  });
});

describe("selection", () => {
  it("offers nothing for metadata, unknown or blocked differences", () => {
    expect(schemaSelectable(meta)).toBe(false);
    expect(schemaSelectable(unknown)).toBe(false);
  });
  it("never selects a removal when selecting everything shown", () => {
    expect(nonDestructiveKeys(items, [0, 1, 2, 3, 4])).toEqual([oc.key, at.key]);
  });
  it("stages in dependency order, not in the order chosen", () => {
    const { ordered, unplaced } = orderedSchemaChanges(diff, new Set([oc.key, at.key]), none);
    expect(ordered.map((o) => o.item.key)).toEqual([at.key, oc.key]);
    expect(unplaced).toEqual([]);
  });
  it("counts a removal only when chosen as one", () => {
    expect(orderedSchemaChanges(diff, new Set([gone.key]), none).ordered).toEqual([]);
    expect(orderedSchemaChanges(diff, none, new Set([gone.key])).ordered.map((o) => o.item.key)).toEqual([gone.key]);
  });
  it("reports a change whose dependency is not selected, and does not add it", () => {
    expect(missingDependencies(items, new Set([oc.key]), none)).toEqual([{ key: oc.key, needs: at.key }]);
    expect(missingDependencies(items, new Set([oc.key, at.key]), none)).toEqual([]);
  });
  it("reports a selected item the order does not place", () => {
    const unordered = { ...diff, order: [oc.key] };
    expect(orderedSchemaChanges(unordered, new Set([oc.key, at.key]), none).unplaced).toEqual([at.key]);
  });
});
