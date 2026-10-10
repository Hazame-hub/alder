import type { Page } from "@playwright/test";
import { expect, test, type Alder } from "../support/fixtures";
import type { InterruptMode } from "../support/proxy";
import { longValue } from "../support/values";

/**
 * The interrupted write, in a changeset.
 *
 * The single-change dialog stops at the panel; a changeset keeps going,
 * because the change that was interrupted is still staged and "Apply in
 * order" is right there. Each verdict decides what happens to it: one that
 * landed leaves the changeset, one that did not is planned again before it
 * can run, and until the directory has been read, applying is held -- the
 * one thing that must not happen is the run being repeated with the
 * interrupted change at its head.
 */

async function stageAndInterrupt(alder: Alder, dn: string, value: string, mode: InterruptMode) {
  const { page } = alder;
  await alder.openEntry(dn);
  const dialog = await alder.reviewEdit("description", "before", value);
  await dialog.getByRole("button", { name: "Add to changeset", exact: true }).click();
  await expect(dialog).toBeHidden();
  await alder.openChangeset();
  await expect(page.getByRole("heading", { name: "1 staged change" })).toBeVisible();

  await page.waitForLoadState("networkidle");
  const dialled = alder.proxy.opened();
  const interrupted = alder.proxy.interruptNextWrite(mode);
  const answered = page.waitForResponse((r) => r.url().includes("/api/v1/changeset/apply"));
  await applyButton(page).click();
  await interrupted;
  await answered;
  expect(alder.proxy.opened(), "a new connection during the run means the change was resent").toBe(dialled);

  await expect(page.getByText("Stopped at change 1, whose outcome is unknown.")).toBeVisible();
  // Held, and saying why, until the directory has been read.
  await expect(applyButton(page)).toBeDisabled();
  await expect(page.getByText("Change 1 may already have been applied. Check the directory before applying again.")).toBeVisible();
  // The check is in the result panel, so the panel cannot be dismissed yet.
  await expect(page.getByTitle("Dismiss")).toHaveCount(0);
}

function applyButton(page: Page) {
  return page.getByRole("button", { name: /^Apply .* in order$/ });
}

test("interrupted changeset that landed: the change leaves the changeset", async ({ alder, disposable, read }) => {
  const { page } = alder;
  const value = longValue(disposable.token);
  await alder.connect();
  await stageAndInterrupt(alder, disposable.dn, value, "lose-reply");

  expect(read(disposable.dn, ["description"])?.description).toEqual([value]);

  await page.getByRole("button", { name: "Check the directory" }).click();
  await expect(page.getByText("The change is in the directory.")).toBeVisible();
  // Gone from the basket: there is nothing left to send twice.
  await expect(page.getByText("The changeset is empty")).toBeVisible();
  await expect(page.getByRole("button", { name: /^Changeset/ })).toHaveText("Changeset");
});

test("interrupted changeset that never arrived: planned again, then applied", async ({ alder, disposable, read }) => {
  const { page } = alder;
  const value = longValue(disposable.token);
  await alder.connect();
  await stageAndInterrupt(alder, disposable.dn, value, "lose-write");

  expect(read(disposable.dn, ["description"])?.description).toEqual(["before"]);

  await page.getByRole("button", { name: "Check the directory" }).click();
  await expect(page.getByText("The change is not in the directory.")).toBeVisible();
  // Still staged, still held: it may run again only from a fresh plan.
  await expect(page.getByRole("heading", { name: "1 staged change" })).toBeVisible();
  await expect(applyButton(page)).toBeDisabled();
  await expect(page.getByText(/^Change 1 did not reach the directory\./)).toBeVisible();

  await page.getByRole("button", { name: "Check against the directory" }).click();
  await expect(applyButton(page)).toBeEnabled();
  await applyButton(page).click();
  await expect(page.getByText("All 1 changes applied.")).toBeVisible();

  expect(read(disposable.dn, ["description"])?.description).toEqual([value]);
});
