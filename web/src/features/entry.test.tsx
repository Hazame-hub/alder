import { describe, expect, it } from "vitest";
import { driftedAttributes, groupAttributes } from "./entry";
import type { EntryAttribute } from "@/lib/api";

// Where each attribute is filed in the viewer.
//
// Five headings built from five independent filters, which is how an
// attribute came to be in two of them: olcAccess is an ordinary attribute of
// an OpenLDAP database entry -- not operational -- so holding it back from
// the operational groups alone left it under Optional as well, once with the
// sentence explaining why there is no field for it and once without.
//
// The property worth asserting is not which heading a given attribute gets.
// It is that the headings partition the entry: every attribute under exactly
// one, none under two, none under none.

const kind = (over: Partial<EntryAttribute["kind"]> = {}): EntryAttribute["kind"] => ({
  name: "x",
  kind: "string",
  known: true,
  ...over,
});

const attr = (
  name: string,
  over: Partial<EntryAttribute> = {},
  kindOver: Partial<EntryAttribute["kind"]> = {},
): EntryAttribute => ({
  name,
  kind: kind({ name, ...kindOver }),
  values: [{ text: "v", size: 1 }],
  ...over,
});

const ACCESS = "Alder reads access rules and does not write them.";

/** One of everything the viewer can be handed. */
const EVERYTHING: EntryAttribute[] = [
  attr("cn", { required: true }),
  attr("mail"),
  attr("userPassword", { withheld: true, valueCount: 1 }, { sensitive: true }),
  // Operational, and the server lets a client set it.
  attr("nsAccountLock", {}, { operational: true }),
  // Operational and NO-USER-MODIFICATION.
  attr("entryUUID", {}, { operational: true, readOnly: true }),
  // Operational, settable, and Alder will not write it.
  attr("aci", {}, { operational: true, elsewhere: ACCESS }),
  // NOT operational, settable, and Alder will not write it. The one that
  // was in two groups.
  attr("olcAccess", {}, { elsewhere: ACCESS }),
  // Not operational, and the directory owns it anyway.
  attr("structuralObjectClass", {}, { readOnly: true }),
];

describe("how the viewer files an entry's attributes", () => {
  it("puts every attribute under exactly one heading", () => {
    const groups = groupAttributes(EVERYTHING);
    const seen = new Map<string, string[]>();
    for (const group of groups) {
      for (const item of group.items) {
        seen.set(item.name, [...(seen.get(item.name) ?? []), group.title]);
      }
    }
    for (const a of EVERYTHING) {
      expect(seen.get(a.name) ?? [], `${a.name} is filed under`).toHaveLength(1);
    }
  });

  it("files an access rule under the heading that explains it", () => {
    const groups = groupAttributes(EVERYTHING);
    const elsewhere = groups.find((g) => g.title === "Shown here, edited elsewhere");
    expect(elsewhere?.items.map((a) => a.name).sort()).toEqual(["aci", "olcAccess"]);
  });

  it("keeps the two operational headings apart on what the server owns", () => {
    const groups = groupAttributes(EVERYTHING);
    const yours = groups.find((g) => g.title.includes("yours to set"));
    const theirs = groups.find((g) => g.title.includes("the directory owns"));
    expect(yours?.items.map((a) => a.name)).toEqual(["nsAccountLock"]);
    expect(theirs?.items.map((a) => a.name)).toEqual(["entryUUID"]);
  });
});

// What counts as somebody else having edited the entry under you.
//
// The banner says "your edits are intact; applying them will overwrite the
// newer values", which is a sentence about a colleague. Once the editor
// started offering the settable operational attributes, the server itself
// began setting it off: a failed bind bumps passwordRetryCount with nobody
// touching anything.

const OPERATIONAL = new Set(["passwordretrycount", "nsaccountlock"]);

describe("what counts as the entry having changed underneath", () => {
  it("reports an ordinary attribute somebody else changed", () => {
    const drifted = driftedAttributes(
      { cn: ["Alice"], description: ["old"] },
      { cn: ["Alice"], description: ["someone else's"] },
      { cn: ["Alice"], description: ["old"] },
      OPERATIONAL,
    );
    expect(drifted).toEqual(["description"]);
  });

  it("says nothing when the server moved its own attribute", () => {
    // The pending change does not mention passwordRetryCount -- computeMods
    // only emits what the draft changed -- so there is nothing to overwrite
    // and nobody to overwrite it from.
    const drifted = driftedAttributes(
      { description: ["old"], passwordRetryCount: ["1"] },
      { description: ["old"], passwordRetryCount: ["2"] },
      { description: ["being typed"], passwordRetryCount: ["1"] },
      OPERATIONAL,
    );
    expect(drifted).toEqual([]);
  });

  it("still reports one the draft is also changing", () => {
    // Two administrators reaching for the same lock at once is a real
    // collision, and the one who applies second wins silently otherwise.
    const drifted = driftedAttributes(
      { nsAccountLock: ["true"] },
      { nsAccountLock: [] },
      { nsAccountLock: [] },
      OPERATIONAL,
    );
    expect(drifted).toEqual(["nsAccountLock"]);
  });

  it("says nothing when nothing moved", () => {
    const same = { cn: ["Alice"] };
    expect(driftedAttributes(same, { cn: ["Alice"] }, same, OPERATIONAL)).toEqual([]);
  });
});
