import { describe, expect, it } from "vitest";
import type { EntryAttribute } from "./api";
import { computeMods, snapshot } from "./mods";

/**
 * Which modification a diff produces is the highest-consequence decision in the
 * front end.
 *
 * Adding one person to a fifty-person group by posting the whole list back
 * removes anybody another administrator added since the entry was read, and
 * group membership is the most concurrently edited attribute a directory has.
 * These tests exist to keep the narrow operations narrow.
 */

const kind = (over: Partial<EntryAttribute["kind"]> = {}): EntryAttribute["kind"] => ({
  name: "x",
  kind: "string",
  known: true,
  ...over,
});

const attr = (
  name: string,
  values: string[],
  over: Partial<EntryAttribute> = {},
): EntryAttribute => ({
  name,
  kind: kind({ name }),
  values: values.map((text) => ({ text, size: text.length })),
  ...over,
});

describe("computeMods", () => {
  it("adds only the value that was added", () => {
    const before = { member: ["cn=a", "cn=b", "cn=c"] };
    const after = { member: ["cn=a", "cn=b", "cn=c", "cn=d"] };
    expect(computeMods(before, after, [])).toEqual([
      { op: "add", name: "member", values: [{ text: "cn=d", size: 4 }] },
    ]);
  });

  it("deletes only the value that was removed", () => {
    const before = { member: ["cn=a", "cn=b", "cn=c"] };
    const after = { member: ["cn=a", "cn=c"] };
    expect(computeMods(before, after, [])).toEqual([
      { op: "delete", name: "member", values: [{ text: "cn=b", size: 4 }] },
    ]);
  });

  it("replaces only when values were both gained and lost", () => {
    const before = { member: ["cn=a", "cn=b"] };
    const after = { member: ["cn=a", "cn=c"] };
    const mods = computeMods(before, after, []);
    expect(mods).toHaveLength(1);
    expect(mods[0]?.op).toBe("replace");
    expect(mods[0]?.values?.map((v) => v.text)).toEqual(["cn=a", "cn=c"]);
  });

  it("emptying an attribute is a valueless delete, not a replace with nothing", () => {
    expect(computeMods({ description: ["gone"] }, { description: [] }, [])).toEqual([
      { op: "delete", name: "description" },
    ]);
  });

  it("says nothing when nothing changed", () => {
    const same = { cn: ["a"], member: ["cn=x", "cn=y"] };
    expect(computeMods(same, { ...same }, [])).toEqual([]);
  });

  it("ignores empty strings, which are how a blank field arrives", () => {
    expect(computeMods({ cn: ["a"] }, { cn: ["a", ""] }, [])).toEqual([]);
    expect(computeMods({ cn: ["a", ""] }, { cn: ["a"] }, [])).toEqual([]);
  });

  it("treats reordering as a replace, because the order is what changed", () => {
    const mods = computeMods({ member: ["a", "b"] }, { member: ["b", "a"] }, []);
    expect(mods[0]?.op).toBe("replace");
  });

  it("adds an attribute the entry did not have", () => {
    const mods = computeMods({}, { title: ["Engineer"] }, ["title"]);
    expect(mods).toEqual([
      { op: "add", name: "title", values: [{ text: "Engineer", size: 8 }] },
    ]);
  });

  it("ignores an added attribute left empty", () => {
    expect(computeMods({}, { title: [""] }, ["title"])).toEqual([]);
  });

  it("does not invent a change for an attribute nobody touched", () => {
    // The draft carries every attribute; only the edited one may produce a mod.
    const before = { cn: ["a"], sn: ["b"], mail: ["c@d"] };
    const after = { cn: ["a"], sn: ["b2"], mail: ["c@d"] };
    const mods = computeMods(before, after, []);
    expect(mods.map((m) => m.name)).toEqual(["sn"]);
  });
});

describe("snapshot", () => {
  it("keeps the editable attributes", () => {
    expect(snapshot([attr("cn", ["alice"]), attr("mail", ["a@b", "c@d"])])).toEqual({
      cn: ["alice"],
      mail: ["a@b", "c@d"],
    });
  });

  it("excludes what the server owns, so an edit never tries to write it", () => {
    const attrs = [
      attr("cn", ["alice"]),
      { ...attr("entryUUID", ["u"]), kind: kind({ name: "entryUUID", operational: true }) },
      { ...attr("creatorsName", ["c"]), kind: kind({ name: "creatorsName", readOnly: true }) },
    ];
    expect(Object.keys(snapshot(attrs))).toEqual(["cn"]);
  });

  it("excludes a withheld attribute rather than reading it as empty", () => {
    // userPassword arrives with its values withheld. Including it here would
    // map to no values, and computeMods would then delete the password.
    const attrs: EntryAttribute[] = [
      attr("cn", ["alice"]),
      { name: "userPassword", kind: kind({ name: "userPassword", sensitive: true }),
        values: [], withheld: true, valueCount: 1 },
    ];
    const draft = snapshot(attrs);
    expect(draft).not.toHaveProperty("userPassword");
    expect(computeMods(draft, draft, [])).toEqual([]);
  });

  it("excludes a binary-valued attribute, which would otherwise read as empty", () => {
    const attrs: EntryAttribute[] = [
      attr("cn", ["alice"]),
      { name: "jpegPhoto", kind: kind({ name: "jpegPhoto", kind: "image" }),
        values: [{ base64: "/9j/4AAQ", size: 6 }] },
    ];
    expect(Object.keys(snapshot(attrs))).toEqual(["cn"]);
  });
});
