import type { ChangeRequest, Plan } from "@/lib/api";

/**
 * The changes a checked plan would actually apply.
 *
 * Two things happen here and both matter. Items that would do nothing — an
 * entry that already holds what the change sets — and items that cannot be done
 * are dropped, so applying a checked changeset sends only work. And each
 * surviving change carries the baseline the plan was computed against, which is
 * what lets the server refuse the whole set if somebody else has edited one of
 * these entries in the meantime.
 *
 * The record comes from the plan rather than from the basket. For a reconciled
 * change those are not the same thing, and the one the plan showed is the one
 * that should run.
 */
export function changesFromPlan(plan: Plan): ChangeRequest[] {
  return plan.items
    .filter((item) => item.record !== undefined && item.baseline !== undefined)
    .map((item) => ({ ...(item.record as ChangeRequest), baseline: item.baseline }));
}
