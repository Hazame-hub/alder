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
  if (dns.length === 0) {
    return { ok: false, staged: 0, message: "Nothing was selected." };
  }

  const result = changeset.addMany(
    dns.map((dn) => ({ change: { dn, type: "delete" as const }, label: `Delete ${dn}` })),
  );

  if (result.refused > 0) {
    return {
      ok: false,
      staged: 0,
      message:
        result.capacity === 0
          ? `The changeset is full at ${dns.length === 1 ? "its limit" : "its limit"}. ` +
            "Apply or discard what is staged, then select these again."
          : `That is ${dns.length} entries and only ${result.capacity} will fit. ` +
            "Nothing was staged. Apply or discard what is already there, or select fewer.",
    };
  }

  return {
    ok: true,
    staged: result.staged,
    message: `${result.staged} ${result.staged === 1 ? "deletion is" : "deletions are"} staged. Nothing has been sent to the directory.`,
  };
}
