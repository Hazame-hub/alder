import { describe, expect, it } from "vitest";
import {
  changedObjects,
  filterConfigItems,
  objectRefusal,
  stageableChanges,
  stageableObjects,
  valueText,
  type ConfigDiff,
  type ConfigDiffItem,
} from "./config-diff";

function item(partial: Partial<ConfigDiffItem> & { id: string; key: string }): ConfigDiffItem {
  return {
    kind: "modified",
    section: "limits",
    actionable: "read_only",
    ...partial,
  };
}

const idle = item({
  id: "limits//olcidletimeout",
  key: "olcIdleTimeout",
  actionable: "writable",
  source: ["0"],
  target: ["1800"],
  dn: "cn=config",
  candidate: { changes: [{ dn: "cn=config", type: "modify" }], destructive: false },
});
const rootpw = item({
  id: "backend/database:dc=alder,dc=test/olcrootpw",
  key: "olcRootPW",
  section: "backend",
  resource: "database:dc=alder,dc=test",
  sensitive: true,
  actionable: "read_only",
});
const paths = item({
  id: "server//olcconfigdir",
  key: "olcConfigDir",
  section: "server",
  kind: "added",
  target: ["/etc/ldap/slapd.d"],
  operational: true,
});

const diff: ConfigDiff = {
  providerMismatch: false,
  provider: "openldap",
  complete: true,
  crossVendor: false,
  counts: { compared: 3, added: 1, removed: 0, modified: 2, unchanged: 0, unknown: 0, actionable: 1 },
  sections: [
    { section: "backend", counts: { compared: 1, added: 0, removed: 0, modified: 1, unchanged: 0, unknown: 0, actionable: 0 } },
    { section: "limits", counts: { compared: 1, added: 0, removed: 0, modified: 1, unchanged: 0, unknown: 0, actionable: 1 } },
  ],
  items: [idle, rootpw, paths],
  objects: [],
  source: { provider: "openldap", completeness: "complete", settings: 88, resources: 6, sections: [] },
  target: { provider: "openldap", completeness: "complete", settings: 88, resources: 6, sections: [] },
};

describe("configuration comparisons", () => {
  it("filters by section, change, actionability, secrets and text", () => {
    expect(filterConfigItems(diff.items, { section: "limits" }).map((i) => i.key)).toEqual(["olcIdleTimeout"]);
    expect(filterConfigItems(diff.items, { kinds: new Set(["added"]) }).map((i) => i.key)).toEqual(["olcConfigDir"]);
    expect(filterConfigItems(diff.items, { actionableOnly: true }).map((i) => i.key)).toEqual(["olcIdleTimeout"]);
    expect(filterConfigItems(diff.items, { hideSensitive: true }).map((i) => i.key)).toEqual(["olcIdleTimeout", "olcConfigDir"]);
    expect(filterConfigItems(diff.items, { text: "slapd.d" }).map((i) => i.key)).toEqual(["olcConfigDir"]);
    expect(filterConfigItems(diff.items, { text: "dc=alder" }).map((i) => i.key)).toEqual(["olcRootPW"]);
  });

  it("stages only what the server said Alder can change", () => {
    const all = new Set(diff.items.map((i) => i.id));
    expect(stageableChanges(diff, all).map((s) => s.item.key)).toEqual(["olcIdleTimeout"]);
    expect(stageableChanges(diff, new Set([rootpw.id]))).toHaveLength(0);
  });

  it("never shows a withheld value", () => {
    expect(valueText(rootpw, "source")).toBe("withheld");
    expect(valueText(idle, "target")).toBe("1800");
    expect(valueText(item({ id: "x", key: "y" }), "source")).toBe("(none)");
  });
});

describe("configuration objects", () => {
  const overlay = {
    id: "overlay:dc=alder,dc=test/memberof",
    kind: "added" as const,
    section: "plugins",
    object: "overlay",
    name: "dc=alder,dc=test/memberof",
    label: "memberof",
    settings: 0,
    actionable: "writable" as const,
    candidate: { destructive: false, changes: [{ dn: "olcOverlay=memberof,olcDatabase={1}mdb,cn=config", type: "add" as const }] },
  };
  const ppolicy = {
    id: "overlay:dc=alder,dc=test/ppolicy",
    kind: "removed" as const,
    section: "plugins",
    object: "overlay",
    name: "dc=alder,dc=test/ppolicy",
    settings: 3,
    actionable: "writable" as const,
    destructive: true,
    candidate: { destructive: true, changes: [{ dn: "olcOverlay={0}ppolicy,olcDatabase={1}mdb,cn=config", type: "delete" as const }] },
  };
  const database = {
    id: "database:dc=second,dc=test",
    kind: "added" as const,
    section: "backend",
    object: "database",
    name: "dc=second,dc=test",
    settings: 4,
    actionable: "read_only" as const,
    refusal: "not_creatable",
  };
  const unchanged = { ...database, id: "database:dc=alder,dc=test", kind: "unchanged" as const };
  const withObjects: ConfigDiff = { ...diff, objects: [overlay, ppolicy, database, unchanged] };

  it("shows only the objects that differ", () => {
    expect(changedObjects(withObjects).map((o) => o.id)).toEqual([overlay.id, ppolicy.id, database.id]);
  });

  it("creates what was selected and removes only what was selected as a removal", () => {
    const all = new Set([overlay.id, ppolicy.id, database.id]);
    expect(stageableObjects(withObjects, all, new Set()).map((s) => s.object.id)).toEqual([overlay.id]);

    const both = stageableObjects(withObjects, all, new Set([ppolicy.id]));
    expect(both.map((s) => s.change.type)).toEqual(["add", "delete"]);
  });

  it("says why an object cannot be acted on", () => {
    expect(objectRefusal(database)).toContain("does not create or remove");
    expect(objectRefusal(overlay)).toBe("");
  });
});
