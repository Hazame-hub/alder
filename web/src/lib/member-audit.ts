import type { ChangeRequest, MemberAudit, MemberAuditGroup } from "./api";

/**
 * The clean-up of a membership audit (docs/proposals/group-membership-tools.md,
 * Part 2).
 *
 * The audit reports values the bind did not find; the operator chooses which
 * to remove; this turns the choice into one `delete` of the chosen values per
 * group and attribute, as the group stores them. Nothing is chosen to begin
 * with, and a group the removal would leave empty while its classes require a
 * member is left out with the reason, rather than staged to be refused.
 */

/** One chosen value, as a key: the group, the attribute and the value. */
export function valueKey(group: string, attribute: string, value: string): string {
  return `${group}\n${attribute}\n${value}`;
}

/** Every value not found, as keys: what "Select all not found" chooses. */
export function allNotFound(audit: MemberAudit): Set<string> {
  const out = new Set<string>();
  for (const g of audit.groups) {
    for (const v of g.notFound) out.add(valueKey(g.dn, v.attribute, v.value));
  }
  return out;
}

/** Groups the audit found anything to say about, in the order it read them. */
export function groupsWithFindings(audit: MemberAudit): MemberAuditGroup[] {
  return audit.groups.filter((g) => g.notFound.length > 0);
}

/** A group that removing every value not found would leave with none. */
export function wouldBeEmpty(g: MemberAuditGroup): boolean {
  return g.notFound.length > 0 && g.remaining === 0;
}

export type CleanupItem = { change: ChangeRequest; label: string };

export type Cleanup = {
  items: CleanupItem[];
  /** Groups left out, and why: the plan would refuse to empty them. */
  leftOut: { group: string; reason: string }[];
  /** Groups the clean-up empties, which their classes allow. */
  emptied: string[];
  /** How many values the items remove. */
  values: number;
};

/**
 * cleanupFor turns the chosen values into records: one `delete` per group and
 * attribute, holding the values exactly as stored.
 */
export function cleanupFor(audit: MemberAudit, chosen: Set<string>): Cleanup {
  const out: Cleanup = { items: [], leftOut: [], emptied: [], values: 0 };
  for (const g of audit.groups) {
    const byAttribute = new Map<string, string[]>();
    for (const v of g.notFound) {
      if (!chosen.has(valueKey(g.dn, v.attribute, v.value))) continue;
      const list = byAttribute.get(v.attribute) ?? [];
      list.push(v.value);
      byAttribute.set(v.attribute, list);
    }
    const removing = [...byAttribute.values()].reduce((n, list) => n + list.length, 0);
    if (removing === 0) continue;

    // Every value the group holds, counted over all its membership
    // attributes: the ones the audit found, plus the ones it did not.
    const holds = g.remaining + g.notFound.length;
    if (removing === holds) {
      if (g.classesRequireMember) {
        out.leftOut.push({
          group: g.dn,
          reason:
            "Its classes require a member, and this would remove its last one, so the plan would refuse it. " +
            "Delete the group instead, if nobody belongs in it.",
        });
        continue;
      }
      out.emptied.push(g.dn);
    }

    for (const [attribute, values] of byAttribute) {
      out.items.push({
        change: {
          dn: g.dn,
          type: "modify",
          mods: [{ op: "delete", name: attribute, values: values.map((text) => ({ text })) }],
        },
        label:
          values.length === 1
            ? `Remove a ${attribute} value not found from ${g.dn}`
            : `Remove ${values.length} ${attribute} values not found from ${g.dn}`,
      });
      out.values += values.length;
    }
  }
  return out;
}

/** What reading a chosen value again said. */
export type Recheck = "not-found" | "found" | "unknown";

/**
 * withoutFound drops from the choice every value a second read found, or could
 * not answer for: a member re-created since the report is not removed, and a
 * read that failed for any other reason keeps the value too.
 */
export function withoutFound(
  chosen: Set<string>,
  answers: Map<string, Recheck>,
): { kept: Set<string>; found: string[]; unknown: string[] } {
  const kept = new Set<string>();
  const found: string[] = [];
  const unknown: string[] = [];
  for (const key of chosen) {
    const answer = answers.get(key) ?? "unknown";
    const value = key.split("\n")[2] ?? key;
    if (answer === "not-found") kept.add(key);
    else if (answer === "found") found.push(value);
    else unknown.push(value);
  }
  return { kept, found, unknown };
}

/**
 * memberDn is the DN a membership value names: uniqueMember's optional
 * `#'…'B` suffix is not part of it.
 */
export function memberDn(value: string): string {
  const hash = value.lastIndexOf("#");
  return hash > 0 && !value.slice(hash).includes(",") ? value.slice(0, hash) : value;
}
