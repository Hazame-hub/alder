import { splitDN } from "./values";

/**
 * Ordering a subtree so it can be deleted.
 *
 * A directory removes entries one at a time, from the bottom: an entry with
 * children cannot be deleted, which is what `notAllowedOnNonLeaf` means and
 * what Alder's own hint already tells the operator. So a set of deletions is
 * only applicable in one order, and getting it wrong does not fail cleanly —
 * the run stops partway, having deleted the leaves it reached and left every
 * container standing.
 *
 * This is the one place in the front end where DN structure is a correctness
 * question rather than a display one.
 */

/** depthOf counts the RDNs in a DN, respecting escaped commas. */
export function depthOf(dn: string): number {
  return splitDN(dn).length;
}

/**
 * orderDeepestFirst returns the DNs ordered so no entry precedes its own
 * descendants.
 *
 * Sorting by depth is enough, and is why this does not need to build the tree:
 * a child always has more RDNs than its parent, so descending depth puts every
 * child ahead of every ancestor. Entries at equal depth in different branches
 * are unordered with respect to each other, which is correct — neither is the
 * other's parent — and their original order is preserved so the list reads the
 * way the server returned it.
 */
export function orderDeepestFirst(dns: string[]): string[] {
  return dns
    .map((dn, index) => ({ dn, index, depth: depthOf(dn) }))
    .sort((a, b) => b.depth - a.depth || a.index - b.index)
    .map((d) => d.dn);
}
