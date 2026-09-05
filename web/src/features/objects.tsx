import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Info, Loader2, RefreshCw } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { ApiFailure, ChangeRequest, ObjectView, ObjectViewId } from "@/lib/api";
import { changeset } from "@/lib/changeset";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui";
import { ChangeDialog, ErrorNote } from "@/components/change-dialog";
import { EntryTable } from "@/components/entry-table";

/**
 * Users, groups and organizational units, as pages of their own.
 *
 * None of these is a thing the protocol has. Each is a search the server told
 * us how to run, over classes it told us it defines — see /views. The panel's
 * job is to run it, show the answer as a table, and never let the friendly
 * label be the only thing on screen: the filter it ran and the classes it means
 * by "user" are one click away on every page, because a view whose definition
 * is hidden is a view you cannot check.
 */

/** What a table asks for beyond its columns, so a row can be acted on. */
const alwaysFetch = ["objectClass"];

/** The page a view loads. Bounded like every other search in the application. */
const pageLimit = 200;

export function ObjectListPanel({
  viewId,
  namingContexts,
  readOnly,
  onOpenEntry,
  onReviewChangeset,
}: {
  viewId: ObjectViewId;
  namingContexts: string[];
  readOnly: boolean;
  onOpenEntry: (dn: string, forEdit?: boolean) => void;
  onReviewChangeset: () => void;
}) {
  const [base, setBase] = useState(namingContexts[0] ?? "");
  const [showDefinition, setShowDefinition] = useState(false);
  const [deleteChange, setDeleteChange] = useState<ChangeRequest | null>(null);
  const [justStaged, setJustStaged] = useState(0);

  // A connection change can leave the old base selected, which would search a
  // suffix this server does not hold.
  useEffect(() => {
    if (!namingContexts.includes(base)) setBase(namingContexts[0] ?? "");
  }, [namingContexts, base]);

  const views = useQuery({
    queryKey: ["views"],
    queryFn: async () => unwrap(await api.GET("/views")),
    staleTime: Infinity, // The schema does not change under a session.
  });

  const view = views.data?.views.find((v) => v.id === viewId);

  const results = useQuery({
    queryKey: ["objects", viewId, base, view?.filter],
    enabled: view !== undefined && base !== "",
    queryFn: async () =>
      unwrap(
        await api.POST("/search", {
          body: {
            baseDn: base,
            scope: "sub",
            filter: (view as ObjectView).filter,
            limit: pageLimit,
            pageSize: 100,
            attributes: [
              ...alwaysFetch,
              ...(view as ObjectView).columns.map((c) => c.attribute),
            ],
          },
        }),
      ),
  });

  if (views.isPending) {
    return (
      <div className="flex items-center gap-2 p-8 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Working out what this directory calls things…
      </div>
    );
  }
  if (views.isError) {
    return (
      <div className="p-6">
        <ErrorNote
          title="The schema could not be read"
          error={views.error as ApiFailure}
        />
      </div>
    );
  }

  // A view is absent when the server defines none of the classes it anchors on.
  if (!view) {
    return <NoSuchView viewId={viewId} />;
  }

  const stageDeletes = (dns: string[]) => {
    for (const dn of dns) {
      changeset.add({ dn, type: "delete" }, `Delete ${dn}`);
    }
    setJustStaged(dns.length);
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="shrink-0 border-b border-border px-4 py-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h2 className="text-base font-semibold">{view.label}</h2>
          {view.description ? (
            <span className="text-sm text-muted-foreground">{view.description}</span>
          ) : null}

          <Button
            variant="ghost"
            size="sm"
            className="text-muted-foreground"
            onClick={() => setShowDefinition((v) => !v)}
            aria-expanded={showDefinition}
          >
            <Info />
            How this is built
          </Button>

          <div className="ml-auto flex items-center gap-2">
            {namingContexts.length > 1 ? (
              <>
                <Label htmlFor="objects-base" className="text-xs text-muted-foreground">
                  Under
                </Label>
                <Select value={base} onValueChange={setBase}>
                  <SelectTrigger id="objects-base" className="w-72">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {namingContexts.map((ctx) => (
                      <SelectItem key={ctx} value={ctx}>
                        {ctx}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </>
            ) : (
              <span className="font-dn text-xs text-muted-foreground">{base}</span>
            )}
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Run the search again"
              onClick={() => void results.refetch()}
            >
              <RefreshCw className={results.isFetching ? "animate-spin" : undefined} />
            </Button>
          </div>
        </div>

        {showDefinition ? <ViewDefinition view={view} base={base} /> : null}

        {justStaged > 0 ? (
          <div className="mt-3 flex flex-wrap items-center gap-2 rounded-md border border-border bg-accent/40 px-3 py-2 text-sm">
            <span>
              {justStaged} {justStaged === 1 ? "deletion is" : "deletions are"} staged.
              Nothing has been sent to the directory.
            </span>
            <Button size="sm" variant="outline" onClick={onReviewChangeset}>
              Review the changeset
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setJustStaged(0)}>
              Dismiss
            </Button>
          </div>
        ) : null}
      </header>

      <div className="min-h-0 flex-1 overflow-hidden">
        {results.isPending && results.fetchStatus !== "idle" ? (
          <div className="flex items-center gap-2 p-8 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Searching {base}
          </div>
        ) : results.isError ? (
          <div className="p-6">
            <ErrorNote
              title={`The ${view.label.toLowerCase()} could not be listed`}
              error={results.error as ApiFailure}
            />
          </div>
        ) : results.data ? (
          <div className="flex h-full min-h-0 flex-col">
            <ResultSummary
              count={results.data.entries.length}
              truncated={results.data.truncated}
              took={results.data.took}
              limit={pageLimit}
            />
            <div className="min-h-0 flex-1 overflow-hidden">
              <EntryTable
                columns={view.columns}
                entries={results.data.entries}
                truncated={results.data.truncated}
                readOnly={readOnly}
                onOpen={onOpenEntry}
                onEdit={(dn) => onOpenEntry(dn, true)}
                onExport={(dn) => {
                  const params = new URLSearchParams({ dn, scope: "base" });
                  window.location.href = `/api/v1/export/ldif?${params.toString()}`;
                }}
                onDelete={(dn) => setDeleteChange({ dn, type: "delete" })}
                onStageDeletes={stageDeletes}
                empty={<EmptyResult view={view} base={base} />}
              />
            </div>
          </div>
        ) : null}
      </div>

      <ChangeDialog
        change={deleteChange}
        open={deleteChange !== null}
        onOpenChange={(open) => !open && setDeleteChange(null)}
        title="Delete this entry"
        destructive
        onApplied={() => void results.refetch()}
      />
    </div>
  );
}

/**
 * What the view actually ran.
 *
 * The filter is shown verbatim so it can be copied into the search page and
 * changed, which is the escape hatch that makes the friendly page safe: nothing
 * here is reachable only through this page.
 */
function ViewDefinition({ view, base }: { view: ObjectView; base: string }) {
  return (
    <div className="mt-3 space-y-2 rounded-md border border-border bg-muted/40 px-3 py-2.5 text-sm">
      <p className="text-muted-foreground">
        A subtree search under <span className="font-dn">{base}</span>, matching
        any entry that carries{" "}
        {view.anchors.length === 1 ? "the class" : "one of the classes"}{" "}
        {view.anchors.map((a, i) => (
          <span key={a}>
            {i > 0 ? ", " : ""}
            <span className="font-dn text-foreground">{a}</span>
          </span>
        ))}
        . A class of your own that inherits from{" "}
        {view.anchors.length === 1 ? "it" : "one of them"} is matched too, without
        being named: an entry carries its superclasses in its own objectClass.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs uppercase tracking-wide text-muted-foreground">
          Filter
        </span>
        <code className="rounded bg-background px-1.5 py-0.5 font-mono text-xs">
          {view.filter}
        </code>
      </div>
      <p className="text-xs text-muted-foreground">
        The columns are the attributes the matched classes permit, so a directory
        without them shows no empty column rather than a blank one.
      </p>
    </div>
  );
}

function ResultSummary({
  count,
  truncated,
  took,
  limit,
}: {
  count: number;
  truncated: boolean;
  took?: string;
  limit: number;
}) {
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-1.5 text-sm">
      <span className="font-medium tabular-nums">
        {count} {count === 1 ? "entry" : "entries"}
      </span>
      {took ? <span className="text-muted-foreground">in {took}</span> : null}
      {truncated ? (
        <Badge variant="warning">
          stopped at {limit} — there are more than this
        </Badge>
      ) : null}
    </div>
  );
}

function EmptyResult({ view, base }: { view: ObjectView; base: string }) {
  return (
    <div className="max-w-prose space-y-2">
      <p className="font-medium text-foreground">
        No {view.label.toLowerCase()} under this suffix.
      </p>
      <p>
        The search ran and the filter was valid — <span className="font-dn">{base}</span>{" "}
        simply holds no entry carrying{" "}
        {view.anchors.length === 1 ? "" : "any of "}
        {view.anchors.join(", ")}.
      </p>
      <p>
        If you expected some, the usual causes are a different suffix, or a bind
        identity that is not allowed to read them — a directory returns "nothing
        matched" and "you may not see these" identically.
      </p>
    </div>
  );
}

function NoSuchView({ viewId }: { viewId: ObjectViewId }) {
  const noun =
    viewId === "users" ? "users" : viewId === "groups" ? "groups" : "organizational units";
  return (
    <div className="max-w-prose space-y-2 p-8 text-sm text-muted-foreground">
      <p className="font-medium text-foreground">
        This directory has no {noun} view.
      </p>
      <p>
        Alder builds each view from the schema the server published, and this one
        publishes none of the classes a {noun} view is anchored on. Rather than
        show a page that could only ever be empty, it is not offered.
      </p>
      <p>
        The schema browser lists what this server does define, and the search
        page will run any filter you write over it.
      </p>
    </div>
  );
}
