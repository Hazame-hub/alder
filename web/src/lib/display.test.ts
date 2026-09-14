import { describe, expect, it } from "vitest";
import { hasUnsafeText, safeText } from "@/lib/display";

const RLO = String.fromCharCode(0x202e);

describe("safeText", () => {
  it("shows a right-to-left override as its escape", () => {
    const dn = `uid=report${RLO}txt.exe,ou=people,dc=alder,dc=test`;
    expect(safeText(dn)).toBe("uid=report\\u202etxt.exe,ou=people,dc=alder,dc=test");
    expect(safeText(dn)).not.toContain(RLO);
  });

  it("escapes every bidi override, embedding, isolate and mark", () => {
    for (const cp of [0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069, 0x200e, 0x200f, 0x061c]) {
      const shown = safeText(`a${String.fromCharCode(cp)}b`);
      expect(shown).toBe(`a\\u${cp.toString(16).padStart(4, "0")}b`);
    }
  });

  it("escapes C0 controls, DEL and C1 controls, and keeps tab and line feed", () => {
    expect(safeText("a" + String.fromCharCode(0) + "b" + String.fromCharCode(7) + "c" + String.fromCharCode(0x1b) + "[2Jd")).toBe(
      "a\\u0000b\\u0007c\\u001b[2Jd",
    );
    expect(safeText("x" + String.fromCharCode(0x7f) + "y" + String.fromCharCode(0x9b) + "z" + String.fromCharCode(13))).toBe(
      "x\\u007fy\\u009bz\\u000d",
    );
    expect(safeText("line one\nline two\tindented")).toBe("line one\nline two\tindented");
  });

  it("leaves an ordinary DN and ordinary Unicode exactly as they are", () => {
    for (const text of [
      "uid=alice,ou=people,dc=alder,dc=test",
      "cn=Zoë Ångström,ou=people,dc=alder,dc=test",
      "cn=José Müller\\, Jr.,dc=alder",
      "cn=山田太郎,dc=alder",
      "cn=مرحبا,dc=alder",
      "cn=שלום,dc=alder",
      "cn=😀,dc=alder",
    ]) {
      expect(safeText(text)).toBe(text);
      expect(hasUnsafeText(text)).toBe(false);
    }
  });

  it("is stable when applied twice, and reports what it would change", () => {
    const once = safeText(`a${RLO}b`);
    expect(safeText(once)).toBe(once);
    expect(hasUnsafeText(`a${RLO}b`)).toBe(true);
    expect(hasUnsafeText(once)).toBe(false);
  });
});
