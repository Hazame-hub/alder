import type { components } from "@/lib/api.gen";

export type DiffItem = components["schemas"]["DiffItem"];
export type DiffKind = components["schemas"]["DiffKind"];
export type ChangeRequest = components["schemas"]["ChangeRequest"];

export type DiffFilter = {
  kinds: ReadonlySet<DiffKind>;
  dn: string;
  attribute: string;
};

/** The DN an item is about: where it is in the target, or in the source if nowhere else. */
export function itemDn(item: DiffItem): string {
  return item.targetDn ?? item.sourceDn ?? "";
}

/**
 * The items a filter keeps, by position in the diff.
 *
 * Positions rather than items, so a selection made on a filtered page still
 * names the same differences when the filter changes.
 */
export function filterItems(items: readonly DiffItem[], filter: DiffFilter): number[] {
  const dn = filter.dn.trim().toLowerCase();
  const attribute = filter.attribute.trim().toLowerCase();
  const out: number[] = [];
  items.forEach((item, index) => {
    if (filter.kinds.size > 0 && !filter.kinds.has(item.kind)) return;
    if (dn && !`${item.sourceDn ?? ""} ${item.targetDn ?? ""}`.toLowerCase().includes(dn)) return;
    if (attribute && !(item.attributes ?? []).some((a) => a.name.toLowerCase().includes(attribute))) return;
    out.push(index);
  });
  return out;
}

/** Whether an item offers changes that can be selected at all. */
export function selectable(item: DiffItem): boolean {
  const c = item.candidate;
  return c !== undefined && c.blocked === undefined && c.changes.length > 0;
}

/**
 * The change requests to stage for a selection.
 *
 * An item that deletes counts only if it was chosen as a deletion, in its own
 * set: selecting "everything" never selects a delete. Blocked items and items
 * without a candidate contribute nothing, whatever the selection says.
 */
export function selectedChanges(
  items: readonly DiffItem[],
  chosen: ReadonlySet<number>,
  deletions: ReadonlySet<number>,
): ChangeRequest[] {
  return selectedChangeItems(items, chosen, deletions).map((s) => s.change);
}

/**
 * The same selection, each change with the item it came from, for a caller
 * that has to name them -- the changeset wants a label per change. One
 * traversal decides what is selected; a second one written beside it is how
 * the two drift apart.
 */
export function selectedChangeItems(
  items: readonly DiffItem[],
  chosen: ReadonlySet<number>,
  deletions: ReadonlySet<number>,
): { item: DiffItem; change: ChangeRequest }[] {
  const out: { item: DiffItem; change: ChangeRequest }[] = [];
  items.forEach((item, index) => {
    if (!selectable(item)) return;
    const destructive = item.candidate?.destructive === true;
    if (destructive ? !deletions.has(index) : !chosen.has(index)) return;
    for (const change of item.candidate?.changes ?? []) out.push({ item, change });
  });
  return out;
}

/** Every selectable item that does not delete, for "select all". */
export function nonDestructive(items: readonly DiffItem[], among: readonly number[]): number[] {
  return among.filter((i) => {
    const item = items[i];
    return item !== undefined && selectable(item) && item.candidate?.destructive !== true;
  });
}
