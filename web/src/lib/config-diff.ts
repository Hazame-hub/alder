import type { components } from "@/lib/api.gen";

export type ConfigDiff = components["schemas"]["ConfigDiff"];
export type ConfigDiffItem = components["schemas"]["ConfigDiffItem"];
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
