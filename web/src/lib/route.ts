/**
 * Where you are, in the URL.
 *
 * The whole application state that is worth sharing lives in the query string
 * of one route: which destination, which entry, which search. That is a
 * deliberate choice over nested paths — a DN carries commas, equals signs and
 * non-ASCII text, and no two proxies agree about double-encoding one, which is
 * the same reason the API takes a DN as a query parameter rather than a path
 * segment.
 *
 * What it buys is the thing the search page was missing: a search you can send
 * to somebody, and a bug report that reproduces. Nothing is stored; the URL is
 * the only place this lives, which keeps it consistent with a stateless v1.
 */

import { parseAttributeList } from "./saved-search";

export type AppView =
  | "overview"
  | "browse"
  | "users"
  | "groups"
  | "organizationalUnits"
  | "search"
  | "schema"
  | "changeset"
  | "import"
  | "snapshots"
  | "packages"
  | "preflight";

const views: AppView[] = [
  "overview",
  "browse",
  "users",
  "groups",
  "organizationalUnits",
  "search",
  "schema",
  "changeset",
  "import",
  "snapshots",
  "packages",
  "preflight",
];

export type SearchScope = "base" | "one" | "sub";

export type AppSearch = {
  view: AppView;
  /** The entry the browse view has open. */
  dn?: string;
  /** Open that entry straight into the editor. Not persisted past a reload. */
  edit?: boolean;
  /** Which kind the Snapshots view opens on, so "compare the configuration" is a link. */
  capture?: "data" | "schema" | "config";
  /** What the schema browser opens looking for, so a refusal can point at it. */
  find?: string;
  /** The search page's base, scope and filter, so a search is a link. */
  base?: string;
  scope?: SearchScope;
  filter?: string;
  limit?: number;
  /**
   * The search page's attributes and columns, comma-separated, so a saved
   * search (or any search) that asks for pwdAccountLockedTime shows it.
   */
  attrs?: string;
  cols?: string;
};

/**
 * validateAppSearch turns whatever is in the query string into AppSearch.
 *
 * Everything is optional and anything unrecognised is dropped rather than
 * refused: a URL somebody hand-edited, or one from an older build, should land
 * on a working page rather than an error. The view falls back to the overview,
 * which is the one destination that needs nothing else to make sense.
 */
export function validateAppSearch(raw: Record<string, unknown>): AppSearch {
  const out: AppSearch = { view: asView(raw.view) };

  const dn = asText(raw.dn);
  if (dn) out.dn = dn;
  if (raw.edit === true || raw.edit === "true") out.edit = true;

  if (raw.capture === "data" || raw.capture === "schema" || raw.capture === "config") {
    out.capture = raw.capture;
  }
  const find = asText(raw.find);
  if (find) out.find = find;

  const base = asText(raw.base);
  if (base) out.base = base;
  if (raw.scope === "base" || raw.scope === "one" || raw.scope === "sub") {
    out.scope = raw.scope;
  }
  const filter = asText(raw.filter);
  if (filter) out.filter = filter;

  const limit = Number(raw.limit);
  if (Number.isFinite(limit) && limit > 0) {
    out.limit = Math.min(Math.trunc(limit), 10000);
  }
  const attrs = asAttributeList(raw.attrs);
  if (attrs) out.attrs = attrs;
  const cols = asAttributeList(raw.cols);
  if (cols) out.cols = cols;
  return out;
}

function asView(v: unknown): AppView {
  return typeof v === "string" && (views as string[]).includes(v)
    ? (v as AppView)
    : "overview";
}

/** Names that are not attribute names are dropped, as anything unrecognised is. */
function asAttributeList(v: unknown): string | undefined {
  const names = parseAttributeList(Array.isArray(v) ? v.filter((x) => typeof x === "string").join(",") : asText(v));
  return names.length ? names.join(",") : undefined;
}

function asText(v: unknown): string | undefined {
  return typeof v === "string" && v !== "" ? v : undefined;
}

/** The Directory section's destinations, which share a second-row nav. */
export const directoryViews: AppView[] = [
  "browse",
  "users",
  "groups",
  "organizationalUnits",
];

export function isDirectoryView(v: AppView): boolean {
  return directoryViews.includes(v);
}
