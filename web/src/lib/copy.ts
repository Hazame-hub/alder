import type { AttributeValue, EntryAttribute } from "./api";
import { textValue } from "./values";

/**
 * Which of an entry's attributes a copy carries.
 *
 * Copying is a write like any other, so the same three exclusions the editor
 * uses apply, plus one of its own:
 *
 * - **NO-USER-MODIFICATION**, because the server owns the value and would
 *   refuse it.
 * - **Withheld**, because the values were never sent to the browser. Copying
 *   an account with no password is worse than refusing to copy it silently,
 *   which is why the dialog says so on screen.
 * - **What Alder does not write** -- an access rule, a schema definition. An
 *   access rule copied onto a new entry is an access rule Alder wrote, which
 *   is the thing the decisions log says it does not do.
 * - **Operational**, which the editor *does* now offer and a copy still does
 *   not. A lock, a failure count and a password-expiry time are facts about
 *   the account that was; carrying them across would create the new account
 *   already locked, counting failures that never happened.
 *
 * The naming attribute is always present carrying the new value, even when
 * the original was named by a different attribute.
 */
export function copiedAttributes(
  attributes: EntryAttribute[],
  rdnAttr: string,
  rdnValue: string,
): { name: string; values: AttributeValue[] }[] {
  const folded = rdnAttr.toLowerCase();
  const out = attributes
    .filter(
      (a) => !a.kind.operational && !a.kind.readOnly && !a.withheld && !a.kind.elsewhere,
    )
    .map((a) =>
      a.name.toLowerCase() === folded
        ? { name: a.name, values: [textValue(rdnValue)] }
        : { name: a.name, values: a.values },
    );

  if (!out.some((a) => a.name.toLowerCase() === folded)) {
    out.push({ name: rdnAttr, values: [textValue(rdnValue)] });
  }
  return out;
}
