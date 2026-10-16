import type { ChangeRequest, SessionInfo } from "@/lib/api";
import { safeText } from "@/lib/display";

/**
 * A saved plan, as the interface reads one: the file `alder plan --out`
 * writes (docs/proposals/saved-plans.md), loaded next to a recovery bundle.
 *
 * It carries no baseline, only exact changes with the state each expects to
 * find, so loading one stages those changes and the ordinary plan checks them
 * against the directory. The reader is as strict as the command line's: a
 * file it does not fully understand is refused, not half-read. A project's
 * plan is refused too. It is applied only by alder project apply, which
 * checks the project's files still render to what was planned; nothing here
 * has those files.
 */

export const savedPlanFormat = "alder-plan";
export const savedPlanVersion = 1;
/** The most the server accepts in one request, which a plan is sent as. */
export const maxSavedPlanBytes = 16 << 20;

export type SavedPlan = {
  createdAt: string;
  alderVersion: string;
  input: { kind: "ldif" | "changes"; mode?: string; digest: string };
  environment: { host: string; bindDn: string };
  changes: ChangeRequest[];
  unchanged: number;
};

type Reading = { ok: true; plan: SavedPlan } | { ok: false; message: string };

const topLevel = new Set([
  "format",
  "version",
  "createdAt",
  "alderVersion",
  "project",
  "input",
  "environment",
  "changes",
  "unchanged",
]);

function isObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}

function unknownKey(o: Record<string, unknown>, known: string[]): string | undefined {
  return Object.keys(o).find((k) => !known.includes(k));
}

/** Whether any value of a change was withheld: a value with neither form. */
function withheld(change: ChangeRequest): string | undefined {
  const lists: { name: string; values: unknown[] | undefined }[] = [
    ...(change.attributes ?? []).map((a) => ({ name: a.name, values: a.values })),
    ...(change.mods ?? []).map((m) => ({ name: m.name, values: m.values })),
    ...(change.expect?.attributes ?? []).map((a) => ({ name: a.name, values: a.values })),
  ];
  for (const { name, values } of lists) {
    for (const v of values ?? []) {
      if (!isObject(v) || (v.text === undefined && v.base64 === undefined)) return name;
    }
  }
  return undefined;
}

export function parseSavedPlan(text: string): Reading {
  const refuse = (message: string): Reading => ({ ok: false, message });
  if (text.length > maxSavedPlanBytes) {
    return refuse(`The file is larger than the ${maxSavedPlanBytes >> 20} MiB the server accepts.`);
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return refuse("The file is not JSON, so it is not a saved plan.");
  }
  if (!isObject(parsed)) return refuse("The file is not a saved plan.");
  const s = parsed;
  if (s.format !== savedPlanFormat) {
    return refuse(`The file is not a saved plan: its format is ${safeText(String(s.format))}, not ${savedPlanFormat}.`);
  }
  if (s.version !== savedPlanVersion) {
    return refuse(`This saved plan is version ${safeText(String(s.version))}; this release reads version ${savedPlanVersion}.`);
  }
  const extra = Object.keys(s).find((k) => !topLevel.has(k));
  if (extra !== undefined) {
    return refuse(`This saved plan holds ${safeText(extra)}, which this release does not know.`);
  }
  const env = s.environment;
  if (!isObject(env) || typeof env.host !== "string" || env.host === "" || typeof env.bindDn !== "string") {
    return refuse("This saved plan does not name the directory it was made against.");
  }
  if (s.project !== undefined) {
    const name = typeof env.name === "string" ? safeText(env.name) : "its environment";
    return refuse(
      `This is a project's plan, for ${name}. Apply it with alder project apply --plan, which checks the project's files still match it.`,
    );
  }
  const input = s.input;
  if (
    !isObject(input) ||
    unknownKey(input, ["kind", "mode", "digest"]) !== undefined ||
    (input.kind !== "ldif" && input.kind !== "changes") ||
    (input.mode !== undefined && typeof input.mode !== "string") ||
    typeof input.digest !== "string" ||
    !input.digest.startsWith("sha256:")
  ) {
    return refuse("This saved plan does not say which input it was made from.");
  }
  if (unknownKey(env, ["host", "bindDn"]) !== undefined) {
    return refuse("This saved plan names an environment or a rebase, which only a project's plan has.");
  }
  if (typeof s.createdAt !== "string" || typeof s.alderVersion !== "string" || typeof s.unchanged !== "number") {
    return refuse("This saved plan is missing when or by what it was made.");
  }
  if (!Array.isArray(s.changes)) return refuse("This saved plan has no changes list.");
  const changes: ChangeRequest[] = [];
  for (const [i, raw] of s.changes.entries()) {
    const n = i + 1;
    if (!isObject(raw) || typeof raw.dn !== "string") return refuse(`Change ${n} is not a change.`);
    const c = raw as unknown as ChangeRequest;
    if (c.baseline !== undefined) return refuse(`Change ${n} carries a baseline, which no saved plan holds.`);
    switch (c.type) {
      case "add":
        break;
      case "modify":
        if (!c.expect) return refuse(`Change ${n} is a modification with no expectation.`);
        break;
      case "delete":
        if (!c.expect?.exhaustive) return refuse(`Change ${n} is a deletion that does not expect the entry exactly.`);
        break;
      default:
        return refuse(`Change ${n} is a ${safeText(String(c.type))}, which a saved plan does not hold.`);
    }
    const name = withheld(c);
    if (name !== undefined) {
      return refuse(`A value of ${safeText(name)} on ${safeText(c.dn)} was withheld from this plan, so it cannot be applied from it.`);
    }
    changes.push(c);
  }
  return {
    ok: true,
    plan: {
      createdAt: s.createdAt,
      alderVersion: s.alderVersion,
      input: { kind: input.kind, mode: input.mode as string | undefined, digest: input.digest },
      environment: { host: env.host, bindDn: env.bindDn },
      changes,
      unchanged: s.unchanged,
    },
  };
}

/** host:port as the command line writes it, with an IPv6 address bracketed. */
export function sessionLabel(session: SessionInfo): string {
  const host = session.host ?? "";
  return `${host.includes(":") ? `[${host}]` : host}:${session.port ?? ""}`;
}

/**
 * Why this session is not where the plan was made, or null when it is. The
 * same check alder apply --plan makes before connecting: the same host:port,
 * binding as the same DN.
 */
export function sessionMismatch(plan: SavedPlan, session: SessionInfo): string | null {
  const here = sessionLabel(session);
  if (plan.environment.host !== here) {
    return `It was made against ${safeText(plan.environment.host)}, and this session is connected to ${safeText(here)}.`;
  }
  const bind = session.bindDn ?? "";
  if (plan.environment.bindDn.toLowerCase() !== bind.toLowerCase()) {
    const as = (dn: string) => (dn === "" ? "anonymously" : `as ${safeText(dn)}`);
    return `It was made binding ${as(plan.environment.bindDn)}, and this session binds ${as(bind)}.`;
  }
  return null;
}

/**
 * The changeset's origin while it is exactly a loaded saved plan. Any edit
 * to the basket drops it, as it drops a project's.
 */
export type SavedPlanOrigin = {
  kind: "saved-plan";
  host: string;
  bindDn: string;
  createdAt: string;
};

export function savedPlanOrigin(plan: SavedPlan): SavedPlanOrigin {
  return { kind: "saved-plan", ...plan.environment, createdAt: plan.createdAt };
}

/**
 * Why a checked plan of a saved plan's changes may not be applied, or null
 * when every one of them would apply. A saved plan is applied whole or not
 * at all, as alder apply --plan applies it: applying the part that still
 * holds would apply something nobody reviewed as a whole.
 */
export function savedPlanRefusal(applicable: number, staged: number): string | null {
  if (applicable === staged) return null;
  return `This saved plan no longer holds: ${applicable} of its ${staged} changes would apply, and a saved plan is applied whole or not at all. Nothing will be written; plan the input again and review that.`;
}
