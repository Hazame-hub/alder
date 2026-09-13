import { describe, expect, it } from "vitest";
import type { ChangeRequest, Plan, PlanItem } from "@/lib/api";
import { reviewSingleChange } from "./single-plan";

const dn = "uid=alice,ou=people,dc=alder,dc=test";

function planOf(item: Partial<PlanItem>): Plan {
  return {
    counts: { examined: 1, add: 0, modify: 0, delete: 0, rename: 0, setPassword: 0, unchanged: 0, conflict: 0 },
    items: [{ index: 0, dn, action: "modify", exists: true, ...item } as PlanItem],
  } as Plan;
}

const change: ChangeRequest = {
  dn,
  type: "modify",
  mods: [{ op: "replace", name: "title", values: [{ text: "Senior Engineer" }] }],
};

describe("reviewSingleChange", () => {
  it("applies the reviewed change itself, with the plan's token", () => {
    const review = reviewSingleChange(planOf({ action: "modify", baseline: "tok" }), change);
    expect(review?.state).toBe("apply");
    if (review?.state !== "apply") return;
    expect(review.body).toEqual({ ...change, baseline: "tok" });
  });

  it("sends the staged password, never the plan's withheld record", () => {
    const password: ChangeRequest = { dn, type: "setpassword", newPassword: "correct-horse" };
    const review = reviewSingleChange(
      planOf({ action: "set_password", baseline: "tok", record: { dn, type: "setpassword" } }),
      password,
    );
    expect(review?.state === "apply" && review.body.newPassword).toBe("correct-horse");
  });

  it("offers nothing to apply when the directory already matches", () => {
    expect(reviewSingleChange(planOf({ action: "unchanged" }), change)?.state).toBe("nothing");
  });

  it("does not offer a conflict or a schema violation for applying", () => {
    expect(reviewSingleChange(planOf({ action: "conflict" }), change)?.state).toBe("blocked");
    expect(reviewSingleChange(planOf({ action: "invalid" }), change)?.state).toBe("blocked");
  });

  it("does not offer a change the server issued no token for", () => {
    expect(reviewSingleChange(planOf({ action: "delete" }), change)?.state).toBe("blocked");
  });

  it("returns nothing for a plan with no items", () => {
    expect(reviewSingleChange({ ...planOf({}), items: [] }, change)).toBeNull();
  });
});
