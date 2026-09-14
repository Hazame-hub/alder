import type {
  PlanItem,
  RecoveryAssessment,
  RecoveryBundle,
  RecoveryDriftState,
  RecoveryReason,
  RecoveryReasonCode,
  RecoveryRecoverability,
} from "@/lib/api";
import { safeText } from "@/lib/display";

/**
 * Recovery, as the interface talks about it.
 *
 * A recovery bundle is compensation, not rollback: the changes Alder could
 * derive from the state an entry was in immediately before a change, which go
 * back through a plan and a review like any other change. Nothing here uses the
 * word rollback or undo, because nothing here is one.
 */

export const RECOVERABILITY: Record<RecoveryRecoverability, string> = {
  exact: "Exact recovery available",
  partial: "Partial recovery only",
  unavailable: "Recovery unavailable",
};

export const REASON: Record<RecoveryReasonCode, string> = {
  password_not_captured: "a previous password is never captured",
  sensitive_value_not_captured: "the earlier value of a sensitive attribute is never captured",
  server_owned_attribute: "the server maintains this attribute",
  identity_regenerated: "a recreated entry gets a new identity and timestamps",
  hidden_attributes_unknown: "attributes this login cannot read are not restored",
  sensitive_values_not_restored: "passwords and other sensitive values are not restored",
  schema_or_config_not_supported: "schema and configuration changes are not recovered",
  pre_state_unavailable: "the entry could not be read before the change",
};

export const DRIFT: Record<RecoveryDriftState, string> = {
  ready: "ready",
  drifted: "the directory has drifted since the original apply",
  already_recovered: "already in the recovered state",
  blocked: "not valid against the schema",
};

export function reasonText(reason: RecoveryReason): string {
  const text = REASON[reason.code] ?? reason.code;
  return reason.attribute ? `${text} (${safeText(reason.attribute)})` : text;
}

/** One line for an assessment: the label, then why it is not exact. */
export function assessmentLine(assessment: RecoveryAssessment): string {
  const label = RECOVERABILITY[assessment.recoverability] ?? assessment.recoverability;
  const reasons = assessment.reasons ?? [];
  if (reasons.length === 0) return label;
  return `${label}: ${reasons.map(reasonText).join("; ")}`;
}

/**
 * The recoverability of everything a plan would apply, or null when it would
 * apply nothing. Exact only when every item is; unavailable only when every
 * item is.
 */
export function overallRecovery(items: PlanItem[]): RecoveryAssessment | null {
  const assessed = items.filter((i) => i.baseline !== undefined && i.recovery !== undefined);
  if (assessed.length === 0) return null;
  const levels = assessed.map((i) => i.recovery!.recoverability);
  let recoverability: RecoveryRecoverability = "partial";
  if (levels.every((l) => l === "exact")) recoverability = "exact";
  else if (levels.every((l) => l === "unavailable")) recoverability = "unavailable";
  const seen = new Set<string>();
  const reasons: RecoveryReason[] = [];
  for (const item of assessed) {
    for (const r of item.recovery?.reasons ?? []) {
      const key = `${r.code}|${r.attribute ?? ""}`;
      if (!seen.has(key)) {
        seen.add(key);
        reasons.push(r);
      }
    }
  }
  return reasons.length ? { recoverability, reasons } : { recoverability };
}

/** A file name safe anywhere, from the bundle's own time. */
export function recoveryFilename(bundle: Pick<RecoveryBundle, "createdAt">): string {
  const stamp = new Date(bundle.createdAt);
  const iso = Number.isNaN(stamp.getTime())
    ? "undated"
    : stamp.toISOString().replace(/\.\d{3}Z$/, "Z").replace(/:/g, "");
  return `alder-recovery-${iso}.json`;
}

/**
 * The bundle as a file. Serialised exactly as it arrived: the server checks
 * the checksum against the content, and a client that reshaped the document
 * would only make that check fail.
 */
export function bundleText(bundle: RecoveryBundle): string {
  return `${JSON.stringify(bundle, null, 2)}\n`;
}

/** The largest bundle file the interface will read and send. */
export const maxBundleBytes = 16 * 1024 * 1024;

/**
 * Reads a bundle file far enough to send it. Only JSON is checked here: what
 * the document says is decided by the server, which refuses anything that is
 * not exactly a valid bundle. A browser check would be a second opinion that
 * could only disagree.
 */
export function parseBundleFile(
  text: string,
): { ok: true; bundle: RecoveryBundle } | { ok: false; message: string } {
  if (text.length > maxBundleBytes) {
    return { ok: false, message: "The file is larger than a recovery bundle can be." };
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return { ok: false, message: "The file is not JSON, so it is not a recovery bundle." };
  }
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    return { ok: false, message: "The file is not a recovery bundle." };
  }
  return { ok: true, bundle: parsed as RecoveryBundle };
}
