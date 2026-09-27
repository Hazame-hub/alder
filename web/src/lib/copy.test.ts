import { describe, expect, it } from "vitest";
import { copiedAttributes } from "./copy";
import type { EntryAttribute } from "./api";

// Copying an entry is a write, and it goes through the same plan and the same
// LDIF preview as any other. So the same question applies: is every attribute
// in it one Alder is willing to write?

const kind = (over: Partial<EntryAttribute["kind"]> = {}): EntryAttribute["kind"] => ({
  name: "x",
  kind: "string",
  known: true,
  ...over,
});

const attr = (
  name: string,
  values: string[],
  over: Partial<EntryAttribute> = {},
): EntryAttribute => ({
  name,
  kind: kind({ name }),
  values: values.map((text) => ({ text, size: text.length })),
  ...over,
});

describe("what a copy carries", () => {
  it("carries the ordinary attributes and renames the entry", () => {
    const out = copiedAttributes(
      [attr("cn", ["Alice"]), attr("sn", ["Liddell"]), attr("mail", ["a@b"])],
      "cn",
      "Bob",
    );
    expect(out.map((a) => a.name)).toEqual(["cn", "sn", "mail"]);
    expect(out[0]?.values).toEqual([{ text: "Bob", size: 3 }]);
  });

  it("adds the naming attribute when the original was named by another", () => {
    const out = copiedAttributes([attr("cn", ["Alice"])], "uid", "bob");
    expect(out.map((a) => a.name)).toEqual(["cn", "uid"]);
  });

  it("does not copy an access rule", () => {
    // An access rule copied onto a new entry is an access rule Alder wrote,
    // which is exactly what the decisions log says it does not do -- and a
    // rule written for one entry is rarely right for another.
    const out = copiedAttributes(
      [
        attr("cn", ["Alice"]),
        attr("aci", ['(targetattr="*")...'], {
          kind: kind({ name: "aci", operational: true, elsewhere: "Alder reads access rules and does not write them." }),
        }),
        attr("olcAccess", ["{0}to * by * read"], {
          kind: kind({ name: "olcAccess", elsewhere: "Alder reads access rules and does not write them." }),
        }),
      ],
      "cn",
      "Bob",
    );
    expect(out.map((a) => a.name)).toEqual(["cn"]);
  });

  it("leaves the directory's own state behind", () => {
    // Operational attributes are offered in the editor now, and still not
    // copied: a lock and a failure count are facts about the account that
    // was, and a copy that carried them would be born locked.
    const out = copiedAttributes(
      [
        attr("cn", ["Alice"]),
        attr("nsAccountLock", ["true"], {
          kind: kind({ name: "nsAccountLock", operational: true }),
        }),
        attr("entryUUID", ["u"], { kind: kind({ name: "entryUUID", readOnly: true }) }),
      ],
      "cn",
      "Bob",
    );
    expect(out.map((a) => a.name)).toEqual(["cn"]);
  });

  it("leaves a secret behind rather than copying an empty one", () => {
    const out = copiedAttributes(
      [
        attr("cn", ["Alice"]),
        { name: "userPassword", kind: kind({ name: "userPassword", sensitive: true }), values: [], withheld: true, valueCount: 1 },
      ],
      "cn",
      "Bob",
    );
    expect(out.map((a) => a.name)).toEqual(["cn"]);
  });
});
