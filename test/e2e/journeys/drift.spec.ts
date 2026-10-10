import { expect, test } from "../support/fixtures";

/**
 * Drift found and put back.
 *
 * Snapshot an entry, change it behind Alder's back, compare the directory
 * with the snapshot, and apply the change the comparison proposes. The
 * comparison is what an operator trusts to say what moved, and the change it
 * proposes goes through the same review as any other, so this checks both:
 * that the drift is found, and that putting it back is an ordinary reviewed
 * change that lands.
 */
test("drift: snapshot, change out of band, compare, put back", async ({ alder, disposable, read, modifyOutOfBand }) => {
  const { page } = alder;
  const drifted = `drifted-${disposable.token}`;

  await alder.connect();
  await alder.openSnapshots();

  // Snapshot just this entry.
  const another = page.getByRole("button", { name: "Capture or upload another snapshot" });
  if (await another.isVisible()) await another.click();
  await page.getByLabel("Base DN", { exact: true }).fill(disposable.dn);
  await page.getByLabel("Scope", { exact: true }).selectOption("base");
  await page.getByRole("button", { name: "Capture into snapshot A" }).click();
  await expect(page.getByText("The directory now", { exact: true })).toBeVisible();

  // Somebody edits it by hand.
  modifyOutOfBand(
    [`dn: ${disposable.dn}`, "changetype: modify", "replace: description", `description: ${drifted}`, ""].join("\n"),
  );
  expect(read(disposable.dn, ["description"])?.description).toEqual([drifted]);

  // The directory now, against the snapshot: changes move it toward the
  // snapshot, which is the undo.
  await page.getByRole("button", { name: "Compare", exact: true }).click();
  const select = page.getByRole("checkbox", { name: `Select the change to ${disposable.dn}` });
  await expect(select).toBeVisible();
  await select.click();

  // One change reviews in the dialog, as the server rendered it.
  await page.getByRole("button", { name: "Review 1 change", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText(`dn: ${disposable.dn}`);
  await expect(dialog).toContainText("changetype: modify");
  await expect(dialog).toContainText("description: before");
  await dialog.getByRole("button", { name: "Apply to the directory", exact: true }).click();
  await expect(dialog).toBeHidden();

  // Back as it was, by the directory's own account.
  expect(read(disposable.dn, ["description"])?.description).toEqual(["before"]);
});
