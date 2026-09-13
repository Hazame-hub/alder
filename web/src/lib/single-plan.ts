import type { ChangeRequest, Plan, PlanItem } from "@/lib/api";

/**
 * What the confirmation dialog may do with the plan of one change.
 *
 * - `apply`: the plan says the change does something, and `body` is what to
 *   send — the change exactly as it was reviewed, carrying the plan's token so
 *   the server can refuse it if it is not that change or the directory has moved.
 * - `nothing`: the directory already holds what the change describes. Applying
 *   would write nothing, so there is nothing to confirm.
 * - `blocked`: the plan found a conflict or a schema violation. Sending it would
 *   only be refused by the directory, or would do something other than what the
 *   operator reviewed.
 *
 * The change sent is the staged one, not the plan's record: a single change is
 * always planned as exactly itself, and the staged copy is the only one that
 * still holds a password or other value the plan withheld.
 */
export type SingleChangeReview =
  | { state: "apply"; item: PlanItem; body: ChangeRequest }
  | { state: "nothing"; item: PlanItem }
  | { state: "blocked"; item: PlanItem };

export function reviewSingleChange(plan: Plan, change: ChangeRequest): SingleChangeReview | null {
  const item = plan.items.find((i) => i.index === 0) ?? plan.items[0];
  if (!item) return null;
  switch (item.action) {
    case "unchanged":
      return { state: "nothing", item };
    case "conflict":
    case "invalid":
      return { state: "blocked", item };
  }
  if (!item.baseline) {
    // Every item that applies carries a token. One without is not a plan this
    // dialog can hold the server to, so it is not offered as one.
    return { state: "blocked", item };
  }
  return { state: "apply", item, body: { ...change, baseline: item.baseline } };
}
