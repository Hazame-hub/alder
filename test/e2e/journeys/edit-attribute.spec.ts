import { expect, test } from "../support/fixtures";

/**
 * The core journey: plan a change in the editor, review the exact LDIF,
 * apply it, and confirm with the directory itself that it is what landed.
 *
 * Each step is the one the charter makes non-negotiable. The review step
 * asserts the record the server rendered, because "no modification reaches
 * the server without showing the exact LDIF first" is only true if the
 * preview is the change; the last step reads the entry out of band, because
 * a UI that shows its own write back proves nothing about the directory.
 */
test("edit an attribute: plan, review, apply, verify", async ({ alder, disposable, read }) => {
  const { page } = alder;
  const value = `after-${disposable.token}`;

  await alder.connect();
  await alder.openEntry(disposable.dn);

  // Plan, then review: the one modification, rendered by the server as a
  // change record.
  const dialog = await alder.reviewEdit("description", "before", value);
  await expect(dialog).toContainText(`dn: ${disposable.dn}`);
  await expect(dialog).toContainText("changetype: modify");
  await expect(dialog).toContainText(`description: ${value}`);
  // Nothing has been written yet: confirming is the user's step, not the
  // dialog's.
  expect(read(disposable.dn, ["description"])?.description).toEqual(["before"]);

  // Apply.
  await dialog.getByRole("button", { name: "Apply to the directory", exact: true }).click();
  await expect(dialog).toBeHidden();
  // The editor closes onto the entry as it now reads.
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toBeVisible();
  await expect(page.getByText(value, { exact: true })).toBeVisible();

  // Verify, without asking Alder.
  expect(read(disposable.dn, ["description"])?.description).toEqual([value]);
});
