import { describe, expect, it } from "vitest";
import { validateAppSearch } from "./route";
import {
  maxListedAttributes,
  parseAttributeList,
  savedSearchFile,
  savedSearchFileName,
  searchLocation,
  showsOrigin,
  type CurrentSearch,
  type SavedSearch,
} from "./saved-search";

const locked: SavedSearch = {
  name: "people/locked-accounts",
  file: "people/locked-accounts.search.yaml",
  title: "Locked accounts",
  base: "ou=people,dc=alder,dc=test",
  scope: "sub",
  filter: "(pwdAccountLockedTime=*)",
  attributes: ["uid", "pwdAccountLockedTime", "objectClass"],
  columns: ["uid", "pwdAccountLockedTime"],
  limit: 500,
};

const asOpened = (s: SavedSearch): CurrentSearch => ({
  base: s.base,
  scope: s.scope,
  filter: s.filter,
  limit: s.limit,
  attrs: s.attributes,
  cols: s.columns,
});

describe("a saved search is a link", () => {
  it("opens at the search page with its values, attributes and columns in the URL", () => {
    expect(searchLocation(locked)).toEqual({
      view: "search",
      base: "ou=people,dc=alder,dc=test",
      scope: "sub",
      filter: "(pwdAccountLockedTime=*)",
      limit: 500,
      attrs: "uid,pwdAccountLockedTime,objectClass",
      cols: "uid,pwdAccountLockedTime",
    });
    const bare = searchLocation({ ...locked, attributes: [], columns: [] });
    expect(bare.attrs).toBeUndefined();
    expect(bare.cols).toBeUndefined();
  });

  it("round-trips through the URL's validation", () => {
    const loc = validateAppSearch(searchLocation(locked) as Record<string, unknown>);
    expect(parseAttributeList(loc.attrs)).toEqual(locked.attributes);
    expect(parseAttributeList(loc.cols)).toEqual(locked.columns);
  });

  it("drops from a hand-written link what is not an attribute name", () => {
    expect(validateAppSearch({ attrs: "uid, (cn),mail,uid,cn;lang-en" }).attrs).toBe("uid,mail,cn;lang-en");
    expect(validateAppSearch({ attrs: "),(" }).attrs).toBeUndefined();
    expect(validateAppSearch({ cols: ["uid", 3, "mail"] }).cols).toBe("uid,mail");
    const many = Array.from({ length: 150 }, (_, i) => `a${i}`).join(",");
    expect(parseAttributeList(many)).toHaveLength(maxListedAttributes);
  });
});

describe("the heading over a saved search", () => {
  const origin = { folder: "directory", search: locked };

  it("stays while the page shows the search as saved", () => {
    expect(showsOrigin(origin, asOpened(locked))).toBe(true);
    expect(showsOrigin(origin, { ...asOpened(locked), cols: ["UID", "pwdaccountlockedtime"] })).toBe(true);
  });

  it("comes down once any part of it is edited", () => {
    const opened = asOpened(locked);
    for (const edited of [
      { ...opened, base: "dc=alder,dc=test" },
      { ...opened, scope: "one" as const },
      { ...opened, filter: "(uid=*)" },
      { ...opened, limit: 100 },
      { ...opened, attrs: ["uid"] },
      { ...opened, cols: [] },
    ]) {
      expect(showsOrigin(origin, edited)).toBe(false);
    }
    expect(showsOrigin(null, opened)).toBe(false);
  });
});

describe("Save as file", () => {
  it("writes the search as typed, every string quoted as YAML reads it", () => {
    const text = savedSearchFile(
      {
        base: "ou=people,dc=alder,dc=test",
        scope: "one",
        filter: ' (&(cn=a: "b")(description=#[x])) ',
        limit: 50,
        attrs: ["uid", "cn"],
        cols: ["cn"],
      },
      "Quoted: \"title\"",
    );
    expect(text).toBe(
      [
        "# An Alder saved search. Commit it to a project, and check it with",
        "# alder project validate.",
        "version: 1",
        'title: "Quoted: \\"title\\""',
        'base: "ou=people,dc=alder,dc=test"',
        "scope: one",
        'filter: "(&(cn=a: \\"b\\")(description=#[x]))"',
        'attributes: ["uid", "cn"]',
        'columns: ["cn"]',
        "limit: 50",
        "",
      ].join("\n"),
    );
  });

  it("leaves out attributes and columns the search does not set", () => {
    const text = savedSearchFile({ ...asOpened(locked), attrs: [], cols: [] });
    expect(text).not.toContain("attributes:");
    expect(text).not.toContain("columns:");
    expect(text).not.toContain("title:");
  });

  it("is named after the saved search it came from, or a placeholder to rename", () => {
    expect(savedSearchFileName({ folder: "directory", search: locked })).toBe("locked-accounts.search.yaml");
    expect(savedSearchFileName(null)).toBe("saved.search.yaml");
  });
});
