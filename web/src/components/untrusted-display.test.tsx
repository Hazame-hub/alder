import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { LdapValueView, valueCopyText } from "./ldap-value";
import { PlanRow } from "./plan-summary";
import { reasonText } from "@/lib/recovery";

// What reaches the screen from a directory string that tries to reorder
// itself, rendered to markup the way the other component tests are. The tree,
// the entry header and clicking through to the original DN are checked in a
// real browser, where navigation exists.

const RLO = String.fromCharCode(0x202e);
const spoofed = `uid=report${RLO}txt.exe,ou=people,dc=alder,dc=test`;
const escaped = "uid=report\\u202etxt.exe,ou=people,dc=alder,dc=test";

/** The text a reader gets: tags dropped, entities decoded. */
function textOf(html: string): string {
  return html
    .replace(/<[^>]+>/g, "")
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&");
}

describe("an attribute value from the directory", () => {
  it("shows an override as its escape, and copies the value as it is", () => {
    const values = [{ text: spoofed }];
    const html = renderToStaticMarkup(
      <LdapValueView values={values} expanded={false} clipped={false} onToggle={() => {}} contentId="v" />,
    );
    expect(html).not.toContain(RLO);
    expect(textOf(html)).toContain(escaped);
    expect(valueCopyText(values)).toBe(spoofed);
  });

  it("leaves ordinary Unicode readable", () => {
    const html = renderToStaticMarkup(
      <LdapValueView
        values={[{ text: "cn=Zoë Ångström,ou=people,dc=alder,dc=test" }]}
        expanded={false}
        clipped={false}
        onToggle={() => {}}
        contentId="v"
      />,
    );
    expect(textOf(html)).toContain("cn=Zoë Ångström,ou=people,dc=alder,dc=test");
  });
});

describe("a plan row", () => {
  it("shows the DN escaped in its text and its tooltip", () => {
    const html = renderToStaticMarkup(
      <ul>
        <PlanRow
          item={{
            index: 0,
            dn: spoofed,
            action: "modify",
            exists: true,
            baseline: "b",
            recovery: { recoverability: "partial", reasons: [{ code: "server_owned_attribute", attribute: `x${RLO}y` }] },
          }}
        />
      </ul>,
    );
    expect(html).not.toContain(RLO);
    expect(textOf(html)).toContain(escaped);
    expect(html).toContain(`title="${escaped}"`);
    expect(textOf(html)).toContain("(x\\u202ey)");
  });

  it("leaves a normal DN unchanged", () => {
    const html = renderToStaticMarkup(
      <ul>
        <PlanRow item={{ index: 0, dn: "uid=alice,ou=people,dc=alder,dc=test", action: "delete", exists: true }} />
      </ul>,
    );
    expect(textOf(html)).toContain("uid=alice,ou=people,dc=alder,dc=test");
  });
});

describe("recovery text", () => {
  it("uses the same escaping as the rest of the interface", () => {
    expect(reasonText({ code: "sensitive_value_not_captured", attribute: `userPassword${RLO}` })).toContain(
      "(userPassword\\u202e)",
    );
  });
});
