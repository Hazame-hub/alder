import { describe, expect, it } from "vitest";
import { buildAddChange, escapeRDNValue, type AddChangeInput } from "./create";

const base: AddChangeInput = {
  parentDN: "ou=people,dc=alder,dc=test",
  structural: "inetOrgPerson",
  auxiliary: [],
  rdnAttr: "cn",
  rdnValue: "Alice Adler",
  attributes: ["cn", "sn"],
  values: { cn: [], sn: ["Adler"] },
};

const valuesOf = (change: ReturnType<typeof buildAddChange>, name: string) =>
  change?.attributes?.find((a) => a.name === name)?.values.map((v) => v.text);

describe("buildAddChange", () => {
  it("writes top and the chosen classes, and nothing else", () => {
    const change = buildAddChange({ ...base, auxiliary: ["posixAccount"] });
    // Not the superior chain: a directory infers organizationalPerson and
    // person itself, and writing them down is something to go stale.
    expect(valuesOf(change, "objectClass")).toEqual([
      "top",
      "inetOrgPerson",
      "posixAccount",
    ]);
  });

  it("fills the naming attribute with the naming value", () => {
    const change = buildAddChange(base);
    expect(change?.dn).toBe("cn=Alice Adler,ou=people,dc=alder,dc=test");
    expect(valuesOf(change, "cn")).toEqual(["Alice Adler"]);
  });

  it("puts the naming value first when the field holds others too", () => {
    const change = buildAddChange({
      ...base,
      values: { cn: ["Alice A. Adler"], sn: ["Adler"] },
    });
    // The RDN names one specific value; it has to be present, and reading it
    // first is what makes the LDIF say what the DN means.
    expect(valuesOf(change, "cn")).toEqual(["Alice Adler", "Alice A. Adler"]);
  });

  it("does not duplicate the naming value when it is already typed", () => {
    const change = buildAddChange({
      ...base,
      values: { cn: ["Alice Adler"], sn: ["Adler"] },
    });
    expect(valuesOf(change, "cn")).toEqual(["Alice Adler"]);
  });

  it("drops fields left blank rather than sending empty values", () => {
    const change = buildAddChange({
      ...base,
      attributes: ["cn", "sn", "title", "mail"],
      values: { sn: ["Adler"], title: ["  "], mail: [] },
    });
    const names = change?.attributes?.map((a) => a.name);
    expect(names).toEqual(["objectClass", "sn", "cn"]);
  });

  it("composes nothing until there is a class and a name", () => {
    expect(buildAddChange({ ...base, structural: "" })).toBeNull();
    expect(buildAddChange({ ...base, rdnValue: "   " })).toBeNull();
    expect(buildAddChange({ ...base, rdnAttr: "" })).toBeNull();
  });

  it("matches the naming attribute case-insensitively", () => {
    // The form offers the schema's spelling; a directory does not care, and
    // treating CN and cn as different would write the value twice.
    const change = buildAddChange({
      ...base,
      rdnAttr: "CN",
      values: { CN: [], sn: ["Adler"] },
      attributes: ["cn", "sn"],
    });
    const cn = change?.attributes?.filter((a) => a.name.toLowerCase() === "cn");
    expect(cn).toHaveLength(1);
  });
});

describe("escapeRDNValue", () => {
  it("escapes the characters RFC 4514 reserves", () => {
    expect(escapeRDNValue("Smith, John")).toBe("Smith\\, John");
    expect(escapeRDNValue('say "hi"')).toBe('say \\"hi\\"');
    expect(escapeRDNValue("a+b")).toBe("a\\+b");
    expect(escapeRDNValue("a;b")).toBe("a\\;b");
    expect(escapeRDNValue("a<b>c")).toBe("a\\<b\\>c");
    expect(escapeRDNValue("back\\slash")).toBe("back\\\\slash");
  });

  it("escapes a leading hash, which would otherwise mean a hex-encoded value", () => {
    expect(escapeRDNValue("#1")).toBe("\\#1");
  });

  it("escapes leading and trailing spaces, which are otherwise dropped", () => {
    expect(escapeRDNValue(" leading")).toBe("\\ leading");
    expect(escapeRDNValue("trailing ")).toBe("trailing\\ ");
  });

  it("leaves an ordinary value alone, including non-ASCII", () => {
    expect(escapeRDNValue("Zweigstelle München")).toBe("Zweigstelle München");
    expect(escapeRDNValue("user0001")).toBe("user0001");
  });

  it("produces a DN a value with a comma cannot break out of", () => {
    const change = buildAddChange({ ...base, rdnValue: "Adler, Alice" });
    expect(change?.dn).toBe("cn=Adler\\, Alice,ou=people,dc=alder,dc=test");
  });
});
