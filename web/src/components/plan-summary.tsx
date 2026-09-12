import {
  CircleAlert,
  CirclePlus,
  Equal,
  KeyRound,
  Pencil,
  SignpostBig,
  Trash2,
} from "lucide-react";
import type { Plan, PlanAction, PlanItem } from "@/lib/api";

/**
 * What a set of changes would do, before any of it is done.
 *
 * The confirmation dialog has always shown the exact LDIF for one change. This
 * is the question above it — twenty staged changes against a directory that has
 * moved since they were staged, and which of them still do what you meant.
 *
 * Deliberately a summary and a list rather than a new view. The exact LDIF is
 * already one tab away and does not need repeating here; what is not available
 * anywhere else is the count, and which rows would do nothing.
 */

const LOOK: Record<PlanAction, { label: string; icon: typeof Pencil; className: string }> = {
  add: { label: "Add", icon: CirclePlus, className: "text-emerald-600 dark:text-emerald-400" },
  modify: { label: "Modify", icon: Pencil, className: "text-blue-600 dark:text-blue-400" },
  delete: { label: "Delete", icon: Trash2, className: "text-destructive" },
  rename: { label: "Rename", icon: SignpostBig, className: "text-blue-600 dark:text-blue-400" },
  set_password: { label: "Set password", icon: KeyRound, className: "text-blue-600 dark:text-blue-400" },
  unchanged: { label: "No change", icon: Equal, className: "text-muted-foreground" },
  conflict: { label: "Conflict", icon: CircleAlert, className: "text-warning-tint-foreground" },
};

export function PlanSummary({ plan }: { plan: Plan }) {
  const c = plan.counts;
  // Only what is non-zero, plus the total. A row of seven zeroes is noise, and
  // the two that matter — nothing to do, and cannot be done — are the ones a
  // reader is scanning for.
  const tallies: Array<[string, number]> = [
    ["addition", c.add],
    ["modification", c.modify],
    ["deletion", c.delete],
    ["rename", c.rename],
    ["password change", c.setPassword],
    ["unchanged", c.unchanged],
    ["conflict", c.conflict],
  ];

  return (
    <div className="mb-4 rounded-md border bg-card p-3">
      <div className="mb-2 flex flex-wrap items-baseline gap-x-4 gap-y-1 text-sm">
        <span className="font-medium">
          {c.examined} entr{c.examined === 1 ? "y" : "ies"} examined
        </span>
        {tallies
          .filter(([, n]) => n > 0)
          .map(([name, n]) => (
            <span key={name} className="text-muted-foreground">
              {n} {name}
              {n === 1 || name === "unchanged" ? "" : "s"}
            </span>
          ))}
        <span className="ml-auto text-xs text-muted-foreground">Nothing has been applied.</span>
      </div>

      <ol className="divide-y rounded border">
        {plan.items.map((item) => (
          <PlanRow key={item.index} item={item} />
        ))}
      </ol>

      {c.conflict > 0 ? (
        <p className="mt-2 text-xs text-warning-tint-foreground">
          A conflict is a change the directory would refuse as things stand. It
          is reported rather than attempted; applying will not send it.
        </p>
      ) : null}
    </div>
  );
}

function PlanRow({ item }: { item: PlanItem }) {
  const look = LOOK[item.action];
  const Icon = look.icon;
  return (
    <li className="flex items-start gap-3 px-3 py-2 text-sm">
      <span
        className={`flex w-32 shrink-0 items-center gap-1.5 text-xs font-medium ${look.className}`}
      >
        <Icon className="size-3.5 shrink-0" />
        {look.label}
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-dn" title={item.dn}>
          {item.dn}
        </div>
        {item.reason ? (
          <div className="mt-0.5 text-xs text-muted-foreground">{item.reason}</div>
        ) : null}
        {item.skippedAttributes?.length ? (
          <div className="mt-0.5 text-xs text-muted-foreground">
            Left alone, because the directory owns them:{" "}
            {item.skippedAttributes.join(", ")}
          </div>
        ) : null}
      </div>
    </li>
  );
}
