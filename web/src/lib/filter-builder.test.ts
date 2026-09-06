import { describe, expect, it } from "vitest";
import {
  buildFilter,
  escapeFilterValue,
  isAttributeName,
  renderClause,
} from "./filter-builder";

describe("escapeFilterValue", () => {
  it("turns metacharacters into escaped assertion values", () => {
    expect(escapeFilterValue("a*b")).toBe("a\\2ab");
    expect(escapeFilterValue("(x)")).toBe("\\28x\\29");
    expect(escapeFilterValue("a\\b")).toBe("a\\5cb");
  });

  it("leaves an ordinary value alone", () => {
    expect(escapeFilterValue("Bob Adler")).toBe("Bob Adler");
  });
});

describe("isAttributeName", () => {
  it("accepts the shapes RFC 4512 allows", () => {
    for (const ok of ["cn", "objectClass", "employeeNumber", "x-custom-attr"]) {
      expect(isAttributeName(ok), ok).toBe(true);
    }
  });

  it("accepts attribute options and numeric OIDs", () => {
    expect(isAttributeName("userCertificate;binary")).toBe(true);
    expect(isAttributeName("2.5.4.3")).toBe(true);
  });

  it("rejects anything that would change the filter's structure", () => {
    // The reason this function exists. Folded into a filter unchecked, each of
    // these writes a query into the box that is not the one described by the
    // row the operator filled in.
    for (const bad of [
      "cn)(objectClass=*",
      "(cn",
      "cn=x",
      "cn*",
      "a b",
      "",
      "   ",
      "1cn",
    ]) {
      expect(isAttributeName(bad), bad).toBe(false);
    }
  });
});

describe("renderClause", () => {
  it("escapes the value", () => {
    expect(renderClause({ attribute: "cn", op: "eq", value: "a*b" })).toBe(
      "(cn=a\\2ab)",
    );
  });

  it("does not escape the attribute, it refuses it", () => {
    // RFC 4515 escaping is for assertion values. An attribute holding a
    // parenthesis is not a name needing escaping — it is not a name, and
    // "\28foo" would be an invalid filter that is also unreadable.
    expect(renderClause({ attribute: "cn)(uid", op: "eq", value: "x" })).toBeNull();
  });

  it("renders each operator", () => {
    const c = (op: string) => renderClause({ attribute: "cn", op, value: "bob" });
    expect(c("contains")).toBe("(cn=*bob*)");
    expect(c("starts")).toBe("(cn=bob*)");
    expect(c("ends")).toBe("(cn=*bob)");
    expect(c("present")).toBe("(cn=*)");
    expect(c("gte")).toBe("(cn>=bob)");
    expect(c("lte")).toBe("(cn<=bob)");
    expect(c("not")).toBe("(!(cn=bob))");
  });
});

describe("buildFilter", () => {
  it("returns a single clause unwrapped", () => {
    expect(buildFilter("and", [{ attribute: "cn", op: "eq", value: "bob" }])).toBe(
      "(cn=bob)",
    );
  });

  it("joins several", () => {
    const f = buildFilter("and", [
      { attribute: "objectClass", op: "eq", value: "inetOrgPerson" },
      { attribute: "cn", op: "starts", value: "Bob" },
    ]);
    expect(f).toBe("(&(objectClass=inetOrgPerson)(cn=Bob*))");
  });

  it("honours the or join", () => {
    const f = buildFilter("or", [
      { attribute: "uid", op: "eq", value: "a" },
      { attribute: "uid", op: "eq", value: "b" },
    ]);
    expect(f).toBe("(|(uid=a)(uid=b))");
  });

  it("skips a row still being filled in", () => {
    const f = buildFilter("and", [
      { attribute: "cn", op: "eq", value: "bob" },
      { attribute: "", op: "eq", value: "" },
    ]);
    expect(f).toBe("(cn=bob)");
  });

  it("never folds an unusable attribute into the filter", () => {
    // Before this, the attribute went in raw: the filter below would have read
    // (&(cn=bob)(x)(objectClass=*)=y) — a different query from the one the two
    // rows describe, written into a box the operator is invited to trust.
    const f = buildFilter("and", [
      { attribute: "cn", op: "eq", value: "bob" },
      { attribute: "x)(objectClass=*", op: "eq", value: "y" },
    ]);
    expect(f).toBe("(cn=bob)");
    expect(f).not.toContain("objectClass=*");
  });

  it("falls back to a filter that matches everything", () => {
    expect(buildFilter("and", [])).toBe("(objectClass=*)");
  });
});
