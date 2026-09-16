import type { components } from "@/lib/api.gen";
import type { ChangeRequest } from "@/lib/api";

export type ChangePackage = components["schemas"]["ChangePackage"];
export type PackageInspection = components["schemas"]["PackageInspection"];
export type PackageValidation = components["schemas"]["PackageValidation"];
export type PackageValidationItem = components["schemas"]["PackageValidationItem"];
export type PackageChange = components["schemas"]["PackageChange"];
export type PackageItemStatus = components["schemas"]["PackageItemStatus"];

/**
 * Reading a validation.
 *
 * A package is intent; a validation is what one directory makes of it. Nothing
 * here decides anything: the ready changes go into the changeset, which plans
 * them against the directory as it is at that moment, and the plan is what
 * says what will be written.
 */

/** What each status means to a reader, and how much it should stand out. */
export const STATUS_LOOK: Record<
  PackageItemStatus,
  { label: string; variant: "success" | "destructive" | "secondary" | "outline" | "warning" }
> = {
  ready: { label: "Ready", variant: "success" },
  already_satisfied: { label: "Already satisfied", variant: "outline" },
  no_op: { label: "Nothing to do", variant: "outline" },
  conflict: { label: "Conflict", variant: "warning" },
  dependency_missing: { label: "Dependency missing", variant: "warning" },
  unsupported: { label: "Unsupported here", variant: "warning" },
  target_incompatible: { label: "Not for this directory", variant: "warning" },
  unknown: { label: "Unknown", variant: "warning" },
};

/** The changes of the ready items, in the order the target gave. */
export function readyChanges(validation: PackageValidation): { item: PackageValidationItem; change: ChangeRequest }[] {
  const byId = new Map(validation.items.map((i) => [i.id, i]));
  const out: { item: PackageValidationItem; change: ChangeRequest }[] = [];
  for (const id of validation.order) {
    const item = byId.get(id);
    if (!item || item.status !== "ready") continue;
    for (const change of item.changes ?? []) out.push({ item, change });
  }
  return out;
}

/** Whether anything in this validation can be staged. */
export function hasWork(validation: PackageValidation): boolean {
  return validation.counts.ready > 0;
}

/** The assumptions this target does not satisfy. */
export function unmetAssumptions(validation: PackageValidation) {
  return validation.assumptions.filter((a) => !a.satisfied);
}

/**
 * The items in reading order: what will be applied first, in the order it will
 * be applied, then everything else by identifier.
 */
export function orderedItems(validation: PackageValidation): PackageValidationItem[] {
  const position = new Map(validation.order.map((id, at) => [id, at]));
  return [...validation.items].sort((a, b) => {
    const left = position.get(a.id);
    const right = position.get(b.id);
    if (left !== undefined && right !== undefined) return left - right;
    if (left !== undefined) return -1;
    if (right !== undefined) return 1;
    return a.id.localeCompare(b.id);
  });
}

/** One change of a package in a line: what it does, to what. */
export function changeLine(change: PackageChange): string {
  if (change.schema) {
    const s = change.schema;
    return `${s.op} ${s.element} ${s.oid}`;
  }
  if (change.data) {
    const d = change.data;
    if (d.type === "rename") {
      const to = [d.newRdn, d.newSuperior].filter(Boolean).join(",");
      return `rename ${d.dn}${to ? ` → ${to}` : ""}`;
    }
    if (d.type === "modify") {
      const names = (d.mods ?? []).map((m) => `${m.op} ${m.name}`).join(", ");
      return `modify ${d.dn}${names ? `: ${names}` : ""}`;
    }
    return `${d.type} ${d.dn}`;
  }
  return change.kind;
}
