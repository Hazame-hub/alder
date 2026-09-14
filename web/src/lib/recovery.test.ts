import { describe, expect, it } from "vitest";
import type { PlanItem, RecoveryBundle } from "@/lib/api";
import {
  assessmentLine,
  bundleText,
  overallRecovery,
  parseBundleFile,
  recoveryFilename,
  visible,
} from "@/lib/recovery";

function item(partial: Partial<PlanItem>): PlanItem {
  return { index: 0, dn: "uid=a,dc=alder,dc=test", action: "modify", exists: true, ...partial };
}

describe("visible", () => {
  it("makes bidi overrides and controls visible and leaves ordinary text alone", () => {
    expect(visible("uid=report\u202etxt.exe")).toBe("uid=report\\u202etxt.exe");
    expect(visible("a\u0007b\u001bc")).toBe("a\\u0007b\\u001bc");
    expect(visible("cn=Zoë Ångström,dc=alder")).toBe("cn=Zoë Ångström,dc=alder");
  });
});

describe("assessmentLine", () => {
  it("never says rollback, and says why a recovery is not exact", () => {
    expect(assessmentLine({ recoverability: "exact" })).toBe("Exact recovery available");
    const line = assessmentLine({
      recoverability: "unavailable",
      reasons: [{ code: "password_not_captured" }],
    });
    expect(line).toMatch(/^Recovery unavailable: a previous password is never captured$/);
    expect(line.toLowerCase()).not.toContain("rollback");
    expect(
      assessmentLine({
        recoverability: "partial",
        reasons: [{ code: "sensitive_value_not_captured", attribute: "userPassword\u202e" }],
      }),
    ).toContain("(userPassword\\u202e)");
  });
});

describe("overallRecovery", () => {
  const exact = { recoverability: "exact" as const };
  it("is null when the plan would apply nothing", () => {
    expect(overallRecovery([item({ action: "unchanged", recovery: exact })])).toBeNull();
  });
  it("is exact only when every applied item is", () => {
    expect(overallRecovery([item({ baseline: "t", recovery: exact }), item({ baseline: "u", recovery: exact })]))
      .toEqual({ recoverability: "exact" });
    expect(
      overallRecovery([
        item({ baseline: "t", recovery: exact }),
        item({ baseline: "u", recovery: { recoverability: "unavailable", reasons: [{ code: "password_not_captured" }] } }),
      ]),
    ).toEqual({ recoverability: "partial", reasons: [{ code: "password_not_captured" }] });
  });
  it("is unavailable when nothing can be recovered, and deduplicates reasons", () => {
    const none = { recoverability: "unavailable" as const, reasons: [{ code: "password_not_captured" as const }] };
    expect(overallRecovery([item({ baseline: "t", recovery: none }), item({ baseline: "u", recovery: none })]))
      .toEqual(none);
  });
});

describe("bundle files", () => {
  const bundle = {
    format: "alder-recovery",
    version: 1,
    createdAt: "2026-09-14T12:34:56Z",
    origin: { namingContexts: ["dc=alder,dc=test"] },
    recoverability: "exact",
    steps: [],
    checksum: "sha256:00",
  } as RecoveryBundle;

  it("names the file from the bundle's time", () => {
    expect(recoveryFilename(bundle)).toBe("alder-recovery-2026-09-14T123456Z.json");
    expect(recoveryFilename({ createdAt: "nonsense" })).toBe("alder-recovery-undated.json");
  });

  it("round-trips through the file unchanged", () => {
    const parsed = parseBundleFile(bundleText(bundle));
    expect(parsed).toEqual({ ok: true, bundle });
  });

  it("refuses what is not a JSON object, without interpreting it", () => {
    expect(parseBundleFile("dn: cn=x").ok).toBe(false);
    expect(parseBundleFile("[1,2]").ok).toBe(false);
    expect(parseBundleFile("null").ok).toBe(false);
  });
});
