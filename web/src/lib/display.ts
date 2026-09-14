/**
 * Text from a directory, made safe to look at.
 *
 * A DN, an attribute value, a schema name or a server's message is directory
 * data, and directory data can carry characters that act rather than show:
 * controls, and bidirectional overrides, embeddings, isolates and marks that
 * reorder what is displayed around them. uid=report<U+202E>txt.exe renders as
 * "uid=reportexe.txt", which is a spoof, not a name. React escapes markup; it
 * does nothing about what characters do once they are text.
 *
 * So every one of them is shown as its escape, \u202e, and nothing else is
 * touched: every printable character in every script stays as it is. Tab and
 * line feed are left too, because they lay text out and cannot reorder it --
 * a multi-line value still reads as lines. This is the same set the command
 * line escapes for a terminal (internal/cli/sanitize.go), which also escapes
 * tab and line feed, a terminal having no other way to show them.
 *
 * Presentation only. The value a copy button copies, the DN a click navigates
 * to and everything sent to the API stay the original string: escape where a
 * string is rendered, never where it is kept.
 */
const UNSAFE = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g;

export function safeText(text: string | null | undefined): string {
  if (!text) return "";
  return text.replace(UNSAFE, (ch) => `\\u${ch.charCodeAt(0).toString(16).padStart(4, "0")}`);
}

/** Whether a string holds anything safeText would escape. */
export function hasUnsafeText(text: string): boolean {
  UNSAFE.lastIndex = 0;
  const found = UNSAFE.test(text);
  UNSAFE.lastIndex = 0;
  return found;
}
