import type {
  AttributeKind,
  ChangeMod,
  ChangeRequest,
  Requirements,
  SearchResultEntry,
  SessionInfo,
} from "./api";
import { changeset } from "./changeset";
import { stageChanges, type StageOutcome } from "./stage-deletes";
import { inConfigTree } from "./config-tree";
import { splitDN } from "./values";

/**
 * One modification over the entries selected in a table
 * (docs/proposals/bulk-attribute-edit.md, accepted 2026-10-22).
 *
 * The browser composes one ChangeRequest per selected DN, each a `modify`
 * with the same single modification, and stages them through stageChanges.
 * Nothing here decides what reaches the directory: the changeset plans every
 * record again, and the planner says which are `unchanged`, which entry is
 * gone (`conflict`, `entry_missing`) and which entry's classes do not permit
 * the attribute (`invalid`). What is decided here is only what the dialog may
 * offer, and the two exclusions the plan does not make: an entry named by the
 * attribute, and a replacement of a membership list.
 *
 * It stays on the allowed side of "no bulk provisioning" by construction: the
 * DNs are ones the directory returned, every record is a `modify`, the value
 * is the operator's and the same for every entry, and the set is fixed when
 * it is staged.
 */

export type BulkOperation = "add" | "remove-values" | "remove-attribute" | "replace";

export const bulkOperations: { op: BulkOperation; label: string; meaning: string }[] = [
  {
    op: "add",
    label: "Add a value",
    meaning: "Each entry gains this value. One that already holds it is left unchanged.",
  },
  {
    op: "remove-values",
    label: "Remove a value",
    meaning: "This value is removed from each entry. One that does not hold it is left unchanged.",
  },
  {
    op: "remove-attribute",
    label: "Remove the attribute",
    meaning: "Every value of the attribute is removed from each entry. One without it is left unchanged.",
  },
  {
    op: "replace",
    label: "Replace with",
    meaning: "Each entry holds exactly these values afterwards. One that already does is left unchanged.",
  },
];

/**
 * Membership attributes this dialog offers, add and remove only, with the
 * value from the entry picker. A replacement of a member list removes whoever
 * another administrator added since the table was read (decisions 2026-09-03
 * and 2026-09-05).
 */
const pickedMembership = new Set(["member", "uniquemember"]);

/**
 * Membership attributes this dialog does not offer at all: a `memberUid` is a
 * login name and a `memberURL` a search, so neither can come from the picker.
 */
const unpickableMembership = new Set(["memberuid", "memberurl"]);

/** Kinds whose value is a file or a secret, never a typed value. */
const unofferedKinds = new Set<AttributeKind["kind"]>(["password", "binary", "certificate", "image"]);

function baseName(name: string): string {
  return (name.split(";")[0] ?? name).toLowerCase();
}

export function isPickedMembership(name: string): boolean {
  return pickedMembership.has(baseName(name));
}

/**
 * Whether the dialog offers an attribute at all.
 *
 * Never a secret (forty passwords set at once is the password reset the
 * charter excludes), never one the server owns or Alder writes elsewhere,
 * never objectClass, never an attribute the schema does not define.
 */
export function offersAttribute(kind: AttributeKind): boolean {
  const name = baseName(kind.name);
  if (name === "objectclass" || unpickableMembership.has(name)) return false;
  if (!kind.known || kind.sensitive || kind.operational || kind.readOnly || kind.obsolete) return false;
  if (kind.elsewhere) return false;
  return !unofferedKinds.has(kind.kind);
}

/** The operations offered on an attribute. */
export function operationsFor(kind: AttributeKind): BulkOperation[] {
  if (isPickedMembership(kind.name)) return ["add", "remove-values"];
  return bulkOperations.map((o) => o.op);
}

/** Whether an operation takes values. */
export function takesValues(op: BulkOperation): boolean {
  return op !== "remove-attribute";
}

/**
 * Whether an operation takes exactly one value.
 *
 * An add or a removal carries one, because an add of two values of which an
 * entry holds one is refused by the directory whole (attributeOrValueExists),
 * and in a changeset that stops the run at that entry; the planner reports it
 * and does not rewrite it (decision 2026-10-22, plan equality). One value per
 * record is always either held or not, so the plan's `unchanged` is exact.
 * A replacement takes the whole list, which is what it means.
 */
export function takesOneValue(op: BulkOperation): boolean {
  return op === "add" || op === "remove-values";
}

/**
 * The attribute types the leftmost RDN is built from, lower-cased.
 *
 * Splits on unescaped `+` for a multi-valued RDN. The server parses the DN;
 * this only recognises the type in front of each `=`, which is plain ASCII.
 */
export function namingAttributes(dn: string): string[] {
  const rdn = splitDN(dn)[0] ?? "";
  const out: string[] = [];
  let current = "";
  let escaped = false;
  for (const ch of rdn) {
    if (escaped) {
      current += ch;
      escaped = false;
    } else if (ch === "\\") {
      current += ch;
      escaped = true;
    } else if (ch === "+") {
      out.push(current);
      current = "";
    } else {
      current += ch;
    }
  }
  out.push(current);
  return out
    .map((ava) => ava.split("=")[0]?.trim().toLowerCase() ?? "")
    .filter((t) => t !== "");
}

/**
 * Why the selection cannot be edited this way, or null.
 *
 * The configuration tree and the schema entry have their own, narrower write
 * rules (charter, section 2), so a selection holding an entry there is not
 * offered the action at all.
 */
export function selectionRefusal(dns: string[], session: SessionInfo | undefined): string | null {
  if (dns.length === 0) return "Nothing is selected.";
  const schema = session?.capabilities?.subschemaSubentry?.trim().toLowerCase();
  const inSchema = (dn: string) => {
    if (!schema) return false;
    const here = dn.trim().toLowerCase();
    return here === schema || here.endsWith(`,${schema}`);
  };
  const blocked = dns.filter((dn) => inConfigTree(session, dn) || inSchema(dn));
  if (blocked.length > 0) {
    return (
      `${blocked.length === 1 ? "One selected entry is" : `${blocked.length} selected entries are`} ` +
      "in the server's configuration or schema, which are not edited this way. Clear " +
      (blocked.length === 1 ? "it" : "them") +
      " from the selection first."
    );
  }
  return null;
}

/** The object classes a row carries, lower-cased and sorted, or null when it carries none. */
export function classesOf(entry: SearchResultEntry): string[] | null {
  const attr = entry.attributes?.find((a) => baseName(a.name) === "objectclass");
  if (!attr || attr.withheld || attr.values.length === 0) return null;
  const names = attr.values
    .map((v) => (v.text ?? "").trim())
    .filter((v) => v !== "")
    .map((v) => v.toLowerCase());
  return [...new Set(names)].sort();
}

/** One key per distinct set of classes, so each set is asked about once. */
export function classSetKey(classes: string[]): string {
  return classes.join(" ");
}

/** What one class set permits, from GET /schema/requirements. */
export type ClassSetPermits = {
  key: string;
  requirements: Requirements;
  kinds: AttributeKind[];
};

export type OfferedAttribute = {
  kind: AttributeKind;
  /** How many of the selected entries' classes permit it. */
  permittedBy: number;
};

/**
 * The attributes to offer for a selection.
 *
 * By default, those every selected entry's classes permit: a list where most
 * choices plan `invalid` for most rows teaches people to ignore `invalid`.
 * Widened, those any of them permits, with the count, so the dialog can say
 * how many will plan `invalid`. A row that carries no object class permits
 * nothing known, so with one in the selection the default list is empty.
 */
export function offeredAttributes(
  entries: SearchResultEntry[],
  permits: ClassSetPermits[],
  widen: boolean,
): OfferedAttribute[] {
  const byKey = new Map(permits.map((p) => [p.key, p]));
  const counts = new Map<string, { kind: AttributeKind; n: number }>();
  for (const entry of entries) {
    const classes = classesOf(entry);
    const set = classes ? byKey.get(classSetKey(classes)) : undefined;
    if (!set) continue;
    const allowed = new Set([...(set.requirements.must ?? []), ...(set.requirements.may ?? [])].map(baseName));
    for (const kind of set.kinds) {
      const name = baseName(kind.name);
      if (!allowed.has(name) || !offersAttribute(kind)) continue;
      const seen = counts.get(name);
      if (seen) seen.n += 1;
      else counts.set(name, { kind, n: 1 });
      allowed.delete(name); // a kind listed twice counts once
    }
  }
  return [...counts.values()]
    .filter((c) => widen || c.n === entries.length)
    .map((c) => ({ kind: c.kind, permittedBy: c.n }))
    .sort((a, b) => a.kind.name.localeCompare(b.kind.name));
}

export type BulkEdit = {
  attribute: string;
  op: BulkOperation;
  /** The operator's values, the same for every entry. Ignored for remove-attribute. */
  values: string[];
};

export type Composition =
  | {
      ok: true;
      items: { change: ChangeRequest; label: string }[];
      /** Selected entries named by the attribute, left out of a removal or a replacement. */
      namedBy: string[];
      /** Set on a replacement of a multi-valued attribute. */
      warning?: string;
    }
  | { ok: false; message: string };

function verb(edit: BulkEdit, dn: string): string {
  switch (edit.op) {
    case "add":
      return `Add ${edit.attribute} to ${dn}`;
    case "remove-values":
      return `Remove ${edit.attribute} values from ${dn}`;
    case "remove-attribute":
      return `Remove ${edit.attribute} from ${dn}`;
    case "replace":
      return `Replace ${edit.attribute} on ${dn}`;
  }
}

function modFor(edit: BulkEdit): ChangeMod {
  const values = edit.values.map((text) => ({ text }));
  switch (edit.op) {
    case "add":
      return { op: "add", name: edit.attribute, values };
    case "remove-values":
      return { op: "delete", name: edit.attribute, values };
    case "remove-attribute":
      return { op: "delete", name: edit.attribute };
    case "replace":
      return { op: "replace", name: edit.attribute, values };
  }
}

/**
 * One record per selected DN, each a `modify` with the one modification.
 *
 * Refused whole, not trimmed, when the edit is not one the dialog offers:
 * a replacement of a membership list, more than one member at once, a value
 * missing, or a value given twice.
 */
export function composeBulkEdit(dns: string[], kind: AttributeKind, edit: BulkEdit): Composition {
  if (dns.length === 0) return { ok: false, message: "Nothing is selected." };
  if (!offersAttribute(kind)) {
    return { ok: false, message: `${kind.name} is not edited over several entries.` };
  }
  if (!operationsFor(kind).includes(edit.op)) {
    return {
      ok: false,
      message:
        `A membership list is never replaced: that removes whoever was added since the table was read. ` +
        `Add or remove one member instead.`,
    };
  }

  const values = takesValues(edit.op) ? edit.values : [];
  if (takesValues(edit.op)) {
    if (values.length === 0 || values.some((v) => v === "")) {
      return { ok: false, message: "Give the value to use." };
    }
    if (new Set(values).size !== values.length) {
      return { ok: false, message: "The same value is given twice." };
    }
    if (isPickedMembership(kind.name) && values.length !== 1) {
      return { ok: false, message: "Membership is changed one member at a time, picked from the directory." };
    }
    if (takesOneValue(edit.op) && values.length !== 1) {
      return {
        ok: false,
        message: "An add or a removal carries one value. Stage a second edit for another value.",
      };
    }
    if (kind.singleValue && values.length > 1) {
      return { ok: false, message: `${kind.name} holds one value, and ${values.length} are given.` };
    }
  }

  // The directory refuses to remove a naming value (notAllowedOnRDN), and in
  // a changeset that refusal stops the run at that entry.
  const removes = edit.op === "remove-values" || edit.op === "remove-attribute" || edit.op === "replace";
  const wanted = baseName(edit.attribute);
  const namedBy = removes ? dns.filter((dn) => namingAttributes(dn).includes(wanted)) : [];
  const named = new Set(namedBy);
  const targets = dns.filter((dn) => !named.has(dn));
  if (targets.length === 0) {
    return {
      ok: false,
      message: `Every selected entry is named by ${edit.attribute}, so none of them can lose it this way.`,
    };
  }

  const normalised: BulkEdit = { ...edit, values };
  const items = targets.map((dn) => ({
    change: { dn, type: "modify" as const, mods: [modFor(normalised)] },
    label: verb(normalised, dn),
  }));
  const warning =
    edit.op === "replace" && !kind.singleValue
      ? `Every other value of ${edit.attribute} is removed from each entry.`
      : undefined;
  return { ok: true, items, namedBy, ...(warning ? { warning } : {}) };
}

/** What a changeset staged from one bulk edit says above itself. */
export type BulkEditOrigin = {
  kind: "bulk-edit";
  attribute: string;
  op: BulkOperation;
  values: string[];
  entries: number;
};

/** The heading's sentence: "One modification over 40 entries: add departmentNumber: 4200". */
export function bulkEditSummary(origin: BulkEditOrigin): string {
  const count = `${origin.entries} ${origin.entries === 1 ? "entry" : "entries"}`;
  const valueText = origin.values.join(", ");
  const what = (() => {
    switch (origin.op) {
      case "add":
        return `add ${origin.attribute}: ${valueText}`;
      case "remove-values":
        return `remove ${origin.attribute}: ${valueText}`;
      case "remove-attribute":
        return `remove every ${origin.attribute}`;
      case "replace":
        return `replace ${origin.attribute} with ${valueText}`;
    }
  })();
  return `One modification over ${count}: ${what}`;
}

/**
 * Stage a composition. The heading is set only when the changeset held
 * nothing before, since a basket that is "this edit plus three other things"
 * is not one modification over these entries; any later change to the basket
 * drops it, as it drops a project's.
 */
export function stageBulkEdit(edit: BulkEdit, items: { change: ChangeRequest; label: string }[]): StageOutcome {
  const wasEmpty = changeset.all().length === 0;
  const outcome = stageChanges(items, { noun: "change", nothing: "Nothing was selected." });
  if (outcome.ok && wasEmpty) {
    changeset.setOrigin({
      kind: "bulk-edit",
      attribute: edit.attribute,
      op: edit.op,
      values: takesValues(edit.op) ? edit.values : [],
      entries: items.length,
    });
  }
  return outcome;
}
