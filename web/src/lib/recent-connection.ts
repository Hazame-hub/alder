import type { SessionInfo } from "./api";

/**
 * The connection details worth remembering between visits.
 *
 * The bind password is deliberately absent, and there is no field for it. A
 * directory admin's password does not belong in localStorage: it survives the
 * tab, it is readable by any script that gets a foothold on the origin, and it
 * would undo the whole point of holding credentials in server memory only. The
 * password is the one thing you retype.
 *
 * Everything here is either public (a host, a port, a CA certificate) or
 * identifying but not secret (a bind DN). Storing the CA is the part that
 * actually saves time: it is a wall of base64 that is tedious to find and paste
 * every time you reconnect to the same directory.
 */
export type RecentConnection = {
  host: string;
  port: number;
  tls: "ldaps" | "starttls" | "plaintext";
  bindDn: string;
  // Identifying but not secret, like bindDn. The configuration password is
  // absent for the same reason the bind password is.
  configBindDn: string;
  serverName: string;
  caCertificate: string;
  insecureSkipVerify: boolean;
};

/**
 * A connection kept under a name: primary, replica, dev, prod.
 *
 * The same fields as the last connection and nothing more, so the same promise
 * holds: no password of either kind, ever. A list of them is the place a
 * password would be most tempting to add -- "save everything so I can switch
 * with one click" -- and the most damaging, a list of every directory's admin
 * credential in one readable place.
 */
export type SavedConnection = {
  name: string;
  connection: RecentConnection;
};

// The last connection keeps the key and the format it has always had. Nothing
// about it changed when the named list arrived, so there is nothing to migrate:
// a connection remembered before the list existed is still read as it was, and
// a browser that goes back to an older build still finds it.
const KEY = "alder.recent-connection.v1";

// The named list is a separate key rather than a v2 of the one above, so that
// neither can corrupt the other and an unreadable list costs the list, not the
// remembered connection.
const SAVED_KEY = "alder.saved-connections.v1";

/**
 * MAX_SAVED bounds the list. It is a picker, not an inventory: past a couple of
 * dozen entries a select menu stops being quicker than typing a host, and each
 * entry may carry a CA certificate of a few kilobytes against a storage quota
 * the whole origin shares. Saving past the limit is refused rather than
 * silently dropping the oldest, because the oldest may be the one you use.
 */
export const MAX_SAVED = 25;

/** MAX_NAME bounds a name to something a select menu can show. */
export const MAX_NAME = 64;

/**
 * fields reads a stored connection back, field by field.
 *
 * Storage is not trusted input. Anything that can run on the origin can write
 * to it, an older or newer build may have written a different shape, and a
 * person may have edited it by hand. So only the known fields are taken, each
 * checked for its type, and anything else -- a password field above all -- is
 * dropped on the way in, not merely ignored later.
 */
function fields(raw: unknown): RecentConnection | null {
  if (typeof raw !== "object" || raw === null) return null;
  const parsed = raw as Partial<Record<keyof RecentConnection, unknown>>;
  if (typeof parsed.host !== "string" || typeof parsed.port !== "number") {
    return null;
  }
  return {
    host: parsed.host,
    port: parsed.port,
    tls: parsed.tls === "starttls" || parsed.tls === "plaintext" ? parsed.tls : "ldaps",
    bindDn: typeof parsed.bindDn === "string" ? parsed.bindDn : "",
    configBindDn: typeof parsed.configBindDn === "string" ? parsed.configBindDn : "",
    serverName: typeof parsed.serverName === "string" ? parsed.serverName : "",
    caCertificate: typeof parsed.caCertificate === "string" ? parsed.caCertificate : "",
    insecureSkipVerify: parsed.insecureSkipVerify === true,
  };
}

/**
 * pick copies exactly the remembered fields, and is what every write goes
 * through.
 *
 * The type has no password field, but a type is a compile-time promise: an
 * object with more properties than its type names is still assignable to it,
 * and JSON.stringify writes every one. Copying field by field makes "never a
 * password" true of the bytes written, not only of the declared shape.
 */
function pick(c: RecentConnection): RecentConnection {
  return {
    host: c.host,
    port: c.port,
    tls: c.tls,
    bindDn: c.bindDn,
    configBindDn: c.configBindDn,
    serverName: c.serverName,
    caCertificate: c.caCertificate,
    insecureSkipVerify: c.insecureSkipVerify,
  };
}

/**
 * load returns the last connection, or null.
 *
 * Every access is guarded: localStorage throws outright in some privacy modes
 * rather than returning null, and a connection screen that cannot render
 * because of a storage preference would be a poor trade for a convenience.
 */
export function load(): RecentConnection | null {
  try {
    const raw = window.localStorage.getItem(KEY);
    if (!raw) return null;
    return fields(JSON.parse(raw));
  } catch {
    // Unreadable, unparsable, or storage denied. Start from a blank form.
    return null;
  }
}

/** save records a connection that succeeded. Failures are not worth reporting. */
export function save(connection: RecentConnection): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(pick(connection)));
  } catch {
    // Quota, private mode, or storage denied. The form still works.
  }
}

/** forget clears the remembered connection. */
export function forget(): void {
  try {
    window.localStorage.removeItem(KEY);
  } catch {
    // As above.
  }
}

/** describes renders a remembered connection for the "reconnect to" hint. */
export function describe(c: RecentConnection): string {
  return c.bindDn ? `${c.host}:${c.port} as ${c.bindDn}` : `${c.host}:${c.port}`;
}

/** matches reports whether a live session is the one that was remembered. */
export function matches(c: RecentConnection, info: SessionInfo): boolean {
  return c.host === info.host && c.port === info.port;
}

/** same reports whether two connections would fill the form identically. */
export function same(a: RecentConnection, b: RecentConnection): boolean {
  const x = pick(a);
  const y = pick(b);
  return (Object.keys(x) as (keyof RecentConnection)[]).every((k) => x[k] === y[k]);
}

/**
 * cleanName is the name as kept: trimmed and bounded. Names are compared
 * without regard to case, because "Prod" and "prod" side by side in a menu are
 * a trap, not two connections.
 */
export function cleanName(name: string): string {
  return name.trim().slice(0, MAX_NAME);
}

function key(name: string): string {
  return cleanName(name).toLowerCase();
}

/**
 * listSaved returns the named connections, in the order they were saved.
 *
 * Guarded like load, and forgiving per entry: one malformed entry costs that
 * entry, not the list. Duplicate names (only possible by editing storage) keep
 * the first, and the cap is applied on the way in too, so a list written by
 * hand cannot make the picker unusable.
 */
export function listSaved(): SavedConnection[] {
  try {
    const raw = window.localStorage.getItem(SAVED_KEY);
    if (!raw) return [];
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const out: SavedConnection[] = [];
    const seen = new Set<string>();
    for (const item of parsed as unknown[]) {
      if (out.length >= MAX_SAVED) break;
      if (typeof item !== "object" || item === null) continue;
      const { name, connection } = item as { name?: unknown; connection?: unknown };
      if (typeof name !== "string") continue;
      const clean = cleanName(name);
      const c = fields(connection);
      if (!clean || !c || seen.has(clean.toLowerCase())) continue;
      seen.add(clean.toLowerCase());
      out.push({ name: clean, connection: c });
    }
    return out;
  } catch {
    return [];
  }
}

/**
 * Why a change to the list did not happen. Unlike the last connection, these
 * are things a person asked for, so a failure is reported rather than
 * swallowed: "saved" followed by an empty list next visit is worse than "could
 * not save".
 */
export type SavedFailure = "empty-name" | "taken" | "missing" | "full" | "storage";

export type SavedResult =
  | { ok: true; list: SavedConnection[] }
  | { ok: false; reason: SavedFailure };

function write(list: SavedConnection[]): SavedResult {
  try {
    const stored = list.map((s) => ({ name: s.name, connection: pick(s.connection) }));
    window.localStorage.setItem(SAVED_KEY, JSON.stringify(stored));
    return { ok: true, list: stored };
  } catch {
    return { ok: false, reason: "storage" };
  }
}

/**
 * saveNamed keeps a connection under a name. Saving under a name already in the
 * list replaces that entry where it stands -- that is how you update one --
 * and only a new name counts against the cap.
 */
export function saveNamed(name: string, connection: RecentConnection): SavedResult {
  const clean = cleanName(name);
  if (!clean) return { ok: false, reason: "empty-name" };
  const list = listSaved();
  const at = list.findIndex((s) => key(s.name) === key(clean));
  const entry = { name: clean, connection: pick(connection) };
  if (at >= 0) return write(list.map((s, i) => (i === at ? entry : s)));
  if (list.length >= MAX_SAVED) return { ok: false, reason: "full" };
  return write([...list, entry]);
}

/**
 * renameSaved renames an entry in place. Renaming onto another entry's name is
 * refused rather than merged: one of the two would be lost without anyone
 * having asked to delete it. A change of case alone is a rename like any other.
 */
export function renameSaved(from: string, to: string): SavedResult {
  const clean = cleanName(to);
  if (!clean) return { ok: false, reason: "empty-name" };
  const list = listSaved();
  const at = list.findIndex((s) => key(s.name) === key(from));
  if (at < 0) return { ok: false, reason: "missing" };
  if (list.some((s, i) => i !== at && key(s.name) === key(clean))) {
    return { ok: false, reason: "taken" };
  }
  return write(list.map((s, i) => (i === at ? { ...s, name: clean } : s)));
}

/** deleteSaved removes an entry. Removing one that is not there is not an error. */
export function deleteSaved(name: string): SavedResult {
  return write(listSaved().filter((s) => key(s.name) !== key(name)));
}

/** failureText says what went wrong in words for the connection screen. */
export function failureText(reason: SavedFailure): string {
  switch (reason) {
    case "empty-name":
      return "Give it a name.";
    case "taken":
      return "Another saved connection already has that name.";
    case "missing":
      return "That saved connection is no longer there.";
    case "full":
      return `At most ${MAX_SAVED} connections can be saved. Delete one first.`;
    case "storage":
      return "Your browser would not store it (private mode, or storage is full or blocked).";
  }
}
