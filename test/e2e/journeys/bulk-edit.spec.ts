import { expect, test } from "../support/fixtures";
import * as directory from "../support/directory";
import { suffix } from "../support/servers";

/**
 * One modification over a selection (docs/proposals/bulk-attribute-edit.md):
 * select three people in a search, add one description value to each, review
 * the changeset, apply it, and ask each server's own ldapsearch what landed.
 *
 * The three entries are this run's own, found by a token in their cn, so the
 * selection is exactly them whatever else the harness holds.
 */
test("bulk edit: select three people, add a description, review, apply, verify", async ({
  alder,
  server,
  read,
}, testInfo) => {
  const { page } = alder;
  const token = `${server.name}-${Date.now().toString(36)}${testInfo.workerIndex}`;
  const dns = ["a", "b", "c"].map((n) => `uid=e2e-bulk-${n}-${token},ou=people,${suffix}`);
  const value = `bulk-${token}`;

  for (const [i, dn] of dns.entries()) {
    directory.add(
      server,
      [
        `dn: ${dn}`,
        "objectClass: top",
        "objectClass: person",
        "objectClass: organizationalPerson",
        "objectClass: inetOrgPerson",
        `uid: e2e-bulk-${"abc"[i]}-${token}`,
        `cn: Bulk ${token}`,
        "sn: Journey",
        "description: before",
        "",
      ].join("\n"),
    );
  }

  try {
    await alder.connect();
    const filter = `(cn=Bulk ${token})`;
    await page.goto(
      `/?view=search&base=${encodeURIComponent(`ou=people,${suffix}`)}&scope=one&filter=${encodeURIComponent(filter)}`,
    );
    await expect(page.getByText("3 entries", { exact: true })).toBeVisible();

    // Select the three rows, then the one modification.
    await page.getByRole("checkbox", { name: "Select every row listed" }).click();
    await expect(page.getByText("3 selected", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Edit an attribute…" }).click();

    const dialog = page.getByRole("dialog");
    await expect(dialog).toContainText("3 entries selected");
    await dialog.getByRole("combobox", { name: "Attribute" }).click();
    await page.getByRole("option", { name: "description", exact: true }).click();
    // Add a value is the operation the dialog starts on.
    await expect(dialog.getByRole("combobox", { name: "Operation" })).toContainText("Add a value");
    await dialog.getByRole("textbox", { name: "description", exact: true }).fill(value);
    await dialog.getByRole("button", { name: "Stage 3 changes", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByText("3 changes are staged. Nothing has been sent to the directory.")).toBeVisible();

    // Nothing has been written by staging.
    for (const dn of dns) {
      expect(read(dn, ["description"])?.description).toEqual(["before"]);
    }

    // Review: one heading for the one edit, and one record per entry.
    await alder.openChangeset();
    await expect(page.getByRole("heading", { name: "3 staged changes" })).toBeVisible();
    await expect(page.getByText(`One modification over 3 entries: add description: ${value}`)).toBeVisible();
    for (const dn of dns) {
      await expect(page.getByText(`Add description to ${dn}`, { exact: true })).toBeVisible();
    }

    await page.getByRole("button", { name: "Check against the directory" }).click();
    const apply = page.getByRole("button", { name: /^Apply .* in order$/ });
    await expect(apply).toBeEnabled();
    await apply.click();
    await expect(page.getByText("All 3 changes applied.")).toBeVisible();

    // Verify, without asking Alder: each entry gained the value and kept its own.
    for (const dn of dns) {
      expect(read(dn, ["description"])?.description?.sort()).toEqual(["before", value].sort());
    }
  } finally {
    for (const dn of dns) directory.remove(server, dn);
  }
});
