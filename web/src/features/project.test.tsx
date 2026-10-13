import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ApiFailure } from "@/lib/api";
import type { ProjectPlan } from "@/lib/project";
import { ProjectFailure, ProjectResult, UnmanagedList } from "./project";

// What Open project shows. Picking a folder is a browser's business; what is
// pinned here is the wording, that the heading is the session's directory,
// and that every string from the files or the directory is shown through
// safeText.

function textOf(html: string): string {
  return html.replace(/<[^>]+>/g, " ").replace(/&#x27;/g, "'").replace(/\s+/g, " ").trim();
}

const RLO = "‮";

const result: ProjectPlan = {
  project: {
    file: "alder.yaml",
    entries: 4,
    subtrees: [
      { base: "ou=people,dc=example,dc=com", files: ["people/a.ldif"], entries: 3 },
      { base: "ou=groups,dc=example,dc=com", files: ["groups.ldif"], entries: 1 },
    ],
    environments: ["production", "staging"],
  },
  host: "ldap.example.com:636",
  bindDn: "cn=admin,dc=example,dc=com",
  plan: {
    counts: { examined: 1, add: 1, modify: 0, delete: 0, rename: 0, setPassword: 0, unchanged: 0, conflict: 0 },
    items: [{ index: 0, dn: "uid=ada,ou=people,dc=example,dc=com", action: "add", exists: false }],
  },
  unmanaged: [
    { base: "ou=people,dc=example,dc=com", entries: [`uid=report${RLO}txt.exe,ou=people,dc=example,dc=com`] },
    { base: "ou=groups,dc=example,dc=com", entries: [], error: "the subtree could not be read whole" },
  ],
};

describe("a project's plan", () => {
  it("is headed by the project, then the session's directory, and says the environments are not used", () => {
    const text = textOf(renderToStaticMarkup(<ProjectResult folder="directory" result={result} onBrowse={() => {}} />));
    expect(text).toContain("Project directory/ — 2 subtrees, 4 entries, from alder.yaml");
    expect(text).toContain("Planned against ldap.example.com:636 as cn=admin,dc=example,dc=com");
    expect(text).toContain("this session's directory. The environments in alder.yaml (production, staging) are not used here.");
    expect(text.indexOf("Project directory/")).toBeLessThan(text.indexOf("Planned against"));
    expect(text).toContain("uid=ada,ou=people,dc=example,dc=com");
  });

  it("shows a folder name from the files with its controls made visible", () => {
    const html = renderToStaticMarkup(<ProjectResult folder={`dir${RLO}ectory`} result={result} onBrowse={() => {}} />);
    expect(html).not.toContain(RLO);
    expect(html).toContain("dir\\u202eectory/");
  });
});

describe("the unmanaged entries", () => {
  it("lists them per subtree, escaped, with an unreadable subtree's reason and no way to delete", () => {
    const html = renderToStaticMarkup(<UnmanagedList unmanaged={result.unmanaged} onBrowse={() => {}} />);
    const text = textOf(html);
    expect(text).toContain("Unmanaged: 1 entry the directory holds that no project file describes. Reported, never deleted.");
    expect(html).not.toContain(RLO);
    expect(text).toContain("uid=report\\u202etxt.exe");
    expect(text).toContain("Unreadable: the subtree could not be read whole");
    expect(text.toLowerCase()).not.toContain("delete ");
  });

  it("says when there is nothing unmanaged", () => {
    const text = textOf(
      renderToStaticMarkup(<UnmanagedList unmanaged={[{ base: "ou=people,dc=example,dc=com", entries: [] }]} onBrowse={() => {}} />),
    );
    expect(text).toContain("Unmanaged: none. Every entry in the managed subtrees is one the project describes.");
  });
});

describe("a project that is not valid", () => {
  it("lists every problem by file and line, escaped, and says nothing was planned", () => {
    const failure = new ApiFailure(400, {
      error: "project_invalid",
      message: "the project is not valid: 2 problems",
      problems: [
        { file: "people/a.ldif", line: 7, message: `uid=x${RLO} carries userPassword, a secret` },
        { file: "alder.yaml", message: "environments.prod: host is required" },
      ],
    });
    const html = renderToStaticMarkup(<ProjectFailure error={failure} />);
    const text = textOf(html);
    expect(text).toContain("This project is not valid, so nothing was planned");
    expect(text).toContain("people/a.ldif:7:");
    expect(text).toContain("alder.yaml: environments.prod: host is required");
    expect(html).not.toContain(RLO);
  });

  it("shows any other refusal as the server wrote it", () => {
    const failure = new ApiFailure(400, { error: "bad_request", message: "There is no alder.yaml at the top of the project." });
    expect(textOf(renderToStaticMarkup(<ProjectFailure error={failure} />))).toContain("There is no alder.yaml at the top");
  });
});
