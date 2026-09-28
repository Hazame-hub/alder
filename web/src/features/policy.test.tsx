import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntryUnlockButton } from "./policy";
import { looksLocked } from "@/lib/locked";
import type { components } from "@/lib/api.gen";

type PolicyReport = components["schemas"]["PolicyReport"];

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

// The badge said "account locked" and offered nothing. The cure was in a
// dialog nobody was told to open, and before 1.29 that dialog did not have it
// either: the only way through was a hand-written LDIF modify in the import
// screen. An audit measured seventeen interactions between knowing the answer
// and applying it.

const DN = "uid=user0007,ou=people,dc=alder,dc=test";

const report = (unlock: PolicyReport["unlock"]): PolicyReport =>
  ({
    dn: DN,
    disclaimer: "what the server records, not a decision about whether a bind would succeed",
    state: {
      locked: true,
      lockedDetail: "the account is administratively locked",
      mustChange: false,
      attributes: [],
    },
    unlock,
  }) as PolicyReport;

/** The rendered words, with the markup taken out. */
function textOf(html: string): string {
  return html
    .replace(/<[^>]+>/g, " ")
    .replace(/&#x27;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
}

function headerMarkup(data: PolicyReport | undefined, readOnly = false): string {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  if (data) client.setQueryData(["policy", DN], data);
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <EntryUnlockButton dn={DN} readOnly={readOnly} />
    </QueryClientProvider>,
  );
}

describe("the unlock beside the badge", () => {
  it("offers the change the report derived", () => {
    const html = headerMarkup(
      report({
        attributes: ["nsAccountLock"],
        why: "clears the administrative lock on the account",
        change: { dn: DN, type: "modify", mods: [{ op: "delete", name: "nsAccountLock" }] },
      }),
    );
    // The word on the button, not only in its tooltip: a title attribute is
    // not shown to somebody who is looking at the screen rather than
    // hovering it, and the button is the whole point of the finding.
    expect(textOf(html)).toContain("Unlock");
    // The reason travels with it, so the button is not a verb with no
    // object. It is in the tooltip because the header has room for the
    // button and not the sentence; the dialog prints it in full.
    expect(html).toContain("nsAccountLock");
    expect(html).toContain("clears the administrative lock");
  });

  it("offers nothing for a lock Alder does not know how to clear", () => {
    // Saying "Unlock" and then failing is worse than saying nothing: the
    // Policy dialog explains an unrecognised lock in words, and a button
    // that cannot work would send somebody past the explanation.
    expect(headerMarkup(report(undefined))).toBe("");
  });

  it("offers nothing before the report has answered", () => {
    expect(headerMarkup(undefined)).toBe("");
  });

  it("offers nothing on a read-only session", () => {
    expect(
      headerMarkup(
        report({
          attributes: ["nsAccountLock"],
          why: "clears the administrative lock on the account",
          change: { dn: DN, type: "modify", mods: [{ op: "delete", name: "nsAccountLock" }] },
        }),
        true,
      ),
    ).toBe("");
  });
});
