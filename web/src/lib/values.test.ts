import { describe, expect, it } from "vitest";
import { booleanPairFor, displayText, rdnOf, parentOf, splitDN } from "./values";

describe("booleanPairFor", () => {
  it("recognises the vocabularies a directory actually uses", () => {
    expect(booleanPairFor("on")).toEqual(["on", "off"]);
    expect(booleanPairFor("off")).toEqual(["on", "off"]);
    expect(booleanPairFor("yes")).toEqual(["yes", "no"]);
    expect(booleanPairFor("no")).toEqual(["yes", "no"]);
    expect(booleanPairFor("true")).toEqual(["true", "false"]);
    expect(booleanPairFor("enabled")).toEqual(["enabled", "disabled"]);
  });

  it("never treats 0 or 1 as a boolean", () => {
    // nsslapd-auditlog-logrotationsyncmin holds 0, and its unit is minutes.
    // Forty fields on one 389 DS config entry hold 0 or 1 and are numbers;
    // nothing in the value distinguishes them from a switch.
    expect(booleanPairFor("0")).toBeNull();
    expect(booleanPairFor("1")).toBeNull();
  });

  it("offers the pair in the casing the server used", () => {
    expect(booleanPairFor("OFF")).toEqual(["ON", "OFF"]);
    expect(booleanPairFor("Off")).toEqual(["On", "Off"]);
    expect(booleanPairFor("off")).toEqual(["on", "off"]);
  });

  it("says nothing about a value that is not one of them", () => {
    expect(booleanPairFor("")).toBeNull();
    expect(booleanPairFor("   ")).toBeNull();
    expect(booleanPairFor("maybe")).toBeNull();
    expect(booleanPairFor("16384")).toBeNull();
    expect(booleanPairFor("none, peer, all")).toBeNull();
  });

  it("ignores surrounding whitespace when matching", () => {
    expect(booleanPairFor("  on  ")).toEqual(["on", "off"]);
  });
});

describe("displayText", () => {
  it("returns text as it is", () => {
    expect(displayText({ text: "alice", size: 5 })).toBe("alice");
  });

  it("describes a binary value rather than showing it", () => {
    expect(displayText({ base64: "/9j/4AAQ", size: 2048 })).toBe("«2.0 kB of binary data»");
  });

  it("returns nothing for a value that is neither", () => {
    expect(displayText({})).toBe("");
  });
});

describe("DN handling", () => {
  it("splits on unescaped commas only", () => {
    expect(splitDN("cn=Smith\\, John,ou=people,dc=test")).toEqual([
      "cn=Smith\\, John",
      "ou=people",
      "dc=test",
    ]);
  });

  it("reads the leftmost RDN and the parent", () => {
    expect(rdnOf("uid=user0001,ou=people,dc=alder,dc=test")).toBe("uid=user0001");
    expect(parentOf("uid=user0001,ou=people,dc=alder,dc=test")).toBe(
      "ou=people,dc=alder,dc=test",
    );
  });

  it("handles a suffix, which has no parent", () => {
    expect(parentOf("dc=alder,dc=test")).toBe("dc=test");
    expect(parentOf("dc=test")).toBe("");
  });

  it("keeps a non-ASCII RDN intact", () => {
    expect(rdnOf("ou=Zweigstelle München,ou=services,dc=alder,dc=test")).toBe(
      "ou=Zweigstelle München",
    );
  });
});
