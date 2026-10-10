import type { ChangeOutcome, ChangesetResult, Plan } from "@/lib/api";
import { sentStagedIndices } from "@/lib/plan";

/**
 * What a changeset run sent, kept beside its result.
 *
 * Outcomes are numbered by position in what was sent. With a checked plan
 * that dropped a change, position *i* sent is not position *i* staged, so a
 * run carries the staged id of each change it sent, and every lookup from an
 * outcome to a staged change goes through it. Reading `staged[o.index]`
 * instead removed the wrong change after a partial run and asked the
 * directory about the wrong change after an interrupted one.
 */
export type SentRun = {
  /** The staged id of each change sent, in the order sent. */
  ids: string[];
};

export function sentIds(stagedIds: string[], plan: Plan | null): string[] {
  if (!plan) return [...stagedIds];
  return sentStagedIndices(plan)
    .map((i) => stagedIds[i])
    .filter((id): id is string => id !== undefined);
}

/** The staged ids of the changes the run applied. */
export function appliedIds(result: ChangesetResult, run: SentRun): string[] {
  return result.outcomes
    .filter((o) => o.applied)
    .map((o) => run.ids[o.index])
    .filter((id): id is string => id !== undefined);
}

/** The staged id of the change whose outcome is unknown, if there is one. */
export function unknownId(result: ChangesetResult, run: SentRun): string | undefined {
  const o = result.outcomes.find((x) => x.state === "unknown");
  return o ? run.ids[o.index] : undefined;
}

/**
 * What the changeset does with an interrupted change once the directory has
 * been read.
 *
 * - `remove`: it is in the directory. Leaving it staged would invite a
 *   second, separate change.
 * - `replan`: it is not in the directory. It stays staged, and the run may go
 *   again only from a fresh plan, so it is applied against the directory as
 *   it is now rather than as it was reviewed before the interruption.
 * - `hold`: something else changed the entry, or reading cannot settle it.
 *   Applying stays blocked until the operator has looked at the entry and
 *   removed the change themselves.
 */
export type Resolution = "remove" | "replan" | "hold";

export function resolve(verdict: ChangeOutcome["verdict"]): Resolution {
  switch (verdict) {
    case "applied":
      return "remove";
    case "not_applied":
      return "replan";
    default:
      return "hold";
  }
}

/**
 * Whether Apply is held back by an interrupted change, and why -- as the
 * sentence shown beside the button, or null when it is not.
 */
export function heldBy(
  interrupted: { id: string; resolution: Resolution | null } | null,
  stagedIds: string[],
  stagedNumber: (id: string) => number,
  hasCurrentPlan: boolean,
): string | null {
  // Removed from the basket, by a verdict or by hand: nothing left to resend.
  if (!interrupted || !stagedIds.includes(interrupted.id)) return null;
  const n = stagedNumber(interrupted.id);
  switch (interrupted.resolution) {
    case null:
      return `Change ${n} may already have been applied. Check the directory before applying again.`;
    case "hold":
      return `Change ${n} cannot be applied again as it was reviewed. Look at the entry, then remove it from the changeset.`;
    case "replan":
      return hasCurrentPlan
        ? null
        : `Change ${n} did not reach the directory. Check against the directory again before applying, so it is planned against the directory as it is now.`;
    default:
      return null;
  }
}
