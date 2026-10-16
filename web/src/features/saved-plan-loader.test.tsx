import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { parseSavedPlan } from "@/lib/saved-plan";
import { SavedPlanSummary } from "./saved-plan-loader";

// What loading a saved plan shows before anything is staged: where it was
// made, from what, and every change with what it expects.

const text = JSON.stringify({
  format: "alder-plan",
  version: 1,
  createdAt: "2026-10-16T01:00:00Z",
  alderVersion: "1.39.0",
  input: { kind: "changes", digest: "sha256:ab12" },
  environment: { host: "ldap.example.test:636", bindDn: "" },
  changes: [
    {
      dn: "uid=gone‮,dc=example,dc=test",
      type: "delete",
      expect: { attributes: [{ name: "uid", values: [{ text: "gone" }] }], exhaustive: true },
    },
  ],
  unchanged: 0,
});

describe("SavedPlanSummary", () => {
  it("shows where the plan was made and what each change expects, with unsafe text made visible", () => {
    const r = parseSavedPlan(text);
    if (!r.ok) throw new Error(r.message);
    const html = renderToStaticMarkup(<SavedPlanSummary plan={r.plan} counts={["1 to delete"]} />);
    expect(html).toContain("ldap.example.test:636, binding anonymously");
    expect(html).toContain("--changes");
    expect(html).toContain("1 to delete");
    expect(html).toContain("uid=gone\\u202e,dc=example,dc=test");
    expect(html).not.toContain("‮");
    expect(html).toContain("expects uid and nothing else as it was planned");
  });
});
