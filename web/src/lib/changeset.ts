import { useSyncExternalStore } from "react";
import type { ChangeRequest } from "@/lib/api";

/**
 * The staged changeset.
 *
 * A module-level store rather than context, because the basket is read by the
 * top bar, written from a dialog three levels down, and rendered by a view that
 * is a sibling of both. Threading it through props would touch every component
 * between, to no benefit: there is exactly one changeset per tab, and it has no
 * reason to be scoped to a subtree.
 *
 * It is held in memory and nowhere else. sessionStorage would survive a
 * refresh, which is genuinely nicer, but a staged password change carries the
 * new password in plaintext, and writing that to browser storage would break
 * the rule that credentials never persist for the sake of a convenience. The
 * changeset view says the basket is lost on refresh rather than letting anyone
 * discover it.
 */

export type StagedChange = {
  /** Stable across reorders, so React keys and outcome lookups stay put. */
  id: string;
  change: ChangeRequest;
  /** What the user was doing when they staged it, for the list. */
  label: string;
};

let staged: StagedChange[] = [];
const listeners = new Set<() => void>();

function emit() {
  // A new array on every change: useSyncExternalStore compares by identity, and
  // mutating in place would render nothing.
  staged = [...staged];
  listeners.forEach((l) => l());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  // Braces on purpose: Set.delete returns a boolean, and an expression-bodied
  // arrow would hand React a boolean where it expects a cleanup function.
  return () => {
    listeners.delete(listener);
  };
}

let counter = 0;

/**
 * The bound the server enforces on a changeset, mirrored here.
 *
 * The server is the one that decides — internal/api/changeset.go refuses past
 * this, and api/openapi.yaml declares it. This copy exists only so a bulk
 * stager can say "that will not fit" before it stages anything, rather than
 * filling the basket and having the preview fail afterwards. If the two ever
 * disagree the server wins, and the failure mode is a refusal at preview time
 * rather than a wrong write.
 */
export const maxStagedChanges = 2000;

export const changeset = {
  all: () => staged,

  /** How many more changes will fit. */
  capacity: () => maxStagedChanges - staged.length,

  add(change: ChangeRequest, label: string) {
    counter += 1;
    staged.push({ id: `c${counter}`, change, label });
    emit();
  },

  /**
   * Stage many changes, or none.
   *
   * All-or-nothing on purpose. Staging a prefix and stopping would leave the
   * basket holding some of a set the operator asked for as a whole — a subtree
   * missing its deepest entries, or the first four hundred records of a
   * document — which looks like it worked and applies to something nobody
   * chose. It returns what it did so the caller can say so.
   */
  addMany(items: { change: ChangeRequest; label: string }[]): {
    staged: number;
    refused: number;
    capacity: number;
  } {
    const capacity = maxStagedChanges - staged.length;
    if (items.length > capacity) {
      return { staged: 0, refused: items.length, capacity };
    }
    for (const item of items) {
      counter += 1;
      staged.push({ id: `c${counter}`, change: item.change, label: item.label });
    }
    emit();
    return { staged: items.length, refused: 0, capacity };
  },

  remove(id: string) {
    staged = staged.filter((s) => s.id !== id);
    emit();
  },

  /**
   * Move one change by a step. Order is the user's, never inferred: it is what
   * the preview warnings talk about, and rearranging it behind their back would
   * mean applying something they did not read.
   */
  move(id: string, delta: number) {
    const from = staged.findIndex((s) => s.id === id);
    const to = from + delta;
    if (from < 0 || to < 0 || to >= staged.length) return;
    const item = staged[from];
    if (!item) return;
    staged.splice(from, 1);
    staged.splice(to, 0, item);
    emit();
  },

  /**
   * Drop the changes that applied, keeping the rest in their order.
   *
   * A partial run leaves the basket holding exactly the work still to do, so
   * the fix-and-retry is "correct the one that failed and apply again" rather
   * than "clear it and rebuild the list from memory".
   */
  removeApplied(appliedIds: string[]) {
    const done = new Set(appliedIds);
    staged = staged.filter((s) => !done.has(s.id));
    emit();
  },

  clear() {
    staged = [];
    emit();
  },
};

export function useChangeset(): StagedChange[] {
  return useSyncExternalStore(subscribe, changeset.all, changeset.all);
}
