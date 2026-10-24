import type { ChangeRequest, MemberList } from "./api";
import { splitDN } from "./values";

/**
 * Many members at once, as one record per group
 * (docs/proposals/group-membership-tools.md, Part 1, accepted 2026-10-22).
 *
 * Adding twenty-five people to a group is one `modify` of the group carrying
 * the twenty-five values it does not already hold, and removing twelve is one
 * `delete` of the twelve it does. Never a replacement of the list: that
 * silently removes whoever another administrator added since it was read
 * (decisions 2026-09-03 and 2026-09-05).
 *
 * Values already present are left out of an add, and values absent out of a
 * removal, because an LDAP modification is atomic per entry and both servers
 * refuse an add that names a value the entry holds -- the whole record, not
 * the one value. The comparison here is the browser's, to compose the
 * record; the plan compares again under distinguishedNameMatch, and the
 * directory is the authority at apply.
 */

/** The most values one record carries. The limit is the review, not the servers. */
export const maxMembersPerRecord = 1000;

/** Membership attributes whose values are DNs, and so can be picked. */
const dnMembership = new Set(["member", "uniquemember"]);

function baseName(name: string): string {
  return (name.split(";")[0] ?? name).toLowerCase();
}

/** Whether a membership attribute holds DNs, which is what these tools write. */
export function isDnMembership(attribute: string): boolean {
  return dnMembership.has(baseName(attribute));
}

/** The DN-valued membership attributes among the ones an entry's classes permit. */
export function dnMembershipAttributes(attributes: string[] | undefined): string[] {
  return (attributes ?? []).filter(isDnMembership);
}

function splitUnescaped(text: string, sep: string): string[] {
  const out: string[] = [];
  let current = "";
  let escaped = false;
  for (const ch of text) {
    if (escaped) {
      current += ch;
      escaped = false;
    } else if (ch === "\\") {
      current += ch;
      escaped = true;
    } else if (ch === sep) {
      out.push(current);
      current = "";
    } else {
      current += ch;
    }
  }
  out.push(current);
  return out;
}

/**
 * A key under which two spellings of one name compare equal: attribute types
 * and values folded to lower case, insignificant spaces dropped, and the parts
 * of a multi-valued RDN in a fixed order.
 *
 * For composing a record only. It errs towards "different" -- an escaped
 * character spelled two ways is two keys -- and a pair it misses is caught by
 * the plan, which compares under distinguishedNameMatch. `uniqueMember`'s
 * optional `#'…'B` suffix is part of the value and kept.
 */
export function memberKey(dn: string): string {
  return splitDN(dn.trim())
    .map((rdn) =>
      splitUnescaped(rdn, "+")
        .map((ava) => {
          const eq = ava.indexOf("=");
          if (eq < 0) return ava.trim().toLowerCase();
          return `${ava.slice(0, eq).trim().toLowerCase()}=${ava.slice(eq + 1).trim().toLowerCase()}`;
        })
        .sort()
        .join("+"),
    )
    .join(",");
}

export type MembersOperation = "add" | "remove";

export type MembersComposition =
  | {
      ok: true;
      change: ChangeRequest;
      label: string;
      /** The values the record carries. */
      values: string[];
      /** Asked for, and left out: already members (add) or not members (remove). */
      leftOut: string[];
    }
  | { ok: false; message: string; leftOut: string[] };

/**
 * One record for one group.
 *
 * `held` is the group's values in `attribute` as read when the dialog opened;
 * `asked` the DNs picked from the directory, or chosen from `held`. A removal
 * sends each value as the group stores it, so the delete names exactly what
 * is there.
 */
export function composeMembers(
  group: string,
  attribute: string,
  held: string[],
  asked: string[],
  op: MembersOperation,
): MembersComposition {
  if (!isDnMembership(attribute)) {
    return { ok: false, leftOut: [], message: `${attribute} does not hold distinguished names.` };
  }
  const stored = new Map<string, string>();
  for (const value of held) stored.set(memberKey(value), value);

  const seen = new Set<string>();
  const values: string[] = [];
  const leftOut: string[] = [];
  for (const dn of asked) {
    const key = memberKey(dn);
    if (key === "" || seen.has(key)) continue;
    seen.add(key);
    if (op === "add") {
      if (stored.has(key)) leftOut.push(dn);
      else values.push(dn);
    } else {
      const value = stored.get(key);
      if (value === undefined) leftOut.push(dn);
      else values.push(value);
    }
  }

  if (values.length === 0) {
    return {
      ok: false,
      leftOut,
      message:
        asked.length === 0
          ? "Nothing is chosen."
          : op === "add"
            ? `Nothing to add: ${leftOut.length === 1 ? "it is" : `all ${leftOut.length} are`} already in ${attribute}.`
            : `Nothing to remove: ${leftOut.length === 1 ? "it is" : `none of the ${leftOut.length} is`} in ${attribute}.`,
    };
  }
  if (values.length > maxMembersPerRecord) {
    return {
      ok: false,
      leftOut,
      message:
        `That is ${values.length} members, and one record carries at most ${maxMembersPerRecord}, ` +
        "so that its review can be read. Choose fewer.",
    };
  }

  const n = values.length;
  const noun = n === 1 ? "member" : "members";
  return {
    ok: true,
    values,
    leftOut,
    change: {
      dn: group,
      type: "modify",
      mods: [{ op: op === "add" ? "add" : "delete", name: attribute, values: values.map((text) => ({ text })) }],
    },
    label: op === "add" ? `Add ${n} ${noun} to ${group}` : `Remove ${n} ${noun} from ${group}`,
  };
}

/**
 * Whether a change is one of these records: a single add or delete of more
 * than one DN in a membership attribute.
 *
 * Such a record is planned against the whole attribute it touches, so a
 * member added or removed by somebody else between the plan and the apply
 * makes it `plan_stale`. The refusal says so, and points at the single-value
 * actions, which still apply on a group that busy.
 */
export function isManyMembersRecord(change: ChangeRequest | null | undefined): boolean {
  if (!change || change.type !== "modify" || change.mods?.length !== 1) return false;
  const mod = change.mods[0]!;
  return (mod.op === "add" || mod.op === "delete") && isDnMembership(mod.name) && (mod.values?.length ?? 0) > 1;
}

export type NestingFinding =
  | { kind: "cycle"; member: string; chain: string[] }
  | { kind: "unchecked"; member: string }
  | { kind: "indirect"; member: string; via: string[] };

/**
 * What nesting says about adding `member` to `group`.
 *
 * `inside` is the expansion of the member, when it is a group: if it reaches
 * the group already, adding it makes a cycle, and the chain is named. A
 * truncated expansion cannot say, and is reported as unchecked, which the
 * dialog refuses. `around` is the expansion of the group itself: a member it
 * already reaches through another group is told so, and still allowed, since
 * a direct and an indirect membership are different facts.
 */
export function nestingFindings(
  group: string,
  members: { dn: string; isGroup: boolean }[],
  inside: Map<string, MemberList>,
  around: MemberList | undefined,
): NestingFinding[] {
  const target = memberKey(group);
  const out: NestingFinding[] = [];
  for (const member of members) {
    const key = memberKey(member.dn);
    if (key === target) {
      out.push({ kind: "cycle", member: member.dn, chain: [member.dn] });
      continue;
    }
    if (member.isGroup) {
      const expansion = inside.get(key);
      if (!expansion) {
        out.push({ kind: "unchecked", member: member.dn });
        continue;
      }
      const reached = expansion.members.find((m) => memberKey(m.dn) === target);
      const viaCycle = (expansion.cycles ?? []).some((c) => memberKey(c) === target);
      if (reached || viaCycle) {
        out.push({ kind: "cycle", member: member.dn, chain: [member.dn, ...(reached?.via ?? [])] });
        continue;
      }
      if (expansion.truncated) {
        out.push({ kind: "unchecked", member: member.dn });
        continue;
      }
    }
    const indirect = around?.members.find((m) => !m.direct && memberKey(m.dn) === key);
    if (indirect) out.push({ kind: "indirect", member: member.dn, via: indirect.via ?? [] });
  }
  return out;
}

/** Whether a picked entry is a group whose expansion has to be checked for a cycle. */
export function looksLikeGroup(classes: string[]): boolean {
  const folded = classes.map((c) => c.toLowerCase());
  return folded.some((c) => c === "groupofnames" || c === "groupofuniquenames");
}
