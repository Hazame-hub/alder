import type { Schemas } from "./api";
import { isAttributeName } from "./filter-builder";
import type { AppSearch, SearchScope } from "./route";

/**
 * Saved searches (docs/PROJECT.md, "Saved searches").
 *
 * A saved search is a `*.search.yaml` file in a project, read and checked by
 * the server with the code `alder project validate` runs, and returned when
 * the project is opened. Here it only becomes a link: opening one is
 * navigating to the search page with its values, so a saved search opened in
 * the browser is exactly a search somebody could have typed. Nothing is kept,
 * in memory past the page or in browser storage: the file is the source of
 * truth, and the URL is the per-browser form.
 *
 * Saving goes the other way as a file, rendered here from the search's URL
 * parameters as they stand. It is not parsed back: `alder project validate`
 * is the check of what was written.
 */

export type SavedSearch = Schemas["SavedSearch"];

/** The search page as the URL holds it, with attributes and columns parsed. */
export type CurrentSearch = {
  base: string;
  scope: SearchScope;
  filter: string;
  limit: number;
  attrs: string[];
  cols: string[];
};

/** Where an opened search came from, for the heading over it. */
export type SavedSearchOrigin = { folder: string; search: SavedSearch };

/** How many attributes a list in the URL keeps, as a search file does. */
export const maxListedAttributes = 100;

/**
 * An attribute list from the URL: comma-separated names. Anything that is
 * not an attribute name is dropped, never sent, as an unrecognised parameter
 * is dropped.
 */
export function parseAttributeList(raw: string | undefined): string[] {
  if (!raw) return [];
  const out: string[] = [];
  for (const part of raw.split(",")) {
    const name = part.trim();
    if (!isAttributeName(name) || out.some((n) => n.toLowerCase() === name.toLowerCase())) continue;
    out.push(name);
    if (out.length === maxListedAttributes) break;
  }
  return out;
}

/** The location a saved search opens at. */
export function searchLocation(s: SavedSearch): Partial<AppSearch> {
  return {
    view: "search",
    base: s.base,
    scope: s.scope,
    filter: s.filter,
    limit: s.limit,
    attrs: s.attributes.length ? s.attributes.join(",") : undefined,
    cols: s.columns.length ? s.columns.join(",") : undefined,
  };
}

function sameList(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((v, i) => v.toLowerCase() === b[i]!.toLowerCase());
}

/**
 * Whether the page still shows the saved search it was opened from. Edited,
 * it is an ordinary search, and the heading naming the file comes down.
 */
export function showsOrigin(origin: SavedSearchOrigin | null, current: CurrentSearch): boolean {
  if (!origin) return false;
  const s = origin.search;
  return (
    s.base === current.base &&
    s.scope === current.scope &&
    s.filter === current.filter &&
    s.limit === current.limit &&
    sameList(s.attributes, current.attrs) &&
    sameList(s.columns, current.cols)
  );
}

/**
 * A YAML scalar for any string: JSON's double-quoted form is YAML's too, so
 * a filter full of colons, quotes and brackets is written as itself.
 */
function scalar(s: string): string {
  return JSON.stringify(s);
}

function list(names: string[]): string {
  return `[${names.map(scalar).join(", ")}]`;
}

/**
 * The search as a `.search.yaml` file: what the page is running, from its
 * URL parameters, as typed. Keys the search does not set are left out, so
 * the file's defaults are the format's.
 */
export function savedSearchFile(current: CurrentSearch, title?: string): string {
  const lines = ["# An Alder saved search. Commit it to a project, and check it with", "# alder project validate.", "version: 1"];
  if (title) lines.push(`title: ${scalar(title)}`);
  lines.push(`base: ${scalar(current.base)}`);
  lines.push(`scope: ${current.scope}`);
  lines.push(`filter: ${scalar(current.filter.trim())}`);
  if (current.attrs.length) lines.push(`attributes: ${list(current.attrs)}`);
  if (current.cols.length) lines.push(`columns: ${list(current.cols)}`);
  lines.push(`limit: ${current.limit}`);
  return lines.join("\n") + "\n";
}

/** The file name offered for download: the saved search's own, or a placeholder to rename. */
export function savedSearchFileName(origin: SavedSearchOrigin | null): string {
  const last = origin?.search.name.split("/").pop();
  return `${last || "saved"}.search.yaml`;
}
