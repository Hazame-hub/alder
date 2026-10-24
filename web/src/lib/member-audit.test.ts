import { describe, expect, it } from "vitest";
import type { MemberAudit, MemberAuditGroup } from "./api";
import { allNotFound, cleanupFor, memberDn, valueKey, withoutFound, wouldBeEmpty } from "./member-audit";

const person = (n: string) => `uid=${n},ou=people,dc=alder,dc=test`;
const groupDn = (n: string) => `cn=${n},ou=groups,dc=alder,dc=test`;

function group(cn: string, extra: Partial<MemberAuditGroup>): MemberAuditGroup {
  return {
    dn: groupDn(cn),
    classesRequireMember: true,
    notFound: [],
    notChecked: [],
    remaining: 0,
    noPersonThroughNesting: false,
    ...extra,
  };
}

function audit(groups: MemberAuditGroup[]): MemberAudit {
  return { bind: "cn=admin,dc=alder,dc=test", readAt: "2026-10-22T00:00:00Z", read: [], referrals: [], groups, cycles: [] };
}

const mixed = group("mixed", {
  notFound: [
    { attribute: "member", value: person("gone") },
    { attribute: "member", value: person("lost") },
  ],
  remaining: 1,
});
const orphan = group("orphan", { notFound: [{ attribute: "member", value: person("never") }], remaining: 0 });
const loose = group("loose", {
  classesRequireMember: false,
  notFound: [{ attribute: "uniqueMember", value: `${person("old")}#'0101'B` }],
  remaining: 0,
});

describe("cleanupFor", () => {
  it("chooses nothing to begin with", () => {
    expect(cleanupFor(audit([mixed]), new Set()).items).toEqual([]);
  });

  it("is one delete per group and attribute, holding the values as stored", () => {
    const out = cleanupFor(audit([mixed, loose]), allNotFound(audit([mixed, loose])));
    expect(out.items.map((i) => i.change)).toEqual([
      {
        dn: groupDn("mixed"),
        type: "modify",
        mods: [{ op: "delete", name: "member", values: [{ text: person("gone") }, { text: person("lost") }] }],
      },
      {
        dn: groupDn("loose"),
        type: "modify",
        mods: [{ op: "delete", name: "uniqueMember", values: [{ text: `${person("old")}#'0101'B` }] }],
      },
    ]);
    expect(out.values).toBe(3);
    // A group whose classes allow it may be emptied, and is named.
    expect(out.emptied).toEqual([groupDn("loose")]);
  });

  it("leaves out a group it would empty when its classes require a member", () => {
    const out = cleanupFor(audit([orphan]), allNotFound(audit([orphan])));
    expect(out.items).toEqual([]);
    expect(out.leftOut).toHaveLength(1);
    expect(out.leftOut[0]?.reason).toMatch(/plan would refuse/);
  });

  it("stages part of a group that requires a member, since something stays", () => {
    const chosen = new Set([valueKey(groupDn("mixed"), "member", person("gone"))]);
    const out = cleanupFor(audit([mixed]), chosen);
    expect(out.items).toHaveLength(1);
    expect(out.leftOut).toEqual([]);
  });
});

describe("withoutFound", () => {
  it("keeps only what a second read still did not find, and names the rest", () => {
    const gone = valueKey(groupDn("mixed"), "member", person("gone"));
    const back = valueKey(groupDn("mixed"), "member", person("lost"));
    const odd = valueKey(groupDn("loose"), "uniqueMember", person("old"));
    const out = withoutFound(
      new Set([gone, back, odd]),
      new Map([
        [gone, "not-found"],
        [back, "found"],
        [odd, "unknown"],
      ]),
    );
    expect([...out.kept]).toEqual([gone]);
    expect(out.found).toEqual([person("lost")]);
    expect(out.unknown).toEqual([person("old")]);
  });
});

describe("wouldBeEmpty and memberDn", () => {
  it("says which groups removing every value not found would empty", () => {
    expect(wouldBeEmpty(orphan)).toBe(true);
    expect(wouldBeEmpty(mixed)).toBe(false);
    expect(wouldBeEmpty(group("fine", { remaining: 0 }))).toBe(false);
  });

  it("drops uniqueMember's suffix and nothing else", () => {
    expect(memberDn(`${person("a")}#'0101'B`)).toBe(person("a"));
    expect(memberDn("cn=a\\#b,dc=x")).toBe("cn=a\\#b,dc=x");
  });
});
