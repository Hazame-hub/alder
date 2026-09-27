import { describe, expect, it } from "vitest";
import { looksLocked } from "./policy";

// The one fact worth showing before anybody opens anything: this account
// cannot log in. Read from the entry already in hand, so it costs no request —
// and read conservatively, because a badge that cries wolf gets ignored.

const value = (text: string) => ({ text });

describe("whether an entry looks locked", () => {
  it("sees each server's own way of saying it", () => {
    expect(looksLocked([{ name: "pwdAccountLockedTime", values: [value("20260101000000Z")] }])).toBe(true);
    expect(looksLocked([{ name: "nsAccountLock", values: [value("true")] }])).toBe(true);
    // Case is the server's business, not a reason to miss a locked account.
    expect(looksLocked([{ name: "nsaccountlock", values: [value("TRUE")] }])).toBe(true);
  });

  it("does not call an account locked because the attribute exists", () => {
    expect(looksLocked([{ name: "nsAccountLock", values: [value("false")] }])).toBe(false);
    expect(looksLocked([{ name: "pwdAccountLockedTime", values: [] }])).toBe(false);
  });

  it("says nothing about an ordinary entry", () => {
    expect(looksLocked([{ name: "cn", values: [value("Alice")] }, { name: "uid", values: [value("alice")] }])).toBe(
      false,
    );
    expect(looksLocked([])).toBe(false);
  });
});
