import type { AppSearch } from "@/lib/route";

/**
 * Going somewhere, from a component that has no business knowing about routing.
 *
 * The app shell owns navigation and hands it down as props, which is right
 * almost everywhere: a panel that navigates is a panel whose parent decided it
 * should. The exception is the error note. It is rendered in a dozen places,
 * several of them three levels inside a dialog, and since 1.22 a refusal can
 * carry somewhere to look -- the parent entry that is missing, the schema an
 * attribute belongs to. Threading a callback from the shell through every one
 * of those call sites, for a button that appears on a fraction of a percent of
 * renders, would be a lot of plumbing for one rare, useful thing.
 *
 * So: one function, set once by the shell, read by the few components that
 * need it. It is deliberately not a general escape hatch -- anything that
 * navigates as part of its ordinary job still takes a prop.
 */

let go: ((search: Partial<AppSearch>) => void) | null = null;

/** Called once by the app shell. */
export function setNavigator(fn: (search: Partial<AppSearch>) => void) {
  go = fn;
}

/** Whether there is anywhere to go, which there is not in a test render. */
export function canNavigate(): boolean {
  return go !== null;
}

export function navigate(search: Partial<AppSearch>) {
  go?.(search);
}
