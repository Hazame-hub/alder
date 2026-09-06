/**
 * The filter builder's logic, out of the component so it can be tested.
 *
 * The server parses and rebuilds every filter it is given, so nothing here is
 * the only defence — an invalid filter is refused with a message rather than
 * sent. What this does is stop the builder writing something into the box that
 * the operator cannot trust: the box is editable and shareable, and a filter
 * that reads as one query while meaning another is the failure to avoid.
 */

export type Clause = { attribute: string; op: string; value: string };

/**
 * escapeFilterValue applies RFC 4515 escaping to a value the user typed.
 *
 * A value holding filter metacharacters becomes an escaped assertion value
 * rather than structure, which is the same rule the server enforces.
 */
export function escapeFilterValue(v: string): string {
  return v.replace(/[\\*()\0]/g, (c) => {
    switch (c) {
      case "\\":
        return "\\5c";
      case "*":
        return "\\2a";
      case "(":
        return "\\28";
      case ")":
        return "\\29";
      default:
        return "\\00";
    }
  });
}

// An attribute description is a name — a letter followed by letters, digits and
// hyphens — or a numeric OID, either optionally carrying ";" options.
const attributeName = /^[A-Za-z][A-Za-z0-9-]*(;[A-Za-z0-9-]+)*$/;
const numericOID = /^[0-9]+(\.[0-9]+)+(;[A-Za-z0-9-]+)*$/;

/**
 * isAttributeName reports whether this can be the left-hand side of an
 * assertion.
 *
 * The attribute is not escaped the way a value is, and it must not be: RFC 4515
 * escaping applies to assertion values, and an attribute name holding a
 * parenthesis is not a name that needs escaping — it is not a name at all.
 * Emitting `\28foo` would produce a filter that is still invalid and now also
 * unreadable. So the builder checks instead, and declines to write a clause it
 * knows is malformed rather than composing one that reads as structure the
 * operator did not ask for.
 */
export function isAttributeName(a: string): boolean {
  const s = a.trim();
  return s !== "" && (attributeName.test(s) || numericOID.test(s));
}

type Operator = {
  id: string;
  label: string;
  render: (a: string, v: string) => string;
};

export const operators: Operator[] = [
  { id: "eq", label: "is", render: (a, v) => `(${a}=${escapeFilterValue(v)})` },
  {
    id: "contains",
    label: "contains",
    render: (a, v) => `(${a}=*${escapeFilterValue(v)}*)`,
  },
  {
    id: "starts",
    label: "starts with",
    render: (a, v) => `(${a}=${escapeFilterValue(v)}*)`,
  },
  {
    id: "ends",
    label: "ends with",
    render: (a, v) => `(${a}=*${escapeFilterValue(v)})`,
  },
  { id: "present", label: "is set", render: (a) => `(${a}=*)` },
  {
    id: "gte",
    label: "is at least",
    render: (a, v) => `(${a}>=${escapeFilterValue(v)})`,
  },
  {
    id: "lte",
    label: "is at most",
    render: (a, v) => `(${a}<=${escapeFilterValue(v)})`,
  },
  {
    id: "not",
    label: "is not",
    render: (a, v) => `(!(${a}=${escapeFilterValue(v)}))`,
  },
];

/** renderClause returns the clause as a filter, or null if it cannot be one. */
export function renderClause(c: Clause): string | null {
  if (!isAttributeName(c.attribute)) return null;
  const op = operators.find((o) => o.id === c.op) ?? operators[0];
  return op!.render(c.attribute.trim(), c.value);
}

/**
 * buildFilter joins the clauses that are usable.
 *
 * A clause with no attribute yet is skipped rather than treated as an error —
 * an empty row is a row still being filled in. A clause whose attribute cannot
 * be an attribute name is skipped too, and the caller shows it as such, because
 * silently folding it into the filter is how the box comes to disagree with
 * what was typed.
 */
export function buildFilter(join: "and" | "or", clauses: Clause[]): string {
  const parts = clauses
    .map(renderClause)
    .filter((p): p is string => p !== null);
  if (parts.length === 0) return "(objectClass=*)";
  if (parts.length === 1) return parts[0] as string;
  return `(${join === "and" ? "&" : "|"}${parts.join("")})`;
}
