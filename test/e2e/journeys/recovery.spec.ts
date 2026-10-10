import { readFileSync } from "node:fs";
import { expect, test } from "../support/fixtures";

/**
 * A change applied with a recovery bundle, and put back from it.
 *
 * The bundle is the one artefact Alder hands over and does not keep, so the
 * journey does what an operator does: downloads it when the change is
 * applied, loads it later, reviews the plan for the compensation, applies
 * it, and checks with the directory that the entry is as it was.
 */
test("recovery: apply with a bundle, load it, plan it, apply it", async ({ alder, disposable, read }, testInfo) => {
  const { page } = alder;
  const value = `recoverable-${disposable.token}`;

  await alder.connect();
  await alder.openEntry(disposable.dn);

  // Stage the change and apply it from the changeset, with a bundle.
  const dialog = await alder.reviewEdit("description", "before", value);
  await dialog.getByRole("button", { name: "Add to changeset", exact: true }).click();
  await expect(dialog).toBeHidden();
  await alder.openChangeset();
  await page.getByRole("checkbox", { name: "Prepare a recovery bundle" }).check();
  await page.getByRole("button", { name: /^Apply .* in order$/ }).click();
  await expect(page.getByText("All 1 changes applied.")).toBeVisible();
  expect(read(disposable.dn, ["description"])?.description).toEqual([value]);

  // Keep the bundle, as the panel says to: Alder does not.
  const downloading = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download recovery bundle", exact: true }).click();
  const bundlePath = testInfo.outputPath("recovery.json");
  await (await downloading).saveAs(bundlePath);
  expect(JSON.parse(readFileSync(bundlePath, "utf8"))).toBeTruthy();

  // Load it, and review the compensation it proposes.
  const choosing = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "Load recovery bundle", exact: true }).click();
  await (await choosing).setFiles(bundlePath);
  await expect(page.getByText("Proposed compensating changes, in the order they would run")).toBeVisible();
  await page.getByRole("button", { name: "Review plan" }).click();

  // Staged and planned; applying is the separate step it says it is.
  await expect(page.getByRole("heading", { name: "1 staged change" })).toBeVisible();
  const apply = page.getByRole("button", { name: /^Apply .* in order$/ });
  await expect(apply).toBeEnabled();
  await apply.click();
  await expect(page.getByText("All 1 changes applied.")).toBeVisible();

  expect(read(disposable.dn, ["description"])?.description).toEqual(["before"]);
});
