import { describe, expect, it } from "vitest";
import { jumpShortcut } from "./shortcut";

describe("how the jump shortcut is written", () => {
  it("uses the command key on Apple platforms", () => {
    for (const p of ["MacIntel", "macOS", "iPhone", "iPad"]) {
      expect(jumpShortcut(p)).toBe("⌘K");
    }
  });

  it("uses Ctrl everywhere else", () => {
    for (const p of ["Win32", "Windows", "Linux x86_64", "FreeBSD"]) {
      expect(jumpShortcut(p)).toBe("Ctrl K");
    }
  });

  it("falls back to Ctrl rather than guessing when the platform is unreadable", () => {
    // Right on Windows and Linux, which is where an unreadable platform is
    // most likely to be; wrong only on a Mac that reports nothing, where the
    // keystroke is one press away from being discovered anyway.
    expect(jumpShortcut("")).toBe("Ctrl K");
  });

  it("reads the running platform when asked nothing", () => {
    expect(["⌘K", "Ctrl K"]).toContain(jumpShortcut());
  });
});
