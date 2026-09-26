import type { SessionInfo } from "@/lib/api";

/**
 * Where an entry sits relative to the server's own configuration.
 *
 * The entry editor treats every entry alike, which is right for directory
 * data and misleading for `cn=config`: it offers all forty-odd attributes as
 * plain fields, says nothing about which ones Alder has proved it can write or
 * which need a restart, and offers "Delete with contents" on the root of the
 * server's configuration. The comparison knows all of that; the editor is
 * simply somewhere else in the application.
 *
 * This is the little fact both need: is this entry the configuration root, or
 * inside it? Matching is by DN text, case-insensitively, as everywhere else in
 * the interface that asks whether one DN is under another.
 */
export type ConfigPlace = "root" | "inside" | null;

export function configPlace(info: SessionInfo | undefined, dn: string): ConfigPlace {
  const root = info?.capabilities?.config?.dn;
  if (!root) return null;
  const here = dn.trim().toLowerCase();
  const base = root.trim().toLowerCase();
  if (here === base) return "root";
  if (here.endsWith(`,${base}`)) return "inside";
  return null;
}

/** Whether an entry is the configuration root or somewhere under it. */
export function inConfigTree(info: SessionInfo | undefined, dn: string): boolean {
  return configPlace(info, dn) !== null;
}
