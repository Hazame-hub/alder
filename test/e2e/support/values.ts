/**
 * Long on purpose: it makes the modify request the one large thing the
 * client sends, which is how the proxy finds the write inside TLS it cannot
 * read. Under 1024 characters, because that is the bound in description's
 * syntax and the editor holds a value to it.
 */
export function longValue(token: string): string {
  const head = `interrupted-${token}-`;
  return head + "x".repeat(1_000 - head.length);
}
