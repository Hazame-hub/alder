import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Plus, Search as SearchIcon, X } from "lucide-react";
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
          <Results data={search.data} readOnly={readOnly} onOpenEntry={onOpenEntry} />
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
  readOnly,
  onOpenEntry,
}: {
  data: SearchResponse;
  readOnly: boolean;
  onOpenEntry: (dn: string, forEdit?: boolean) => void;
}) {
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
      </div>

      <div className="min-h-0 flex-1 overflow-hidden">
        <EntryTable
          columns={columns}
          entries={data.entries}
          truncated={data.truncated}
          readOnly={readOnly}
          onOpen={onOpenEntry}
          onEdit={(dn) => onOpenEntry(dn, true)}
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

type Clause = { attribute: string; op: string; value: string };

const operators = [
  { id: "eq", label: "is", render: (a: string, v: string) => `(${a}=${esc(v)})` },
  {
    id: "contains",
    label: "contains",
    render: (a: string, v: string) => `(${a}=*${esc(v)}*)`,
  },
  {
    id: "starts",
    label: "starts with",
    render: (a: string, v: string) => `(${a}=${esc(v)}*)`,
  },
  {
    id: "ends",
    label: "ends with",
    render: (a: string, v: string) => `(${a}=*${esc(v)})`,
  },
  { id: "present", label: "is set", render: (a: string) => `(${a}=*)` },
  { id: "gte", label: "is at least", render: (a: string, v: string) => `(${a}>=${esc(v)})` },
  { id: "lte", label: "is at most", render: (a: string, v: string) => `(${a}<=${esc(v)})` },
  {
    id: "not",
    label: "is not",
    render: (a: string, v: string) => `(!(${a}=${esc(v)}))`,
  },
];

/**
 * esc applies RFC 4515 escaping to a value the user typed.
 *
 * The server escapes again when it parses and rebuilds the filter, so this is
 * belt and braces rather than the only defence. It is here so the filter the
 * builder writes into the box is one the user can read and trust, instead of
 * one that looks broken until the server fixes it.
 */
function esc(v: string): string {
  return v.replace(/[\\*()\0]/g, (c) => {
    switch (c) {
      case "\\":
        return "\\5c";
      case "*":
        return "\\2a";
      case "(":
        return "\\28";
      case ")":
        return "\\29";
      default:
        return "\\00";
    }
  });
}

function FilterBuilder({ onApply }: { onApply: (filter: string) => void }) {
  const [join, setJoin] = useState<"and" | "or">("and");
  const [clauses, setClauses] = useState<Clause[]>([
    { attribute: "objectClass", op: "eq", value: "inetOrgPerson" },
  ]);

  const build = () => {
    const parts = clauses
      .filter((c) => c.attribute.trim() !== "")
      .map((c) => {
        const op = operators.find((o) => o.id === c.op) ?? operators[0];
        return op!.render(c.attribute.trim(), c.value);
      });
    if (parts.length === 0) return "(objectClass=*)";
    if (parts.length === 1) return parts[0] as string;
    return `(${join === "and" ? "&" : "|"}${parts.join("")})`;
  };

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
              className="w-44 font-dn"
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
