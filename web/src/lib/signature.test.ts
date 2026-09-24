import { describe, expect, it } from "vitest";
import { SIGNATURE_LOOK, shortKey, signerLine, worthShowing } from "./signature";

describe("what a signature is shown as", () => {
  it("keeps the three answers apart", () => {
    expect(SIGNATURE_LOOK.verified.variant).toBe("success");
    expect(SIGNATURE_LOOK.untrusted.variant).toBe("warning");
    expect(SIGNATURE_LOOK.unsigned.variant).toBe("outline");
    // Every label says what it means without a legend.
    expect(SIGNATURE_LOOK.untrusted.label).toContain("not trusted");
  });

  it("shows an unsigned document as ordinary rather than as a problem", () => {
    expect(worthShowing({ status: "unsigned" })).toBe(false);
    expect(worthShowing(undefined)).toBe(false);
    expect(worthShowing({ status: "verified" })).toBe(true);
  });

  it("names the signer, or the key when nobody gave a name", () => {
    expect(signerLine({ keyId: "a".repeat(64), signer: "alice", signedAt: "2026-09-23T10:00:00Z", trusted: true })).toBe(
      "alice on 2026-09-23T10:00:00Z",
    );
    const key = "abcdef0123456789abcdef";
    expect(signerLine({ keyId: key, trusted: false })).toBe(`${shortKey(key)} (key not trusted)`);
  });

  it("shortens a key id to what a person can compare", () => {
    expect(shortKey("a".repeat(64))).toBe("aaaaaaaa…aaaaaaaa");
    expect(shortKey("short")).toBe("short");
  });
});
