import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import {
  CheckCircle2,
  FileSearch,
  FileUp,
  ListChecks,
  Loader2,
  Upload,
} from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ChangeRequest, ImportResult, Plan } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/input";
import { Checkbox } from "@/components/ui";
import { ChangeDialog, ErrorNote } from "@/components/change-dialog";
import { LdifBlock } from "@/components/ldif-block";
import { PlanSummary } from "@/components/plan-summary";
import { stageChanges, type StageOutcome } from "@/lib/stage-deletes";
import { safeText } from "@/lib/display";

/**
 * ImportPanel parses an LDIF document and offers two ways to act on it: one
 * record at a time, or the whole document staged for a single review.
 *
 * Applying one at a time is what happens either way. A directory has no
 * transaction across entries, so a run that fails halfway leaves a state nobody
 * chose — and the changeset applies its records in order, one at a time,
 * stopping at the first failure. What staging changes is where the reading
 * happens: the combined document, its ordering warnings, and the playbook only
 * exist for a set, and the whole-set validation Alder already wrote could not
 * be reached from here at all.
 *
 * A record is in exactly one of three states, and that is deliberate. Once it
 * is staged its own button is disabled, because a document with two live routes
 * to the directory is a document you can apply twice — harmless for an add,
 * which fails with entryAlreadyExists, and silent for a delete or a replace.
 */
export function ImportPanel({
  onReviewChangeset,
}: {
  onReviewChangeset: () => void;
}) {
  const [text, setText] = useState("");
  const [applied, setApplied] = useState<Set<number>>(new Set());
  const [staged, setStaged] = useState<Set<number>>(new Set());
  const [staging, setStaging] = useState<StageOutcome | null>(null);
  const [pending, setPending] = useState<{
    index: number;
    change: ChangeRequest;
  } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const [reconcile, setReconcile] = useState(false);

  const parse = useMutation<ImportResult, ApiFailure>({
    mutationFn: async () =>
      unwrap(
        await api.POST("/import/ldif", { body: { ldif: text, reconcile } }),
      ),
    onSuccess: () => {
      setApplied(new Set());
      setStaged(new Set());
      setStaging(null);
    },
  });

  // The same document planned against the directory. The checkbox decides how
  // it is read, and the server refuses a desired-state document that also says
  // what to do, rather than choosing one reading for it. Planning here is for
  // reading: whatever is staged is planned again by the changeset before it
  // can be applied, so nothing applied is ever a plan nobody saw.
  const plan = useMutation<Plan, ApiFailure>({
    mutationFn: async () =>
      unwrap(
        await api.POST("/plan", {
          body: {
            ldif: text,
            mode: reconcile ? "desired" : "changes",
            reconcile: false,
          },
        }),
      ),
  });

  // What the button would act on: everything parsed that is neither already
  // applied from this panel nor already in the basket.
  const remaining = (parse.data?.requests ?? [])
    .map((request, index) => ({ request, index }))
    .filter(
      ({ request, index }) =>
        request && !applied.has(index) && !staged.has(index),
    );

  const loadFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => {
      setText(String(reader.result ?? ""));
      parse.reset();
      plan.reset();
    };
    reader.readAsText(file);
  };

  return (
    <div className="mx-auto max-w-4xl space-y-4 p-6">
      <header>
        <h2 className="text-lg font-semibold">Import LDIF</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Paste or drop an LDIF document. It is parsed and shown back to you as
          individual changes; nothing is applied until you confirm each one.
        </p>
      </header>

      <div
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault();
          const file = e.dataTransfer.files[0];
          if (file) loadFile(file);
        }}
        className="rounded-lg border border-dashed border-border p-1"
      >
        <Textarea
          value={text}
          rows={12}
          spellCheck={false}
          className="border-0 font-mono text-[12.5px] shadow-none focus-visible:ring-0"
          placeholder={
            "dn: cn=example,ou=people,dc=example,dc=test\n" +
            "changetype: add\n" +
            "objectClass: top\n" +
            "objectClass: person\n" +
            "cn: example\n" +
            "sn: Example\n"
          }
          onChange={(e) => {
            setText(e.target.value);
            parse.reset();
            plan.reset();
          }}
        />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          onClick={() => parse.mutate()}
          disabled={!text.trim() || parse.isPending}
        >
          {parse.isPending ? <Loader2 className="animate-spin" /> : <FileUp />}
          Parse
        </Button>
        <Button
          variant="outline"
          onClick={() => plan.mutate()}
          disabled={!text.trim() || plan.isPending}
        >
          {plan.isPending ? (
            <Loader2 className="animate-spin" />
          ) : (
            <FileSearch />
          )}
          Plan against the directory
        </Button>
        <Button variant="outline" onClick={() => fileInput.current?.click()}>
          <Upload />
          Choose a file
        </Button>
        <input
          ref={fileInput}
          type="file"
          accept=".ldif,.txt,text/plain"
          className="hidden"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) loadFile(file);
          }}
        />
        <p className="text-xs text-muted-foreground">
          A record with no <code className="font-mono">changetype</code> is
          treated as an add, which is what an exported entry is.
        </p>
      </div>

      <label className="flex items-start gap-2.5 text-sm">
        <Checkbox
          checked={reconcile}
          onCheckedChange={(v) => {
            setReconcile(v === true);
            parse.reset();
            plan.reset();
          }}
          className="mt-0.5"
        />
        <span>
          Read it as desired state
          <span className="block text-xs text-muted-foreground">
            A record with no <code className="font-mono">changetype</code> says
            what that entry should hold, and an entry that is already there gets
            the modification that brings it there. Planned this way, a document
            may not also contain <code className="font-mono">changetype</code>{" "}
            records, which say what to do rather than what should be.
          </span>
          <span className="mt-1 block text-xs text-muted-foreground">
            A record for an entry that is already there becomes a modification
            bringing it to what the record says, instead of an add the directory
            refuses. It replaces the attributes the record names and leaves
            every other attribute alone — a document that does not mention{" "}
            <span className="font-dn">userPassword</span> is not a document
            asking for it to be removed.
          </span>
        </span>
      </label>

      {plan.isError ? (
        <div className="space-y-1.5">
          <ErrorNote
            title="This document could not be planned"
            error={plan.error}
          />
          {plan.error.affected?.length ? (
            <ul className="ml-5 list-disc space-y-0.5 text-xs text-muted-foreground">
              {plan.error.affected.map((a) => (
                <li key={a.index}>
                  Record {a.index + 1}: <span className="font-dn">{safeText(a.dn)}</span>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {plan.data ? (
        <section className="space-y-2">
          <h3 className="font-medium">What this document would do</h3>
          <p className="text-xs text-muted-foreground">
            Read against the directory as it is now. Nothing has been written.
            To apply it, parse and stage the records: the changeset plans them
            again before anything runs.
          </p>
          <PlanSummary plan={plan.data} />
        </section>
      ) : null}

      {parse.isError ? (
        <ErrorNote title="The LDIF could not be used" error={parse.error} />
      ) : null}

      {parse.data ? (
        <section className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="font-medium">
              {parse.data.changes.length} change
              {parse.data.changes.length === 1 ? "" : "s"}
            </h3>
            {applied.size > 0 ? (
              <Badge variant="success">{applied.size} applied</Badge>
            ) : null}
            {staged.size > 0 ? (
              <Badge variant="secondary">{staged.size} staged</Badge>
            ) : null}
            {parse.data.reconciled ? (
              <Badge variant="outline">
                {parse.data.reconciled} updated in place
              </Badge>
            ) : null}

            {remaining.length > 0 ? (
              <Button
                size="sm"
                variant="outline"
                className="ml-auto"
                onClick={() => {
                  const outcome = stageChanges(
                    remaining.map(({ request, index }) => ({
                      change: request,
                      label: parse.data?.changes[index]?.summary ?? request.dn,
                    })),
                    {
                      noun: "change",
                      nothing:
                        "Every record here is already applied or staged.",
                    },
                  );
                  setStaging(outcome);
                  if (outcome.ok) {
                    setStaged((prev) => {
                      const next = new Set(prev);
                      remaining.forEach(({ index }) => next.add(index));
                      return next;
                    });
                  }
                }}
              >
                <ListChecks />
                Stage the remaining {remaining.length}
              </Button>
            ) : null}
          </div>

          {staging ? (
            <div className={cnNotice(staging.ok)}>
              <span>{staging.message}</span>
              <Button size="sm" variant="outline" onClick={onReviewChangeset}>
                {staging.ok ? "Review the changeset" : "Open the changeset"}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setStaging(null)}
              >
                Dismiss
              </Button>
            </div>
          ) : null}

          {parse.data.unchanged?.length ? (
            <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm">
              <span className="font-medium">
                {parse.data.unchanged.length === 1
                  ? "1 entry already matches the document"
                  : `${parse.data.unchanged.length} entries already match the document`}
              </span>
              <span className="block text-xs text-muted-foreground">
                Nothing is offered for these: a change that does nothing still
                has to be read and confirmed, and there is nothing to confirm.
              </span>
              <ul className="mt-1.5 max-h-32 space-y-0.5 overflow-y-auto">
                {parse.data.unchanged.map((dn) => (
                  <li
                    key={dn}
                    className="truncate font-dn text-xs text-muted-foreground"
                  >
                    {safeText(dn)}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}

          {parse.data.skippedAttributes?.length ? (
            <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-warning-tint-foreground">
              Left out of the updates because the directory owns them:{" "}
              <span className="font-dn">
                {safeText(parse.data.skippedAttributes.join(", "))}
              </span>
              . Enforcing one fails the whole record, so they are not applied —
              this document was exported with operational attributes.
            </p>
          ) : null}

          {parse.data.changes.map((change, i) => {
            const request = parse.data?.requests?.[i];
            const done = applied.has(i);
            const inBasket = staged.has(i);
            return (
              <div key={i} className={cnCard(done, inBasket)}>
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2">
                  <div className="flex items-center gap-2">
                    {done ? (
                      <CheckCircle2 className="size-4 text-success" />
                    ) : null}
                    {inBasket ? (
                      <ListChecks className="size-4 text-muted-foreground" />
                    ) : null}
                    <span className="font-dn text-sm">{safeText(change.summary)}</span>
                  </div>
                  <Button
                    size="sm"
                    variant={done || inBasket ? "outline" : "default"}
                    disabled={!request || done || inBasket}
                    onClick={() =>
                      request && setPending({ index: i, change: request })
                    }
                  >
                    {done
                      ? "Applied"
                      : inBasket
                        ? "Staged"
                        : "Review and apply"}
                  </Button>
                </div>
                {change.warnings?.length ? (
                  <ul className="ml-5 list-disc space-y-0.5 px-3 py-2 text-xs text-warning-tint-foreground">
                    {change.warnings.map((w) => (
                      <li key={w}>{w}</li>
                    ))}
                  </ul>
                ) : null}
                <div className="p-3">
                  <LdifBlock text={change.ldif} />
                </div>
              </div>
            );
          })}
        </section>
      ) : null}

      <ChangeDialog
        change={pending?.change ?? null}
        open={pending !== null}
        onOpenChange={(open) => !open && setPending(null)}
        onApplied={() => {
          if (pending) {
            const index = pending.index;
            setApplied((prev) => new Set(prev).add(index));
          }
          setPending(null);
        }}
      />
    </div>
  );
}

function cnCard(done: boolean, staged: boolean) {
  return [
    "overflow-hidden rounded-lg border",
    done
      ? "border-success/40 bg-success/5"
      : staged
        ? "border-primary/40 bg-primary/5"
        : "border-border bg-card",
  ].join(" ");
}

function cnNotice(ok: boolean) {
  return [
    "flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm",
    ok
      ? "border-border bg-accent/40"
      : "border-warning/40 bg-warning/10 text-warning-tint-foreground",
  ].join(" ");
}
