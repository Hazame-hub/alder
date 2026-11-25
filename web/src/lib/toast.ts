import { useSyncExternalStore } from "react";

/**
 * Short confirmations after something has happened, such as "Change applied".
 *
 * An apply used to close its dialog and say nothing, so whether it had worked
 * was read from the entry changing underneath. A toast says it in words, then
 * goes. It is a notice, never a question: nothing here waits for an answer,
 * and anything that must be decided stays in a dialog.
 *
 * Kept in memory only, like the changeset. A reload drops what is showing.
 */
export type Toast = {
  id: number;
  title: string;
  /** Untrusted when it carries a DN; rendered through safeText. */
  description?: string;
  tone: "success" | "info";
  action?: { label: string; run: () => void };
};

const lifetimeMs = 6000;
let toasts: Toast[] = [];
let next = 1;
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

export const toast = {
  show(t: Omit<Toast, "id" | "tone"> & { tone?: Toast["tone"] }): number {
    const id = next++;
    // At most three at once: an apply of many changes says so once, not N times.
    toasts = [...toasts.slice(-2), { tone: "success", ...t, id }];
    emit();
    setTimeout(() => toast.dismiss(id), lifetimeMs);
    return id;
  },
  dismiss(id: number) {
    const before = toasts.length;
    toasts = toasts.filter((t) => t.id !== id);
    if (toasts.length !== before) emit();
  },
  all(): Toast[] {
    return toasts;
  },
  /** For tests. */
  clear() {
    toasts = [];
    emit();
  },
};

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useToasts(): Toast[] {
  return useSyncExternalStore(subscribe, toast.all, toast.all);
}
