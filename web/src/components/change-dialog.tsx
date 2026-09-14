import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CircleCheck, ListChecks, Loader2, RefreshCw, ShieldAlert } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ApplyResult, ChangeRequest } from "@/lib/api";
import { reviewSingleChange } from "@/lib/single-plan";
import { assessmentLine, bundleText, recoveryFilename } from "@/lib/recovery";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/misc";
import { DownloadButton, LdifBlock } from "@/components/ldif-block";
import { PlanImpactList, PlanRow } from "@/components/plan-summary";
import { changeset } from "@/lib/changeset";

/**
 * ChangeDialog is the confirmation step every single change goes through.
 *
 * The components that build a ChangeRequest — the entry editor, rename, delete,
 * a password, a membership, a schema definition — hand it here, and this dialog
 * plans it against the directory before anything else: what the change would
 * do, the problems and impact that planning finds, and the exact LDIF the
 * server rendered from that plan. Applying sends the reviewed change with the
 * plan's token, so the server refuses it if the directory has moved since, or
 * if it is somehow not the change that was planned.
 *
 * A refused plan is never replanned and applied in one step. The operator asks
 * for a new plan, sees it, and only then can apply it.
 *
 * With "Prepare a recovery bundle" ticked, the server returns a bundle once the
 * change has applied, and the dialog stays open to offer it for download. Alder
 * keeps no copy: a bundle not downloaded here is gone.
 */
export function ChangeDialog({
  change,
  open,
  onOpenChange,
  onApplied,
  onStaged,
  title,
  destructive,
}: {
  change: ChangeRequest | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onApplied?: (result: ApplyResult) => void;
  onStaged?: () => void;
  title?: string;
  destructive?: boolean;
}) {
  const queryClient = useQueryClient();
  const [prepareRecovery, setPrepareRecovery] = useState(false);
  // An applied change whose bundle is still on offer. onApplied is held back
  // until the dialog closes, because what it does -- navigating away from a
  // renamed or deleted entry -- can unmount the dialog and the bundle with it.
  const [applied, setApplied] = useState<ApplyResult | null>(null);

  const planned = useQuery({
    queryKey: ["change-plan", change],
    enabled: open && change !== null,
    retry: false,
    // A plan is replaced only when the operator asks for it. Refetching on
    // focus or reconnect would swap the plan being reviewed for another one
    // underneath the Apply button; and nothing is kept once the dialog closes,
    // so reopening it always plans afresh.
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    queryFn: async () =>
      unwrap(
        await api.POST("/plan", {
          body: { changes: [change as ChangeRequest], reconcile: false },
        }),
      ),
  });

  const review = planned.data && change ? reviewSingleChange(planned.data, change) : null;
  const item = review?.item;
  const preview = item?.preview;
  const recoverable = item?.recovery !== undefined && item.recovery.recoverability !== "unavailable";

  const apply = useMutation({
    mutationFn: async () => {
      if (review?.state !== "apply") throw new Error("There is no planned change to apply.");
      const wantRecovery = prepareRecovery && recoverable;
      return unwrap(
        await api.POST("/changes/apply", {
          params: { query: wantRecovery ? { recovery: true } : {} },
          body: review.body,
        }),
      );
    },
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["entry"] });
      void queryClient.invalidateQueries({ queryKey: ["tree"] });
      void queryClient.invalidateQueries({ queryKey: ["search"] });
      if (result.recovery) {
        setApplied(result);
        return;
      }
      onOpenChange(false);
      onApplied?.(result);
    },
  });

  const close = () => {
    const done = applied;
    setApplied(null);
    setPrepareRecovery(false);
    apply.reset();
    onOpenChange(false);
    if (done) onApplied?.(done);
  };

  const planError = planned.error as ApiFailure | null;
  const applyError = apply.error instanceof ApiFailure ? apply.error : null;
  // The server refused the plan: the directory moved, or the request was not
  // the planned change. Apply stays disabled until a new plan has been shown.
  const stale = applyError?.isStalePlan ? applyError : null;
  const replanning = planned.isFetching;
  const canApply = review?.state === "apply" && !apply.isPending && !replanning && !stale;
  const bundle = applied?.recovery;

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) close();
        else onOpenChange(next);
      }}
    >
      <DialogContent wide>
        <DialogHeader>
          <DialogTitle>{bundle ? "Change applied" : (title ?? "Review this change")}</DialogTitle>
          <DialogDescription>
            {preview ? (
              <span className="font-dn">{preview.summary}</span>
            ) : item ? (
              <span className="font-dn">{item.dn}</span>
            ) : (
              "Planning against the directory…"
            )}
          </DialogDescription>
        </DialogHeader>

        {bundle ? (
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
            <div className="flex items-start gap-2 rounded-md border border-success/40 bg-success/10 p-3 text-sm">
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-success" />
              <div className="font-medium">The change was applied.</div>
            </div>
            <div className="space-y-2 rounded-md border p-3 text-sm">
              <div className="font-medium">Recovery bundle</div>
              <p className={bundle.recoverability === "exact" ? "text-muted-foreground" : "text-warning-tint-foreground"}>
                {assessmentLine({
                  recoverability: bundle.recoverability,
                  reasons: bundle.steps.flatMap((s) => s.reasons ?? []),
                })}
              </p>
              <p className="text-xs text-muted-foreground">
                It describes the compensating changes Alder could derive from the
                entry as it was immediately before this change. It is not a
                backup and is not kept anywhere: download it now. Loading it later
                in the Changeset view turns it into changes you plan and review
                before anything is applied.
              </p>
              <DownloadButton
                text={bundleText(bundle)}
                filename={recoveryFilename(bundle)}
                label="Download recovery bundle"
                mime="application/json"
              />
            </div>
          </div>
        ) : (
          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
            {replanning ? (
              <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Planning against the directory…
              </div>
            ) : null}

            {planError && !replanning ? (
              <ErrorNote title="This change cannot be planned" error={planError} />
            ) : null}

            {planned.data && item && !replanning ? (
              <>
                <div className="space-y-2">
                  <ol className="rounded-md border">
                    <PlanRow item={item} />
                  </ol>
                  <PlanImpactList plan={planned.data} />
                </div>

                {review?.state === "apply" && recoverable ? (
                  <label className="flex items-start gap-2 rounded-md border p-3 text-sm">
                    <input
                      type="checkbox"
                      className="mt-0.5 size-4 accent-primary"
                      checked={prepareRecovery}
                      onChange={(e) => setPrepareRecovery(e.target.checked)}
                    />
                    <span>
                      <span className="font-medium">Prepare a recovery bundle</span>
                      <span className="block text-xs text-muted-foreground">
                        Offered for download once the change has applied. Recovering
                        from it is a new change, planned and reviewed like this one.
                      </span>
                    </span>
                  </label>
                ) : null}

                {review?.state === "nothing" ? (
                  <div className="flex items-start gap-2 rounded-md border border-border bg-muted/40 p-3 text-sm">
                    <CircleCheck className="mt-0.5 size-4 shrink-0 text-success" />
                    <div>
                      <div className="font-medium">No changes required.</div>
                      <p className="text-muted-foreground">
                        The directory already holds what this change describes, so
                        applying it would write nothing.
                      </p>
                    </div>
                  </div>
                ) : null}

                {review?.state === "blocked" ? (
                  <div className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
                    <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                    <div>
                      <div className="font-medium">This change would not apply as things stand.</div>
                      {item.reason ? <p className="text-warning-tint-foreground/90">{item.reason}</p> : null}
                    </div>
                  </div>
                ) : null}

                {preview?.warnings?.length ? (
                  <div className="rounded-md border border-warning/40 bg-warning/10 p-3">
                    <div className="mb-1.5 flex items-center gap-1.5 text-sm font-medium text-warning-tint-foreground">
                      <AlertTriangle className="size-4" />
                      Read this before applying
                    </div>
                    <ul className="ml-5 list-disc space-y-1 text-sm text-warning-tint-foreground/90">
                      {preview.warnings.map((w) => (
                        <li key={w}>{w}</li>
                      ))}
                    </ul>
                    <p className="mt-2 text-xs text-muted-foreground">
                      Nothing here blocks the change. The directory decides, and you
                      can still apply this.
                    </p>
                  </div>
                ) : null}

                {preview ? (
                  <Tabs defaultValue="ldif">
                    <div className="mb-3 flex items-center justify-between gap-3">
                      <TabsList>
                        <TabsTrigger value="ldif">LDIF</TabsTrigger>
                        <TabsTrigger value="ansible">Ansible</TabsTrigger>
                      </TabsList>
                      {preview.affectedAttributes?.length ? (
                        <div className="flex flex-wrap items-center gap-1">
                          <span className="text-xs text-muted-foreground">touches</span>
                          {preview.affectedAttributes.map((a) => (
                            <Badge key={a} variant="outline" className="font-mono">
                              {a}
                            </Badge>
                          ))}
                        </div>
                      ) : null}
                    </div>

                    <TabsContent value="ldif">
                      <LdifBlock text={preview.ldif} filename="change.ldif" />
                      <p className="mt-2 text-xs text-muted-foreground">
                        The exact change record this plan will send, rendered by the
                        server from the plan itself. The download is folded at 76
                        columns as RFC 2849 asks; what you see here is not.
                      </p>
                    </TabsContent>

                    <TabsContent value="ansible">
                      <LdifBlock text={preview.ansible} language="yaml" filename="change.task.yaml" />
                      <p className="mt-2 text-xs text-muted-foreground">
                        Rendered from the same change record as the LDIF. The
                        connection settings are variables on purpose: a bind password
                        does not belong in a generated file.
                      </p>
                    </TabsContent>
                  </Tabs>
                ) : null}
              </>
            ) : null}

            {stale ? (
              <div className="rounded-md border border-warning/40 bg-warning/10 p-3">
                <div className="mb-1 flex items-center gap-1.5 text-sm font-medium text-warning-tint-foreground">
                  <AlertTriangle className="size-4" />
                  {stale.code === "plan_mismatch"
                    ? "This is not the change that was planned"
                    : "The directory has changed since this plan was made"}
                </div>
                <p className="text-sm text-warning-tint-foreground/90">{stale.message}</p>
                {stale.affected?.length ? (
                  <ul className="mt-2 ml-5 list-disc space-y-0.5 text-xs text-warning-tint-foreground/90">
                    {stale.affected.map((a) => (
                      <li key={a.index} className="font-dn">
                        {a.dn}
                      </li>
                    ))}
                  </ul>
                ) : null}
                <div className="mt-3 flex items-center gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={replanning}
                    onClick={() => {
                      apply.reset();
                      void planned.refetch();
                    }}
                  >
                    {replanning ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                    Recompute plan
                  </Button>
                  <span className="text-xs text-muted-foreground">
                    Applying stays disabled until you have seen the new plan.
                  </span>
                </div>
              </div>
            ) : null}

            {applyError && !stale ? (
              <ErrorNote title="The directory refused this change" error={applyError} />
            ) : null}
          </div>
        )}

        <DialogFooter>
          {bundle ? (
            <Button onClick={close}>Done</Button>
          ) : (
            <>
              <Button variant="outline" onClick={close}>
                Cancel
              </Button>
              {/*
                Staging queues the reviewed ChangeRequest. The changeset plans it
                again, with everything staged beside it, before it can be applied;
                a change that would do nothing is not worth queueing.
              */}
              <Button
                variant="outline"
                disabled={!review || review.state === "nothing" || apply.isPending || replanning}
                onClick={() => {
                  changeset.add(change as ChangeRequest, preview?.summary ?? item?.dn ?? "change");
                  close();
                  onStaged?.();
                }}
              >
                <ListChecks />
                Add to changeset
              </Button>
              <Button
                variant={destructive ? "destructive" : "default"}
                disabled={!canApply}
                onClick={() => apply.mutate()}
              >
                {apply.isPending ? <Loader2 className="animate-spin" /> : null}
                {review?.state === "nothing"
                  ? "Nothing to apply"
                  : destructive
                    ? "Apply and delete"
                    : "Apply to the directory"}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ErrorNote({ title, error }: { title: string; error: ApiFailure }) {
  return (
    <div className="rounded-md border border-destructive/40 bg-destructive/8 p-3">
      <div className="flex items-center gap-1.5 text-sm font-medium text-destructive">
        <ShieldAlert className="size-4" />
        {title}
      </div>
      <p className="mt-1 text-sm">{error.message}</p>
      {/*
        The likely cause comes before the raw diagnostic and the code. A result
        code is what the server said; the hint is what to do about it, and that
        is what the reader wants first.
      */}
      {error.hint ? <p className="mt-1.5 text-sm">{error.hint}</p> : null}
      {error.detail ? (
        <p className="mt-1 font-mono text-xs text-muted-foreground">{error.detail}</p>
      ) : null}
      {error.ldapCode !== undefined ? (
        <p className="mt-1 text-xs text-muted-foreground">
          LDAP result code {error.ldapCode}
        </p>
      ) : null}
    </div>
  );
}
