import type { components } from "@/lib/api.gen";

export type PreflightReport = components["schemas"]["PreflightReport"];
export type PreflightFinding = components["schemas"]["PreflightFinding"];
export type PreflightClassification = components["schemas"]["PreflightClassification"];
export type PreflightCategory = components["schemas"]["PreflightCategory"];
export type PreflightOverall = components["schemas"]["PreflightOverall"];

/**
 * Reading a preflight report.
 *
 * A report is analysis. Nothing here decides compatibility -- the server did,
 * finding by finding -- and nothing here leads to a write: the report says what
 * would carry across, and whatever the operator does about it goes through the
 * ordinary path.
 */

type Variant = "success" | "destructive" | "secondary" | "outline" | "warning";

export const CLASSIFICATION_LOOK: Record<PreflightClassification, { label: string; variant: Variant }> = {
  portable: { label: "Portable", variant: "success" },
  already_satisfied: { label: "Already present", variant: "outline" },
  prerequisite_required: { label: "Needs a prerequisite", variant: "warning" },
  incompatible: { label: "Incompatible", variant: "destructive" },
  unsupported: { label: "Unsupported", variant: "destructive" },
  unknown: { label: "Unknown", variant: "warning" },
  excluded: { label: "Not migrated", variant: "secondary" },
};

export const OVERALL_LOOK: Record<PreflightOverall, { label: string; variant: Variant; summary: string }> = {
  compatible: {
    label: "Compatible",
    variant: "success",
    summary: "Everything this artifact holds could carry across to this directory as it is.",
  },
  compatible_with_prerequisites: {
    label: "Compatible with prerequisites",
    variant: "warning",
    summary: "It could carry across once the prerequisites listed below are done.",
  },
  incompatible: {
    label: "Incompatible",
    variant: "destructive",
    summary: "Something in this artifact contradicts this directory or cannot be represented here.",
  },
  incomplete: {
    label: "Incomplete",
    variant: "warning",
    summary: "Something could not be decided from what this connection can see, so this is not a clean result.",
  },
};

export const CATEGORY_LABEL: Record<PreflightCategory, string> = {
  artifact: "Artifact",
  schema: "Schema",
  naming: "Naming",
  entries: "Entries",
  references: "References",
  configuration: "Configuration",
  capabilities: "Capabilities",
  operational: "Operational attributes",
  sensitive: "Sensitive data",
};

export const SOURCE_LABEL = {
  change_package: "Change package",
  schema_snapshot: "Schema snapshot",
  data_snapshot: "Data snapshot",
  config_snapshot: "Configuration snapshot",
} as const satisfies Record<PreflightReport["source"]["type"], string>;

/** Classifications that need no attention. */
const QUIET: PreflightClassification[] = ["portable", "already_satisfied"];

export type FindingFilter = {
  category?: PreflightCategory | "all";
  classification?: PreflightClassification | "all";
  /** Only what needs attention: everything but portable and already present. */
  attentionOnly?: boolean;
  /** Matches the code, the explanation and the source object. */
  text?: string;
};

export function filterFindings(report: PreflightReport, filter: FindingFilter): PreflightFinding[] {
  const needle = (filter.text ?? "").trim().toLowerCase();
  return report.findings.filter((f) => {
    if (filter.category && filter.category !== "all" && f.category !== filter.category) return false;
    if (filter.classification && filter.classification !== "all" && f.classification !== filter.classification) return false;
    if (filter.attentionOnly && QUIET.includes(f.classification)) return false;
    if (!needle) return true;
    return [f.code, f.explanation, subjectLine(f)].some((s) => s.toLowerCase().includes(needle));
  });
}

/** What a finding is about, in one line. */
export function subjectLine(f: PreflightFinding): string {
  const s = f.source;
  const parts = [s.item, s.element, s.oid, s.name, s.resource, s.dn, s.attribute, s.value].filter((p): p is string => !!p);
  if (f.count && f.count > 1) parts.push(`×${f.count}`);
  return parts.join(" · ");
}

/**
 * The chain of causes behind a finding, nearest first, following the report's
 * own links. Bounded and cycle-safe: a report is server output, but a chain is
 * walked in the browser and must not hang it.
 */
export function causeChain(report: PreflightReport, id: string, limit = 32): PreflightFinding[] {
  const byId = new Map(report.findings.map((f) => [f.id, f]));
  const out: PreflightFinding[] = [];
  const seen = new Set<string>([id]);
  let queue = [...(byId.get(id)?.causes ?? [])];
  while (queue.length > 0 && out.length < limit) {
    const next: string[] = [];
    for (const cause of queue) {
      if (seen.has(cause)) continue;
      seen.add(cause);
      const finding = byId.get(cause);
      if (!finding) continue;
      out.push(finding);
      next.push(...(finding.causes ?? []));
      if (out.length >= limit) break;
    }
    queue = next;
  }
  return out;
}

/**
 * What an uploaded document claims to be, for the heading only. The server
 * decides what it is; this never gates anything.
 */
export function claimedArtifact(document: unknown): { label: string; objects: number } | null {
  if (!document || typeof document !== "object" || Array.isArray(document)) return null;
  const d = document as Record<string, unknown>;
  if (d.format === "alder-change-package") {
    return { label: SOURCE_LABEL.change_package, objects: Array.isArray(d.changes) ? d.changes.length : 0 };
  }
  if (d.format === "alder-snapshot" && d.kind === "schema") {
    const ats = Array.isArray(d.attributeTypes) ? d.attributeTypes.length : 0;
    const ocs = Array.isArray(d.objectClasses) ? d.objectClasses.length : 0;
    return { label: SOURCE_LABEL.schema_snapshot, objects: ats + ocs };
  }
  if (d.format === "alder-snapshot" && d.kind === "config") {
    return { label: SOURCE_LABEL.config_snapshot, objects: Array.isArray(d.settings) ? d.settings.length : 0 };
  }
  if (d.format === "alder-snapshot" && d.kind === "data") {
    return { label: SOURCE_LABEL.data_snapshot, objects: Array.isArray(d.entries) ? d.entries.length : 0 };
  }
  return null;
}
