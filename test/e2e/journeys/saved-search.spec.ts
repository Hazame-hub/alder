import { cpSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as directory from "../support/directory";
import { expect, test } from "../support/fixtures";
import { suffix } from "../support/servers";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");

/**
 * A saved search, opened from a project.
 *
 * examples/project/directory, moved under a disposable OU, is opened the way
 * a person opens it -- the folder handed to Open project -- and its
 * people/engineers search is opened from the list under the plan. The rows
 * on screen are then checked against the server's own ldapsearch with the
 * search's base, scope and filter: the search page ran the file's search,
 * and nothing else. Save as file is checked to write that search back.
 */
test("saved search: open a project, open its search, check the rows", async ({ alder, server }, testInfo) => {
  const { page } = alder;
  const token = `${server.name}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  const base = `ou=e2e-search-${token},${suffix}`;
  const people = `ou=people,${base}`;
  const person = (uid: string, department: string) =>
    [
      `dn: uid=${uid},${people}`,
      "objectClass: top",
      "objectClass: person",
      "objectClass: organizationalPerson",
      "objectClass: inetOrgPerson",
      `uid: ${uid}`,
      `cn: ${uid} ${token}`,
      "sn: Journey",
      `title: ${department} lead`,
      `departmentNumber: ${department}`,
      "",
    ].join("\n");
  const created = [`uid=ada,${people}`, `uid=grace,${people}`, `uid=margaret,${people}`, people, base];

  directory.add(server, [`dn: ${base}`, "objectClass: top", "objectClass: organizationalUnit", `ou: e2e-search-${token}`, ""].join("\n"));
  try {
    directory.add(server, [`dn: ${people}`, "objectClass: top", "objectClass: organizationalUnit", "ou: people", ""].join("\n"));
    directory.add(server, person("ada", "engineering"));
    directory.add(server, person("grace", "engineering"));
    directory.add(server, person("margaret", "operations"));

    // The example, with its suffix moved under the OU.
    const folder = testInfo.outputPath("directory");
    mkdirSync(folder, { recursive: true });
    cpSync(path.join(repoRoot, "examples", "project", "directory"), folder, { recursive: true });
    const rewrite = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const file = path.join(dir, name);
        if (statSync(file).isDirectory()) {
          rewrite(file);
          continue;
        }
        writeFileSync(file, readFileSync(file, "utf8").replaceAll("dc=example,dc=com", base));
      }
    };
    rewrite(folder);

    await alder.connect();
    await page.goto("/?view=import");
    await page.locator("input[webkitdirectory]").setInputFiles(folder);
    await expect(page.getByText("Saved searches: 2", { exact: false })).toBeVisible();

    await page.getByRole("button", { name: /people\/engineers/ }).click();
    await expect(page.getByText("Saved search people/engineers", { exact: false })).toBeVisible();
    await expect(page.getByText("from directory/", { exact: false })).toBeVisible();

    // The rows, against the server's own answer to the same search.
    const truth = directory.search(server, people, "one", "(&(objectClass=inetOrgPerson)(departmentNumber=engineering))");
    expect(truth).toHaveLength(2);
    await expect(page.getByText(`${truth.length} entries`, { exact: true })).toBeVisible();
    const rows = page.locator("tbody tr");
    await expect(rows).toHaveCount(truth.length);
    for (const dn of truth) {
      await expect(page.locator("tbody").getByTitle(dn, { exact: true }).first()).toBeVisible();
    }
    // The file's columns, the title among them, and not the ones it left out.
    await expect(page.locator("thead")).toContainText("title");
    await expect(page.locator("thead")).not.toContainText("Email");
    await expect(page.locator("tbody")).not.toContainText("margaret");

    // Save as file writes the search back as the file says it.
    const [download] = await Promise.all([
      page.waitForEvent("download"),
      page.getByRole("button", { name: "Save as file", exact: true }).click(),
    ]);
    expect(download.suggestedFilename()).toBe("engineers.search.yaml");
    const saved = readFileSync(await download.path(), "utf8");
    expect(saved).toContain(`base: "${people}"`);
    expect(saved).toContain("scope: one");
    expect(saved).toContain('filter: "(&(objectClass=inetOrgPerson)(departmentNumber=engineering))"');
    expect(saved).toContain('columns: ["uid", "cn", "title"]');
    expect(saved).toContain("limit: 500");

    // Edited, it is an ordinary search, and the heading comes down.
    await page.getByLabel("Search base", { exact: true }).fill(base);
    await page.getByLabel("Search base", { exact: true }).press("Enter");
    await expect(page.getByText("Saved search people/engineers", { exact: false })).toBeHidden();
  } finally {
    for (const dn of created) directory.remove(server, dn);
  }
});
