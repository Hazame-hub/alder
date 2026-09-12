import { describe, expect, it } from "vitest";
import { changesFromPlan } from "./plan";
import type { ChangeRequest, Plan, PlanItem } from "./api";

function item(over: Partial<PlanItem>): PlanItem {
  return {
    index: 0,
    dn: "uid=alice,ou=people,dc=alder,dc=test",
    action: "modify",
    exists: true,
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

const record = { dn: "uid=alice,ou=people,dc=alder,dc=test", type: "modify" } as const;

describe("changesFromPlan", () => {
  it("keeps what would run, with its baseline attached", () => {
    const changes = changesFromPlan(
      planOf([item({ index: 0, record, baseline: "b0" })]),
    );
    expect(changes).toHaveLength(1);
    expect(changes[0]?.baseline).toBe("b0");
    expect(changes[0]?.dn).toBe(record.dn);
  });

  it("drops what would do nothing", () => {
    const changes = changesFromPlan(
      planOf([
        item({ index: 0, action: "unchanged", reason: "already set" }),
        item({ index: 1, record, baseline: "b1" }),
      ]),
    );
    expect(changes).toHaveLength(1);
    expect(changes[0]?.baseline).toBe("b1");
  });

  it("drops what cannot be done", () => {
    const changes = changesFromPlan(
      planOf([item({ index: 0, action: "conflict", reason: "no such entry" })]),
    );
    expect(changes).toHaveLength(0);
  });

  // A plan whose items carry a record but no baseline would apply unchecked,
  // which is the one outcome worse than not planning: it looks checked.
  it("will not send a change it cannot have checked", () => {
    const changes = changesFromPlan(planOf([item({ index: 0, record })]));
    expect(changes).toHaveLength(0);
  });

  it("uses the record the plan returned, not the one that produced it", () => {
    // A reconciled add comes back as a modify. Sending the add would recreate
    // exactly the failure the reconcile exists to avoid.
    const reconciled: ChangeRequest = {
      dn: "uid=alice,ou=people,dc=alder,dc=test",
      type: "modify",
      mods: [{ op: "replace", name: "mail", values: [{ text: "new@alder.test" }] }],
    };
    const changes = changesFromPlan(
      planOf([item({ index: 0, action: "modify", record: reconciled, baseline: "b" })]),
    );
    expect(changes).toHaveLength(1);
    expect(changes[0]?.type).toBe("modify");
    expect(changes[0]?.mods).toHaveLength(1);
  });

  it("plans nothing from an empty set", () => {
    expect(changesFromPlan(planOf([]))).toHaveLength(0);
  });
});
