import type { ChangeMod, EntryAttribute } from "./api";
import { textValue } from "./values";

/**
 * Turning an edited entry back into a modification.
 *
 * This is pure, and it lives here rather than beside the editor because it is
 * the part with consequences: which modification a diff produces decides
 * whether adding one person to a group removes forty-nine others. It is the
 * most-tested code in the front end for that reason.
 */

/** The editable text values of an entry, by attribute name. */
export type Draft = Record<string, string[]>;

/**
 * snapshot reduces an entry's attributes to the editable text values.
 *
 * Operational, read-only, withheld and binary-valued attributes are excluded
 * deliberately. Excluding binary ones matters: their values arrive as base64
 * and would map to empty strings here, which computeMods would then read as
 * "the user cleared this attribute" and turn into a delete.
 */
export function snapshot(attributes: EntryAttribute[]): Draft {
  const out: Draft = {};
  for (const a of attributes) {
    if (a.kind.operational || a.kind.readOnly || a.withheld) continue;
    if (a.values.some((v) => v.base64 !== undefined)) continue;
    out[a.name] = a.values.map((v) => v.text ?? "");
  }
  return out;
}

/**
 * computeMods diffs the draft against the original values.
 *
 * An attribute that gained values only becomes an `add` of those values, and
 * one that lost values only becomes a `delete` of those. Anything else -- a
 * mixed edit, a reordering, a single-valued attribute -- becomes a `replace`
 * carrying the whole new set, and an emptied attribute becomes a valueless
 * `delete`.
 *
 * The narrow operations are not a cosmetic choice. Replacing is destructive
 * under concurrency: adding one person to a fifty-person group by replacing the
 * whole member list silently removes anyone another administrator added since
 * the entry was read, and group membership is the most concurrently edited
 * attribute a directory has. An `add` of the one value both administrators
 * intended succeeds for both.
 *
 * It also makes the LDIF say what was meant. "add: member" with one line is the
 * change; fifty-one lines of "replace: member" is the same change buried in its
 * own context.
 */
export function computeMods(original: Draft, draft: Draft, added: string[]): ChangeMod[] {
  const mods: ChangeMod[] = [];
  const names = new Set([...Object.keys(original), ...added]);

  for (const name of names) {
    const before = (original[name] ?? []).filter((v) => v !== "");
    const after = (draft[name] ?? []).filter((v) => v !== "");

    if (sameValues(before, after)) continue;

    if (after.length === 0) {
      mods.push({ op: "delete", name });
      continue;
    }

    const gained = after.filter((v) => !before.includes(v));
    const lost = before.filter((v) => !after.includes(v));

    if (gained.length > 0 && lost.length === 0) {
      mods.push({ op: "add", name, values: gained.map(textValue) });
      continue;
    }
    if (lost.length > 0 && gained.length === 0) {
      mods.push({ op: "delete", name, values: lost.map(textValue) });
      continue;
    }

    // A mixed edit, or a reordering with the same members. Replace states the
    // end result, which is the only thing that describes it honestly.
    mods.push({ op: "replace", name, values: after.map(textValue) });
  }
  return mods;
}

function sameValues(a: string[], b: string[]) {
  const left = a.filter((v) => v !== "");
  const right = b.filter((v) => v !== "");
  if (left.length !== right.length) return false;
  return left.every((v, i) => v === right[i]);
}
