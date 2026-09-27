import { describe, expect, it } from "vitest";
import type { components } from "@/lib/api.gen";
import {
  accessQuery,
  accessQueryKey,
  canAskAboutAnotherIdentity,
  dnEquals,
  rulesScopeNote,
  searchBaseFor,
  shortDn,
  subjectView,
} from "./access-subject";

// Asking the server about somebody else changes exactly one field of the
// report — the verdict. Everything pinned here exists to stop the screen
// implying it changed more than that.

describe("who the verdict is about", () => {
  it("is decided by what was asked, not by what the server echoed", () => {
    // The handler fills a blank `as` with the session's own bind DN before it
    // asks, so the echoed subject is non-empty for every bound session and
    // cannot tell "me" from "somebody else". Reading it that way is what made
    // the panel say "this identity" to a person asking about themselves.
    expect(subjectView("", "cn=admin,dc=alder,dc=test")).toEqual({
      kind: "you",
      dn: "cn=admin,dc=alder,dc=test",
    });
    expect(subjectView("cn=svc,ou=services,dc=alder,dc=test", "cn=admin,dc=alder,dc=test").kind).toBe("other");
  });

  it("calls it yours when the identity asked about is the one you are bound as", () => {
    const view = subjectView("CN=Admin, DC=alder, DC=test ", "cn=admin, dc=alder, dc=test");
    expect(view.kind).toBe("you");
  });

  it("says nothing about scope when the question is about the reader", () => {
    expect(rulesScopeNote(subjectView("", "cn=admin,dc=alder,dc=test"), true)).toBe("");
    expect(rulesScopeNote(subjectView("", "cn=admin,dc=alder,dc=test"), false)).toBe("");
  });

  it("keeps the rules list honest while somebody else's verdict is on screen", () => {
    // The whole point. The rules are about the entry; only the verdict is
    // about the person. A screen that changed the subject and left the rules
    // looking like an answer about them would be the failure this view exists
    // to avoid.
    const view = subjectView("cn=svc-alder,ou=services,dc=alder,dc=test", "cn=admin,dc=alder,dc=test");
    const note = rulesScopeNote(view, true);
    expect(note).toContain("the same list whoever you ask about");
    expect(note).toContain("only the verdict above is about cn=svc-alder");
  });

  it("does not point at a verdict the server declined to give", () => {
    // Not an edge case: on 389 DS every non-root bind asking about somebody
    // else gets a numeric error code where the rights letters go, which
    // becomes "the server declined to say" and no verdict at all. The old
    // wording named something the reader could not see.
    const view = subjectView("cn=svc-alder,ou=services,dc=alder,dc=test", "cn=admin,dc=alder,dc=test");
    const note = rulesScopeNote(view, false);
    expect(note).toContain("the same list whoever you ask about");
    expect(note).not.toContain("the verdict above");
    expect(note).toContain("gave no verdict about cn=svc-alder");
  });
});

describe("the request the control sends", () => {
  it("carries the identity asked about, and nothing when none was", () => {
    // The one line that makes the control do anything. Deleting `as` here
    // turns the whole feature inert: the heading changes and the verdict
    // does not, which is the misattributed-verdict failure it exists to
    // avoid.
    expect(accessQuery("uid=a,dc=x", "cn=svc,dc=x")).toEqual({ dn: "uid=a,dc=x", as: "cn=svc,dc=x" });
    expect(accessQuery("uid=a,dc=x", "")).toEqual({ dn: "uid=a,dc=x" });
    expect(accessQuery("uid=a,dc=x", "   ")).toEqual({ dn: "uid=a,dc=x" });
  });

  it("keys the cache on the identity, so a new subject is not answered from the old one", () => {
    expect(accessQueryKey("uid=a,dc=x", "cn=svc,dc=x")).not.toEqual(accessQueryKey("uid=a,dc=x", ""));
  });
});

const caps = (effectiveRights: boolean): components["schemas"]["Capabilities"] => ({
  namingContexts: ["dc=alder,dc=test"],
  subschemaSubentry: "cn=Subschema",
  paging: true,
  effectiveRights,
});

describe("whether the question can be asked at all", () => {
  it("is offered only where the server publishes an effective-rights control", () => {
    // Without one, the response is byte-identical whatever identity is named.
    // A control that changed nothing would make Alder's reading of the rule
    // text look like the server's answer about a person.
    expect(canAskAboutAnotherIdentity(caps(true))).toBe(true);
    expect(canAskAboutAnotherIdentity(caps(false))).toBe(false);
    expect(canAskAboutAnotherIdentity(undefined)).toBe(false);
  });
});

describe("comparing and shortening DNs", () => {
  it("folds case and trims, and never calls two blanks equal", () => {
    expect(dnEquals("CN=A,DC=x", " cn=a,dc=x ")).toBe(true);
    expect(dnEquals("cn=a,dc=x", "cn=b,dc=x")).toBe(false);
    expect(dnEquals("", "")).toBe(false);
    expect(dnEquals(undefined, "cn=a")).toBe(false);
  });

  it("sees through the spacing, because the two sides are typed by different hands", () => {
    // The bind DN is what the operator typed on the connect screen, kept
    // verbatim; the subject is what the picker returned in the server's own
    // spelling. Comparing them raw printed "not you" over a person's own
    // rights, which is an affirmative falsehood rather than a missing
    // nicety.
    expect(dnEquals("cn=admin, dc=alder, dc=test", "cn=admin,dc=alder,dc=test")).toBe(true);
    expect(dnEquals("cn = admin,dc=alder,dc=test", "cn=admin,dc=alder,dc=test")).toBe(true);
    expect(dnEquals("cn=a+sn=b,dc=x", "cn=a + sn=b,dc=x")).toBe(true);
    // And still says no to two different accounts.
    expect(dnEquals("cn=admin, dc=alder, dc=test", "cn=admin2,dc=alder,dc=test")).toBe(false);
  });

  it("shortens to the RDN for a heading that has to stay on one line", () => {
    expect(shortDn("cn=svc-alder,ou=services,dc=alder,dc=test")).toBe("cn=svc-alder");
    expect(shortDn("dc=alder")).toBe("dc=alder");
    expect(shortDn("")).toBe("");
  });
});

describe("where a picker should search", () => {
  it("uses the most specific naming context that contains the entry", () => {
    const contexts = ["dc=alder,dc=test", "dc=other,dc=test"];
    expect(searchBaseFor("uid=a,ou=people,dc=alder,dc=test", contexts)).toBe("dc=alder,dc=test");
    expect(searchBaseFor("dc=alder,dc=test", contexts)).toBe("dc=alder,dc=test");
  });

  it("falls back to the entry's own parent rather than to nothing", () => {
    // A configuration entry is below no naming context the server publishes,
    // and a picker with an empty base searches the RootDSE and finds nobody.
    expect(searchBaseFor("olcDatabase={1}mdb,cn=config", ["dc=alder,dc=test"])).toBe("cn=config");
    expect(searchBaseFor("cn=config", undefined)).toBe("cn=config");
  });
});
