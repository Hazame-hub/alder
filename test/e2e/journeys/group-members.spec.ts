import { expect, test } from "../support/fixtures";
import * as directory from "../support/directory";
import { suffix } from "../support/servers";

/**
 * Many members at once (docs/proposals/group-membership-tools.md, Part 1):
 * select five people in a search, add them to a group as one change, review
 * it, apply it, and ask each server's own ldapsearch what the group holds.
 *
 * One of the five is a member already, so the journey also sees the record
 * leave it out: an add naming a value the group holds is refused whole.
 */
test("group members: add five people from a search to a group in one change", async ({
  alder,
  server,
  read,
}, testInfo) => {
  const { page } = alder;
  const token = `${server.name}-${Date.now().toString(36)}${testInfo.workerIndex}`;
  const uids = ["a", "b", "c", "d", "e"].map((n) => `e2e-gm-${n}-${token}`);
  const dns = uids.map((uid) => `uid=${uid},ou=people,${suffix}`);
  const groupCn = `e2e-gm-${token}`;
  const group = `cn=${groupCn},ou=groups,${suffix}`;

  for (const [i, dn] of dns.entries()) {
    directory.add(
      server,
      [
        `dn: ${dn}`,
        "objectClass: top",
        "objectClass: person",
        "objectClass: organizationalPerson",
        "objectClass: inetOrgPerson",
        `uid: ${uids[i]}`,
        `cn: Members ${token}`,
        "sn: Journey",
        "",
      ].join("\n"),
    );
  }
  directory.add(
    server,
    [`dn: ${group}`, "objectClass: top", "objectClass: groupOfNames", `cn: ${groupCn}`, `member: ${dns[0]}`, ""].join(
      "\n",
    ),
  );

  try {
    await alder.connect();
    const filter = `(cn=Members ${token})`;
    await page.goto(
      `/?view=search&base=${encodeURIComponent(`ou=people,${suffix}`)}&scope=one&filter=${encodeURIComponent(filter)}`,
    );
    await expect(page.getByText("5 entries", { exact: true })).toBeVisible();

    await page.getByRole("checkbox", { name: "Select every row listed" }).click();
    await page.getByRole("button", { name: "Add to a group…" }).click();

    const dialog = page.getByRole("dialog", { name: "Add 5 entries to a group" });
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: "Choose a group" }).click();

    const picker = page.getByRole("dialog", { name: "Choose the group" });
    await picker.getByLabel("Name contains").fill(groupCn);
    await picker.getByRole("button", { name: "Search", exact: true }).click();
    await picker.getByRole("button", { name: new RegExp(groupCn) }).first().click();

    await expect(dialog.getByTestId("members-group")).toHaveText(group);
    // The one already in the group is left out of the record.
    await expect(dialog).toContainText("1 is already a member and is left out");
    await dialog.getByRole("button", { name: "Review: add 4 members" }).click();

    const review = page.getByRole("dialog", { name: "Add to the group" });
    await expect(review).toContainText(`dn: ${group}`);
    await expect(review).toContainText("add: member");
    for (const dn of dns.slice(1)) await expect(review).toContainText(`member: ${dn}`);
    // Nothing has been written yet.
    expect(read(group, ["member"])?.member).toEqual([dns[0]]);

    await review.getByRole("button", { name: "Apply to the directory", exact: true }).click();
    await expect(review).toBeHidden();

    // Verify, without asking Alder.
    const members = (read(group, ["member"])?.member ?? []).map((m) => m.toLowerCase()).sort();
    expect(members).toEqual(dns.map((d) => d.toLowerCase()).sort());
  } finally {
    directory.remove(server, group);
    for (const dn of dns) directory.remove(server, dn);
  }
});
