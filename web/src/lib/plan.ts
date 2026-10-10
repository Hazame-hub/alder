import type { ChangeRequest, Plan } from "@/lib/api";

/**
 * The changes a checked plan would actually apply.
 *
 * Items that would do nothing — an entry that already holds what the change
 * sets — and items that cannot be done are dropped, so applying a checked
 * changeset sends only work. Each surviving change carries the baseline the
 * plan issued, which binds both the operation and the directory state it was
 * planned against: the server refuses the whole set if either no longer holds.
 *
 * Which record is sent depends on how the change was read.
 *
 * An exact change is planned as written, so the operation the plan bound is the
 * one that was staged — and the staged copy is the one to send, because a plan
 * withholds sensitive values from its response. A password in a staged change
 * reaches the server from the browser that already had it, never from the plan.
 *
 * A desired-state change may have been rewritten by the planner (an add of an
 * existing entry becomes the modification that makes it match). Its operation
 * exists only in the plan, so the plan's record is sent. If that record carries
 * a withheld value, the server refuses it rather than writing an empty one, and
 * the change has to be applied from the document it came from.
 */
export function changesFromPlan(
  plan: Plan,
  staged: ChangeRequest[],
): ChangeRequest[] {
  return sentItems(plan).map((item) => {
    const exact = item.intent !== "desired";
    const source = exact ? staged[item.index] : undefined;
    const record = source ?? (item.record as ChangeRequest);
    return { ...record, baseline: item.baseline };
  });
}

/**
 * Which staged changes a checked plan sends, by their index in the staged
 * list, in the order they are sent.
 *
 * A run's outcomes are numbered by what was sent, not by what was staged, and
 * the two differ whenever the plan dropped something. Anything that maps an
 * outcome back to a staged change goes through this, so it cannot drift from
 * what changesFromPlan actually sent.
 */
export function sentStagedIndices(plan: Plan): number[] {
  return sentItems(plan).map((item) => item.index);
}

function sentItems(plan: Plan): Plan["items"] {
  return plan.items.filter((item) => item.record !== undefined && item.baseline !== undefined);
}
