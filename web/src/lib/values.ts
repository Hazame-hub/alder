import type { AttributeKind, AttributeValue } from "./api";

/**
 * An attribute value crosses the wire as either text or base64, and the editor
 * has to work in one representation. These helpers are the only place that
 * conversion happens, so a binary value cannot be quietly turned into text by
 * some component that forgot to check.
 */

/** isBinary reports a value the UI must not offer a text box for. */
export function isBinary(v: AttributeValue): boolean {
  return v.base64 !== undefined;
}

/** displayText renders a value for reading. Binary values are described, not shown. */
export function displayText(v: AttributeValue): string {
  if (v.text !== undefined) return v.text;
  if (v.base64 !== undefined) return `«${formatBytes(v.size ?? 0)} of binary data»`;
  return "";
}

/** editableText returns the text of a value, or null when it cannot be edited as text. */
export function editableText(v: AttributeValue): string | null {
  return v.text ?? null;
}

export function textValue(text: string): AttributeValue {
  return { text, size: new TextEncoder().encode(text).length };
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} kB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * A JPEG or certificate value can be previewed. Nothing else binary can, and
 * guessing at a MIME type from bytes we did not inspect would be worse than
 * showing the size.
 */
export function imageDataUrl(v: AttributeValue, kind: AttributeKind): string | null {
  if (kind.kind !== "image" || v.base64 === undefined) return null;
  return `data:image/jpeg;base64,${v.base64}`;
}

/**
 * multiline reports a value the editor should give a textarea rather than a
 * single-line input, either because its syntax says so or because the value it
 * already holds contains a newline.
 */
export function multiline(kind: AttributeKind, values: AttributeValue[]): boolean {
  if (kind.kind === "text") return true;
  return values.some((v) => (v.text ?? "").includes("\n"));
}

/** inputType maps a schema kind onto an HTML input type. */
export function inputType(kind: AttributeKind): string {
  switch (kind.kind) {
    case "integer":
      return "number";
    case "password":
      return "password";
    default:
      return "text";
  }
}

/**
 * A GeneralizedTime is "20260902071142Z", which nobody reads at a glance.
 * Rendering it as a local timestamp is the single most useful piece of
 * formatting in an entry viewer.
 */
export function formatGeneralizedTime(raw: string): string | null {
  const m = /^(\d{4})(\d{2})(\d{2})(\d{2})(\d{2})(\d{2})?(?:[.,]\d+)?(Z|[+-]\d{2,4})?$/.exec(
    raw,
  );
  if (!m) return null;
  const [, y, mo, d, h, mi, s, zone] = m;
  let iso = `${y}-${mo}-${d}T${h}:${mi}:${s ?? "00"}`;
  if (zone === "Z" || zone === undefined) {
    iso += "Z";
  } else {
    const sign = zone.slice(0, 1);
    const digits = zone.slice(1);
    const hh = digits.slice(0, 2);
    const mm = digits.length > 2 ? digits.slice(2, 4) : "00";
    iso += `${sign}${hh}:${mm}`;
  }
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return null;
  return parsed.toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** rdnOf returns the leftmost RDN of a DN string, respecting escaping. */
export function rdnOf(dn: string): string {
  return splitDN(dn)[0] ?? dn;
}

/** parentOf returns the DN one level up, or "" at the top. */
export function parentOf(dn: string): string {
  return splitDN(dn).slice(1).join(",");
}

/**
 * splitDN splits on unescaped commas.
 *
 * The server is the authority on DN syntax and this is only for display, but
 * splitting on a bare comma would break every DN whose RDN contains an escaped
 * one, and the harness has such an entry precisely because tools get this wrong.
 */
export function splitDN(dn: string): string[] {
  const parts: string[] = [];
  let current = "";
  let escaped = false;
  for (const ch of dn) {
    if (escaped) {
      current += ch;
      escaped = false;
      continue;
    }
    if (ch === "\\") {
      current += ch;
      escaped = true;
      continue;
    }
    if (ch === ",") {
      parts.push(current);
      current = "";
      continue;
    }
    current += ch;
  }
  parts.push(current);
  return parts;
}

/** ancestorsOf returns every DN from the given one up to its topmost component. */
export function ancestorsOf(dn: string): string[] {
  const parts = splitDN(dn);
  const out: string[] = [];
  for (let i = parts.length - 1; i >= 0; i--) {
    out.push(parts.slice(i).join(","));
  }
  return out;
}
