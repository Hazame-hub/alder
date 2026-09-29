import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { JumpButton } from "./app";

// The header is where an audit went looking for a way to search and found
// nothing, then discovered Ctrl+K by guessing. What is checked here is that
// there is something to find.

function textOf(html: string): string {
  return html
    .replace(/<[^>]+>/g, " ")
    .replace(/&#x27;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, "&")
    .replace(/\s+/g, " ")
    .trim();
}

const header = () => renderToStaticMarkup(<JumpButton onClick={() => {}} />);

describe("reaching the jump palette without knowing it exists", () => {
  it("offers something to click, worded as what it does", () => {
    expect(textOf(header())).toContain("Jump to");
  });

  it("prints the keystroke on it, so the second visit costs no click", () => {
    // Either spelling is correct; which one depends on the platform the
    // browser reports, and jumpShortcut has its own tests for that.
    expect(textOf(header())).toMatch(/⌘K|Ctrl K/);
  });

  it("renders it as a <kbd>, not as ordinary text", () => {
    expect(header()).toContain("<kbd");
  });
});
