import { beforeEach, describe, expect, it } from "vitest";
import { changeset, maxStagedChanges } from "./changeset";
import { stageDeletions } from "./stage-deletes";

/**
 * The property under test is all-or-nothing.
 *
 * Staging as many as fit and stopping is the tempting implementation and the
 * wrong one: it leaves the basket holding part of a set the operator asked for
 * as a whole — a subtree missing its deepest entries, the first four hundred
 * records of a document — which looks like it worked, and then applies to
 * something nobody chose.
 */

const dns = (n: number, prefix = "uid=user") =>
  Array.from({ length: n }, (_, i) => `${prefix}${i},ou=people,dc=alder,dc=test`);

beforeEach(() => changeset.clear());

describe("stageDeletions", () => {
  it("stages every selected DN as its own narrow delete", () => {
    const out = stageDeletions(dns(3));
    expect(out.ok).toBe(true);
    expect(out.staged).toBe(3);

    const all = changeset.all();
    expect(all).toHaveLength(3);
    expect(all.map((s) => s.change.type)).toEqual(["delete", "delete", "delete"]);
    // A delete names the entry and carries no mods — nothing here can turn into
    // a replace of something the operator did not read.
    expect(all.every((s) => s.change.mods === undefined)).toBe(true);
    expect(all[0]?.change.dn).toBe("uid=user0,ou=people,dc=alder,dc=test");
  });

  it("adds to what is already staged rather than replacing it", () => {
    stageDeletions(dns(2));
    stageDeletions(dns(2, "cn=group"));
    expect(changeset.all()).toHaveLength(4);
  });

  it("fills the basket exactly to the cap", () => {
    const out = stageDeletions(dns(maxStagedChanges));
    expect(out.ok).toBe(true);
    expect(changeset.all()).toHaveLength(maxStagedChanges);
    expect(changeset.capacity()).toBe(0);
  });

  it("stages NOTHING when the set would not fit", () => {
    const out = stageDeletions(dns(maxStagedChanges + 1));
    expect(out.ok).toBe(false);
    expect(out.staged).toBe(0);
    // The whole point: not a prefix.
    expect(changeset.all()).toHaveLength(0);
  });

  it("counts what is already staged against the room left", () => {
    stageDeletions(dns(maxStagedChanges - 2));
    const out = stageDeletions(dns(3, "cn=late"));
    expect(out.ok).toBe(false);
    expect(changeset.all()).toHaveLength(maxStagedChanges - 2);
    // The refusal has to name the room left, or there is nothing to act on.
    expect(out.message).toContain("2");
  });

  it("says the basket is full rather than offering a number when it is", () => {
    stageDeletions(dns(maxStagedChanges));
    const out = stageDeletions(dns(1, "cn=one-more"));
    expect(out.ok).toBe(false);
    expect(out.message).toMatch(/full/i);
    expect(changeset.all()).toHaveLength(maxStagedChanges);
  });

  it("refuses an empty selection without touching the basket", () => {
    stageDeletions(dns(2));
    const out = stageDeletions([]);
    expect(out.ok).toBe(false);
    expect(changeset.all()).toHaveLength(2);
  });
});

describe("changeset.capacity", () => {
  it("starts at the cap and falls as changes are staged", () => {
    expect(changeset.capacity()).toBe(maxStagedChanges);
    stageDeletions(dns(10));
    expect(changeset.capacity()).toBe(maxStagedChanges - 10);
  });

  it("matches what the server enforces", () => {
    // internal/api/changeset.go refuses past MaxChangesetChanges, and
    // api/openapi.yaml declares the same number. Three files, one bound.
    expect(maxStagedChanges).toBe(2000);
  });
});
