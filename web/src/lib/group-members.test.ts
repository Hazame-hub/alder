import { describe, expect, it } from "vitest";
import type { MemberList } from "./api";
import {
  composeMembers,
  dnMembershipAttributes,
  isManyMembersRecord,
  looksLikeGroup,
  maxMembersPerRecord,
  memberKey,
  nestingFindings,
} from "./group-members";

const group = "cn=engineering,ou=groups,dc=alder,dc=test";
const person = (n: string) => `uid=${n},ou=people,dc=alder,dc=test`;

describe("memberKey", () => {
  it("folds case and insignificant spaces, and orders a multi-valued RDN", () => {
    expect(memberKey("UID=Ana, OU=People,DC=alder , dc=test")).toBe(memberKey(person("ana")));
    expect(memberKey("cn=Ana+uid=ana,dc=x")).toBe(memberKey("UID=ana + CN=ana,dc=x"));
    expect(memberKey("uid=ana,dc=x")).not.toBe(memberKey("uid=bo,dc=x"));
  });

  it("keeps an escaped comma inside its value", () => {
    expect(memberKey("cn=Doe\\, Ana,dc=x")).not.toBe(memberKey("cn=Doe,cn=Ana,dc=x"));
  });

  it("keeps uniqueMember's optional UID suffix as part of the value", () => {
    expect(memberKey("uid=ana,dc=x#'0101'B")).not.toBe(memberKey("uid=ana,dc=x"));
  });
});

describe("composeMembers", () => {
  it("adds, in one record, only the values the group does not hold", () => {
    const out = composeMembers(group, "member", [person("ana")], ["UID=Ana,ou=people,dc=alder,dc=test", person("bo"), person("cy")], "add");
    expect(out.ok).toBe(true);
    if (!out.ok) return;
    expect(out.change).toEqual({
      dn: group,
      type: "modify",
      mods: [{ op: "add", name: "member", values: [{ text: person("bo") }, { text: person("cy") }] }],
    });
    expect(out.leftOut).toEqual(["UID=Ana,ou=people,dc=alder,dc=test"]);
    expect(out.label).toBe(`Add 2 members to ${group}`);
  });

  it("removes each value as the group stores it, and leaves out what it lacks", () => {
    const stored = "uid=Ana,ou=People,dc=alder,dc=test";
    const out = composeMembers(group, "member", [stored, person("bo")], [person("ana"), person("zed")], "remove");
    expect(out.ok).toBe(true);
    if (!out.ok) return;
    expect(out.change.mods).toEqual([{ op: "delete", name: "member", values: [{ text: stored }] }]);
    expect(out.leftOut).toEqual([person("zed")]);
  });

  it("names a picked entry once, however often it was picked", () => {
    const out = composeMembers(group, "member", [], [person("ana"), "UID=ana,ou=people,dc=alder,dc=test"], "add");
    expect(out.ok && out.values).toEqual([person("ana")]);
  });

  it("refuses when nothing would change, saying why", () => {
    const out = composeMembers(group, "member", [person("ana")], [person("ana")], "add");
    expect(out.ok).toBe(false);
    if (out.ok) return;
    expect(out.message).toMatch(/already in member/);
  });

  it("never writes a membership attribute that does not hold DNs", () => {
    expect(composeMembers(group, "memberUid", [], ["ana"], "add").ok).toBe(false);
    expect(composeMembers(group, "memberURL", [], ["ldap:///dc=x??sub?(uid=*)"], "add").ok).toBe(false);
  });

  it("carries at most the record limit", () => {
    const many = Array.from({ length: maxMembersPerRecord + 1 }, (_, i) => person(`u${i}`));
    expect(composeMembers(group, "member", [], many, "add").ok).toBe(false);
    expect(composeMembers(group, "member", [], many.slice(1), "add").ok).toBe(true);
  });

  it("works on uniqueMember the same way", () => {
    const out = composeMembers(group, "uniqueMember", [], [person("ana"), person("bo")], "add");
    expect(out.ok && out.change.mods?.[0]?.name).toBe("uniqueMember");
  });
});

describe("isManyMembersRecord", () => {
  it("is a single add or delete of several DNs in a membership attribute", () => {
    const two = composeMembers(group, "member", [], [person("a"), person("b")], "add");
    const one = composeMembers(group, "member", [], [person("a")], "add");
    expect(two.ok && isManyMembersRecord(two.change)).toBe(true);
    expect(one.ok && isManyMembersRecord(one.change)).toBe(false);
    expect(
      isManyMembersRecord({
        dn: group,
        type: "modify",
        mods: [{ op: "replace", name: "member", values: [{ text: "a" }, { text: "b" }] }],
      }),
    ).toBe(false);
    expect(isManyMembersRecord(null)).toBe(false);
  });
});

describe("dnMembershipAttributes and looksLikeGroup", () => {
  it("keeps only the attributes that hold DNs", () => {
    expect(dnMembershipAttributes(["member", "memberUid", "uniqueMember", "memberURL"])).toEqual(["member", "uniqueMember"]);
  });

  it("recognises the two DN-valued group classes", () => {
    expect(looksLikeGroup(["top", "groupOfNames"])).toBe(true);
    expect(looksLikeGroup(["GROUPOFUNIQUENAMES"])).toBe(true);
    expect(looksLikeGroup(["inetOrgPerson"])).toBe(false);
  });
});

describe("nestingFindings", () => {
  const platform = "cn=platform,ou=groups,dc=alder,dc=test";
  const backend = "cn=backend,ou=groups,dc=alder,dc=test";
  const list = (members: MemberList["members"], extra: Partial<MemberList> = {}): MemberList => ({
    members,
    truncated: false,
    ...extra,
  });

  it("refuses a group that already contains the target, naming the chain", () => {
    const inside = new Map([
      [memberKey(platform), list([{ dn: group, group: true, direct: false, via: [backend] }])],
    ]);
    const out = nestingFindings(group, [{ dn: platform, isGroup: true }], inside, list([]));
    expect(out).toEqual([{ kind: "cycle", member: platform, chain: [platform, backend] }]);
  });

  it("refuses the group itself as its own member", () => {
    const out = nestingFindings(group, [{ dn: group.toUpperCase(), isGroup: true }], new Map(), list([]));
    expect(out[0]?.kind).toBe("cycle");
  });

  it("cannot check a group whose expansion stopped early, or was not read", () => {
    const inside = new Map([[memberKey(platform), list([], { truncated: true })]]);
    expect(nestingFindings(group, [{ dn: platform, isGroup: true }], inside, list([]))[0]?.kind).toBe("unchecked");
    expect(nestingFindings(group, [{ dn: platform, isGroup: true }], new Map(), list([]))[0]?.kind).toBe("unchecked");
  });

  it("tells, and allows, a member the group already reaches through another", () => {
    const around = list([{ dn: person("ana"), group: false, direct: false, via: [backend] }]);
    const out = nestingFindings(group, [{ dn: person("ana"), isGroup: false }], new Map(), around);
    expect(out).toEqual([{ kind: "indirect", member: person("ana"), via: [backend] }]);
  });

  it("says nothing about a plain new member", () => {
    expect(nestingFindings(group, [{ dn: person("bo"), isGroup: false }], new Map(), list([]))).toEqual([]);
  });
});
