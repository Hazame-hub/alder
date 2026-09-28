/**
 * Whether an entry Alder has already read looks locked.
 *
 * Read from the attributes in hand rather than by asking the server again: the
 * one fact worth putting in front of somebody before they open anything is
 * that this account cannot log in, and it should not cost a request.
 *
 * It lives in lib rather than beside the Policy dialog because the entry
 * header is no longer the only thing that asks. A table of accounts wants the
 * same answer for two hundred rows at once, and a component must not import a
 * feature to get it.
 *
 * Both spellings, because a view does not know which server it is looking at:
 * OpenLDAP's ppolicy overlay writes pwdAccountLockedTime, 389 DS writes
 * nsAccountLock. Presence is the signal for the first -- the value is the
 * timestamp of the lock -- and the string "true" for the second.
 */
export function looksLocked(attributes: { name: string; values: { text?: string }[] }[]): boolean {
  for (const attr of attributes) {
    const name = attr.name.toLowerCase();
    if (name === "pwdaccountlockedtime" && attr.values.length > 0) return true;
    if (name === "nsaccountlock" && attr.values.some((v) => (v.text ?? "").toLowerCase() === "true")) {
      return true;
    }
  }
  return false;
}
