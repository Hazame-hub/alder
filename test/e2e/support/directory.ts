import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import type { DirectoryServer } from "./servers";

/**
 * Reading and arranging the directory without going through Alder.
 *
 * A journey ends by asking the directory what is true, and it must not ask
 * Alder: an application that confirms its own write proves only that it
 * agrees with itself. So this uses the servers' own command-line tools,
 * inside their containers, on the container loopback -- a different client,
 * a different code path, and the one an administrator would reach for to
 * check Alder's work by hand.
 *
 * It also arranges the state a journey starts from and removes it after, for
 * the same reason the conformance suite does: proofs that leave entries
 * behind break the inventory assertions, and the next run.
 */

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const composeFile = path.join(repoRoot, "test", "compose", "docker-compose.yml");

export type Attributes = Record<string, string[]>;

function run(server: DirectoryServer, tool: string, args: string[], input?: string) {
  const res = spawnSync(
    "docker",
    [
      "compose", "-f", composeFile, "exec", "-T", server.service,
      tool, "-x",
      "-H", `ldap://localhost:${server.innerLdapPort}`,
      "-D", server.bindDN, "-w", server.bindPassword,
      ...args,
    ],
    { input, encoding: "utf8" },
  );
  if (res.error) {
    throw new Error(`could not run ${tool} in ${server.service}: ${res.error.message} -- is the harness up? (task compose:up)`);
  }
  return { code: res.status ?? -1, out: res.stdout ?? "", err: res.stderr ?? "" };
}

/**
 * The entry as the server holds it, or null when it does not exist.
 *
 * Attribute names come back lower-cased, so a journey compares without
 * caring how a server chose to spell them.
 */
export function read(server: DirectoryServer, dn: string, attributes: string[] = []): Attributes | null {
  const { code, out, err } = run(server, "ldapsearch", ["-LLL", "-o", "ldif-wrap=no", "-b", dn, "-s", "base", ...attributes]);
  if (code === 32) return null; // noSuchObject: an answer, not a failure
  if (code !== 0) throw new Error(`ldapsearch ${dn} on ${server.name} exited ${code}: ${err.trim()}`);
  return parseEntry(out);
}

/** Adds an entry from LDIF, failing loudly if the server refuses it. */
export function add(server: DirectoryServer, ldif: string): void {
  const { code, err } = run(server, "ldapmodify", ["-a"], ldif);
  if (code !== 0) throw new Error(`adding on ${server.name} exited ${code}: ${err.trim()}`);
}

/** Removes an entry; one that is already gone is not an error. */
export function remove(server: DirectoryServer, dn: string): void {
  const { code, err } = run(server, "ldapdelete", [dn]);
  if (code !== 0 && code !== 32) throw new Error(`deleting ${dn} on ${server.name} exited ${code}: ${err.trim()}`);
}

/**
 * Parses one LDIF entry. The output is requested unwrapped, so there is no
 * line folding to undo; a value the server chose to base64-encode is
 * decoded, so a journey compares the text a person typed.
 */
export function parseEntry(ldif: string): Attributes {
  const out: Attributes = {};
  for (const line of ldif.split(/\r?\n/)) {
    if (!line || line.startsWith("#") || line.startsWith("dn:")) continue;
    const b64 = line.indexOf(":: ");
    const plain = line.indexOf(": ");
    let name: string;
    let value: string;
    if (b64 > 0 && (plain < 0 || b64 < plain)) {
      name = line.slice(0, b64);
      value = Buffer.from(line.slice(b64 + 3), "base64").toString("utf8");
    } else if (plain > 0) {
      name = line.slice(0, plain);
      value = line.slice(plain + 2);
    } else {
      continue;
    }
    (out[name.toLowerCase()] ??= []).push(value);
  }
  return out;
}
