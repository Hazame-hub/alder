/**
 * Whether a subtree can be staged for deletion, and why not.
 *
 * Pure, and separate from the dialog, because it is the safety logic of the
 * most destructive action in the application. Every branch here is a refusal
 * that prevents a partial subtree delete — which removes the leaves it reached
 * and leaves every container standing, a state worse than not having started.
 * That is not something to leave reachable only by clicking through a dialog.
 */

export type SubtreeCheck = {
  /** What GET /count returned. */
  count: number;
  truncated: boolean;
  /** Whether the server offers paged results at all. */
  paging: boolean;
  /** How many more changes the changeset will hold. */
  capacity: number;
};

export type SubtreeVerdict =
  | { ok: true; count: number }
  | { ok: false; why: string; detail: string };

export function checkSubtree({
  count,
  truncated,
  paging,
  capacity,
}: SubtreeCheck): SubtreeVerdict {
  // Paging is what makes a complete subtree obtainable. Without it a search
  // returns whatever the server's own size limit allowed and there is no way to
  // know what was left out.
  if (!paging) {
    return {
      ok: false,
      why: "This server does not offer paged results.",
      detail:
        "Without paging a search returns whatever the server's own size limit allowed, " +
        "so Alder cannot obtain a complete subtree — and an incomplete one would delete " +
        "the leaves it found and leave the containers standing.",
    };
  }

  if (truncated) {
    return {
      ok: false,
      why: `There are more than ${count.toLocaleString()} entries under this one.`,
      detail:
        "The count stopped at its limit, so Alder cannot see the whole subtree and will " +
        "not stage part of it. Delete some of it from further down first.",
    };
  }

  if (count === 0) {
    return {
      ok: false,
      why: "There is nothing under this entry.",
      detail: "Delete it directly instead.",
    };
  }

  // The basket is shared with everything else staged, so the limit that matters
  // is what is left in it, not the cap.
  if (count > capacity) {
    return {
      ok: false,
      why: `That is ${count.toLocaleString()} entries and only ${capacity.toLocaleString()} will fit in the changeset.`,
      detail:
        capacity === 0
          ? "Apply or discard what is already staged, then try again."
          : "Apply or discard what is already staged, or delete part of this subtree first.",
    };
  }

  return { ok: true, count };
}
