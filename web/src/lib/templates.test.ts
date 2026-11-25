import { describe, expect, it } from "vitest";
import { allocationLine, newLabel, templatesFor } from "./templates";
import type { EntryTemplate } from "./api";

const tpl = (name: string, view: string) =>
  ({ name, view, title: name, rdn: "cn", objectClasses: [], fields: [], attributes: [], allocates: [], source: "built-in", usable: true }) as EntryTemplate;

describe("templates", () => {
  it("offers a view's own templates", () => {
    expect(templatesFor([tpl("a", "users"), tpl("b", "groups")], "groups").map((t) => t.name)).toEqual(["b"]);
    expect(templatesFor(undefined, "users")).toEqual([]);
  });

  it("names the button after the view", () => {
    expect(newLabel("users")).toBe("New user");
    expect(newLabel("groups")).toBe("New group");
  });

  it("says where an allocated number came from", () => {
    expect(
      allocationLine({
        allocated: [
          { attribute: "uidNumber", value: 10301, highestInUse: 10300, scanned: 300, bases: [] },
          { attribute: "gidNumber", value: 10000, scanned: 0, bases: [] },
        ],
      }),
    ).toBe("uidNumber 10301, the next free after 10300; gidNumber 10000, the first of its range (none in use)");
  });
});
