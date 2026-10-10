import { expect, test, type Alder } from "../support/fixtures";
import type { Locator } from "@playwright/test";
import type { InterruptMode } from "../support/proxy";

/**
 * The connection dies while a change is going out.
 *
 * Alder cannot know whether the directory applied it, and the one thing it
 * must not do is send it again: not by itself, and not by leaving a button
 * that does so beside a panel saying it will not. What it offers instead is
 * a read -- "Check the directory" -- and the verdict that read reaches.
 *
 * Both sides of the ambiguity are driven on purpose, on both servers, by the
 * proxy (see interruptNextWrite): once with the write delivered and its reply
 * lost, so the change is in the directory; once with the write lost, so it is
 * not. A test that could only produce whichever outcome the race favoured
 * would prove half of this.
 */

/**
 * Long on purpose: it makes the modify request the one large thing the
 * client sends, which is how the proxy finds the write inside TLS it cannot
 * read. Under 1024 characters, because that is the bound in description's
 * syntax and the editor holds a value to it.
 */
function longValue(token: string): string {
  const head = `interrupted-${token}-`;
  return head + "x".repeat(1_000 - head.length);
}

/** Applies the reviewed change with the next write interrupted. */
async function applyInterrupted(alder: Alder, dialog: Locator, mode: InterruptMode) {
  const { page } = alder;
  // Nothing else in flight. The tree loads in the background, and a request
  // still running when the connection is cut reopens it -- which is a dial,
  // and would look exactly like a resent write.
  await page.waitForLoadState("networkidle");
  const dialled = alder.proxy.opened();

  const interrupted = alder.proxy.interruptNextWrite(mode);
  const answered = page.waitForResponse((r) => r.url().includes("/api/v1/changes/apply"));
  await dialog.getByRole("button", { name: "Apply to the directory", exact: true }).click();
  await interrupted;
  const response = await answered;

  // Not sent again. A retry cannot reuse the socket it lost, so it would
  // have had to dial before answering; counted the moment the answer comes.
  expect(alder.proxy.opened(), "a new connection during the apply means the change was resent").toBe(dialled);
  // 409, not 502: a 502 is what proxies and retry middleware try again.
  expect(response.status()).toBe(409);
  expect((await response.json()).error).toBe("write_outcome_unknown");

  // The panel for this state, not an error. "The directory refused this
  // change" would be false, and believing it is how a change gets made twice.
  await expect(dialog.getByText("This change may already have been applied")).toBeVisible();
  await expect(dialog).not.toContainText("The directory refused this change");

  // And no way offered to send it again from here: the review behind this
  // dialog was of a directory that may since have moved.
  await expect(dialog.getByRole("button", { name: "Apply to the directory", exact: true })).toBeDisabled();
  await expect(dialog.getByRole("button", { name: "Add to changeset", exact: true })).toBeDisabled();
}

test("interrupted write that landed: Alder says so by reading, and does not resend", async ({ alder, disposable, read }) => {
  const value = longValue(disposable.token);

  await alder.connect();
  await alder.openEntry(disposable.dn);
  const dialog = await alder.reviewEdit("description", "before", value);

  await applyInterrupted(alder, dialog, "lose-reply");

  // The write reached the server before its answer was lost.
  expect(read(disposable.dn, ["description"])?.description).toEqual([value]);

  // Checking reads, and says what the directory holds.
  await dialog.getByRole("button", { name: "Check the directory" }).click();
  await expect(dialog.getByText("The change is in the directory.")).toBeVisible();
  await expect(dialog.getByText("Sending it again would be a second change.", { exact: false })).toBeVisible();
});

test("interrupted write that never arrived: Alder says so, and the way back is a fresh plan", async ({ alder, disposable, read }) => {
  const { page } = alder;
  const value = longValue(disposable.token);

  await alder.connect();
  await alder.openEntry(disposable.dn);
  let dialog = await alder.reviewEdit("description", "before", value);

  await applyInterrupted(alder, dialog, "lose-write");

  // Never forwarded, and never resent: had Alder retried, it would be here.
  expect(read(disposable.dn, ["description"])?.description).toEqual(["before"]);

  await dialog.getByRole("button", { name: "Check the directory" }).click();
  await expect(dialog.getByText("The change is not in the directory.")).toBeVisible();
  await expect(dialog.getByText("Make the change again from the entry.", { exact: false })).toBeVisible();

  // The route back is the ordinary one: close, review again -- a new plan,
  // against the directory as it is now -- and confirm.
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole("button", { name: "Review 1 change", exact: true }).click();
  dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("replace: description");
  await expect(dialog).not.toContainText("This change may already have been applied");
  await dialog.getByRole("button", { name: "Apply to the directory", exact: true }).click();
  await expect(dialog).toBeHidden();

  expect(read(disposable.dn, ["description"])?.description).toEqual([value]);
});
