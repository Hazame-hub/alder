import { beforeEach, describe, expect, it } from "vitest";
import type { AttributeKind, SearchResultEntry, SessionInfo } from "./api";
import { changeset } from "./changeset";
import {
  bulkEditSummary,
  classesOf,
  classSetKey,
  composeBulkEdit,
  namingAttributes,
  offeredAttributes,
  offersAttribute,
  operationsFor,
  selectionRefusal,
  stageBulkEdit,
  type ClassSetPermits,
} from "./bulk-edit";

const kind = (name: string, extra: Partial<AttributeKind> = {}): AttributeKind => ({
  name,
  kind: "string",
  known: true,
  ...extra,
});

const description = kind("description");
const displayName = kind("displayName", { singleValue: true });
const member = kind("member", { kind: "dn" });
const uid = kind("uid");

const people = [
  "uid=ana,ou=people,dc=alder,dc=test",
  "uid=bo,ou=people,dc=alder,dc=test",
  "cn=Cy Doe,ou=people,dc=alder,dc=test",
];

beforeEach(() => changeset.clear());

describe("composeBulkEdit", () => {
  it("makes one modify per selected entry, each with the one modification", () => {
    const out = composeBulkEdit(people, description, { attribute: "description", op: "add", values: ["on call"] });
    expect(out.ok).toBe(true);
    if (!out.ok) return;
    expect(out.items).toHaveLength(3);
    for (const [i, item] of out.items.entries()) {
      expect(item.change).toEqual({
        dn: people[i],
        type: "modify",
        mods: [{ op: "add", name: "description", values: [{ text: "on call" }] }],
      });
    }
    expect(out.items[0]!.label).toBe("Add description to uid=ana,ou=people,dc=alder,dc=test");
    expect(out.namedBy).toEqual([]);
  });

  it("writes each operation as the record the proposal names", () => {
    const mods = (op: "add" | "remove-values" | "remove-attribute" | "replace") => {
      const out = composeBulkEdit(people.slice(2), description, { attribute: "description", op, values: ["x"] });
      if (!out.ok) throw new Error(out.message);
      return out.items[0]!.change.mods;
    };
    expect(mods("add")).toEqual([{ op: "add", name: "description", values: [{ text: "x" }] }]);
    expect(mods("remove-values")).toEqual([{ op: "delete", name: "description", values: [{ text: "x" }] }]);
    // Values given before the operation changed are not sent with it.
    expect(mods("remove-attribute")).toEqual([{ op: "delete", name: "description" }]);
    expect(mods("replace")).toEqual([{ op: "replace", name: "description", values: [{ text: "x" }] }]);
  });

  it("leaves an entry named by the attribute out of a removal or a replacement, and names it", () => {
    const out = composeBulkEdit(people, uid, { attribute: "uid", op: "replace", values: ["shared"] });
    expect(out.ok).toBe(true);
    if (!out.ok) return;
    expect(out.items.map((i) => i.change.dn)).toEqual(["cn=Cy Doe,ou=people,dc=alder,dc=test"]);
    expect(out.namedBy).toEqual(people.slice(0, 2));
  });

  it("keeps an entry named by the attribute in an add, which cannot remove its name", () => {
    const out = composeBulkEdit(people, uid, { attribute: "uid", op: "add", values: ["alias"] });
    expect(out.ok && out.items.length).toBe(3);
  });

  it("refuses when every entry is named by the attribute", () => {
    const out = composeBulkEdit(people.slice(0, 2), uid, { attribute: "UID", op: "remove-attribute", values: [] });
    expect(out.ok).toBe(false);
  });

  it("never replaces a membership list, and takes one picked member at a time", () => {
    expect(operationsFor(member)).toEqual(["add", "remove-values"]);
    const replace = composeBulkEdit(people, member, {
      attribute: "member",
      op: "replace",
      values: ["uid=ana,ou=people,dc=alder,dc=test"],
    });
    expect(replace.ok).toBe(false);
    const two = composeBulkEdit(people, member, {
      attribute: "member",
      op: "add",
      values: ["uid=ana,ou=people,dc=alder,dc=test", "uid=bo,ou=people,dc=alder,dc=test"],
    });
    expect(two.ok).toBe(false);
    const one = composeBulkEdit(people, member, {
      attribute: "member",
      op: "add",
      values: ["uid=ana,ou=people,dc=alder,dc=test"],
    });
    expect(one.ok).toBe(true);
  });

  it("warns on a replacement of a multi-valued attribute, and not of a single-valued one", () => {
    const multi = composeBulkEdit(people, description, { attribute: "description", op: "replace", values: ["x"] });
    expect(multi.ok && multi.warning).toMatch(/every other value/i);
    const single = composeBulkEdit(people, displayName, { attribute: "displayName", op: "replace", values: ["x"] });
    expect(single.ok && single.warning).toBeFalsy();
  });

  it("refuses a missing value, a repeated one, and two values for a single-valued attribute", () => {
    expect(composeBulkEdit(people, description, { attribute: "description", op: "add", values: [""] }).ok).toBe(false);
    expect(composeBulkEdit(people, description, { attribute: "description", op: "replace", values: ["a", "a"] }).ok).toBe(
      false,
    );
    expect(composeBulkEdit(people, displayName, { attribute: "displayName", op: "add", values: ["a", "b"] }).ok).toBe(
      false,
    );
  });

  it("carries one value in an add or a removal, and the whole list in a replacement", () => {
    expect(composeBulkEdit(people, description, { attribute: "description", op: "add", values: ["a", "b"] }).ok).toBe(false);
    expect(
      composeBulkEdit(people, description, { attribute: "description", op: "remove-values", values: ["a", "b"] }).ok,
    ).toBe(false);
    const replace = composeBulkEdit(people, description, { attribute: "description", op: "replace", values: ["a", "b"] });
    expect(replace.ok && replace.items[0]!.change.mods?.[0]?.values).toEqual([{ text: "a" }, { text: "b" }]);
  });

  it("refuses an attribute the dialog never offers, whatever is asked", () => {
    const password = kind("userPassword", { kind: "password", sensitive: true });
    expect(composeBulkEdit(people, password, { attribute: "userPassword", op: "replace", values: ["x"] }).ok).toBe(false);
  });
});

describe("offersAttribute", () => {
  it("offers an ordinary user attribute", () => {
    expect(offersAttribute(description)).toBe(true);
    expect(offersAttribute(member)).toBe(true);
  });

  it.each([
    ["a secret", kind("userPassword", { kind: "password", sensitive: true })],
    ["a sensitive value", kind("krbPrincipalKey", { sensitive: true })],
    ["an operational attribute", kind("createTimestamp", { operational: true, readOnly: true })],
    ["a server-owned value", kind("entryUUID", { readOnly: true })],
    ["an access rule", kind("aci", { elsewhere: "Access rules are read, not authored here." })],
    ["objectClass", kind("objectClass")],
    ["memberUid, which is a login name", kind("memberUid")],
    ["memberURL, which is a search", kind("memberURL")],
    ["a binary value", kind("jpegPhoto", { kind: "image" })],
    ["an attribute the schema does not define", kind("madeUp", { known: false })],
  ])("never offers %s", (_, k) => {
    expect(offersAttribute(k)).toBe(false);
  });
});

describe("namingAttributes", () => {
  it("reads the type of each part of the leftmost RDN", () => {
    expect(namingAttributes("uid=ana,ou=people,dc=x")).toEqual(["uid"]);
    expect(namingAttributes("CN=Ana+UID=ana,ou=people,dc=x")).toEqual(["cn", "uid"]);
    // An escaped comma or plus is part of the value.
    expect(namingAttributes("cn=Doe\\, Ana\\+1,ou=people,dc=x")).toEqual(["cn"]);
  });
});

describe("selectionRefusal", () => {
  const session: SessionInfo = {
    connected: true,
    capabilities: {
      namingContexts: ["dc=alder,dc=test"],
      subschemaSubentry: "cn=Subschema",
      paging: true,
      config: { dn: "cn=config" },
    } as SessionInfo["capabilities"],
  };

  it("lets an ordinary selection through", () => {
    expect(selectionRefusal(people, session)).toBeNull();
  });

  it("refuses a selection holding a configuration or schema entry", () => {
    expect(selectionRefusal([...people, "olcDatabase={1}mdb,cn=config"], session)).toMatch(/configuration or schema/);
    expect(selectionRefusal(["cn=Subschema"], session)).toMatch(/configuration or schema/);
  });
});

describe("offeredAttributes", () => {
  const row = (dn: string, classes: string[]): SearchResultEntry => ({
    dn,
    attributes: [{ name: "objectClass", kind: kind("objectClass"), values: classes.map((text) => ({ text })) }],
  });
  const personSet: ClassSetPermits = {
    key: classSetKey(["inetorgperson", "organizationalperson", "person", "top"]),
    requirements: { must: ["cn", "sn", "objectClass"], may: ["description", "departmentNumber", "userPassword"] },
    kinds: [kind("cn"), kind("sn"), kind("objectClass"), description, kind("departmentNumber"), kind("userPassword", { sensitive: true, kind: "password" })],
  };
  const groupSet: ClassSetPermits = {
    key: classSetKey(["groupofnames", "top"]),
    requirements: { must: ["cn", "member", "objectClass"], may: ["description"] },
    kinds: [kind("cn"), member, kind("objectClass"), description],
  };
  const entries = [
    row("uid=ana,ou=people,dc=x", ["top", "person", "organizationalPerson", "inetOrgPerson"]),
    row("cn=ops,ou=groups,dc=x", ["groupOfNames", "top"]),
  ];

  it("keys a row by its classes, whatever their case or order", () => {
    expect(classSetKey(classesOf(entries[0]!)!)).toBe(personSet.key);
  });

  it("offers what every entry permits, and widened, what any does, with the count", () => {
    const every = offeredAttributes(entries, [personSet, groupSet], false).map((o) => o.kind.name);
    expect(every).toEqual(["cn", "description"]);
    const any = offeredAttributes(entries, [personSet, groupSet], true);
    expect(any.map((o) => o.kind.name)).toEqual(["cn", "departmentNumber", "description", "member", "sn"]);
    expect(any.find((o) => o.kind.name === "member")?.permittedBy).toBe(1);
  });

  it("offers nothing on every entry when a row carries no object class", () => {
    const bare: SearchResultEntry = { dn: "uid=zed,ou=people,dc=x" };
    expect(offeredAttributes([entries[0]!, bare], [personSet], false)).toEqual([]);
  });
});

describe("stageBulkEdit", () => {
  const edit = { attribute: "description", op: "add" as const, values: ["on call"] };
  const items = () => {
    const out = composeBulkEdit(people, description, edit);
    if (!out.ok) throw new Error(out.message);
    return out.items;
  };

  it("stages every change and heads an otherwise empty changeset with the edit", () => {
    const outcome = stageBulkEdit(edit, items());
    expect(outcome.ok).toBe(true);
    expect(changeset.all()).toHaveLength(3);
    const origin = changeset.origin();
    expect(origin?.kind).toBe("bulk-edit");
    if (origin?.kind !== "bulk-edit") return;
    expect(bulkEditSummary(origin)).toBe("One modification over 3 entries: add description: on call");
  });

  it("adds no heading to a changeset that already held something", () => {
    changeset.add({ dn: "cn=x,dc=alder,dc=test", type: "delete" }, "Delete cn=x");
    stageBulkEdit(edit, items());
    expect(changeset.all()).toHaveLength(4);
    expect(changeset.origin()).toBeNull();
  });

  it("drops the heading on any other change to the basket", () => {
    stageBulkEdit(edit, items());
    changeset.add({ dn: "cn=x,dc=alder,dc=test", type: "delete" }, "Delete cn=x");
    expect(changeset.origin()).toBeNull();
  });
});
