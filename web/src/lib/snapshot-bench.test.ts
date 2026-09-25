import { beforeEach, describe, expect, it } from "vitest";
import { bench, type Comparison, type Slot } from "./snapshot-bench";

// The bench is what an operator has in front of them: the documents loaded,
// the two sides, and the comparison. Its whole point is that it outlives the
// view — leaving for the changeset and coming back used to find it empty.

const slot = (name: "A" | "B"): Slot => ({
  name,
  raw: "{}",
  doc: {
    version: 1,
    kind: "config",
    createdAt: "2026-09-24T19:49:38Z",
    source: { provider: "openldap", root: "cn=config" },
    counts: { settings: 88, resources: 6, withheld: 2 },
  } as Slot["doc"],
  inspection: { kind: "config", createdAt: "2026-09-24T19:49:38Z" } as Slot["inspection"],
  filename: `${name}.json`,
  origin: "uploaded",
  bytes: 27_000,
});

const comparison = { diff: { kind: "config" }, sourceLabel: "The directory now", targetLabel: "Snapshot A" } as Comparison;

describe("the snapshot bench", () => {
  beforeEach(() => bench.clear());

  it("keeps the documents and the comparison after the view has gone", () => {
    bench.load(slot("A"));
    bench.setComparison(comparison);

    // What a remount reads: the store, not component state.
    expect(bench.state().slots.A?.filename).toBe("A.json");
    expect(bench.state().comparison?.targetLabel).toBe("Snapshot A");
  });

  it("drops a comparison that is no longer about what is loaded", () => {
    bench.load(slot("A"));
    bench.setComparison(comparison);
    bench.load(slot("B"));
    expect(bench.state().comparison).toBeNull();

    bench.setComparison(comparison);
    bench.setSide("target", "B");
    expect(bench.state().comparison).toBeNull();
    expect(bench.state().target).toBe("B");
  });

  it("starts from the directory as it is now, against snapshot A", () => {
    expect(bench.state().source).toBe("live");
    expect(bench.state().target).toBe("A");
  });

  it("is emptied when the session ends", () => {
    bench.load(slot("A"));
    bench.setSide("source", "B");
    bench.setComparison(comparison);
    bench.clear();
    expect(bench.state()).toEqual({ slots: {}, source: "live", target: "A", comparison: null });
  });
});
