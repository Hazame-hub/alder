import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as recent from "./recent-connection";
import type { RecentConnection } from "./recent-connection";

// The connection screen remembers where you connected and, by name, where you
// connect often. It never remembers a password, and a storage that is broken,
// full, denied or tampered with must cost the convenience, not the screen.
//
// Tests run in Node, so localStorage is a small in-memory stand-in on a stubbed
// window. That is enough: what is under test is what gets written and what is
// believed on the way back, not the browser's storage.

class MemoryStorage {
  data = new Map<string, string>();
  denied = false;
  getItem(k: string) {
    if (this.denied) throw new Error("SecurityError");
    return this.data.has(k) ? this.data.get(k)! : null;
  }
  setItem(k: string, v: string) {
    if (this.denied) throw new Error("QuotaExceededError");
    this.data.set(k, String(v));
  }
  removeItem(k: string) {
    if (this.denied) throw new Error("SecurityError");
    this.data.delete(k);
  }
}

let storage: MemoryStorage;

beforeEach(() => {
  storage = new MemoryStorage();
  vi.stubGlobal("window", { localStorage: storage });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

const conn = (over: Partial<RecentConnection> = {}): RecentConnection => ({
  host: "ldap.example.test",
  port: 636,
  tls: "ldaps",
  bindDn: "cn=admin,dc=example,dc=test",
  configBindDn: "",
  serverName: "",
  caCertificate: "",
  insecureSkipVerify: false,
  ...over,
});

// An object that is a RecentConnection to the type checker and carries
// passwords anyway -- exactly what a form's state spread into save() would be.
const withPasswords = (c: RecentConnection) =>
  ({ ...c, bindPassword: "hunter2", configBindPassword: "s3cret" }) as RecentConnection;

describe("the last connection", () => {
  it("still reads what an earlier build wrote under the v1 key", () => {
    storage.setItem(
      "alder.recent-connection.v1",
      JSON.stringify({ host: "old.example.test", port: 389, tls: "starttls", bindDn: "cn=x" }),
    );
    expect(recent.load()).toEqual(
      conn({ host: "old.example.test", port: 389, tls: "starttls", bindDn: "cn=x" }),
    );
  });

  it("never writes a password, even one smuggled in past the type", () => {
    recent.save(withPasswords(conn()));
    const stored = storage.getItem("alder.recent-connection.v1")!;
    expect(stored).not.toContain("hunter2");
    expect(stored).not.toContain("s3cret");
    expect(stored).not.toMatch(/password/i);
  });

  it("drops a password found in storage instead of carrying it along", () => {
    storage.setItem(
      "alder.recent-connection.v1",
      JSON.stringify({ ...conn(), bindPassword: "hunter2" }),
    );
    expect(recent.load()).toEqual(conn());
  });

  it("is unaffected by the named list, and the list by it", () => {
    recent.save(conn({ host: "last.example.test" }));
    recent.saveNamed("prod", conn({ host: "prod.example.test" }));
    recent.forget();
    expect(recent.load()).toBeNull();
    expect(recent.listSaved().map((s) => s.name)).toEqual(["prod"]);
  });
});

describe("saved connections", () => {
  it("starts empty, and an empty list is not an error", () => {
    expect(recent.listSaved()).toEqual([]);
  });

  it("keeps connections under names, in the order saved", () => {
    recent.saveNamed("primary", conn({ host: "a" }));
    const r = recent.saveNamed("  replica  ", conn({ host: "b" }));
    expect(r.ok).toBe(true);
    expect(recent.listSaved()).toEqual([
      { name: "primary", connection: conn({ host: "a" }) },
      { name: "replica", connection: conn({ host: "b" }) },
    ]);
  });

  it("never writes a password into the list", () => {
    recent.saveNamed("prod", withPasswords(conn()));
    const stored = storage.getItem("alder.saved-connections.v1")!;
    expect(stored).not.toContain("hunter2");
    expect(stored).not.toContain("s3cret");
    expect(stored).not.toMatch(/password/i);
  });

  it("updates an entry saved again under the same name, whatever its case", () => {
    recent.saveNamed("dev", conn({ host: "a" }));
    recent.saveNamed("prod", conn({ host: "b" }));
    recent.saveNamed("DEV", conn({ host: "c" }));
    expect(recent.listSaved()).toEqual([
      { name: "DEV", connection: conn({ host: "c" }) },
      { name: "prod", connection: conn({ host: "b" }) },
    ]);
  });

  it("refuses a blank name", () => {
    expect(recent.saveNamed("   ", conn())).toEqual({ ok: false, reason: "empty-name" });
    expect(recent.listSaved()).toEqual([]);
  });

  it("bounds a name's length", () => {
    recent.saveNamed("x".repeat(500), conn());
    expect(recent.listSaved()[0]?.name).toHaveLength(recent.MAX_NAME);
  });

  it("refuses a new name past the cap, and evicts nothing to make room", () => {
    for (let i = 0; i < recent.MAX_SAVED; i++) {
      expect(recent.saveNamed(`c${i}`, conn({ port: i + 1 })).ok).toBe(true);
    }
    expect(recent.saveNamed("one more", conn())).toEqual({ ok: false, reason: "full" });
    expect(recent.listSaved()).toHaveLength(recent.MAX_SAVED);
    expect(recent.listSaved()[0]?.name).toBe("c0");
    // Updating an existing one is still allowed at the cap.
    expect(recent.saveNamed("c0", conn({ host: "moved" })).ok).toBe(true);
  });

  it("renames in place, and refuses to rename onto another entry", () => {
    recent.saveNamed("a", conn({ host: "a" }));
    recent.saveNamed("b", conn({ host: "b" }));
    expect(recent.renameSaved("b", "A")).toEqual({ ok: false, reason: "taken" });
    expect(recent.renameSaved("missing", "c")).toEqual({ ok: false, reason: "missing" });
    expect(recent.renameSaved("a", "")).toEqual({ ok: false, reason: "empty-name" });
    expect(recent.renameSaved("a", "primary").ok).toBe(true);
    // A change of case alone is a rename, not a collision with itself.
    expect(recent.renameSaved("b", "B").ok).toBe(true);
    expect(recent.listSaved().map((s) => s.name)).toEqual(["primary", "B"]);
  });

  it("deletes by name", () => {
    recent.saveNamed("a", conn());
    recent.saveNamed("b", conn());
    recent.deleteSaved("A");
    expect(recent.listSaved().map((s) => s.name)).toEqual(["b"]);
    expect(recent.deleteSaved("nope").ok).toBe(true);
  });

  it("reports storage that will not take a write, rather than pretending", () => {
    storage.denied = true;
    expect(recent.saveNamed("prod", conn())).toEqual({ ok: false, reason: "storage" });
    expect(recent.listSaved()).toEqual([]);
    expect(recent.load()).toBeNull();
    // And the last connection's write still swallows it.
    expect(() => recent.save(conn())).not.toThrow();
  });

  it("survives a tampered list, keeping what it can read", () => {
    storage.setItem(
      "alder.saved-connections.v1",
      JSON.stringify([
        { name: "good", connection: { ...conn(), bindPassword: "hunter2" } },
        { name: "no connection" },
        { name: 7, connection: conn() },
        "junk",
        null,
        { name: "  ", connection: conn() },
        { name: "GOOD", connection: conn({ host: "dupe" }) },
        { name: "port as string", connection: { ...conn(), port: "636" } },
      ]),
    );
    expect(recent.listSaved()).toEqual([{ name: "good", connection: conn() }]);

    storage.setItem("alder.saved-connections.v1", "{not json");
    expect(recent.listSaved()).toEqual([]);
    storage.setItem("alder.saved-connections.v1", JSON.stringify({ name: "x" }));
    expect(recent.listSaved()).toEqual([]);
  });

  it("applies the cap to a list written by hand", () => {
    const many = Array.from({ length: recent.MAX_SAVED + 10 }, (_, i) => ({
      name: `n${i}`,
      connection: conn(),
    }));
    storage.setItem("alder.saved-connections.v1", JSON.stringify(many));
    expect(recent.listSaved()).toHaveLength(recent.MAX_SAVED);
  });
});

describe("comparing connections", () => {
  it("compares the remembered fields and nothing else", () => {
    expect(recent.same(conn(), withPasswords(conn()))).toBe(true);
    expect(recent.same(conn(), conn({ port: 389 }))).toBe(false);
    expect(recent.same(conn(), conn({ caCertificate: "x" }))).toBe(false);
  });
});
