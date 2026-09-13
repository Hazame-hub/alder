import { describe, expect, it } from "vitest";
import { changesFromPlan } from "./plan";
import type { ChangeRequest, Plan, PlanItem } from "./api";

function item(over: Partial<PlanItem>): PlanItem {
  return {
    index: 0,
    dn: "uid=alice,ou=people,dc=alder,dc=test",
    action: "modify",
    exists: true,
    intent: "exact",
    ...over,
  } as PlanItem;
}

function planOf(items: PlanItem[]): Plan {
  return {
    counts: {
      examined: items.length,
      add: 0,
      modify: 0,
      delete: 0,
      rename: 0,
      setPassword: 0,
      unchanged: 0,
      conflict: 0,
    },
    items,
  };
}

const staged: ChangeRequest = {
  dn: "uid=alice,ou=people,dc=alder,dc=test",
  type: "modify",
  mods: [{ op: "replace", name: "mail", values: [{ text: "new@alder.test" }] }],
};

describe("changesFromPlan", () => {
  it("sends the staged change with the plan's baseline for an exact item", () => {
    const changes = changesFromPlan(
      planOf([item({ record: staged, baseline: "b0" })]),
      [staged],
    );
    expect(changes).toHaveLength(1);
    expect(changes[0]?.baseline).toBe("b0");
    expect(changes[0]?.mods).toEqual(staged.mods);
  });

  // The plan withholds sensitive values. The staged change holds them, so an
  // exact item must be sent from the staged copy, never from the plan's record.
  it("never sends a withheld value from the plan for an exact item", () => {
    const withPassword: ChangeRequest = {
      dn: staged.dn,
      type: "modify",
      mods: [
        {
          op: "replace",
          name: "userPassword",
          values: [{ text: "{SSHA}real" }],
        },
      ],
    };
    const withheld: ChangeRequest = {
      ...withPassword,
      mods: [{ op: "replace", name: "userPassword", values: [{ size: 10 }] }],
    };
    const changes = changesFromPlan(
      planOf([item({ record: withheld, baseline: "b" })]),
      [withPassword],
    );
    expect(changes[0]?.mods?.[0]?.values?.[0]).toEqual({ text: "{SSHA}real" });
  });

  it("drops what would do nothing", () => {
    const changes = changesFromPlan(
      planOf([
        item({ index: 0, action: "unchanged", reason: "already set" }),
        item({ index: 1, record: staged, baseline: "b1" }),
      ]),
      [staged, staged],
    );
    expect(changes).toHaveLength(1);
    expect(changes[0]?.baseline).toBe("b1");
  });

  it("drops conflicts and invalid changes", () => {
    const changes = changesFromPlan(
      planOf([
        item({
          index: 0,
          action: "conflict",
          problem: { code: "entry_missing" },
        }),
        item({
          index: 1,
          action: "invalid",
          problem: { code: "attribute_undefined" },
        }),
      ]),
      [staged, staged],
    );
    expect(changes).toHaveLength(0);
  });

  // A record with no baseline would apply unchecked, which is the one outcome
  // worse than not planning: it looks checked.
  it("will not send a change it cannot have checked", () => {
    expect(
      changesFromPlan(planOf([item({ record: staged })]), [staged]),
    ).toHaveLength(0);
  });

  it("uses the plan's record for a desired-state item the planner rewrote", () => {
    const addSent: ChangeRequest = {
      dn: staged.dn,
      type: "add",
      attributes: [],
    };
    const changes = changesFromPlan(
      planOf([item({ intent: "desired", record: staged, baseline: "b" })]),
      [addSent],
    );
    expect(changes[0]?.type).toBe("modify");
  });

  it("plans nothing from an empty set", () => {
    expect(changesFromPlan(planOf([]), [])).toHaveLength(0);
  });
});
