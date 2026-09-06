import { describe, expect, it } from "vitest";
import { checkSubtree } from "./subtree-refusal";

/**
 * Each of these is a refusal that prevents a partial subtree delete.
 *
 * A run that stages some of a subtree removes the leaves it reached and leaves
 * every container standing — worse than not having started, and not something
 * the operator can see coming. So the default here is to refuse, and every
 * "ok" needs all four conditions to hold.
 */

const ok = { count: 10, truncated: false, paging: true, capacity: 500 };

describe("checkSubtree", () => {
  it("allows a subtree it can see completely and hold", () => {
    expect(checkSubtree(ok)).toEqual({ ok: true, count: 10 });
  });

  it("refuses when the server cannot page", () => {
    // Without paging a search is bounded by the server's own limit, silently.
    const v = checkSubtree({ ...ok, paging: false });
    expect(v.ok).toBe(false);
    if (!v.ok) expect(v.why).toMatch(/paged/i);
  });

  it("refuses when the count itself was truncated", () => {
    const v = checkSubtree({ ...ok, count: 10_000, truncated: true });
    expect(v.ok).toBe(false);
    // Formatted for the viewer's locale, so the separator is not ours to
    // assert — a thousands mark is a comma, a space or a stop depending on
    // where the reader is.
    if (!v.ok) expect(v.why).toContain((10_000).toLocaleString());
  });

  it("refuses when the subtree will not fit in what is left of the changeset", () => {
    const v = checkSubtree({ ...ok, count: 305, capacity: 196 });
    expect(v.ok).toBe(false);
    // Both numbers, or the operator cannot act on it.
    if (!v.ok) {
      expect(v.why).toContain("305");
      expect(v.why).toContain("196");
    }
  });

  it("says the basket is full rather than offering a number when it is", () => {
    const v = checkSubtree({ ...ok, capacity: 0 });
    expect(v.ok).toBe(false);
    if (!v.ok) expect(v.detail).toMatch(/apply or discard/i);
  });

  it("allows a subtree that fits exactly", () => {
    expect(checkSubtree({ ...ok, count: 200, capacity: 200 })).toEqual({
      ok: true,
      count: 200,
    });
  });

  it("refuses one entry past what will fit", () => {
    expect(checkSubtree({ ...ok, count: 201, capacity: 200 }).ok).toBe(false);
  });

  it("refuses an empty subtree, because a plain delete is the right action", () => {
    const v = checkSubtree({ ...ok, count: 0 });
    expect(v.ok).toBe(false);
    if (!v.ok) expect(v.detail).toMatch(/delete it directly/i);
  });

  it("reports the reason it hits first, and paging comes before everything", () => {
    // A server with no paging and a truncated count is refused for the paging,
    // because that is the fact that cannot be worked around by deleting less.
    const v = checkSubtree({ count: 9, truncated: true, paging: false, capacity: 0 });
    expect(v.ok).toBe(false);
    if (!v.ok) expect(v.why).toMatch(/paged/i);
  });
});
