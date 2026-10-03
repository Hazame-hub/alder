import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { UnknownOutcome } from "./unknown-outcome";
import type { ChangeRequest } from "@/lib/api";

// What the panel is allowed to say.
//
// Every assertion here is about a sentence somebody reads after a change
// went out and nothing came back. The failure that matters is not an ugly
// panel; it is a panel that reads as "this failed", because the next thing
// that happens is the change gets made a second time.

const change: ChangeRequest = {
  dn: "uid=alice,ou=people,dc=alder,dc=test",
  type: "delete",
};

function markup(node: React.ReactElement): string {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderToStaticMarkup(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

function textOf(html: string): string {
  return html
    .replace(/<[^>]+>/g, " ")
    .replace(/&#x27;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
}

const panel = () => textOf(markup(<UnknownOutcome change={change} />));

describe("the panel for a change nobody can account for", () => {
  it("says the change may already have been applied", () => {
    expect(panel()).toContain("may already have been applied");
  });

  it("never says the change failed, or was refused, or did not happen", () => {
    // The whole reason this is not an ErrorNote.
    const text = panel().toLowerCase();
    for (const wrong of [
      "refused",
      "the change failed",
      "was not applied",
      "did not happen",
    ]) {
      expect(text).not.toContain(wrong);
    }
  });

  it("offers to read the directory, and says that reading changes nothing", () => {
    const text = panel();
    expect(text).toContain("Check the directory");
    expect(text).toContain("Changes nothing");
  });

  it("offers no way to send the change again", () => {
    // There is no retry button, and no word that invites one. A reader who
    // wants the change made goes back to the entry and makes it, with a
    // fresh plan and a fresh look at the LDIF.
    const text = panel().toLowerCase();
    for (const wrong of ["retry", "try again", "resend", "send again", "apply again"]) {
      expect(text).not.toContain(wrong);
    }
  });

  it("states plainly that Alder will not resend it", () => {
    expect(panel()).toContain("will not send it again");
  });
});
