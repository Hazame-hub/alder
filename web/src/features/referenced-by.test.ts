import { describe, expect, it } from "vitest";
import type { Reference } from "@/lib/api";
import { matches, removalOf } from "./referenced-by";

const ref = (over: Partial<Reference> = {}): Reference => ({
  dn: "cn=admins,ou=groups,dc=alder,dc=test",
  rdn: "cn=admins",
  attribute: "member",
  value: "uid=alice,ou=people,dc=alder,dc=test",
  ...over,
});

describe("removalOf", () => {
  it("changes the entry doing the naming, not the one being named", () => {
    const change = removalOf(ref());
    expect(change.dn).toBe("cn=admins,ou=groups,dc=alder,dc=test");
    expect(change.type).toBe("modify");
  });

  it("deletes one value of the attribute the server reported", () => {
    // Not a replace of the remaining list: a group's membership is the most
    // concurrently edited attribute a directory has, and posting back what was
    // read would drop anyone added in between.
    const mods = removalOf(ref({ attribute: "owner" })).mods ?? [];
    expect(mods).toHaveLength(1);
    expect(mods[0]).toMatchObject({ op: "delete", name: "owner" });
    expect(mods[0]?.values).toHaveLength(1);
  });

  it("names the value the directory stores, not the subject's DN", () => {
    // uniqueMemberMatch compares the optional UID as well, so a delete of the
    // bare DN matches nothing — and succeeds, leaving the reference in place.
    const stored = "uid=alice,ou=people,dc=alder,dc=test#'01'B";
    const mods = removalOf(ref({ attribute: "uniqueMember", value: stored })).mods ?? [];
    expect(mods[0]?.values?.[0]).toEqual({ text: stored });
  });
});

describe("matches", () => {
  it("keeps everything when nothing is typed", () => {
    expect(matches(ref(), "")).toBe(true);
    expect(matches(ref(), "   ")).toBe(true);
  });

  it("looks in the DN and in the attribute name", () => {
    expect(matches(ref(), "admins")).toBe(true);
    expect(matches(ref(), "MEMBER")).toBe(true);
    expect(matches(ref(), "owner")).toBe(false);
  });
});
