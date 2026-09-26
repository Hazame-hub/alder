import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReviewActions, reviewsInDialog } from "./review-actions";
import type { ChangeRequest } from "@/lib/api";

// What a comparison offers once something is ticked. Clicking is checked in a
// real browser; what is pinned here is the wording, and which review a
// selection goes to.

const change = (dn: string): { change: ChangeRequest; label: string } => ({
  change: { dn, type: "modify", mods: [{ op: "replace", name: "olcIdleTimeout", values: [{ text: "0" }] }] },
  label: `config olcIdleTimeout on ${dn}`,
});

function markup(changes: { change: ChangeRequest; label: string }[]): string {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderToStaticMarkup(
    <QueryClientProvider client={client}>
      <ReviewActions changes={changes} onReviewChangeset={() => {}} />
    </QueryClientProvider>,
  );
}

function textOf(html: string): string {
  return html.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
}

describe("what a comparison offers", () => {
  it("names the review by the number of changes, and always offers the changeset", () => {
    expect(textOf(markup([change("cn=config")]))).toContain("Review 1 change");
    expect(textOf(markup([change("cn=config"), change("olcDatabase={1}mdb,cn=config")]))).toContain("Review 2 changes");
    expect(textOf(markup([change("cn=config")]))).toContain("Add to changeset");
  });

  it("offers nothing to do when nothing is selected", () => {
    expect(markup([])).toContain("disabled");
  });

  it("reviews one change in the dialog and several in the changeset", () => {
    // One change reaches the same dialog as an edit anywhere else in Alder,
    // rather than a second screen; a set is what the changeset is for.
    expect(reviewsInDialog(1)).toBe(true);
    expect(reviewsInDialog(2)).toBe(false);
    expect(reviewsInDialog(0)).toBe(false);
  });
});
