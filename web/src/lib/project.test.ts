import { afterEach, describe, expect, it } from "vitest";
import { changeset, maxStagedChanges } from "./changeset";
import {
  hasWithheldValue,
  maxProjectFiles,
  originOf,
  projectChanges,
  projectRequest,
  selectProjectFiles,
  stageProjectPlan,
  type ProjectPlan,
} from "./project";
import type { PlanItem } from "./api";

const picked = (...paths: string[]) => paths.map((relativePath) => ({ relativePath, file: relativePath }));

describe("which of a chosen folder's files are sent", () => {
  it("sends alder.yaml from the top and every .ldif outside dot-directories, by path below the folder", () => {
    const s = selectProjectFiles(
      picked(
        "directory/alder.yaml",
        "directory/people/engineering.ldif",
        "directory/groups.ldif",
        "directory/README.md",
        "directory/.git/objects/ab/cd.ldif",
        "directory/people/.drafts/x.ldif",
        "directory/secrets.env",
        "directory/CAPS.LDIF",
      ),
    );
    expect(s.ok).toBe(true);
    if (!s.ok) return;
    expect(s.folder).toBe("directory");
    expect(s.send.map((f) => f.path)).toEqual(["CAPS.LDIF", "alder.yaml", "groups.ldif", "people/engineering.ldif"]);
  });

  it("opens the one project below the chosen folder when there is none at its top", () => {
    const s = selectProjectFiles(
      picked("repo/README.md", "repo/directory/alder.yaml", "repo/directory/a.ldif", "repo/other.ldif"),
    );
    expect(s.ok).toBe(true);
    if (!s.ok) return;
    expect(s.folder).toBe("repo/directory");
    expect(s.send.map((f) => f.path)).toEqual(["a.ldif", "alder.yaml"]);
  });

  it("asks which project when several folders below have one, and opens the one picked", () => {
    const files = picked(
      "repo/prod/alder.yaml",
      "repo/prod/a.ldif",
      "repo/lab/alder.yaml",
      "repo/lab/b.ldif",
      "repo/.old/alder.yaml",
    );
    const s = selectProjectFiles(files);
    expect(s.ok).toBe(false);
    if (s.ok) return;
    expect(s.projects).toEqual(["lab", "prod"]);
    expect(s.message).toContain("Choose which project");

    const lab = selectProjectFiles(files, "lab");
    expect(lab.ok).toBe(true);
    if (!lab.ok) return;
    expect(lab.folder).toBe("repo/lab");
    expect(lab.send.map((f) => f.path)).toEqual(["alder.yaml", "b.ldif"]);

    const nowhere = selectProjectFiles(files, ".old");
    expect(nowhere.ok).toBe(false);
  });

  it("prefers the project at the top, and does not send another project's alder.yaml below it", () => {
    const s = selectProjectFiles(picked("repo/alder.yaml", "repo/sub/alder.yaml", "repo/sub/c.ldif"));
    expect(s.ok).toBe(true);
    if (!s.ok) return;
    expect(s.folder).toBe("repo");
    expect(s.send.map((f) => f.path)).toEqual(["alder.yaml", "sub/c.ldif"]);
  });

  it("says plainly when there is no project at all", () => {
    const s = selectProjectFiles(picked("photos/a.jpg", "photos/b.ldif"));
    expect(s.ok).toBe(false);
    if (s.ok) return;
    expect(s.message).toContain("not an Alder project");
  });

  it("does not look inside a dot-directory for alder.yaml either", () => {
    const s = selectProjectFiles(picked("repo/.cache/alder.yaml"));
    expect(s.ok).toBe(false);
    if (s.ok) return;
    expect(s.message).not.toContain(".cache");
  });

  it("refuses more files than one request carries, rather than sending part of the project", () => {
    const many = ["p/alder.yaml"];
    for (let i = 0; i < maxProjectFiles; i++) many.push(`p/people/${i}.ldif`);
    const s = selectProjectFiles(picked(...many));
    expect(s.ok).toBe(false);
    if (s.ok) return;
    expect(s.message).toContain("alder project plan");
  });

  it("refuses a request larger than the server's body limit", () => {
    expect(projectRequest([{ path: "alder.yaml", content: "version: 1\n" }]).tooLarge).toBeNull();
    const big = projectRequest([{ path: "a.ldif", content: "x".repeat(17 << 20) }]);
    expect(big.tooLarge).toContain("alder project plan");
  });
});

function item(over: Partial<PlanItem>): PlanItem {
  return { index: 0, dn: "uid=a,ou=people,dc=example,dc=com", action: "add", exists: false, ...over } as PlanItem;
}

function result(items: PlanItem[]): ProjectPlan {
  return {
    project: { file: "alder.yaml", entries: 3, subtrees: [{ base: "ou=people,dc=example,dc=com", files: [], entries: 3 }], environments: ["prod"] },
    host: "ldap.example.com:636",
    bindDn: "cn=admin,dc=example,dc=com",
    plan: { counts: { examined: items.length, add: 0, modify: 0, delete: 0, rename: 0, setPassword: 0, unchanged: 0, conflict: 0 }, items },
    unmanaged: [],
  } as ProjectPlan;
}

const add = item({
  index: 0,
  action: "add",
  record: { dn: "uid=a,ou=people,dc=example,dc=com", type: "add", attributes: [{ name: "cn", values: [{ text: "A" }] }] },
  baseline: "t1",
});
const unchanged = item({ index: 1, action: "unchanged", dn: "uid=b,ou=people,dc=example,dc=com" });
const conflict = item({ index: 2, action: "conflict", dn: "uid=c,ou=people,dc=example,dc=com" });

describe("staging a project's plan", () => {
  afterEach(() => changeset.clear());

  it("stages only what the plan would apply, without its baseline, and keeps the heading", () => {
    const r = result([add, unchanged, conflict]);
    expect(projectChanges(r.plan).map((c) => c.change.dn)).toEqual(["uid=a,ou=people,dc=example,dc=com"]);
    const outcome = stageProjectPlan(originOf("directory", r), r.plan);
    expect(outcome.ok).toBe(true);
    expect(changeset.all()).toHaveLength(1);
    expect(changeset.all()[0]!.change.baseline).toBeUndefined();
    const origin = changeset.origin();
    expect(origin?.kind === "project" ? origin.host : undefined).toBe("ldap.example.com:636");
  });

  it("refuses a changeset that already holds something, with the count, and replaces nothing", () => {
    changeset.add({ dn: "cn=x,dc=example,dc=com", type: "delete" }, "Delete cn=x");
    changeset.add({ dn: "cn=y,dc=example,dc=com", type: "delete" }, "Delete cn=y");
    const r = result([add]);
    const outcome = stageProjectPlan(originOf("directory", r), r.plan);
    expect(outcome.ok).toBe(false);
    expect(outcome.stagedAlready).toBe(2);
    expect(outcome.message).toContain("already holds 2 changes");
    expect(changeset.all().map((s) => s.change.dn)).toEqual(["cn=x,dc=example,dc=com", "cn=y,dc=example,dc=com"]);
    expect(changeset.origin()).toBeNull();
  });

  it("says there is nothing to stage when the directory already matches", () => {
    const r = result([unchanged]);
    const outcome = stageProjectPlan(originOf("directory", r), r.plan);
    expect(outcome.ok).toBe(false);
    expect(outcome.message).toContain("already matches");
  });

  it("refuses a plan larger than a changeset, and names the command that applies it", () => {
    const items: PlanItem[] = [];
    for (let i = 0; i <= maxStagedChanges; i++) items.push({ ...add, index: i, dn: `uid=${i},dc=example,dc=com` });
    const r = result(items);
    const outcome = stageProjectPlan(originOf("directory", r), r.plan);
    expect(outcome.ok).toBe(false);
    expect(outcome.message).toContain("alder project apply");
    expect(changeset.all()).toHaveLength(0);
  });

  it("drops the heading the moment the changeset is no longer the project's plan", () => {
    const r = result([add]);
    stageProjectPlan(originOf("directory", r), r.plan);
    expect(changeset.origin()).not.toBeNull();
    changeset.add({ dn: "cn=x,dc=example,dc=com", type: "delete" }, "Delete cn=x");
    expect(changeset.origin()).toBeNull();
  });

  it("recognises a value the plan withheld", () => {
    expect(hasWithheldValue({ dn: "x", type: "add", attributes: [{ name: "a", values: [{ size: 4 }] }] })).toBe(true);
    expect(hasWithheldValue({ dn: "x", type: "add", attributes: [{ name: "a", values: [{ text: "v" }] }] })).toBe(false);
  });
});
