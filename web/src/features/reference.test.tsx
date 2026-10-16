import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { SessionInfo } from "@/lib/api";
import { serverLabel } from "@/lib/reference";
import { AppliesTo } from "@/components/applies-to";
import { TooltipProvider } from "@/components/ui";
import { ReferenceChip } from "./reference";

// A second server inside one session (B6). Two servers on one screen are only
// safe if every place shows which is which: the header names the reference
// and says it is read-only, and every write dialog names where it writes.

const session: SessionInfo = {
  connected: true,
  role: "session",
  host: "ldap-primary.example.test",
  port: 636,
  tls: "ldaps",
  bindDn: "cn=admin,dc=example,dc=test",
};

const reference: SessionInfo = {
  connected: true,
  role: "reference",
  host: "ldap-replica.example.test",
  port: 636,
  tls: "ldaps",
  readOnly: true,
};

function render(node: React.ReactNode, seed: (qc: QueryClient) => void): string {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  seed(qc);
  return renderToStaticMarkup(
    <QueryClientProvider client={qc}>
      <TooltipProvider>{node}</TooltipProvider>
    </QueryClientProvider>,
  );
}

function textOf(html: string): string {
  return html.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
}

describe("the reference in the header", () => {
  it("offers to add one when none is attached", () => {
    const html = render(<ReferenceChip session={session} />, (qc) => qc.setQueryData(["reference"], null));
    expect(textOf(html)).toContain("Add a reference");
    expect(html).not.toContain("reference-chip");
  });

  it("names the reference and says it is read-only once attached", () => {
    const html = render(<ReferenceChip session={session} />, (qc) => qc.setQueryData(["reference"], reference));
    const text = textOf(html);
    expect(text).toContain("ldap-replica.example.test:636");
    expect(text).toContain("read-only");
    expect(html).toContain('aria-label="Detach the reference"');
  });
});

describe("where a write goes", () => {
  it("names the session's server, never the reference", () => {
    const html = render(<AppliesTo />, (qc) => {
      qc.setQueryData(["session"], session);
      qc.setQueryData(["reference"], reference);
    });
    const text = textOf(html);
    expect(text).toContain("Applies to ldap-primary.example.test:636");
    expect(text).not.toContain("ldap-replica");
  });
});

describe("serverLabel", () => {
  it("is host:port, and empty for nothing", () => {
    expect(serverLabel(reference)).toBe("ldap-replica.example.test:636");
    expect(serverLabel({ host: "h" })).toBe("h");
    expect(serverLabel(null)).toBe("");
  });
});
