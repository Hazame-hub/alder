import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ConnectScreen } from "./connect";

// The connection screen reads what this browser remembered before anything
// else renders, and what it reads is storage any script on the origin could
// have written. It is shown escaped, it never puts a password in a field, and
// the saved list says plainly when there is nothing in it.

const render = (items: Record<string, string>) => {
  vi.stubGlobal("window", {
    localStorage: {
      getItem: (k: string) => items[k] ?? null,
      setItem: () => {},
      removeItem: () => {},
    },
  });
  return renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <ConnectScreen onConnected={() => {}} />
    </QueryClientProvider>,
  );
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the connection screen", () => {
  it("escapes the remembered connection it describes", () => {
    const html = render({
      "alder.recent-connection.v1": JSON.stringify({
        host: "ldap.example.test",
        port: 636,
        bindDn: "uid=report‮txt.exe,dc=example,dc=test",
        bindPassword: "hunter2",
      }),
    });
    // The bind DN field holds the original string -- it is what gets sent, and
    // an input shows its value as text to edit -- but the sentence describing
    // the connection is display, and is escaped.
    const hint = html.slice(html.indexOf("Last connected to"), html.indexOf("Passwords are never"));
    expect(hint).toContain("uid=report\\u202etxt.exe");
    expect(hint).not.toContain("‮");
    expect(html).not.toContain("hunter2");
  });

  it("offers the saved list, and says when it is empty", () => {
    expect(render({})).toContain("No saved connections yet");
    const html = render({
      "alder.saved-connections.v1": JSON.stringify([
        { name: "prod", connection: { host: "prod.example.test", port: 636 } },
      ]),
    });
    expect(html).toContain("Choose a saved connection");
    expect(html).toContain("never with a password");
  });

  it("still renders when storage is denied outright", () => {
    vi.stubGlobal("window", {
      get localStorage(): Storage {
        throw new Error("SecurityError");
      },
    });
    const html = renderToStaticMarkup(
      <QueryClientProvider client={new QueryClient()}>
        <ConnectScreen onConnected={() => {}} />
      </QueryClientProvider>,
    );
    expect(html).toContain("No saved connections yet");
  });
});
