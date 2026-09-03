import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  Check,
  ListChecks,
  Loader2,
  Minus,
  ShieldAlert,
  Trash2,
  X,
} from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ChangesetResult } from "@/lib/api";
import { changeset, useChangeset } from "@/lib/changeset";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/misc";
import { LdifBlock } from "@/components/ldif-block";
import { ErrorNote } from "@/components/change-dialog";

/**
 * The changeset view: several staged changes, read as one document and applied
 * in order.
 *
 * This is the difference between a tool that edits a directory and one whose
 * output is code. A day's work is rarely one modification; it is a new group,
 * six members added to it, two people moved and one disabled. Reviewing that as
 * one LDIF file and handing it over as one playbook is the thing being claimed
 * on the front page.
 */
export function ChangesetView({ onBrowse }: { onBrowse: (dn: string) => void }) {
  const staged = useChangeset();
  const queryClient = useQueryClient();
  const [result, setResult] = useState<ChangesetResult | null>(null);

  const body = { changes: staged.map((s) => s.change) };

  const preview = useQuery({
    // Keyed on the changes themselves, so reordering re-renders the document
    // rather than showing the previous order's warnings.
    queryKey: ["changeset-preview", body],
    enabled: staged.length > 0,
    retry: false,
    queryFn: async () => unwrap(await api.POST("/changeset/preview", { body })),
  });

  const apply = useMutation({
    mutationFn: async () => unwrap(await api.POST("/changeset/apply", { body })),
    onSuccess: (res) => {
      setResult(res);
      void queryClient.invalidateQueries({ queryKey: ["entry"] });
      void queryClient.invalidateQueries({ queryKey: ["tree"] });
      void queryClient.invalidateQueries({ queryKey: ["search"] });
      // Keep what did not apply, in order. The basket ends up holding exactly
      // the work still to do.
      const applied = res.outcomes
        .filter((o) => o.applied)
        .map((o) => staged[o.index]?.id)
        .filter((id): id is string => id !== undefined);
      changeset.removeApplied(applied);
    },
  });

  const previewError = preview.error as ApiFailure | null;
  const applyError = apply.error as ApiFailure | null;
  const data = preview.data;

  if (staged.length === 0) {
    return (
      <div className="mx-auto max-w-2xl px-6 py-16">
        {result ? <ResultPanel result={result} onDismiss={() => setResult(null)} /> : null}
        <div className="rounded-lg border border-dashed p-10 text-center">
          <ListChecks className="mx-auto mb-3 size-8 text-muted-foreground" />
          <h2 className="text-base font-medium">The changeset is empty</h2>
          <p className="mx-auto mt-2 max-w-md text-sm text-muted-foreground">
            Every change dialog has an <strong>Add to changeset</strong> button
            next to Apply. Stage several, read them as one LDIF document, and
            apply them in order — or export the lot as a single Ansible playbook
            and never apply them here at all.
          </p>
          <p className="mx-auto mt-3 max-w-md text-xs text-muted-foreground">
            The changeset lives in this tab only. Reloading the page clears it,
            and it is never written to browser storage, because a staged password
            change carries the password.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-5xl px-6 py-6">
      <div className="mb-4 flex items-baseline justify-between gap-4">
        <div>
          <h2 className="text-lg font-medium">
            {staged.length} staged change{staged.length === 1 ? "" : "s"}
          </h2>
          <p className="text-sm text-muted-foreground">
            Applied top to bottom, one at a time. LDAP has no transaction across
            entries, so a failure stops the run and reports what already landed.
          </p>
        </div>
        <Button variant="ghost" size="sm" onClick={() => changeset.clear()}>
          <Trash2 />
          Discard all
        </Button>
      </div>

      {result ? <ResultPanel result={result} onDismiss={() => setResult(null)} /> : null}

      <ol className="mb-5 space-y-1.5">
        {staged.map((item, i) => (
          <li
            key={item.id}
            className="flex items-center gap-3 rounded-md border bg-card px-3 py-2"
          >
            <span className="w-5 shrink-0 text-right text-xs tabular-nums text-muted-foreground">
              {i + 1}
            </span>
            <button
              className="min-w-0 flex-1 truncate text-left font-dn text-sm hover:underline"
              title={`Browse to ${item.change.dn}`}
              onClick={() => onBrowse(item.change.dn)}
            >
              {item.label}
            </button>
            <div className="flex shrink-0 items-center gap-0.5">
              <Button
                variant="ghost"
                size="icon"
                disabled={i === 0}
                title="Move earlier"
                onClick={() => changeset.move(item.id, -1)}
              >
                <ArrowUp />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                disabled={i === staged.length - 1}
                title="Move later"
                onClick={() => changeset.move(item.id, 1)}
              >
                <ArrowDown />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                title="Remove from the changeset"
                onClick={() => changeset.remove(item.id)}
              >
                <X />
              </Button>
            </div>
          </li>
        ))}
      </ol>

      {previewError ? (
        <ErrorNote title="This changeset cannot be rendered" error={previewError} />
      ) : null}

      {data?.warnings?.length ? (
        <div className="mb-4 rounded-md border border-warning/40 bg-warning/10 p-3">
          <div className="mb-1.5 flex items-center gap-1.5 text-sm font-medium text-warning-tint-foreground">
            <AlertTriangle className="size-4" />
            About the order of these changes
          </div>
          <ul className="ml-5 list-disc space-y-1 text-sm text-warning-tint-foreground/90">
            {data.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
          <p className="mt-2 text-xs text-muted-foreground">
            Alder will not reorder these for you. Moving an entry under something
            created later is a legitimate thing to want, and guessing which of
            the two you meant would change what you reviewed.
          </p>
        </div>
      ) : null}

      {preview.isPending ? (
        <div className="flex items-center gap-2 py-8 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          Rendering the document…
        </div>
      ) : null}

      {data ? (
        <Tabs defaultValue="ldif">
          <TabsList className="mb-3">
            <TabsTrigger value="ldif">LDIF</TabsTrigger>
            <TabsTrigger value="ansible">Ansible playbook</TabsTrigger>
          </TabsList>

          <TabsContent value="ldif">
            <LdifBlock text={data.ldif} filename="changeset.ldif" />
            <p className="mt-2 text-xs text-muted-foreground">
              One document, in the order above. This is exactly what applying
              will send, record by record.
            </p>
          </TabsContent>

          <TabsContent value="ansible">
            <LdifBlock
              text={data.ansiblePlaybook ?? ""}
              language="yaml"
              filename="changeset.yaml"
            />
            <p className="mt-2 text-xs text-muted-foreground">
              A single playbook, runnable as it stands once the connection
              variables are supplied. Every task is idempotent, so re-running it
              against a directory already in this state changes nothing.
            </p>
          </TabsContent>
        </Tabs>
      ) : null}

      {applyError ? (
        <div className="mt-4">
          <ErrorNote title="The changeset could not be applied" error={applyError} />
        </div>
      ) : null}

      <div className="mt-5 flex items-center justify-end gap-2 border-t pt-4">
        <Button
          disabled={!data || apply.isPending}
          onClick={() => {
            setResult(null);
            apply.mutate();
          }}
        >
          {apply.isPending ? <Loader2 className="animate-spin" /> : null}
          Apply {staged.length} change{staged.length === 1 ? "" : "s"} in order
        </Button>
      </div>
    </div>
  );
}

/**
 * What the run actually did, per change.
 *
 * It stays on screen after the basket empties, because "it worked" is a claim
 * the user should be able to check against the list they submitted rather than
 * infer from the absence of an error.
 */
function ResultPanel({
  result,
  onDismiss,
}: {
  result: ChangesetResult;
  onDismiss: () => void;
}) {
  const failed = result.failedIndex !== undefined;
  return (
    <div
      className={`mb-5 rounded-md border p-3 ${
        failed ? "border-warning/40 bg-warning/10" : "border-success/40 bg-success/10"
      }`}
    >
      <div className="flex items-start justify-between gap-3">
        <div
          className={`flex items-center gap-1.5 text-sm font-medium ${
            failed ? "text-warning-tint-foreground" : "text-success"
          }`}
        >
          {failed ? <ShieldAlert className="size-4" /> : <Check className="size-4" />}
          {!failed
            ? `All ${result.appliedCount} changes applied.`
            : result.appliedCount === 0
              ? `Stopped at change ${(result.failedIndex ?? 0) + 1}. Nothing was applied.`
              : `Stopped at change ${(result.failedIndex ?? 0) + 1}, after applying ${result.appliedCount}.`}
        </div>
        <Button variant="ghost" size="icon" onClick={onDismiss} title="Dismiss">
          <X />
        </Button>
      </div>

      <ul className="mt-2.5 space-y-1">
        {result.outcomes.map((o) => (
          <li key={o.index} className="flex items-start gap-2 text-sm">
            <span className="mt-0.5 shrink-0">
              {o.applied ? (
                <Check className="size-3.5 text-success" />
              ) : o.error ? (
                <X className="size-3.5 text-destructive" />
              ) : (
                <Minus className="size-3.5 text-muted-foreground" />
              )}
            </span>
            <span className="min-w-0">
              <span className="font-dn">{o.summary}</span>
              {o.error ? (
                <span className="ml-2 text-destructive">{o.error.message}</span>
              ) : !o.applied ? (
                <span className="ml-2 text-muted-foreground">not attempted</span>
              ) : null}
            </span>
          </li>
        ))}
      </ul>

      {failed ? (
        <p className="mt-2.5 text-xs text-muted-foreground">
          The changes that applied are done and are not rolled back — LDAP has no
          transaction spanning entries. The ones that did not are still staged,
          in order, so fixing the failure and applying again resumes rather than
          repeats.
        </p>
      ) : null}
    </div>
  );
}
