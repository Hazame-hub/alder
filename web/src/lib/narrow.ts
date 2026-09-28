import type { components } from "@/lib/api.gen";
import { looksLocked } from "@/lib/locked";

type SearchResultEntry = components["schemas"]["SearchResultEntry"];

/**
 * Narrowing a table of entries to the ones a typed word appears in.
 *
 * This filters rows already fetched. It does not re-run the search, and it
 * cannot reach an entry the search stopped short of -- the caller says so on
 * screen, because a filter that silently searches less than the directory
 * holds is how somebody concludes an account does not exist.
 *
 * Matched against: the DN, every value on the row, and the word "locked" for
 * an entry whose lock attributes say so. The last is there because the values
 * behind that badge are "true" and a generalised timestamp, and "which of
 * these accounts cannot log in" is the question the list is opened for.
 */
export function narrowRows(entries: SearchResultEntry[], needle: string): SearchResultEntry[] {
  const q = needle.trim().toLowerCase();
  if (q === "") return entries;
  return entries.filter((e) => {
    if (e.dn.toLowerCase().includes(q)) return true;
    if ("locked".includes(q) && looksLocked(e.attributes ?? [])) return true;
    return (e.attributes ?? []).some((a) =>
      (a.values ?? []).some((v) => (v.text ?? "").toLowerCase().includes(q)),
    );
  });
}
