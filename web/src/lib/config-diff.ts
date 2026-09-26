import type { components } from "@/lib/api.gen";

export type ConfigDiff = components["schemas"]["ConfigDiff"];
export type ConfigDiffItem = components["schemas"]["ConfigDiffItem"];
export type ConfigDiffObject = components["schemas"]["ConfigDiffObject"];
export type ConfigActionable = components["schemas"]["ConfigActionable"];
export type DiffKind = components["schemas"]["DiffKind"];

/**
 * Reading a configuration comparison.
 *
 * Nothing here decides anything about a server's configuration: the provider's
 * model did that on the server. This picks out what to show and what may be
 * staged, and the rule for staging is the server's own answer -- a setting is
 * offered only when Alder already changes that exact setting through a plan.
 */

export type ConfigFilter = {
  section?: string | "all";
  kinds?: Set<DiffKind>;
  /** Only settings Alder could change. */
  actionableOnly?: boolean;
  /** Hide settings whose values are withheld. */
  hideSensitive?: boolean;
  /** Matches the key, the resource, the entry DN and the values. */
  text?: string;
};

export function filterConfigItems(items: ConfigDiffItem[], filter: ConfigFilter): ConfigDiffItem[] {
  const needle = (filter.text ?? "").trim().toLowerCase();
  return items.filter((item) => {
    if (filter.section && filter.section !== "all" && item.section !== filter.section) return false;
    if (filter.kinds && filter.kinds.size > 0 && !filter.kinds.has(item.kind)) return false;
    if (filter.actionableOnly && item.actionable !== "writable") return false;
    if (filter.hideSensitive && item.sensitive) return false;
    if (!needle) return true;
    const haystack = [item.key, item.resource ?? "", item.resourceLabel ?? "", item.dn ?? "", ...(item.source ?? []), ...(item.target ?? [])];
    return haystack.some((s) => s.toLowerCase().includes(needle));
  });
}

/** The sections a comparison actually has something in, in the server's order. */
export function configSections(diff: ConfigDiff): string[] {
  return diff.sections.map((s) => s.section);
}

/**
 * The change requests for the settings selected, in the order the comparison
 * gave. A setting the server did not offer a change for is never included --
 * the selection cannot make something actionable that is not.
 */
export function stageableChanges(
  diff: ConfigDiff,
  selected: Set<string>,
): { item: ConfigDiffItem; change: NonNullable<ConfigDiffItem["candidate"]>["changes"][number] }[] {
  const out: { item: ConfigDiffItem; change: NonNullable<ConfigDiffItem["candidate"]>["changes"][number] }[] = [];
  for (const item of diff.items) {
    if (!selected.has(item.id)) continue;
    if (item.actionable !== "writable") continue;
    for (const change of item.candidate?.changes ?? []) {
      out.push({ item, change });
    }
  }
  return out;
}

/**
 * The configuration objects worth showing: the ones that differ. An object
 * both sides hold is the ordinary case and says nothing.
 */
export function changedObjects(diff: ConfigDiff): ConfigDiffObject[] {
  return diff.objects.filter((object) => object.kind !== "unchanged");
}

/**
 * What the comparison offers to change, settings and objects together.
 *
 * The server counts settings, because that is what it compared setting by
 * setting. Counting only those made a comparison whose one actionable
 * difference was an overlay read "0 Alder can change" directly above a row
 * offering to create it, which is how an operator decides there is nothing
 * here for them.
 */
export function actionableCount(diff: ConfigDiff): { settings: number; objects: number; total: number } {
  const objects = actionableObjects(diff).length;
  return { settings: diff.counts.actionable, objects, total: diff.counts.actionable + objects };
}

/** The differing objects Alder could create or remove. */
export function actionableObjects(diff: ConfigDiff): ConfigDiffObject[] {
  return changedObjects(diff).filter((object) => object.actionable === "writable");
}

/**
 * The objects to show. "Only what Alder can change" is about what Alder can
 * act on, so it hides the objects it cannot act on as well as the settings.
 */
export function visibleObjects(diff: ConfigDiff, actionableOnly: boolean): ConfigDiffObject[] {
  return actionableOnly ? actionableObjects(diff) : changedObjects(diff);
}

/**
 * The changes for the objects selected, in the order the comparison gave.
 *
 * A removal is included only when it was selected as a removal: selecting
 * everything on a page never removes anything from a server. Creating an
 * object comes first and removing one last, which is the order a person would
 * do it in by hand.
 */
export function stageableObjects(
  diff: ConfigDiff,
  selected: Set<string>,
  removals: Set<string>,
): { object: ConfigDiffObject; change: NonNullable<ConfigDiffObject["candidate"]>["changes"][number] }[] {
  const additions: { object: ConfigDiffObject; change: NonNullable<ConfigDiffObject["candidate"]>["changes"][number] }[] = [];
  const removes: typeof additions = [];
  for (const object of diff.objects) {
    if (object.actionable !== "writable") continue;
    const wanted = object.destructive ? removals.has(object.id) : selected.has(object.id);
    if (!wanted) continue;
    for (const change of object.candidate?.changes ?? []) {
      (object.destructive ? removes : additions).push({ object, change });
    }
  }
  return [...additions, ...removes];
}

/**
 * Why Alder cannot act on a setting, in a person's words.
 *
 * The comparison carries stable codes -- no_write_path, not_read -- and a row
 * used to print them, or, where a setting was "Unknown" and carried no code at
 * all, to print nothing. A badge saying "Unknown" with nothing beside it reads
 * as a fault in Alder rather than a fact about the setting.
 */
export function itemReason(item: ConfigDiffItem): string {
  if (item.actionable === "writable") return "";
  for (const problem of item.problems ?? []) {
    switch (problem) {
      case "not_read":
        return "part of one side could not be read, so its absence here says nothing";
      case "sensitive_withheld":
        return "the value is a secret: a comparison counts them, and never writes one back";
      case "not_comparable":
        return "nothing here parses this value, so the two sides are compared as text";
      case "no_write_path":
        return "Alder has no proven way to write this setting on this server";
      case "resource_missing":
        return "the database, overlay or plugin it belongs to is not on both sides";
      case "provider_specific":
        return "this setting belongs to one server's software and has no counterpart on the other";
    }
  }
  if (item.actionable === "unknown") {
    return "Alder's model of this server's configuration does not cover this setting";
  }
  return "";
}

/** Why Alder cannot act on an object difference, in a person's words. */
export function objectRefusal(object: ConfigDiffObject): string {
  switch (object.refusal) {
    case undefined:
    case "":
      return "";
    case "module_not_loaded":
      return "the server has not loaded the module this overlay needs";
    case "not_creatable":
      return "Alder does not create or remove objects of this kind";
    case "parent_missing":
      return "the database it belongs to is not on this server";
    case "source_not_live":
      return "a change is proposed only when the source is the directory itself";
    case "not_present":
      return "it is not on this server";
    default:
      return object.refusal;
  }
}

/** What a value looks like in the interface, with a withheld one named as such. */
export function valueText(item: ConfigDiffItem, side: "source" | "target"): string {
  if (item.sensitive) return "withheld";
  const values = side === "source" ? item.source : item.target;
  if (!values || values.length === 0) return "(none)";
  return values.join(item.ordered ? " → " : ", ");
}

export const ACTION_LOOK: Record<ConfigActionable, { label: string; variant: "success" | "outline" | "warning" }> = {
  writable: { label: "Alder can change this", variant: "success" },
  read_only: { label: "Reported only", variant: "outline" },
  unknown: { label: "Unknown", variant: "warning" },
};
