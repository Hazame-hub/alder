import { describe, expect, it } from "vitest";
import type { ChangeRequest, ChangesetResult, Plan, PlanItem } from "@/lib/api";
import { appliedIds, heldBy, resolve, sentIds, unknownId } from "./changeset-outcome";
import { changesFromPlan } from "./plan";

const dn = (uid: string) => `uid=${uid},ou=people,dc=alder,dc=test`;
const change = (uid: string): ChangeRequest => ({
  dn: dn(uid),
  type: "modify",
  mods: [{ op: "replace", name: "title", values: [{ text: uid }] }],
});

// Three staged changes; the plan drops the first, which would do nothing.
const staged = [change("a"), change("b"), change("c")];
const stagedIds = ["c1", "c2", "c3"];
const plan = {
  counts: { examined: 3, add: 0, modify: 2, delete: 0, rename: 0, setPassword: 0, unchanged: 1, conflict: 0 },
  items: [
    { index: 0, dn: dn("a"), action: "unchanged", exists: true },
    { index: 1, dn: dn("b"), action: "modify", exists: true, record: change("b"), baseline: "t1" },
    { index: 2, dn: dn("c"), action: "modify", exists: true, record: change("c"), baseline: "t2" },
  ] as PlanItem[],
} as Plan;

function result(outcomes: ChangesetResult["outcomes"], failedIndex?: number): ChangesetResult {
  return { appliedCount: outcomes.filter((o) => o.applied).length, outcomes, failedIndex } as ChangesetResult;
}

describe("mapping a run's outcomes back to staged changes", () => {
  it("numbers what was sent, not what was staged, when the plan dropped one", () => {
    const ids = sentIds(stagedIds, plan);
    expect(ids).toEqual(["c2", "c3"]);
    // The same changes, in the same order, as changesFromPlan sends.
    expect(changesFromPlan(plan, staged).map((c) => c.dn)).toEqual([dn("b"), dn("c")]);
  });

  it("removes the change that applied, not the one at the same staged position", () => {
    const run = { ids: sentIds(stagedIds, plan) };
    const res = result([
      { index: 0, applied: true, summary: "b" },
      { index: 1, applied: false, state: "unknown", summary: "c" },
    ] as ChangesetResult["outcomes"], 1);
    // staged[0] is "a", which was never sent; reading by staged position
    // would have removed it and left "b" -- already applied -- in the basket.
    expect(appliedIds(res, run)).toEqual(["c2"]);
    expect(unknownId(res, run)).toBe("c3");
  });

  it("is the staged list itself without a plan", () => {
    expect(sentIds(stagedIds, null)).toEqual(stagedIds);
  });
});

describe("what a verdict does to the interrupted change", () => {
  it("removes one that landed, replans one that did not, and holds the rest", () => {
    expect(resolve("applied")).toBe("remove");
    expect(resolve("not_applied")).toBe("replan");
    expect(resolve("conflicted")).toBe("hold");
    expect(resolve("undeterminable")).toBe("hold");
  });
});

describe("whether Apply is held", () => {
  const number = (id: string) => stagedIds.indexOf(id) + 1;

  it("holds until the directory has been checked", () => {
    expect(heldBy({ id: "c3", resolution: null }, stagedIds, number, false)).toMatch(/^Change 3 may already/);
    // A plan made since does not release it: the question is whether the
    // change landed, which planning does not answer.
    expect(heldBy({ id: "c3", resolution: null }, stagedIds, number, true)).not.toBeNull();
  });

  it("releases a change that did not land only once it has been planned again", () => {
    expect(heldBy({ id: "c3", resolution: "replan" }, stagedIds, number, false)).toMatch(/did not reach/);
    expect(heldBy({ id: "c3", resolution: "replan" }, stagedIds, number, true)).toBeNull();
  });

  it("keeps holding a conflicted change until the operator removes it", () => {
    expect(heldBy({ id: "c3", resolution: "hold" }, stagedIds, number, true)).toMatch(/Look at the entry/);
    expect(heldBy({ id: "c3", resolution: "hold" }, ["c1", "c2"], number, true)).toBeNull();
  });

  it("holds nothing once the change has left the changeset", () => {
    expect(heldBy({ id: "c3", resolution: "remove" }, ["c1", "c2"], number, false)).toBeNull();
    expect(heldBy(null, stagedIds, number, false)).toBeNull();
  });
});
