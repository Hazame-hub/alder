import type { ChangeRequest } from "./api";
import { changeset } from "./changeset";

/**
 * Staging a set of deletions from a table's selection.
 *
 * Shared because it is now called from two tables — the object views and the
 * search results — and because the interesting part is not the loop. It is the
 * refusal: the basket is bounded, the server enforces that bound, and a caller
 * that staged as many as fit would leave a partial set behind that reads as a
 * complete one.
 *
 * It returns a sentence rather than a boolean so both callers say the same
 * thing. Wording a refusal twice is how two callers end up explaining the same
 * limit differently.
 */
export type StageOutcome = {
  ok: boolean;
  /** How many were staged; zero when refused. */
  staged: number;
  /** What to show the operator, either way. */
  message: string;
};

export function stageDeletions(dns: string[]): StageOutcome {
  return stageChanges(
    dns.map((dn) => ({ change: { dn, type: "delete" as const }, label: `Delete ${dn}` })),
    { noun: "deletion", nothing: "Nothing was selected." },
  );
}

/**
 * Stage an arbitrary set of changes, with one wording for the refusal.
 *
 * The two callers are a table's selection and a parsed document, and they refuse
 * for the same reason. Wording that twice is how two callers end up explaining
 * the same limit differently.
 */
export function stageChanges(
  items: { change: ChangeRequest; label: string }[],
  words: { noun: string; nothing: string },
): StageOutcome {
  if (items.length === 0) {
    return { ok: false, staged: 0, message: words.nothing };
  }

  const result = changeset.addMany(items);

  if (result.refused > 0) {
    return {
      ok: false,
      staged: 0,
      message:
        result.capacity === 0
          ? "The changeset is already full. Apply or discard what is staged, then try again."
          : `That is ${items.length} changes and only ${result.capacity} will fit. ` +
            "Nothing was staged. Apply or discard what is already there, or take fewer.",
    };
  }

  const plural = result.staged === 1 ? `${words.noun} is` : `${words.noun}s are`;
  return {
    ok: true,
    staged: result.staged,
    message: `${result.staged} ${plural} staged. Nothing has been sent to the directory.`,
  };
}
