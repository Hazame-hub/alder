import { describe, expect, it } from "vitest";
import { causeChain, claimedArtifact, filterFindings, subjectLine, type PreflightFinding, type PreflightReport } from "./preflight";

function finding(partial: Partial<PreflightFinding> & { id: string }): PreflightFinding {
  return {
    code: "definition_portable",
    classification: "portable",
    category: "schema",
    scope: "definition",
    source: {},
    explanation: "",
    blocksPortability: false,
    blocksPlan: false,
    manualAction: false,
    ...partial,
  };
}

function report(findings: PreflightFinding[]): PreflightReport {
  const counts = { portable: 0, alreadySatisfied: 0, prerequisiteRequired: 0, incompatible: 0, unsupported: 0, unknown: 0, excluded: 0 };
  return {
    reportVersion: 1,
    createdAt: "2026-09-17T00:00:00Z",
    source: { type: "change_package", format: "alder-change-package", version: 1, integrity: "verified", objects: 3 },
    target: { namingContexts: ["dc=alder,dc=test"], schemaWritable: true, crossVendor: true },
    overall: "incompatible",
    complete: true,
    reasons: [],
    counts,
    sections: [],
    capabilities: [],
    notEvaluated: [],
    findings,
    truncated: false,
  };
}

const chain = report([
  finding({ id: "f1", code: "definition_conflict", classification: "incompatible", source: { item: "c1", oid: "1.2.3" } }),
  finding({ id: "f2", code: "dependency_blocked", classification: "incompatible", source: { item: "c2", oid: "1.2.4" }, causes: ["f1"] }),
  finding({
    id: "f3",
    code: "entry_blocked",
    classification: "incompatible",
    category: "entries",
    source: { item: "c3", dn: "uid=pat,ou=people,dc=alder,dc=test" },
    causes: ["f2"],
  }),
  finding({ id: "f4", code: "reference_ready", category: "references", source: { dn: "cn=g", attribute: "member" }, count: 3 }),
  finding({ id: "f5", code: "reference_unknown", classification: "unknown", category: "references", source: { value: "uid=hidden" } }),
]);

describe("preflight findings", () => {
  it("follows causes from an entry to its root", () => {
    expect(causeChain(chain, "f3").map((f) => f.id)).toEqual(["f2", "f1"]);
  });

  it("does not loop on a cycle and stops at its limit", () => {
    const loop = report([
      finding({ id: "a", causes: ["b"] }),
      finding({ id: "b", causes: ["a", "c"] }),
      finding({ id: "c", causes: ["b"] }),
    ]);
    expect(causeChain(loop, "a").map((f) => f.id)).toEqual(["b", "c"]);
    expect(causeChain(loop, "a", 1)).toHaveLength(1);
  });

  it("filters by section, classification, attention and text", () => {
    expect(filterFindings(chain, { category: "references" }).map((f) => f.id)).toEqual(["f4", "f5"]);
    expect(filterFindings(chain, { classification: "unknown" }).map((f) => f.id)).toEqual(["f5"]);
    expect(filterFindings(chain, { attentionOnly: true }).map((f) => f.id)).toEqual(["f1", "f2", "f3", "f5"]);
    expect(filterFindings(chain, { text: "uid=pat" }).map((f) => f.id)).toEqual(["f3"]);
    expect(filterFindings(chain, { category: "all", classification: "all" })).toHaveLength(5);
  });

  it("names what a finding is about", () => {
    const ready = chain.findings.find((f) => f.id === "f4");
    expect(ready && subjectLine(ready)).toBe("cn=g · member · ×3");
  });

  it("reads an artifact's claim without trusting it", () => {
    expect(claimedArtifact({ format: "alder-change-package", changes: [{}, {}] })).toEqual({ label: "Change package", objects: 2 });
    expect(claimedArtifact({ format: "alder-snapshot", kind: "schema", attributeTypes: [{}], objectClasses: [{}] })).toEqual({
      label: "Schema snapshot",
      objects: 2,
    });
    expect(claimedArtifact({ format: "alder-snapshot", kind: "config", settings: [{}, {}, {}] })).toEqual({
      label: "Configuration snapshot",
      objects: 3,
    });
    expect(claimedArtifact({ format: "alder-snapshot", kind: "acl" })).toBeNull();
    expect(claimedArtifact([{ format: "alder-change-package" }])).toBeNull();
    expect(claimedArtifact("alder-change-package")).toBeNull();
  });
});
