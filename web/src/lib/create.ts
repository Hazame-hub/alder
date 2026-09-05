import type { ChangeRequest } from "./api";
import { textValue } from "./values";

/**
 * Composing the add record a creation form produces.
 *
 * Pure, and separate from the form, for the same reason the modification diff
 * is: this is the part that decides what reaches the directory. A form that
 * looks right and composes the wrong record is the failure that matters, and it
 * is not one you can see by looking at the form.
 */

export type AddChangeInput = {
  parentDN: string;
  /** The single structural class. Nothing is composed without one. */
  structural: string;
  auxiliary: string[];
  /** The attribute the entry is named by, and its value. */
  rdnAttr: string;
  rdnValue: string;
  /** Every attribute the form offered a field for, required or added. */
  attributes: string[];
  values: Record<string, string[]>;
};

/**
 * buildAddChange returns the record, or null when the form cannot yet make one.
 *
 * Only `top` and the classes chosen are written. A directory infers the rest of
 * the superior chain itself, and listing them would be writing down something
 * the server already knows — which then goes stale if the schema changes.
 */
export function buildAddChange(input: AddChangeInput): ChangeRequest | null {
  const { parentDN, structural, auxiliary, rdnAttr, rdnValue } = input;
  if (structural === "" || rdnAttr === "" || rdnValue.trim() === "") return null;

  const attributes: { name: string; values: ReturnType<typeof textValue>[] }[] = [
    { name: "objectClass", values: ["top", structural, ...auxiliary].map(textValue) },
  ];

  const seen = new Set<string>();
  for (const name of input.attributes) {
    const key = name.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    const set = (input.values[name] ?? []).filter((v) => v.trim() !== "");
    if (set.length === 0) continue;
    attributes.push({ name, values: set.map(textValue) });
  }

  // The naming attribute has to carry the naming value, or the directory
  // refuses the entry. Filling it in beats making somebody type it twice — and
  // it has to go first, because that is the value the RDN names.
  const existing = attributes.find(
    (a) => a.name.toLowerCase() === rdnAttr.toLowerCase(),
  );
  if (!existing) {
    attributes.push({ name: rdnAttr, values: [textValue(rdnValue)] });
  } else if (!existing.values.some((v) => v.text === rdnValue)) {
    existing.values.unshift(textValue(rdnValue));
  }

  return {
    dn: `${rdnAttr}=${escapeRDNValue(rdnValue)},${parentDN}`,
    type: "add",
    attributes,
  };
}

/**
 * escapeRDNValue applies the RFC 4514 escaping an RDN value needs.
 *
 * The server parses and re-renders the DN, so this is not the only defence. It
 * is here so the DN shown under the name field is the DN that will be created,
 * rather than one that looks wrong until the server corrects it.
 */
export function escapeRDNValue(value: string): string {
  let out = value.replace(/(["+,;<>\\])/g, "\\$1");
  if (out.startsWith("#") || out.startsWith(" ")) out = "\\" + out;
  if (out.endsWith(" ")) out = out.slice(0, -1) + "\\ ";
  return out;
}
