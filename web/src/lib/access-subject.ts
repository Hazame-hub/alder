import type { components } from "@/lib/api.gen";

type Capabilities = components["schemas"]["Capabilities"];

/**
 * The decisions the "ask about another identity" control makes, as plain
 * functions so they can be tested without a dialog.
 *
 * The one fact everything here turns on: asking about somebody else changes
 * *only* the server's own verdict. The rules the server holds for an entry are
 * the rules for that entry — they are the same list whoever you ask about, and
 * so are Alder's applies/maybe/no marks on them, which are read from the rule
 * text and the target DN alone. A screen that changed the subject and left the
 * rules looking like an answer about that subject would be the exact failure
 * the access view was written to avoid.
 */

/**
 * Whether two DNs name the same thing, the way the rest of the app compares
 * them on the client: case-folded with the edges trimmed.
 *
 * This is deliberately not RFC 4517 matching — that needs the schema, and the
 * server does it properly. Here it decides one thing: whether the identity
 * being asked about is the one this session is bound as, so the panel can say
 * "you" rather than repeating a DN the reader already knows. Getting it wrong
 * in the conservative direction shows a DN; there is nothing to leak.
 */
export function dnEquals(a: string | undefined, b: string | undefined): boolean {
  if (!a || !b) return false;
  return a.trim().toLowerCase() === b.trim().toLowerCase();
}

/** The most specific naming context that contains this DN, for a DN picker to
 * search below. Falls back to the DN's own parent, which is always a place the
 * server will accept as a base. */
export function searchBaseFor(dn: string, contexts: string[] | undefined): string {
  let best = "";
  for (const context of contexts ?? []) {
    const suffix = context.trim().toLowerCase();
    if (!suffix) continue;
    const target = dn.trim().toLowerCase();
    if (target === suffix || target.endsWith("," + suffix)) {
      if (suffix.length > best.length) best = context;
    }
  }
  if (best) return best;
  const comma = dn.indexOf(",");
  return comma >= 0 ? dn.slice(comma + 1) : dn;
}

export type SubjectView =
  /** No subject asked for: the verdict is about the session's own identity. */
  | { kind: "you"; dn: string }
  /** A subject was asked for, and it is this session's own identity anyway. */
  | { kind: "you"; dn: string; asked: true }
  /** A subject was asked for, and it is somebody else. */
  | { kind: "other"; dn: string };

/**
 * Who the verdict on screen is about, decided from what the UI asked rather
 * than from what the server echoed back.
 *
 * The distinction matters: the handler fills a blank `as` with the session's
 * own bind DN before it asks the server, so `effective.subject` is non-empty
 * for every bound session and cannot tell "me" from "somebody else". Reading
 * it that way is what made the panel say "this identity" to a person asking
 * about themselves.
 */
export function subjectView(asked: string, bindDn: string | undefined): SubjectView {
  const trimmed = asked.trim();
  if (!trimmed) return { kind: "you", dn: bindDn ?? "" };
  if (dnEquals(trimmed, bindDn)) return { kind: "you", dn: trimmed, asked: true };
  return { kind: "other", dn: trimmed };
}

/**
 * Whether this server can be asked the question at all.
 *
 * Only where it publishes an effective-rights control. On a server without
 * one the response is byte-identical whatever subject is named — the rules are
 * about the entry, and the verdict is the only identity-scoped thing in the
 * report. Offering a control that changes nothing would make Alder's reading
 * of the rule text look like a per-identity answer from the server, which is
 * the one claim this view must never make.
 */
export function canAskAboutAnotherIdentity(caps: Capabilities | undefined): boolean {
  return caps?.effectiveRights === true;
}

/** The short form of a DN, for a heading that must stay on one line. */
export function shortDn(dn: string): string {
  const comma = dn.indexOf(",");
  return comma > 0 ? dn.slice(0, comma) : dn;
}

/**
 * The sentence that keeps the rules list honest while another identity's
 * verdict is on screen. Empty when the verdict is about the reader, because
 * then there is nothing to disclaim.
 */
export function rulesScopeNote(view: SubjectView): string {
  if (view.kind !== "other") return "";
  return (
    "The rules below are the ones this server holds about this entry. They are the same list " +
    "whoever you ask about, and so are the marks on them — only the verdict above is about " +
    shortDn(view.dn) +
    "."
  );
}
