import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, FileUp, SearchCheck, X } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import { changeset } from "@/lib/changeset";
import {
  parseSavedPlan,
  savedPlanOrigin,
  sessionMismatch,
} from "@/lib/saved-plan";
import type { SavedPlan, SavedPlanOrigin } from "@/lib/saved-plan";
import { Button } from "@/components/ui/button";
import { safeText } from "@/lib/display";

/**
 * Loading a plan alder plan --out saved: read it, show what it holds, and
 * stage its changes for review.
 *
 * As with a recovery bundle, nothing is applied on loading. "Review plan"
 * stages the changes exactly as written, each with the state it expects, and
 * the ordinary plan checks them against the directory: a change whose entry
 * moved since the plan was saved shows as a conflict, and the whole plan is
 * then refused, as alder apply --plan refuses it, rather than applied in part. A plan made against another directory
 * or bind is refused outright, as alder apply --plan refuses it.
 */
const countLabel = {
  add: "to add",
  modify: "to modify",
  delete: "to delete",
  modrdn: "to rename or move",
} as const;

export function SavedPlanLoader({ onStaged }: { onStaged: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [plan, setPlan] = useState<SavedPlan | null>(null);
  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => unwrap(await api.GET("/session")),
  });

  const reset = () => {
    setPlan(null);
    setError(null);
  };

  const load = (text: string) => {
    const reading = parseSavedPlan(text);
    if (reading.ok) setPlan(reading.plan);
    else setError(reading.message);
  };

  const mismatch =
    plan && session.data
      ? sessionMismatch(plan.environment, session.data)
      : null;

  const stage = () => {
    if (!plan) return;
    const outcome = changeset.addMany(
      plan.changes.map((c) => ({
        change: c,
        label: `Saved plan: ${c.type} ${c.dn}`,
      })),
    );
    if (outcome.staged > 0) {
      // Set after staging: staging drops any origin, and this one says the
      // basket is exactly the plan, which is applied whole or not at all.
      changeset.setOrigin(savedPlanOrigin(plan));
      reset();
      onStaged();
    } else {
      setError(
        `The changeset has room for ${outcome.capacity} more changes, and this plan has ${outcome.refused}.`,
      );
    }
  };

  const counts = plan
    ? (["add", "modify", "delete", "modrdn"] as const)
        .map(
          (t) => [t, plan.changes.filter((c) => c.type === t).length] as const,
        )
        .filter(([, n]) => n > 0)
        .map(([t, n]) => `${n} ${countLabel[t]}`)
    : [];
  const canReview =
    plan !== null &&
    plan.changes.length > 0 &&
    session.data !== undefined &&
    mismatch === null;

  return (
    <div className="mb-5 rounded-md border p-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-sm font-medium">Saved plan</div>
          <p className="text-xs text-muted-foreground">
            Load a plan saved with <code>alder plan --out</code>. Its changes
            are staged here, each expecting the entry as it was planned; nothing
            is applied on loading.
          </p>
        </div>
        {plan ? (
          <Button variant="ghost" size="icon" title="Close" onClick={reset}>
            <X />
          </Button>
        ) : (
          <Button
            variant="outline"
            size="sm"
            onClick={() => input.current?.click()}
          >
            <FileUp />
            Load saved plan
          </Button>
        )}
        <input
          ref={input}
          type="file"
          accept="application/json,.json"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (!file) return;
            reset();
            void file.text().then(load);
          }}
        />
      </div>

      {error ? <p className="mt-2 text-sm text-destructive">{error}</p> : null}

      {plan ? <SavedPlanSummary plan={plan} counts={counts} /> : null}

      {mismatch ? (
        <div className="mt-3 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
          <div className="flex items-center gap-1.5 font-medium">
            <AlertTriangle className="size-4" />
            This plan was made for another directory or login
          </div>
          <p className="mt-1 text-xs">
            {mismatch} A saved plan is applied only where it was made; plan the
            input again against this directory instead.
          </p>
        </div>
      ) : null}

      {plan ? (
        <div className="mt-3 flex items-center gap-3">
          <Button size="sm" disabled={!canReview} onClick={stage}>
            <SearchCheck />
            Review plan
          </Button>
          <span className="text-xs text-muted-foreground">
            {plan.changes.length === 0
              ? "This plan changes nothing."
              : "Stages these changes and checks them against the directory. Applying is a separate step."}
          </span>
        </div>
      ) : null}
    </div>
  );
}

export function SavedPlanSummary({
  plan,
  counts,
}: {
  plan: SavedPlan;
  counts: string[];
}) {
  return (
    <div className="mt-3 space-y-3 text-sm">
      <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">Created</dt>
        <dd>
          {safeText(plan.createdAt)} by Alder {safeText(plan.alderVersion)}
        </dd>
        <dt className="text-muted-foreground">Made from</dt>
        <dd>
          {plan.input.kind === "ldif" ? "an LDIF document" : "--changes"}
          {plan.input.mode ? ` read as ${safeText(plan.input.mode)}` : ""}
          <span className="block break-all font-dn text-muted-foreground">
            {safeText(plan.input.digest)}
          </span>
        </dd>
        <dt className="text-muted-foreground">Directory</dt>
        <dd className="break-all font-dn">
          {safeText(plan.environment.host)}, binding{" "}
          {plan.environment.bindDn === ""
            ? "anonymously"
            : `as ${safeText(plan.environment.bindDn)}`}
        </dd>
        <dt className="text-muted-foreground">Changes</dt>
        <dd>
          {counts.length > 0 ? counts.join(", ") : "none"}
          {plan.unchanged > 0 ? `; ${plan.unchanged} already as described` : ""}
        </dd>
      </dl>
      {plan.changes.length > 0 ? (
        <ol className="space-y-1">
          {plan.changes.map((c, i) => (
            <li key={i} className="rounded border px-2 py-1 font-dn text-xs">
              {i + 1}. {c.type} {safeText(c.dn)}
              {c.expect ? (
                <span className="block text-muted-foreground">
                  expects{" "}
                  {c.expect.attributes
                    .map((a) => safeText(a.name))
                    .join(", ") || "the entry"}
                  {c.expect.exhaustive ? " and nothing else" : ""} as it was
                  planned
                </span>
              ) : null}
            </li>
          ))}
        </ol>
      ) : null}
    </div>
  );
}

/** The changeset's heading while it is exactly a loaded saved plan. */
export function SavedPlanHeading({ origin }: { origin: SavedPlanOrigin }) {
  return (
    <div className="space-y-0.5 rounded-md border bg-muted/40 px-3 py-2 text-sm">
      <div>
        <span className="font-medium">Saved plan</span>
        <span className="text-muted-foreground">
          {" "}
          — made {safeText(origin.createdAt)}
        </span>
      </div>
      <div>
        <span className="font-medium">
          Planned against{" "}
          <span className="break-all font-dn">{safeText(origin.host)}</span>
          {origin.bindDn ? (
            <>
              {" "}
              as{" "}
              <span className="break-all font-dn">
                {safeText(origin.bindDn)}
              </span>
            </>
          ) : (
            " anonymously"
          )}
        </span>
        <span className="text-muted-foreground">
          {" "}
          — applied whole or not at all. Removing a change makes the rest an
          ordinary changeset.
        </span>
      </div>
    </div>
  );
}
