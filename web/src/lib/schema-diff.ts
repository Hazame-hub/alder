import type { components } from "@/lib/api.gen";

export type SchemaDiff = components["schemas"]["SchemaDiff"];
export type SchemaDiffItem = components["schemas"]["SchemaDiffItem"];
export type SchemaElementKind = components["schemas"]["SchemaElementKind"];
export type DiffKind = components["schemas"]["DiffKind"];
export type ChangeRequest = components["schemas"]["ChangeRequest"];

export type Actionable = "all" | "actionable" | "not_actionable";

export type SchemaFilter = {
  /** Part of an OID or of a NAME. A search, not an identity: items are still told apart by OID. */
  text: string;
  elements: ReadonlySet<SchemaElementKind>;
  kinds: ReadonlySet<DiffKind>;
  actionable: Actionable;
};

/** Whether an item offers changes that can be selected at all. */
export function schemaSelectable(item: SchemaDiffItem): boolean {
  const c = item.candidate;
  return c !== undefined && c.blocked === undefined && c.changes.length > 0;
}

/** The positions of the items a filter keeps. */
export function filterSchemaItems(items: readonly SchemaDiffItem[], filter: SchemaFilter): number[] {
  const text = filter.text.trim().toLowerCase();
  const out: number[] = [];
  items.forEach((item, index) => {
    if (filter.elements.size > 0 && !filter.elements.has(item.element)) return;
    if (filter.kinds.size > 0 && !filter.kinds.has(item.kind)) return;
    if (filter.actionable === "actionable" && !schemaSelectable(item)) return;
    if (filter.actionable === "not_actionable" && schemaSelectable(item)) return;
    if (
      text &&
      !item.oid.toLowerCase().includes(text) &&
      !(item.names ?? []).some((n) => n.toLowerCase().includes(text))
    ) {
      return;
    }
    out.push(index);
  });
  return out;
}

/**
 * Whether an item is in a selection. A removal counts only when it was chosen
 * as a removal, in its own set; a blocked item never counts.
 */
export function isChosen(item: SchemaDiffItem, chosen: ReadonlySet<string>, deletions: ReadonlySet<string>): boolean {
  if (!schemaSelectable(item)) return false;
  return item.candidate?.destructive === true ? deletions.has(item.key) : chosen.has(item.key);
}

export type MissingDependency = { key: string; needs: string };

/**
 * Selected changes that need another change nobody selected. Reported, never
 * repaired: adding what is missing is the person's decision.
 */
export function missingDependencies(
  items: readonly SchemaDiffItem[],
  chosen: ReadonlySet<string>,
  deletions: ReadonlySet<string>,
): MissingDependency[] {
  const selected = new Set(items.filter((i) => isChosen(i, chosen, deletions)).map((i) => i.key));
  const out: MissingDependency[] = [];
  for (const item of items) {
    if (!selected.has(item.key)) continue;
    for (const needs of item.candidate?.requires ?? []) {
      if (!selected.has(needs)) out.push({ key: item.key, needs });
    }
  }
  return out;
}

/**
 * The selected changes in the comparison's dependency order -- never in the
 * order they were clicked, and never lexically. A selected item the order does
 * not place is returned as unplaced rather than left out.
 */
export function orderedSchemaChanges(
  diff: SchemaDiff,
  chosen: ReadonlySet<string>,
  deletions: ReadonlySet<string>,
): {
  ordered: { item: SchemaDiffItem; changes: ChangeRequest[] }[];
  unplaced: string[];
} {
  const byKey = new Map(diff.items.map((i) => [i.key, i]));
  const ordered: { item: SchemaDiffItem; changes: ChangeRequest[] }[] = [];
  const placed = new Set<string>();
  for (const key of diff.order) {
    const item = byKey.get(key);
    if (!item || !isChosen(item, chosen, deletions)) continue;
    ordered.push({ item, changes: item.candidate?.changes ?? [] });
    placed.add(key);
  }
  const unplaced = diff.items.filter((i) => isChosen(i, chosen, deletions) && !placed.has(i.key)).map((i) => i.key);
  return { ordered, unplaced };
}

/** Every selectable item among these positions that removes nothing, for "select all". */
export function nonDestructiveKeys(items: readonly SchemaDiffItem[], among: readonly number[]): string[] {
  return among.flatMap((i) => {
    const item = items[i];
    return item !== undefined && schemaSelectable(item) && item.candidate?.destructive !== true ? [item.key] : [];
  });
}

/** How an item is named to a person: its first NAME, or its OID. */
export function schemaItemLabel(item: SchemaDiffItem): string {
  return item.names?.[0] ?? item.oid;
}
