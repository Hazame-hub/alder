import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import type { Plan } from "@/lib/api";
import { PlanSummary } from "./plan-summary";

// A plan with a conflict: an ordinary changeset sends the rest, a saved plan
// sends nothing, and the card must not promise the rest for the latter.

const plan = {
  counts: { examined: 2, add: 1, modify: 0, delete: 0, rename: 0, setPassword: 0, unchanged: 0, conflict: 1, invalid: 0 },
  items: [],
} as unknown as Plan;

describe("PlanSummary", () => {
  it("says the rest is sent when the plan may be applied in part", () => {
    const html = renderToStaticMarkup(<PlanSummary plan={plan} />);
    expect(html).toContain("Conflicts and invalid changes are reported, not attempted.");
    expect(html).toContain("sends only the changes that would do something");
  });

  it("does not say the rest is sent when the plan is applied whole or not at all", () => {
    const html = renderToStaticMarkup(<PlanSummary plan={plan} whole />);
    expect(html).toContain("Conflicts and invalid changes are reported, not attempted.");
    expect(html).not.toContain("sends only the changes");
  });
});
