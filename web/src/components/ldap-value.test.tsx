import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { LdapValueView, valueCopyText } from "./ldap-value";

// Rendered to markup rather than into a DOM: what matters here is what each
// state puts on the page, and the tests environment is deliberately DOM-free.
// Measuring whether a value is clipped needs a real layout, and is checked in
// a browser instead.

const aci =
  '(target ="ldap:///cn=monitor*")(targetattr != "aci || connection")(version 3.0; acl "monitor"; ' +
  'allow( read, search, compare ) userdn = "ldap:///anyone";)';
const dn = "cn=monitor,cn=userRoot,cn=ldbm database,cn=plugins,cn=config";

function render(props: Partial<Parameters<typeof LdapValueView>[0]> & { values: { text?: string; base64?: string; size?: number }[] }) {
  return renderToStaticMarkup(
    <LdapValueView expanded={false} clipped={false} onToggle={() => {}} contentId="v" {...props} />,
  );
}

/** The text a reader or a selection gets: tags dropped, entities decoded. */
function textOf(html: string): string {
  return html
    .replace(/<[^>]+>/g, "")
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&amp;/g, "&");
}

const valuesIn = (html: string) =>
  [...html.matchAll(/<div data-value=""[^>]*>(.*?)<\/div>/g)].map((m) => textOf(m[1] ?? ""));

describe("LdapValueView", () => {
  it("renders a short value as itself, with no controls", () => {
    const html = render({ values: [{ text: "17" }] });
    expect(valuesIn(html)).toEqual(["17"]);
    expect(html).not.toContain("<button");
    expect(html).toContain('data-state="collapsed"');
  });

  it("keeps the whole of a clipped value in the page, only collapsed by height", () => {
    const html = render({ values: [{ text: aci }], clipped: true });
    expect(valuesIn(html)).toEqual([aci]);
    expect(html).toContain("max-h-[2lh]");
    expect(html).toContain('aria-expanded="false"');
    expect(html).toContain('aria-controls="v"');
    expect(textOf(html)).toContain("Show full value");
  });

  it("shows the complete value when expanded, and offers to collapse it", () => {
    const html = render({ values: [{ text: dn }], clipped: true, expanded: true });
    expect(valuesIn(html)).toEqual([dn]);
    expect(html).toContain('data-state="expanded"');
    expect(html).not.toContain("max-h-[2lh]");
    expect(html).toContain('aria-expanded="true"');
    expect(textOf(html)).toContain("Show less");
  });

  it("returns to the compact presentation when collapsed again", () => {
    const expanded = render({ values: [{ text: dn }], clipped: true, expanded: true });
    const collapsed = render({ values: [{ text: dn }], clipped: true, expanded: false });
    expect(expanded).not.toContain("max-h-[2lh]");
    expect(collapsed).toContain("max-h-[2lh]");
    expect(valuesIn(collapsed)).toEqual([dn]);
  });

  it("breaks a DN between RDNs without changing its text", () => {
    const html = render({ values: [{ text: dn }] });
    expect(html.match(/<wbr\/>/g)).toHaveLength(4);
    expect(valuesIn(html)).toEqual([dn]);
  });

  it("keeps several values distinct, one element each", () => {
    const connections = [
      "1:20260913124517Z:12:12:-:cn=directory manager:0:0:0:14196:ip=172.21.0.4",
      "2:20260912160248Z:4:4:-:cn=Directory Manager:0:0:0:4:ip=local",
      "3:20260913125134Z:7:6:-:cn=directory manager:0:0:0:14265:ip=172.21.0.4",
    ];
    const html = render({ values: connections.map((text) => ({ text })), clipped: true });
    expect(valuesIn(html)).toEqual(connections);
    expect(textOf(html)).toContain("Show all 3 values");
    expect(html).toContain('aria-label="Copy all values"');
  });

  it("labels the copy control for a single value", () => {
    const html = render({ values: [{ text: aci }], clipped: true });
    expect(html).toContain('aria-label="Copy full value"');
  });

  it("renders an absent value as a dash", () => {
    expect(textOf(render({ values: [] }))).toBe("—");
  });
});

describe("valueCopyText", () => {
  it("copies a value exactly, never a presentation of it", () => {
    expect(valueCopyText([{ text: aci }])).toBe(aci);
    expect(valueCopyText([{ text: "  spaced  value " }])).toBe("  spaced  value ");
  });

  it("copies several values one per line, in order", () => {
    expect(valueCopyText([{ text: "a,b" }, { text: "c" }])).toBe("a,b\nc");
  });

  it("copies a binary value as its base64, not its size label", () => {
    expect(valueCopyText([{ base64: "/9j/4AAQ", size: 2048 }])).toBe("/9j/4AAQ");
  });
});
