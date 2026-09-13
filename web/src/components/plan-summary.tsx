import {
  Ban,
  CircleAlert,
  CirclePlus,
  Cog,
  Equal,
  FileCode2,
  GitBranch,
  KeyRound,
  Link2Off,
  Pencil,
  SignpostBig,
  Trash2,
  Users,
} from "lucide-react";
import type { Plan, PlanAction, PlanItem, PlanProblemCode } from "@/lib/api";

/**
 * What a set of changes would do, before any of it is done.
 *
 * The confirmation dialog has always shown the exact LDIF for one change. This
 * is the question above it — what a whole set would do to a directory that may
 * have moved since the set was put together — and it is read top down: what
 * kind of change, how much of it, what it touches beyond the entries it names,
 * and then each item.
 *
 * Everything here comes from fields the API returns for a client to switch on:
 * action, problem code, target area, membership and reference counts. Nothing
 * is inferred from prose, which is also what lets a command-line client show
 * the same thing. The exact LDIF stays one tab away, as the authoritative
 * low-level form; this does not restate it.
 */

const LOOK: Record<
  PlanAction,
  { label: string; icon: typeof Pencil; className: string }
> = {
  add: {
    label: "Add",
    icon: CirclePlus,
    className: "text-emerald-600 dark:text-emerald-400",
  },
  modify: {
    label: "Modify",
    icon: Pencil,
    className: "text-blue-600 dark:text-blue-400",
  },
  delete: { label: "Delete", icon: Trash2, className: "text-destructive" },
  rename: {
    label: "Rename",
    icon: SignpostBig,
    className: "text-blue-600 dark:text-blue-400",
  },
  set_password: {
    label: "Set password",
    icon: KeyRound,
    className: "text-blue-600 dark:text-blue-400",
  },
  unchanged: {
    label: "No change",
    icon: Equal,
    className: "text-muted-foreground",
  },
  conflict: {
    label: "Conflict",
    icon: CircleAlert,
    className: "text-warning-tint-foreground",
  },
  invalid: { label: "Invalid", icon: Ban, className: "text-destructive" },
};

/** What each problem code means, in a sentence. The code itself is shown too. */
const PROBLEM: Record<PlanProblemCode, string> = {
  entry_missing: "the entry it needs does not exist",
  entry_exists: "the entry already exists",
  has_children: "the entry has children",
  rename_target_exists: "the new name is already taken",
  object_class_undefined: "an object class is not defined by the schema",
  attribute_undefined: "an attribute is not defined by the schema",
  attribute_not_permitted: "no object class on the entry permits the attribute",
  single_value_violation:
    "a single-valued attribute would hold more than one value",
  missing_required_attribute: "a required attribute is missing",
};

function plural(n: number, one: string, many = `${one}s`) {
  return `${n} ${n === 1 ? one : many}`;
}

export function PlanSummary({ plan }: { plan: Plan }) {
  const c = plan.counts;
  const invalid = c.invalid ?? 0;
  // Only what is non-zero. A row of zeroes is noise, and the counts a reader is
  // scanning for — nothing to do, cannot be done — stand out better alone.
  const tallies: Array<[number, string, string?]> = [
    [c.add, "addition"],
    [c.modify, "modification"],
    [c.delete, "deletion"],
    [c.rename, "rename"],
    [c.setPassword, "password change"],
    [c.unchanged, "unchanged", "unchanged"],
    [c.conflict, "conflict"],
    [invalid, "invalid change"],
  ];
  const applicable = c.add + c.modify + c.delete + c.rename + c.setPassword;

  return (
    <div className="mb-4 space-y-3 rounded-md border bg-card p-3">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 text-sm">
        <span className="font-medium">
          {plural(c.examined, "change")} examined
        </span>
        {tallies
          .filter(([n]) => n > 0)
          .map(([n, one, many]) => (
            <span key={one} className="text-muted-foreground">
              {plural(n, one, many)}
            </span>
          ))}
        <span className="ml-auto text-xs text-muted-foreground">
          {applicable === 0
            ? "Nothing would be applied."
            : "Nothing has been applied."}
        </span>
      </div>

      <PlanImpactList plan={plan} />

      <ol className="divide-y rounded border">
        {plan.items.map((item) => (
          <PlanRow key={item.index} item={item} />
        ))}
      </ol>

      {c.conflict + invalid > 0 ? (
        <p className="text-xs text-warning-tint-foreground">
          Conflicts and invalid changes are reported, not attempted. Applying
          the plan sends only the changes that would do something.
        </p>
      ) : null}
    </div>
  );
}

/**
 * The facts a plan establishes beyond the entries it names. Shared with the
 * single-change dialog, which shows the same facts for its one change.
 */
export function PlanImpactList({ plan }: { plan: Plan }) {
  const impact = plan.impact;
  const lines: Array<{ icon: typeof Pencil; tone?: string; text: string }> = [];

  for (const st of plan.subtrees ?? []) {
    lines.push({
      icon: GitBranch,
      tone: "text-destructive",
      text: `Removes the whole branch under ${st.root}: ${plural(st.entries, "entry", "entries")}.`,
    });
  }
  if (impact) {
    if (impact.kinds.schema > 0) {
      lines.push({
        icon: FileCode2,
        tone: "text-warning-tint-foreground",
        text: `${plural(impact.kinds.schema, "change")} to the schema, which changes what every entry may hold.`,
      });
    }
    if (impact.kinds.config > 0) {
      lines.push({
        icon: Cog,
        tone: "text-warning-tint-foreground",
        text: `${plural(impact.kinds.config, "change")} to the server's own configuration.`,
      });
    }
    const m = impact.membership;
    if (m.gained + m.removed > 0) {
      const parts = [];
      if (m.gained > 0) parts.push(`${plural(m.gained, "membership")} gained`);
      if (m.removed > 0)
        parts.push(`${plural(m.removed, "membership")} removed`);
      lines.push({
        icon: Users,
        text: `${parts.join(", ")} across ${plural(m.groups, "entry", "entries")}.`,
      });
    }
    const r = impact.references;
    if (r.analysed && r.found > 0) {
      lines.push({
        icon: Link2Off,
        tone: r.dangling > 0 ? "text-warning-tint-foreground" : undefined,
        text:
          `${plural(r.found, "reference")} to entries this plan deletes or renames` +
          (r.dangling > 0
            ? `; ${r.dangling} would be left pointing at nothing.`
            : ", all of them dealt with by the plan.") +
          (r.truncated
            ? " The search reached its bound, so there may be more."
            : ""),
      });
    } else if (
      !r.analysed &&
      r.reason &&
      (plan.counts.delete > 0 || plan.counts.rename > 0)
    ) {
      lines.push({ icon: Link2Off, text: r.reason });
    }
  }
  if (lines.length === 0) return null;

  return (
    <ul className="space-y-1 text-sm">
      {lines.map((line) => (
        <li
          key={line.text}
          className={`flex items-start gap-2 ${line.tone ?? ""}`}
        >
          <line.icon className="mt-0.5 size-3.5 shrink-0" />
          <span>{line.text}</span>
        </li>
      ))}
      {plan.impact?.references.analysed ? (
        <li className="pl-5 text-xs text-muted-foreground">
          References are searched as your own bind, so an entry the access rules
          hide from you is not counted.
        </li>
      ) : null}
    </ul>
  );
}

export function PlanRow({ item }: { item: PlanItem }) {
  const look = LOOK[item.action] ?? LOOK.conflict;
  const Icon = look.icon;
  const kind = item.kind && item.kind !== "data" ? item.kind : null;
  return (
    <li className="flex items-start gap-3 px-3 py-2 text-sm">
      <span
        className={`flex w-32 shrink-0 items-center gap-1.5 text-xs font-medium ${look.className}`}
      >
        <Icon className="size-3.5 shrink-0" />
        {look.label}
      </span>
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-dn" title={item.dn}>
            {item.dn}
          </span>
          {kind ? (
            <span className="shrink-0 rounded border px-1 text-[10px] uppercase tracking-wide text-warning-tint-foreground">
              {kind}
            </span>
          ) : null}
          {item.intent === "desired" ? (
            <span className="shrink-0 rounded border px-1 text-[10px] uppercase tracking-wide text-muted-foreground">
              desired state
            </span>
          ) : null}
        </div>
        {item.problem ? (
          <div className="text-xs text-warning-tint-foreground">
            <code className="font-mono">{item.problem.code}</code>
            {" — "}
            {PROBLEM[item.problem.code] ?? "the change would not apply"}
            {item.problem.attribute ? (
              <>
                {" "}
                (<code className="font-mono">{item.problem.attribute}</code>)
              </>
            ) : null}
          </div>
        ) : null}
        {item.reason && !item.problem ? (
          <div className="text-xs text-muted-foreground">{item.reason}</div>
        ) : null}
        {item.membership?.map((m) => (
          <div key={m.attribute} className="space-y-0.5 text-xs">
            {m.gained.map((v) => (
              <div
                key={`+${v}`}
                className="font-dn text-emerald-700 dark:text-emerald-400"
              >
                + {m.attribute}: {v}
              </div>
            ))}
            {m.removed.map((v) => (
              <div key={`-${v}`} className="font-dn text-destructive">
                − {m.attribute}: {v}
              </div>
            ))}
          </div>
        ))}
        {item.references && item.references.count > 0 ? (
          <div
            className={`text-xs ${item.references.dangling > 0 ? "text-warning-tint-foreground" : "text-muted-foreground"}`}
          >
            Named by {plural(item.references.count, "reference")} (
            {item.references.byAttribute
              .map((b) => `${b.count} ${b.attribute}`)
              .join(", ")}
            )
            {item.references.dangling > 0
              ? `; ${item.references.dangling} left dangling by this plan`
              : "; all removed by this plan"}
            .
          </div>
        ) : null}
        {item.skippedAttributes?.length ? (
          <div className="text-xs text-muted-foreground">
            Left alone, because the directory owns them:{" "}
            {item.skippedAttributes.join(", ")}
          </div>
        ) : null}
      </div>
    </li>
  );
}
