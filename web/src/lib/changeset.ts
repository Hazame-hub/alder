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

export const changeset = {
  all: () => staged,

  add(change: ChangeRequest, label: string) {
    counter += 1;
    staged.push({ id: `c${counter}`, change, label });
    emit();
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
