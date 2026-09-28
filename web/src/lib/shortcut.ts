/**
 * How to write the jump shortcut for the keyboard in front of the reader.
 *
 * A hint that says the wrong key is worse than none: it teaches a gesture
 * that does nothing, and the reader concludes the feature is broken rather
 * than that the label is. So this reads the platform rather than picking a
 * house style.
 *
 * `navigator.platform` is deprecated but is still the only thing every
 * browser agrees on; `userAgentData.platform` is Chromium-only. Both are
 * consulted, and anything unrecognised gets `Ctrl`, which is right
 * everywhere except macOS.
 *
 * The argument exists so a test can ask about a platform it is not running
 * on. In the application nothing passes it.
 */
export function jumpShortcut(platform?: string): string {
  const name = platform ?? detectPlatform();
  return /mac|iphone|ipad|ipod/i.test(name) ? "⌘K" : "Ctrl K";
}

function detectPlatform(): string {
  // Server-rendered in tests, and a browser is not guaranteed to expose
  // either of these. An unreadable platform is not an error here; it is the
  // ordinary case that falls through to Ctrl.
  if (typeof navigator === "undefined") return "";
  const data = (navigator as { userAgentData?: { platform?: string } }).userAgentData;
  return data?.platform || navigator.platform || navigator.userAgent || "";
}
