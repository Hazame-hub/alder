import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, Search as SearchIcon } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { SearchResponse } from "@/lib/api";
import { rdnOf } from "@/lib/values";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
 * DnPicker finds an entry and returns its DN.
 *
 * It exists because a DN-valued attribute previously had to be typed by hand,
 * which for `member` -- adding somebody to a group, the most common thing
 * anyone does to a directory -- meant transcribing a full distinguished name
 * without a typo.
 *
 * It is deliberately generic rather than a group-membership feature. Every
 * attribute whose syntax is a DN gets it: member, uniqueMember, manager,
 * secretary, seeAlso, owner. Special-casing groups would have solved one case
 * and left the rest.
 */
export function DnPicker({
  open,
  onOpenChange,
  onPick,
  baseDn,
  title,
  /** Filter suggestion for the attribute being filled, e.g. people for member. */
  suggestedFilter = "(objectClass=person)",
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPick: (dn: string) => void;
  baseDn: string;
  title?: string;
  suggestedFilter?: string;
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
            filter: buildFilter(filter, term),
            attributes: ["cn", "uid", "objectClass"],
            limit: 50,
            pageSize: 50,
          },
        }),
      ),
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent wide className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{title ?? "Choose an entry"}</DialogTitle>
          <DialogDescription>
            Searching below <span className="font-dn">{baseDn}</span>
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
          <div className="flex items-end gap-2">
            <div className="flex-1 space-y-1.5">
              <Label htmlFor="pick-term">Name contains</Label>
              <Input
                id="pick-term"
                value={term}
                autoFocus
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

          <div className="mt-2 space-y-1.5">
            <Label htmlFor="pick-filter">Restricted to</Label>
            <Input
              id="pick-filter"
              value={filter}
              className="font-dn"
              onChange={(e) => setFilter(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && search.mutate()}
            />
            <p className="text-xs text-muted-foreground">
              Combined with the name above into one filter. Both are escaped, so
              a name containing filter characters searches for those characters.
            </p>
          </div>

          {search.isError ? (
            <div className="mt-4">
              <ErrorNote title="The search failed" error={search.error} />
            </div>
          ) : null}

          {search.data ? (
            <div className="mt-4">
              <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
                <span>
                  {search.data.entries.length}{" "}
                  {search.data.entries.length === 1 ? "match" : "matches"}
                </span>
                {search.data.truncated ? (
                  <Badge variant="warning">truncated — narrow the search</Badge>
                ) : null}
              </div>
              <ul className="divide-y divide-border rounded-md border border-border">
                {search.data.entries.map((entry) => (
                  <li key={entry.dn}>
                    <button
                      type="button"
                      className="block w-full px-3 py-2 text-left transition-colors hover:bg-accent/60"
                      onClick={() => {
                        onPick(entry.dn);
                        onOpenChange(false);
                      }}
                    >
                      <div className="font-dn text-sm">{rdnOf(entry.dn)}</div>
                      <div className="truncate font-dn text-xs text-muted-foreground">
                        {entry.dn}
                      </div>
                    </button>
                  </li>
                ))}
              </ul>
              {search.data.entries.length === 0 ? (
                <p className="rounded-md border border-border px-3 py-4 text-sm text-muted-foreground">
                  Nothing matched.
                </p>
              ) : null}
            </div>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * buildFilter combines the restriction with the typed name.
 *
 * The typed value is escaped here so the filter shown to the user is one they
 * can read and trust. The server parses and re-escapes it regardless, so this
 * is the readable layer rather than the security boundary.
 */
function buildFilter(restriction: string, term: string): string {
  const trimmed = term.trim();
  if (!trimmed) return restriction || "(objectClass=*)";
  const v = escapeFilterValue(trimmed);
  const byName = `(|(cn=*${v}*)(uid=*${v}*)(sn=*${v}*)(mail=*${v}*))`;
  return restriction ? `(&${restriction}${byName})` : byName;
}

function escapeFilterValue(v: string): string {
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
