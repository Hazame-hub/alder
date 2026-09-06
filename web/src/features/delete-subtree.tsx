import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, Trash2 } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { ApiFailure, CountResult, SearchResponse, SessionInfo } from "@/lib/api";
import { changeset } from "@/lib/changeset";
import { orderDeepestFirst } from "@/lib/subtree";
import { checkSubtree } from "@/lib/subtree-refusal";
import { stageChanges, type StageOutcome } from "@/lib/stage-deletes";
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
import { ErrorNote } from "@/components/change-dialog";

/**
 * Deleting a container, by staging everything under it.
 *
 * Alder already tells the operator why a container cannot be deleted — "the
 * entry has children, and a directory removes entries one at a time from the
 * bottom" — and then offered no way to do that. This is the missing half.
 *
 * It counts before it walks, which is the whole shape of the feature. A count
 * is a bounded search that returns no attributes, so it is cheap and it is
 * honest: if the count came back truncated, or the server cannot page, then a
 * complete subtree cannot be obtained and the answer is a refusal rather than a
 * partial list. A partial subtree delete is the worst outcome available here —
 * it removes the leaves it reached and leaves every container standing.
 *
 * Nothing is applied. The set goes into the changeset, deepest-first, where it
 * is read as one document before a single Apply.
 */

type Stage =
  | { kind: "counting" }
  | { kind: "counted"; count: CountResult }
  | { kind: "refused"; why: string; detail?: string }
  | { kind: "staging" }
  | { kind: "staged"; outcome: StageOutcome };

export function DeleteSubtreeButton({
  dn,
  info,
  onReviewChangeset,
}: {
  dn: string;
  info: SessionInfo;
  onReviewChangeset: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [stage, setStage] = useState<Stage>({ kind: "counting" });

  const count = useMutation<CountResult, ApiFailure>({
    mutationFn: async () => unwrap(await api.GET("/count", { params: { query: { dn } } })),
    onSuccess: (result) => {
      const verdict = checkSubtree({
        count: result.count,
        truncated: result.truncated,
        paging: info.capabilities?.paging === true,
        capacity: changeset.capacity(),
      });
      setStage(
        verdict.ok
          ? { kind: "counted", count: result }
          : { kind: "refused", why: verdict.why, detail: verdict.detail },
      );
    },
    onError: (err) =>
      setStage({ kind: "refused", why: "The entries under this one could not be counted.", detail: err.message }),
  });

  const walk = useMutation<SearchResponse, ApiFailure>({
    mutationFn: async () =>
      unwrap(
        await api.POST("/search", {
          body: {
            baseDn: dn,
            scope: "sub",
            filter: "(objectClass=*)",
            // No attributes at all: this needs DNs and nothing else, and asking
            // for values would pull a page of entries across for no reason.
            attributes: ["1.1"],
            limit: changeset.capacity(),
            pageSize: 200,
          },
        }),
      ),
    onSuccess: (res) => {
      if (res.truncated) {
        setStage({
          kind: "refused",
          why: "The subtree came back truncated.",
          detail: "Alder will not stage part of a subtree. Nothing was staged.",
        });
        return;
      }
      const ordered = orderDeepestFirst(res.entries.map((e) => e.dn));
      setStage({
        kind: "staged",
        outcome: stageChanges(
          ordered.map((d) => ({
            change: { dn: d, type: "delete" as const },
            label: `Delete ${d}`,
          })),
          { noun: "deletion", nothing: "There is nothing under this entry." },
        ),
      });
    },
    onError: (err) =>
      setStage({ kind: "refused", why: "The subtree could not be read.", detail: err.message }),
  });

  const start = () => {
    setStage({ kind: "counting" });
    setOpen(true);
    count.mutate();
  };

  return (
    <>
      <Button variant="outline" size="sm" onClick={start}>
        <Trash2 />
        Delete with contents
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete this entry and everything under it</DialogTitle>
            <DialogDescription>
              <span className="font-dn">{dn}</span>
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3 px-5 py-4 text-sm">
            {stage.kind === "counting" ? (
              <p className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Counting what is under it…
              </p>
            ) : null}

            {stage.kind === "refused" ? (
              <div className="space-y-2 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-warning-tint-foreground">
                <p className="font-medium">{stage.why}</p>
                {stage.detail ? <p className="text-xs">{stage.detail}</p> : null}
              </div>
            ) : null}

            {stage.kind === "counted" ? (
              <>
                <p>
                  <Badge variant="outline" className="tabular-nums">
                    {stage.count.count.toLocaleString()}
                  </Badge>{" "}
                  {stage.count.count === 1 ? "entry" : "entries"}, including this
                  one, would be staged for deletion — deepest first, because a
                  directory removes entries one at a time from the bottom.
                </p>
                <p className="text-xs text-muted-foreground">
                  Nothing is sent now. They go into the changeset, where you read
                  them as one document before a single Apply. That run stops at
                  the first failure and leaves the rest staged.
                </p>
              </>
            ) : null}

            {stage.kind === "staging" ? (
              <p className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Reading the subtree…
              </p>
            ) : null}

            {stage.kind === "staged" ? (
              <div
                className={
                  stage.outcome.ok
                    ? "rounded-md border border-border bg-accent/40 px-3 py-2"
                    : "rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-warning-tint-foreground"
                }
              >
                {stage.outcome.message}
              </div>
            ) : null}

            {count.isError || walk.isError ? (
              <ErrorNote
                title="The directory refused"
                error={(count.error ?? walk.error) as ApiFailure}
              />
            ) : null}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              {stage.kind === "staged" && stage.outcome.ok ? "Close" : "Cancel"}
            </Button>
            {stage.kind === "counted" ? (
              <Button
                variant="destructive"
                onClick={() => {
                  setStage({ kind: "staging" });
                  walk.mutate();
                }}
              >
                <Trash2 />
                Stage {stage.count.count.toLocaleString()} deletions
              </Button>
            ) : null}
            {stage.kind === "staged" && stage.outcome.ok ? (
              <Button
                onClick={() => {
                  setOpen(false);
                  onReviewChangeset();
                }}
              >
                Review the changeset
              </Button>
            ) : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
