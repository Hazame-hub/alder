import { describe, expect, it } from "vitest";
import type { SessionInfo } from "@/lib/api";
import { changeset } from "@/lib/changeset";
import { parseSavedPlan, savedPlanOrigin, savedPlanRefusal, sessionLabel, sessionMismatch } from "@/lib/saved-plan";

// What alder plan --out writes (internal/cli/savedplan.go): exact changes,
// each modification and deletion with the state it expects, and no baseline.
function file(overrides: Record<string, unknown> = {}) {
  return {
    format: "alder-plan",
    version: 1,
    createdAt: "2026-10-16T01:00:00Z",
    alderVersion: "1.39.0",
    input: { kind: "ldif", mode: "changes", digest: "sha256:ab12" },
    environment: { host: "ldap.example.test:636", bindDn: "cn=admin,dc=example,dc=test" },
    changes: [
      { dn: "uid=new,ou=people,dc=example,dc=test", type: "add", attributes: [{ name: "uid", values: [{ text: "new" }] }] },
      {
        dn: "uid=a,ou=people,dc=example,dc=test",
        type: "modify",
        mods: [{ op: "replace", name: "title", values: [{ text: "Lead" }] }],
        expect: { attributes: [{ name: "title", values: [{ text: "Engineer" }] }] },
      },
      {
        dn: "uid=gone,ou=people,dc=example,dc=test",
        type: "delete",
        expect: { attributes: [{ name: "uid", values: [{ text: "gone" }] }], exhaustive: true },
      },
    ],
    unchanged: 2,
    ...overrides,
  };
}

const read = (o: Record<string, unknown> = {}) => parseSavedPlan(JSON.stringify(file(o)));

function refusal(o: Record<string, unknown>): string {
  const r = read(o);
  if (r.ok) throw new Error("expected a refusal");
  return r.message;
}

describe("parseSavedPlan", () => {
  it("reads what alder plan --out writes", () => {
    const r = read();
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.plan.changes.map((c) => c.type)).toEqual(["add", "modify", "delete"]);
    expect(r.plan.environment.host).toBe("ldap.example.test:636");
    expect(r.plan.unchanged).toBe(2);
  });

  it("refuses a project's plan, which only alder project apply checks against its files", () => {
    const message = refusal({
      input: undefined,
      project: { digest: "sha256:cd34" },
      environment: { name: "prod", host: "ldap.example.test:636", bindDn: "" },
    });
    expect(message).toContain("project's plan, for prod");
    expect(message).toContain("alder project apply --plan");
  });

  it("refuses what it does not fully understand", () => {
    expect(parseSavedPlan("not json").ok).toBe(false);
    expect(refusal({ format: "alder-recovery" })).toContain("not a saved plan");
    expect(refusal({ version: 2 })).toContain("version 2");
    expect(refusal({ signature: "x" })).toContain("signature");
    expect(refusal({ input: { kind: "ldif", digest: "sha256:ab", extra: true } })).toContain("which input");
    expect(refusal({ input: { kind: "csv", digest: "sha256:ab" } })).toContain("which input");
    expect(refusal({ environment: { host: "", bindDn: "" } })).toContain("does not name the directory");
    expect(
      refusal({ environment: { host: "h:389", bindDn: "", rebase: { from: "a", to: "b" } } }),
    ).toContain("only a project's plan");
    expect(refusal({ changes: undefined })).toContain("no changes list");
  });

  it("refuses changes a saved plan never holds", () => {
    const change = (c: Record<string, unknown>) => refusal({ changes: [{ dn: "uid=a,dc=example,dc=test", ...c }] });
    expect(change({ type: "add", baseline: "t" })).toContain("carries a baseline");
    expect(change({ type: "modify", mods: [] })).toContain("modification with no expectation");
    expect(change({ type: "delete", expect: { attributes: [] } })).toContain("does not expect the entry exactly");
    expect(change({ type: "modrdn", newRdn: "uid=b" })).toContain("modrdn");
    expect(change({ type: "setpassword", newPassword: "x" })).toContain("setpassword");
  });

  it("refuses a change whose shape it does not know, rather than staging it or failing to show it", () => {
    const change = (c: Record<string, unknown>) => refusal({ changes: [{ dn: "uid=a,dc=example,dc=test", ...c }] });
    expect(change({ type: "modify", mods: [], expect: { attributes: null } })).toContain("not a change this release reads");
    expect(change({ type: "add", attributes: { name: "cn" } })).toContain("not a change this release reads");
    expect(change({ type: "add", attributes: [], newSuperior: "dc=x" })).toContain("not a change this release reads");
    expect(change({ type: "modify", mods: [{ op: "rename", name: "cn" }], expect: { attributes: [] } })).toContain(
      "not a change this release reads",
    );
    expect(change({ type: "add", attributes: [{ name: "cn", values: [{ text: 1 }] }] })).toContain(
      "not a change this release reads",
    );
  });

  it("refuses a plan with a withheld value, and never shows one", () => {
    const message = refusal({
      changes: [
        {
          dn: "uid=a,dc=example,dc=test",
          type: "modify",
          mods: [{ op: "replace", name: "userPassword", values: [{}] }],
          expect: { attributes: [] },
        },
      ],
    });
    expect(message).toContain("userPassword");
    expect(message).toContain("withheld");
  });

  it("makes control characters in what it quotes visible", () => {
    expect(refusal({ format: "alder‮plan" })).toContain("\\u202e");
  });
});

describe("sessionMismatch", () => {
  const session: SessionInfo = {
    connected: true,
    host: "ldap.example.test",
    port: 636,
    bindDn: "CN=Admin,DC=example,DC=test",
  };
  const plan = () => {
    const r = read();
    if (!r.ok) throw new Error(r.message);
    return r.plan;
  };

  it("accepts the same host:port and bind, the DN compared as alder apply --plan does", () => {
    expect(sessionMismatch(plan().environment, session)).toBeNull();
  });

  it("refuses another host, port or bind", () => {
    expect(sessionMismatch(plan().environment, { ...session, host: "ldap-replica.example.test" })).toContain(
      "connected to ldap-replica.example.test:636",
    );
    expect(sessionMismatch(plan().environment, { ...session, port: 389 })).toContain("ldap.example.test:389");
    expect(sessionMismatch(plan().environment, { ...session, bindDn: "" })).toContain("this session binds anonymously");
  });

  it("refuses a session that is not connected", () => {
    expect(sessionMismatch(plan().environment, { connected: false })).toContain("not connected");
  });

  it("writes an IPv6 address as the command line does", () => {
    expect(sessionLabel({ connected: true, host: "::1", port: 10636 })).toBe("[::1]:10636");
  });
});

describe("savedPlanRefusal", () => {
  it("applies a saved plan whole or not at all, as alder apply --plan does", () => {
    expect(savedPlanRefusal(3, 3)).toBeNull();
    const why = savedPlanRefusal(1, 2);
    expect(why).toContain("1 of its 2 changes would apply");
    expect(why).toContain("Nothing will be written");
  });
});

describe("a saved plan's origin in the changeset", () => {
  it("lasts while the changeset is exactly the plan, and ends when a run applies part of it", () => {
    changeset.clear();
    const r = read();
    if (!r.ok) throw new Error(r.message);
    changeset.addMany(r.plan.changes.map((c) => ({ change: c, label: c.dn })));
    changeset.setOrigin(savedPlanOrigin(r.plan));
    expect(changeset.origin()?.kind).toBe("saved-plan");
    changeset.removeApplied([]);
    expect(changeset.origin()?.kind).toBe("saved-plan");
    changeset.removeApplied([changeset.all()[0]!.id]);
    expect(changeset.origin()).toBeNull();
    changeset.clear();
  });
});
