import { expect, test } from "../support/fixtures";
import * as directory from "../support/directory";
import { suffix } from "../support/servers";

/**
 * Members nobody can find (docs/proposals/group-membership-tools.md, Part 2):
 * a group names a person who exists and one who never did. Check membership
 * from the Groups view, choose the value not found, stage the clean-up, apply
 * it from the changeset, and ask each server's own ldapsearch what the group
 * holds now.
 */
test("member audit: remove a value not found from the Groups view", async ({ alder, server, read }, testInfo) => {
  const { page } = alder;
  const token = `${server.name}-${Date.now().toString(36)}${testInfo.workerIndex}`;
  const uid = `e2e-ma-${token}`;
  const present = `uid=${uid},ou=people,${suffix}`;
  const ghost = `uid=e2e-ma-ghost-${token},ou=people,${suffix}`;
  const groupCn = `e2e-ma-${token}`;
  const group = `cn=${groupCn},ou=groups,${suffix}`;

  directory.add(
    server,
    [
      `dn: ${present}`,
      "objectClass: top",
      "objectClass: person",
      "objectClass: organizationalPerson",
      "objectClass: inetOrgPerson",
      `uid: ${uid}`,
      `cn: Audit ${token}`,
      "sn: Journey",
      "",
    ].join("\n"),
  );
  directory.add(
    server,
    [
      `dn: ${group}`,
      "objectClass: top",
      "objectClass: groupOfNames",
      `cn: ${groupCn}`,
      `member: ${present}`,
      `member: ${ghost}`,
      "",
    ].join("\n"),
  );

  try {
    await alder.connect();
    await page.goto("/?view=groups");
    await page.getByRole("button", { name: "Check membership" }).click();

    const dialog = page.getByRole("dialog", { name: "Check membership" });
    // Said, above everything: who did not find it.
    await expect(dialog.getByTestId("audit-bind")).toContainText("Not found by");
    await expect(dialog.getByTestId("audit-bind")).toContainText("may not see");
    await expect(dialog).not.toContainText("deleted member");

    await dialog.getByRole("checkbox", { name: `Remove ${ghost} from ${group}` }).click();
    await dialog.getByRole("button", { name: "Review the clean-up: 1 value" }).click();

    const confirm = page.getByRole("dialog", { name: "Stage the clean-up" });
    await expect(confirm.getByTestId("cleanup-caveat")).toContainText("may not see is removed the same way");
    await confirm.getByRole("button", { name: "Check again and stage" }).click();
    await expect(confirm).toBeHidden();
    await expect(page.getByText("1 change is staged. Nothing has been sent to the directory.")).toBeVisible();

    // Staging wrote nothing.
    expect(read(group, ["member"])?.member?.length).toBe(2);

    await alder.openChangeset();
    await page.getByRole("button", { name: "Check against the directory" }).click();
    const apply = page.getByRole("button", { name: /^Apply .* in order$/ });
    await expect(apply).toBeEnabled();
    await apply.click();
    await expect(page.getByText("All 1 changes applied.")).toBeVisible();

    // Verify, without asking Alder.
    const members = (read(group, ["member"])?.member ?? []).map((m) => m.toLowerCase());
    expect(members).toEqual([present.toLowerCase()]);
  } finally {
    directory.remove(server, group);
    directory.remove(server, present);
  }
});
