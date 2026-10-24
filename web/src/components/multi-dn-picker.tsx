import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, Search as SearchIcon } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { SearchResponse, SearchResultEntry } from "@/lib/api";
import { rdnOf } from "@/lib/values";
import { safeText } from "@/lib/display";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/misc";
import { ErrorNote } from "@/components/change-dialog";
import { nameFilter } from "@/components/dn-picker";

/** An entry chosen in the picker: its DN, and its classes for the nesting check. */
export type PickedEntry = { dn: string; classes: string[] };

function classesOf(entry: SearchResultEntry): string[] {
  const attr = entry.attributes?.find((a) => (a.name.split(";")[0] ?? a.name).toLowerCase() === "objectclass");
  return (attr?.values ?? []).map((v) => v.text ?? "").filter((v) => v !== "");
}

/**
 * MultiDnPicker finds entries and lets several be chosen from one search.
 *
 * The DnPicker beside it returns one DN and closes. Adding twenty-five people
 * to a group through that is twenty-five searches; this keeps the results on
 * screen and collects ticks across searches. There is still no box to paste
 * DNs into: every member chosen is an entry the directory returned.
 */
export function MultiDnPicker({
  baseDn,
  suggestedFilter = "(|(objectClass=person)(objectClass=groupOfNames)(objectClass=groupOfUniqueNames))",
  chosen,
  onChosen,
}: {
  baseDn: string;
  suggestedFilter?: string;
  chosen: PickedEntry[];
  onChosen: (next: PickedEntry[]) => void;
}) {
  const [term, setTerm] = useState("");
  const [filter, setFilter] = useState(suggestedFilter);

  const search = useMutation<SearchResponse, ApiFailure>({
    mutationFn: async () =>
      unwrap(
        await api.POST("/search", {
          body: {
            baseDn,
            scope: "sub",
            filter: nameFilter(filter, term),
            attributes: ["cn", "uid", "objectClass"],
            limit: 200,
            pageSize: 200,
          },
        }),
      ),
  });

  const isChosen = (dn: string) => chosen.some((c) => c.dn === dn);
  const toggle = (entry: SearchResultEntry) =>
    onChosen(
      isChosen(entry.dn)
        ? chosen.filter((c) => c.dn !== entry.dn)
        : [...chosen, { dn: entry.dn, classes: classesOf(entry) }],
    );

  return (
    <div className="space-y-3">
      <div className="flex items-end gap-2">
        <div className="flex-1 space-y-1.5">
          <Label htmlFor="multi-pick-term">Name contains</Label>
          <Input
            id="multi-pick-term"
            value={term}
            placeholder="alice"
            className="font-dn"
            onChange={(e) => setTerm(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && search.mutate()}
          />
        </div>
        <Button onClick={() => search.mutate()} disabled={search.isPending}>
          {search.isPending ? <Loader2 className="animate-spin" /> : <SearchIcon />}
          Search
        </Button>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="multi-pick-filter">Restricted to</Label>
        <Input
          id="multi-pick-filter"
          value={filter}
          className="font-dn"
          onChange={(e) => setFilter(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && search.mutate()}
        />
      </div>

      {search.isError ? <ErrorNote title="The search failed" error={search.error} /> : null}

      {search.data ? (
        <div>
          <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
            <span>
              {search.data.entries.length} {search.data.entries.length === 1 ? "match" : "matches"}
            </span>
            {search.data.truncated ? <Badge variant="warning">truncated — narrow the search</Badge> : null}
          </div>
          <ul className="max-h-64 divide-y divide-border overflow-y-auto rounded-md border border-border">
            {search.data.entries.map((entry) => (
              <li key={entry.dn}>
                <label className="flex cursor-pointer items-center gap-2 px-3 py-1.5 hover:bg-accent/60">
                  <Checkbox
                    checked={isChosen(entry.dn)}
                    onCheckedChange={() => toggle(entry)}
                    aria-label={`Choose ${entry.dn}`}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block font-dn text-sm">{safeText(rdnOf(entry.dn))}</span>
                    <span className="block truncate font-dn text-xs text-muted-foreground">
                      {safeText(entry.dn)}
                    </span>
                  </span>
                </label>
              </li>
            ))}
            {search.data.entries.length === 0 ? (
              <li className="px-3 py-4 text-sm text-muted-foreground">Nothing matched.</li>
            ) : null}
          </ul>
        </div>
      ) : null}

      <p className="text-xs text-muted-foreground">
        {chosen.length} chosen. Ticks are kept across searches.
      </p>
    </div>
  );
}
