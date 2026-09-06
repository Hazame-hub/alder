import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Loader2,
  Plus,
  Search as SearchIcon,
  Terminal,
  X,
} from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { SearchResponse } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui";
import { ErrorNote } from "@/components/change-dialog";
import { EntryTable } from "@/components/entry-table";
import { ExportMenu } from "@/components/export-menu";
import { LdifBlock } from "@/components/ldif-block";
import {
  buildFilter,
  isAttributeName,
  operators,
  type Clause,
} from "@/lib/filter-builder";
import { stageDeletions, type StageOutcome } from "@/lib/stage-deletes";
import { cn } from "@/lib/utils";

type Scope = "base" | "one" | "sub";

/**
 * SearchPanel offers two ways to say the same thing: a builder for people who
 * do not write RFC 4515 by hand, and a raw filter box for people who do.
 *
 * The builder writes into the raw box rather than living in a parallel state,
 * so what runs is always the filter the user can see, and the builder is a
 * convenience rather than a second source of truth.
 */
export function SearchPanel({
  base,
  scope,
  filter,
  limit,
  readOnly,
  onChange,
  onOpenEntry,
  onReviewChangeset,
}: {
  base: string;
  scope: Scope;
  filter: string;
  limit: number;
  readOnly: boolean;
  /** Writes the search back into the URL, which is what makes it shareable. */
  onChange: (next: {
    base?: string;
    scope?: Scope;
    filter?: string;
    limit?: number;
  }) => void;
  onOpenEntry: (dn: string, forEdit?: boolean) => void;
  onReviewChangeset: () => void;
}) {
  // The boxes are local while they are being typed in: putting every keystroke
  // in the URL would fill the history with half-written filters. The location is
  // written when a search actually runs, which is also when it becomes worth
  // sharing.
  const [draftBase, setDraftBase] = useState(base);
  const [draftFilter, setDraftFilter] = useState(filter);
  const [draftLimit, setDraftLimit] = useState(limit);

  // A location reached by back, forward or a pasted link has to land in the
  // boxes, or the page would show one search and have run another.
  useEffect(() => setDraftBase(base), [base]);
  useEffect(() => setDraftFilter(filter), [filter]);
  useEffect(() => setDraftLimit(limit), [limit]);

  const search = useQuery<SearchResponse, ApiFailure>({
    queryKey: ["search", base, scope, filter, limit],
    // Nothing runs without a base: an empty one searches the root DSE, which is
    // never what was meant.
    enabled: base !== "",
    queryFn: async () =>
      unwrap(
        await api.POST("/search", {
          body: {
            baseDn: base,
            scope,
            filter,
            limit,
            // The page size is a wire detail; the limit is what the user chose.
            pageSize: Math.min(limit, 200),
            attributes: ["cn", "objectClass", "uid", "mail", "ou", "description"],
          },
        }),
      ),
  });

  const run = () =>
    onChange({ base: draftBase, filter: draftFilter, limit: draftLimit, scope });

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="shrink-0 space-y-3 border-b border-border p-4">
        <div className="grid gap-3 md:grid-cols-[1fr_10rem_7rem]">
          <div className="space-y-1.5">
            <Label htmlFor="base">Search base</Label>
            <Input
              id="base"
              value={draftBase}
              className="font-dn"
              onChange={(e) => setDraftBase(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && run()}
            />
          </div>
          <div className="space-y-1.5">
            <Label>Scope</Label>
            <Select value={scope} onValueChange={(v) => onChange({ scope: v as Scope })}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="base">This entry</SelectItem>
                <SelectItem value="one">One level</SelectItem>
                <SelectItem value="sub">Subtree</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="limit">Limit</Label>
            <Input
              id="limit"
              type="number"
              min={1}
              max={10000}
              value={draftLimit}
              onChange={(e) => setDraftLimit(Number(e.target.value) || 100)}
            />
          </div>
        </div>

        <Tabs defaultValue="raw">
          <TabsList>
            <TabsTrigger value="raw">Filter</TabsTrigger>
            <TabsTrigger value="builder">Builder</TabsTrigger>
          </TabsList>

          <TabsContent value="raw" className="pt-3">
            <div className="flex gap-2">
              <Input
                value={draftFilter}
                className="font-dn"
                placeholder="(objectClass=inetOrgPerson)"
                onChange={(e) => setDraftFilter(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && run()}
              />
              <Button onClick={run} disabled={search.isFetching}>
                {search.isFetching ? <Loader2 className="animate-spin" /> : <SearchIcon />}
                Search
              </Button>
            </div>
            <p className="mt-1.5 text-xs text-muted-foreground">
              An RFC 4515 filter. It is parsed, not pasted: a value containing
              filter metacharacters becomes an escaped assertion, never
              structure. The search is in the address bar, so the link
              reproduces it exactly.
            </p>
          </TabsContent>

          <TabsContent value="builder" className="pt-3">
            <FilterBuilder
              onApply={(f) => {
                setDraftFilter(f);
                onChange({ base: draftBase, filter: f, limit: draftLimit, scope });
              }}
            />
          </TabsContent>
        </Tabs>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden">
        {search.isError ? (
          <div className="p-4">
            <ErrorNote title="The search failed" error={search.error} />
          </div>
        ) : search.data ? (
          <Results
            data={search.data}
            base={base}
            scope={scope}
            filter={filter}
            limit={limit}
            readOnly={readOnly}
            onOpenEntry={onOpenEntry}
            onReviewChangeset={onReviewChangeset}
          />
        ) : search.isFetching ? (
          <p className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Searching {base}
          </p>
        ) : (
          <p className="p-6 text-sm text-muted-foreground">
            Run a search to see results.
          </p>
        )}
      </div>
    </div>
  );
}

function Results({
  data,
  base,
  scope,
  filter,
  limit,
  readOnly,
  onOpenEntry,
  onReviewChangeset,
}: {
  data: SearchResponse;
  base: string;
  scope: Scope;
  filter: string;
  limit: number;
  readOnly: boolean;
  onOpenEntry: (dn: string, forEdit?: boolean) => void;
  onReviewChangeset: () => void;
}) {
  const [staging, setStaging] = useState<StageOutcome | null>(null);
  const [showCommand, setShowCommand] = useState(false);
  // The columns are the attributes that were asked for and that something
  // actually returned. A search spans object classes, so a fixed set would show
  // an email column over a page of organizational units.
  const columns = useMemo(() => {
    const present = new Set<string>();
    for (const entry of data.entries) {
      for (const a of entry.attributes ?? []) {
        const name = (a.name.split(";")[0] ?? a.name).toLowerCase();
        if (name !== "objectclass" && a.values.length > 0) present.add(name);
      }
    }
    return ["cn", "uid", "mail", "ou", "description"]
      .filter((n) => present.has(n))
      .map((n) => ({ attribute: n, label: labels[n] ?? n }));
  }, [data.entries]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-sm">
        <span className="font-medium tabular-nums">
          {data.entries.length} {data.entries.length === 1 ? "entry" : "entries"}
        </span>
        {data.took ? <span className="text-muted-foreground">in {data.took}</span> : null}
        {data.truncated ? (
          <Badge variant="warning">
            truncated — there are more results than were returned
          </Badge>
        ) : null}
        {data.referrals?.length ? (
          <Badge variant="outline">
            {data.referrals.length} referral(s), not followed
          </Badge>
        ) : null}

        {/*
          The same search, as the command you would have typed. It sits beside
          the export because both answer "how do I take this away with me" —
          one as a file, one as something to paste into a runbook.
        */}
        {data.command ? (
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto text-muted-foreground"
            title="Show the ldapsearch that runs this search"
            onClick={() => setShowCommand((v) => !v)}
          >
            <Terminal />
            {showCommand ? "Hide command" : "Command"}
          </Button>
        ) : null}

        {/*
          The same filter, base and scope the page ran, so the file is what is
          on screen rather than a subtree that happens to contain it. The
          export applies its own limit and says so in its header.
        */}
        <span className={data.command ? undefined : "ml-auto"}>
          <ExportMenu
            dn={base}
            scope={scope}
            filter={filter}
            limit={limit}
            label="Export these"
          />
        </span>
      </div>

      {showCommand && data.command ? (
        <div className="shrink-0 space-y-2 border-b border-border px-4 py-3">
          <LdifBlock text={data.command} language="shell" filename="search.sh" />
          <p className="text-xs text-muted-foreground">
            The filter is the one the server parsed and sent, which is not
            always the text in the box — that is the point of parsing it.{" "}
            <span className="font-dn">-W</span> prompts for the password; it is
            not here, and it never will be.
          </p>
        </div>
      ) : null}

      {staging ? (
        <div
          className={cn(
            "flex shrink-0 flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-sm",
            staging.ok ? "bg-accent/40" : "bg-warning/10 text-warning-tint-foreground",
          )}
        >
          <span>{staging.message}</span>
          <Button size="sm" variant="outline" onClick={onReviewChangeset}>
            {staging.ok ? "Review the changeset" : "Open the changeset"}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setStaging(null)}>
            Dismiss
          </Button>
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-hidden">
        <EntryTable
          columns={columns}
          entries={data.entries}
          truncated={data.truncated}
          readOnly={readOnly}
          onOpen={onOpenEntry}
          onEdit={(dn) => onOpenEntry(dn, true)}
          onStageDeletes={(dns) => setStaging(stageDeletions(dns))}
          onExport={(dn) => {
            const params = new URLSearchParams({ dn, scope: "base" });
            window.location.href = "/api/v1/export/ldif?" + params.toString();
          }}
          empty={
            <div className="max-w-prose space-y-2">
              <p className="font-medium text-foreground">Nothing matched.</p>
              <p>
                The filter was valid; the directory holds no entry satisfying it
                under this base. A directory answers "nothing matched" and "you
                may not see these" identically, so an unexpected empty result can
                also be the bind identity.
              </p>
            </div>
          }
        />
      </div>
    </div>
  );
}

/** Headings for the attributes the results table asks for. */
const labels: Record<string, string> = {
  cn: "Name",
  uid: "User ID",
  mail: "Email",
  ou: "Unit",
  description: "Description",
};

function FilterBuilder({ onApply }: { onApply: (filter: string) => void }) {
  const [join, setJoin] = useState<"and" | "or">("and");
  const [clauses, setClauses] = useState<Clause[]>([
    { attribute: "objectClass", op: "eq", value: "inetOrgPerson" },
  ]);

  const build = () => buildFilter(join, clauses);

  const set = (i: number, patch: Partial<Clause>) =>
    setClauses((prev) => prev.map((c, j) => (j === i ? { ...c, ...patch } : c)));

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-sm">
        <span className="text-muted-foreground">Match</span>
        <Select value={join} onValueChange={(v) => setJoin(v as "and" | "or")}>
          <SelectTrigger className="w-28">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="and">all of</SelectItem>
            <SelectItem value="or">any of</SelectItem>
          </SelectContent>
        </Select>
        <span className="text-muted-foreground">these conditions</span>
      </div>

      {clauses.map((clause, i) => {
        const needsValue = clause.op !== "present";
        return (
          <div key={i} className="flex flex-wrap items-center gap-2">
            <Input
              value={clause.attribute}
              placeholder="attribute"
              className={cn(
                "w-44 font-dn",
                clause.attribute.trim() !== "" && !isAttributeName(clause.attribute)
                  ? "border-destructive"
                  : undefined,
              )}
              aria-invalid={
                clause.attribute.trim() !== "" && !isAttributeName(clause.attribute)
              }
              title={
                clause.attribute.trim() !== "" && !isAttributeName(clause.attribute)
                  ? "Not an attribute name, so this condition is left out of the filter"
                  : undefined
              }
              onChange={(e) => set(i, { attribute: e.target.value })}
            />
            <Select value={clause.op} onValueChange={(v) => set(i, { op: v })}>
              <SelectTrigger className="w-36">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {operators.map((o) => (
                  <SelectItem key={o.id} value={o.id}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {needsValue ? (
              <Input
                value={clause.value}
                placeholder="value"
                className="w-56 font-dn"
                onChange={(e) => set(i, { value: e.target.value })}
              />
            ) : null}
            {clauses.length > 1 ? (
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => setClauses((prev) => prev.filter((_, j) => j !== i))}
                aria-label="Remove condition"
              >
                <X />
              </Button>
            ) : null}
          </div>
        );
      })}

      <div className="flex items-center gap-2 pt-1">
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            setClauses((prev) => [...prev, { attribute: "", op: "eq", value: "" }])
          }
        >
          <Plus />
          Condition
        </Button>
        <Button size="sm" onClick={() => onApply(build())}>
          Use this filter
        </Button>
        <code className="ml-auto truncate font-mono text-xs text-muted-foreground">
          {build()}
        </code>
      </div>
    </div>
  );
}
