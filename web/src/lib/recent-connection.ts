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
  serverName: string;
  caCertificate: string;
  insecureSkipVerify: boolean;
};

const KEY = "alder.recent-connection.v1";

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
    const parsed = JSON.parse(raw) as Partial<RecentConnection>;
    if (typeof parsed.host !== "string" || typeof parsed.port !== "number") {
      return null;
    }
    return {
      host: parsed.host,
      port: parsed.port,
      tls: parsed.tls === "starttls" || parsed.tls === "plaintext" ? parsed.tls : "ldaps",
      bindDn: typeof parsed.bindDn === "string" ? parsed.bindDn : "",
      serverName: typeof parsed.serverName === "string" ? parsed.serverName : "",
      caCertificate:
        typeof parsed.caCertificate === "string" ? parsed.caCertificate : "",
      insecureSkipVerify: parsed.insecureSkipVerify === true,
    };
  } catch {
    // Unreadable, unparsable, or storage denied. Start from a blank form.
    return null;
  }
}

/** save records a connection that succeeded. Failures are not worth reporting. */
export function save(connection: RecentConnection): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(connection));
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
