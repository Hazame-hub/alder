import { useSyncExternalStore } from "react";
import type { components } from "@/lib/api.gen";

type Snapshot = components["schemas"]["Snapshot"];
type SchemaSnapshot = components["schemas"]["SchemaSnapshot"];
type ConfigSnapshot = components["schemas"]["ConfigSnapshot"];
type Inspection = components["schemas"]["SnapshotInspection"];
type Diff = components["schemas"]["Diff"];

/** A snapshot of any kind. Its `kind` tells them apart. */
export type AnySnapshot = Snapshot | SchemaSnapshot | ConfigSnapshot;

/** One loaded document, and how it got here. */
export type Slot = {
  name: "A" | "B";
  raw: string;
  doc: AnySnapshot;
  inspection: Inspection;
  filename: string;
  origin: "captured" | "uploaded";
  /** The compact JSON the server is sent, which is what its request limit counts. */
  bytes: number;
};

export type SideChoice = "live" | "A" | "B";

/** A comparison and the two sides it was made of, as they were named then. */
export type Comparison = {
  diff: Diff;
  sourceLabel: string;
  targetLabel: string;
};

export type BenchState = {
  slots: Partial<Record<"A" | "B", Slot>>;
  source: SideChoice;
  target: SideChoice;
  comparison: Comparison | null;
};

/**
 * The snapshot bench: the documents loaded for comparison, which sides are
 * being compared, and the last comparison made.
 *
 * A module-level store rather than component state, for one reason found by
 * walking the product: the comparison tells the operator to go and review the
 * changeset, and going there used to throw the comparison away. They came back
 * to two empty slots and had to find the file and compare again -- while the
 * comparison is the only record of what *else* differed, which is exactly what
 * somebody halfway through fixing a drift wants beside the changeset.
 *
 * The lifetime is the tab's, the same as [changeset]: in memory, never written
 * to browser storage. A configuration snapshot withholds secrets, but it still
 * describes somebody's infrastructure, and it is dropped on Disconnect because
 * the next session in this tab may be a different operator on a different
 * server.
 *
 * Loading a document, or changing a side, clears the comparison: a result
 * labelled "Snapshot A" that is no longer about the document in slot A is worse
 * than no result at all.
 */

let state: BenchState = { slots: {}, source: "live", target: "A", comparison: null };
const listeners = new Set<() => void>();

function set(next: Partial<BenchState>) {
  // A new object every time: useSyncExternalStore compares by identity.
  state = { ...state, ...next };
  listeners.forEach((l) => l());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export const bench = {
  state: () => state,

  /**
   * Put a captured or uploaded document in its slot, and point the comparison
   * at it.
   *
   * A document loaded into B while the target was A left the screen asking for
   * a side that was empty. What somebody just loaded is what they want
   * compared, so it becomes the target -- unless it is already the source, in
   * which case they have said what they mean.
   */
  load(slot: Slot) {
    const side = state.source === slot.name ? {} : { target: slot.name };
    set({ slots: { ...state.slots, [slot.name]: slot }, comparison: null, ...side });
  },

  setSide(which: "source" | "target", choice: SideChoice) {
    set({ [which]: choice, comparison: null } as Partial<BenchState>);
  },

  setComparison(comparison: Comparison) {
    set({ comparison });
  },

  clear() {
    set({ slots: {}, source: "live", target: "A", comparison: null });
  },
};

export function useBench(): BenchState {
  return useSyncExternalStore(subscribe, bench.state, bench.state);
}
