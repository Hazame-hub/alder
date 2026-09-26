import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { TooltipProvider } from "@/components/ui";
import { AttributeEditor, type ConfigMark } from "./attribute-editor";

// A field on a configuration entry says what the model knows about it. The
// mark marks and never blocks: an attribute Alder's model has no answer for is
// still editable, because the model states what Alder changes and the
// directory decides what it takes.

function field(mark?: ConfigMark): string {
  return renderToStaticMarkup(
    <TooltipProvider>
      <AttributeEditor
        name="olcIdleTimeout"
        kind={{ name: "olcIdleTimeout", kind: "integer", singleValue: true, known: true }}
        required={false}
        values={["0"]}
        onChange={() => {}}
        configMark={mark}
      />
    </TooltipProvider>,
  );
}

function textOf(html: string): string {
  return html
    .replace(/<[^>]+>/g, " ")
    .replace(/&#x27;/g, "'")
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
}

describe("a field on a configuration entry", () => {
  it("says which settings Alder changes", () => {
    expect(textOf(field({ mutability: "writable" }))).toContain("Alder changes this");
  });

  it("says when a setting does nothing until the server restarts", () => {
    const text = textOf(field({ mutability: "read_only", restartRequired: true }));
    expect(text).toContain("next start");
  });

  it("separates what the model does not change from what it does not know", () => {
    expect(textOf(field({ mutability: "read_only" }))).toContain("not changed by Alder");
    expect(textOf(field({ mutability: "unknown" }))).toContain("not in Alder's model");
  });

  it("marks runtime state as not configuration", () => {
    expect(textOf(field({ mutability: "read_only", excluded: true }))).toContain("not configuration");
  });

  it("leaves the field editable whatever the mark says, and unmarked without one", () => {
    // No disabled input: the model is conservative, and a lock would make the
    // editor less capable than the server it is editing.
    // React writes a disabled control as disabled=""; the word also appears
    // inside Tailwind class names, which is not the same thing.
    expect(field({ mutability: "read_only" })).not.toContain('disabled=""');
    expect(textOf(field())).not.toContain("Alder");
  });
});
