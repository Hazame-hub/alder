import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangle, FileUp, Loader2, SearchCheck, X } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { RecoveryInspection } from "@/lib/api";
import { changeset } from "@/lib/changeset";
import { DRIFT, RECOVERABILITY, assessmentLine, parseBundleFile, visible } from "@/lib/recovery";
import { Button } from "@/components/ui/button";
import { ErrorNote } from "@/components/change-dialog";

/**
 * Loading a recovery bundle: read it, show what it says, and stage its changes
 * for review.
 *
 * There is no "recover now". The bundle is sent to the server, which validates
 * it and returns ordinary changes; this shows them next to what the bundle
 * claims and what the directory looks like now, and "Review plan" stages them
 * in the changeset, where they are checked against the directory and applied
 * like anything else. Everything shown from the file is untrusted text, and is
 * displayed with its control and bidirectional characters made visible.
 */
export function RecoveryLoader({
  canStage,
  onStaged,
}: {
  canStage: boolean;
  onStaged: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [localError, setLocalError] = useState<string | null>(null);
  const [inspection, setInspection] = useState<RecoveryInspection | null>(null);
  const [originConfirmed, setOriginConfirmed] = useState(false);

  const inspect = useMutation({
    mutationFn: async (text: string) => {
      const parsed = parseBundleFile(text);
      if (!parsed.ok) {
        setLocalError(parsed.message);
        return null;
      }
      return unwrap(await api.POST("/recovery/inspect", { body: parsed.bundle }));
    },
    onSuccess: (result) => {
      setInspection(result);
      setOriginConfirmed(false);
    },
  });

  const reset = () => {
    setInspection(null);
    setLocalError(null);
    setOriginConfirmed(false);
    inspect.reset();
  };

  const stage = () => {
    if (!inspection) return;
    const outcome = changeset.addMany(
      inspection.changes.map((c) => ({ change: c, label: `Recovery: ${c.type} ${visible(c.dn)}` })),
    );
    if (outcome.staged > 0) {
      reset();
      onStaged();
    } else {
      setLocalError(`The changeset has room for ${outcome.capacity} more changes, and this recovery has ${outcome.refused}.`);
    }
  };

  const changes = inspection?.changes ?? [];
  const canReview = canStage && changes.length > 0 && (inspection?.originMatches || originConfirmed);

  return (
    <div className="mb-5 rounded-md border p-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <div className="text-sm font-medium">Recovery bundle</div>
          <p className="text-xs text-muted-foreground">
            Load a bundle saved when a change was applied. Its compensating changes
            are staged here for a plan and a review; nothing is applied on loading.
          </p>
        </div>
        {inspection ? (
          <Button variant="ghost" size="icon" title="Close" onClick={reset}>
            <X />
          </Button>
        ) : (
          <Button
            variant="outline"
            size="sm"
            disabled={inspect.isPending}
            onClick={() => input.current?.click()}
          >
            {inspect.isPending ? <Loader2 className="animate-spin" /> : <FileUp />}
            Load recovery bundle
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
            void file.text().then((text) => inspect.mutate(text));
          }}
        />
      </div>

      {localError ? <p className="mt-2 text-sm text-destructive">{localError}</p> : null}
      {inspect.error ? (
        <div className="mt-3">
          <ErrorNote title="This recovery bundle cannot be used" error={inspect.error as ApiFailure} />
        </div>
      ) : null}

      {inspection ? (
        <div className="mt-3 space-y-3 text-sm">
          <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs">
            <dt className="text-muted-foreground">Created</dt>
            <dd>{visible(inspection.createdAt)}</dd>
            <dt className="text-muted-foreground">Directory</dt>
            <dd className="font-dn">
              {visible(inspection.origin.vendor ?? "unknown vendor")} ·{" "}
              {inspection.origin.namingContexts.map(visible).join(", ") || "no naming contexts"}
            </dd>
            <dt className="text-muted-foreground">Integrity</dt>
            <dd>
              {inspection.integrity === "verified"
                ? "checksum verified (corruption check, not a signature)"
                : "no checksum: the content could not be checked for corruption"}
            </dd>
            <dt className="text-muted-foreground">Recovery</dt>
            <dd className={inspection.recoverability === "exact" ? "" : "text-warning-tint-foreground"}>
              {RECOVERABILITY[inspection.recoverability]}
            </dd>
          </dl>

          {!inspection.originMatches ? (
            <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-warning-tint-foreground">
              <div className="flex items-center gap-1.5 font-medium">
                <AlertTriangle className="size-4" />
                This bundle was made against a directory that announces itself differently
              </div>
              <p className="mt-1 text-xs">
                Different: {(inspection.originDifferences ?? []).join(", ")}. A vendor and
                naming contexts are what a directory announces, not proof of which one
                it is.
              </p>
              <label className="mt-2 flex items-center gap-2 text-xs">
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={originConfirmed}
                  onChange={(e) => setOriginConfirmed(e.target.checked)}
                />
                This is the directory I mean to recover.
              </label>
            </div>
          ) : null}

          <div>
            <div className="mb-1 text-xs font-medium text-muted-foreground">What was applied</div>
            <ol className="space-y-1">
              {inspection.steps.map((step) => (
                <li key={step.index} className="rounded border px-2 py-1">
                  <div className="font-dn text-xs">
                    {step.index + 1}. {visible(step.original.type)} {visible(step.original.dn)}
                    {step.original.targetDn ? ` → ${visible(step.original.targetDn)}` : ""}
                  </div>
                  <div
                    className={`text-xs ${step.recoverability === "exact" ? "text-muted-foreground" : "text-warning-tint-foreground"}`}
                  >
                    {assessmentLine({ recoverability: step.recoverability, reasons: step.reasons })}
                  </div>
                </li>
              ))}
            </ol>
          </div>

          <div>
            <div className="mb-1 text-xs font-medium text-muted-foreground">
              Proposed compensating changes, in the order they would run
            </div>
            {changes.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                Nothing in this bundle can be compensated.
              </p>
            ) : (
              <ol className="space-y-1">
                {changes.map((c, i) => {
                  const drift = inspection.drift[i];
                  return (
                    <li key={i} className="flex items-start justify-between gap-3 rounded border px-2 py-1 text-xs">
                      <span className="font-dn">
                        {c.type} {visible(c.dn)}
                        {c.expect ? (
                          <span className="block text-muted-foreground">
                            expects {c.expect.attributes.map((a) => visible(a.name)).join(", ") || "the entry"}
                            {c.expect.exhaustive ? " and nothing else" : ""} as the original change left it
                          </span>
                        ) : null}
                      </span>
                      {drift ? (
                        <span
                          className={`shrink-0 ${drift.state === "ready" ? "text-muted-foreground" : "text-warning-tint-foreground"}`}
                        >
                          {DRIFT[drift.state]}
                          {drift.problem ? ` (${drift.problem.code})` : ""}
                        </span>
                      ) : null}
                    </li>
                  );
                })}
              </ol>
            )}
          </div>

          <div className="flex items-center gap-3">
            <Button size="sm" disabled={!canReview} onClick={stage}>
              <SearchCheck />
              Review plan
            </Button>
            <span className="text-xs text-muted-foreground">
              {canStage
                ? "Stages these changes and checks them against the directory. Applying is a separate step."
                : "Apply or discard what is staged first: a recovery is reviewed on its own."}
            </span>
          </div>
        </div>
      ) : null}
    </div>
  );
}
