import { describe, expect, it } from "vitest";
import { ago, formatMoment } from "./replication";

// How long ago a server's last change was is the number this whole view turns
// on, and it is read at a glance rather than computed. These are the two ways
// it can be wrong on screen: a clock that disagrees, and a value that is not
// a moment at all.

const now = new Date("2026-09-27T12:00:00Z");

describe("how long ago a change was", () => {
  it("uses the unit a person would", () => {
    expect(ago("2026-09-27T11:59:30Z", now)).toBe("30s ago");
    expect(ago("2026-09-27T11:45:00Z", now)).toBe("15m ago");
    expect(ago("2026-09-27T09:00:00Z", now)).toBe("3h ago");
    expect(ago("2026-09-25T12:00:00Z", now)).toBe("2d ago");
  });

  it("says so rather than counting backwards when the peer's clock is ahead", () => {
    // A supplier whose clock runs fast writes a change stamped in our future.
    // "-40s ago" reads as a bug in Alder; the disagreement is the fact.
    expect(ago("2026-09-27T12:00:40Z", now)).toBe("in the future by this server's clock");
  });

  it("shows nothing for a value that is not a moment", () => {
    expect(ago("", now)).toBe("");
    expect(ago("not a date", now)).toBe("");
  });
});

describe("a moment on screen", () => {
  it("falls back to the server's own text rather than Invalid Date", () => {
    expect(formatMoment("not a date")).toBe("not a date");
  });

  it("renders a real timestamp as something", () => {
    expect(formatMoment("2026-09-27T12:00:00Z")).not.toBe("");
    expect(formatMoment("2026-09-27T12:00:00Z")).not.toContain("Invalid");
  });
});
