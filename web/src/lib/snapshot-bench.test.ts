import { beforeEach, describe, expect, it } from "vitest";
import { bench } from "./snapshot-bench";
import { changeset } from "./changeset";
import type { components } from "@/lib/api.gen";

type Diff = components["schemas"]["Diff"];

// A comparison whose source is "the directory now" stops being true the
// moment anything is applied. The result is kept -- it is the only record of
// what *else* differed, which is what somebody halfway through fixing a
// drift wants beside the changeset -- but a panel that does not say it is
// out of date invites applying the same change twice. An audit came back
// from a successful apply to a panel still offering the three indexes it had
// just created.

const diff = { kind: "config" } as Diff;

beforeEach(() => {
  bench.clear();
  changeset.clear();
});

describe("a comparison that the directory has moved past", () => {
  it("is marked when changes are applied", () => {
    bench.setComparison({ diff, sourceLabel: "the directory now", targetLabel: "Snapshot A" });
    expect(bench.state().comparison?.stale).toBeFalsy();
    changeset.removeApplied(["change-1"]);
    expect(bench.state().comparison?.stale).toBe(true);
  });

  it("is kept, not thrown away", () => {
    // Losing it costs the operator the record of everything else that
    // differed, which is the reason the bench outlives a screen at all.
    bench.setComparison({ diff, sourceLabel: "the directory now", targetLabel: "Snapshot A" });
    changeset.removeApplied(["change-1"]);
    expect(bench.state().comparison?.diff).toBe(diff);
    expect(bench.state().comparison?.sourceLabel).toBe("the directory now");
  });

  it("is not marked when an apply run changed nothing", () => {
    bench.setComparison({ diff, sourceLabel: "the directory now", targetLabel: "Snapshot A" });
    changeset.removeApplied([]);
    expect(bench.state().comparison?.stale).toBeFalsy();
  });

  it("starts fresh again once compared", () => {
    bench.setComparison({ diff, sourceLabel: "the directory now", targetLabel: "Snapshot A" });
    changeset.removeApplied(["change-1"]);
    bench.setComparison({ diff, sourceLabel: "the directory now", targetLabel: "Snapshot A" });
    expect(bench.state().comparison?.stale).toBeFalsy();
  });
});
