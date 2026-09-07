import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Users } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { MemberList } from "@/lib/api";
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
import { rdnOf } from "@/lib/values";

/**
 * Who is actually in this group.
 *
 * The entry view already lists the `member` values, and for a flat group that
 * is the whole answer. It stops being one the moment a group holds another
 * group: `cn=everyone` in the test harness lists five members and contains no
 * people at all, because every one of the five is itself a group — and three
 * hundred people are in it.
 *
 * So this shows the people, and for each one the chain of groups that brought
 * them in. That chain is the part a flat list cannot give you and the part you
 * need in order to remove somebody from the *right* group.
 */
export function ExpandMembersButton({ dn, onNavigate }: {
  dn: string;
  onNavigate: (dn: string) => void;
}) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        title="Resolve this group's membership, following nested groups"
      >
        <Users />
        Members
      </Button>
      {open ? (
        <MembersDialog
          dn={dn}
          onClose={() => setOpen(false)}
          onNavigate={(target) => {
            setOpen(false);
            onNavigate(target);
          }}
        />
      ) : null}
    </>
  );
}

const memberLimit = 500;

function MembersDialog({ dn, onClose, onNavigate }: {
  dn: string;
  onClose: () => void;
  onNavigate: (dn: string) => void;
}) {
  const [text, setText] = useState("");

  const query = useQuery<MemberList, ApiFailure>({
    queryKey: ["members", dn],
    queryFn: async () =>
      unwrap(await api.GET("/members", {
        params: { query: { dn, limit: memberLimit } },
      })),
  });

  const all = query.data?.members ?? [];
  // Groups are the structure, people are the answer. Showing them together
  // buries the answer in the structure.
  const people = all.filter((m) => !m.group);
  const groups = all.filter((m) => m.group);

  const needle = text.trim().toLowerCase();
  const shown = needle
    ? people.filter((m) => m.dn.toLowerCase().includes(needle))
    : people;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent wide>
        <DialogHeader>
          <DialogTitle>Members</DialogTitle>
          <DialogDescription>
            Everyone in <span className="font-dn">{dn}</span>, including through
            nested groups.
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-5 py-4">
          {query.isPending ? (
            <p className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Walking the membership…
            </p>
          ) : null}

          {query.error ? (
            <ErrorNote title="Could not resolve the membership" error={query.error} />
          ) : null}

          {query.data ? (
            <>
              {query.data.truncated ? (
                <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-warning-tint-foreground">
                  The walk stopped at {memberLimit} members, so this list is not
                  everybody. When the question is who can get in, a list that
                  quietly omits people is worse than no list.
                </p>
              ) : null}

              {query.data.cycles?.length ? (
                <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-warning-tint-foreground">
                  These groups contain themselves, directly or through others,
                  so the walk stopped rather than looping:{" "}
                  <span className="font-dn">{query.data.cycles.join(", ")}</span>
                </p>
              ) : null}

              {query.data.dangling?.length ? (
                <p className="rounded-md border border-destructive/40 bg-destructive/8 px-3 py-2 text-sm">
                  Named as members but not readable — an entry deleted while a
                  group went on naming it:{" "}
                  <span className="font-dn">{query.data.dangling.join(", ")}</span>
                </p>
              ) : null}

              {query.data.unresolvable?.length ? (
                <p className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
                  Not distinguished names, so they cannot be followed to an
                  entry — <span className="font-dn">memberUid</span> holds a
                  login name and <span className="font-dn">memberURL</span> a
                  search:{" "}
                  <span className="font-dn">
                    {query.data.unresolvable.join(", ")}
                  </span>
                </p>
              ) : null}

              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge variant="outline" className="tabular-nums">
                  {people.length} {people.length === 1 ? "member" : "members"}
                </Badge>
                {groups.length > 0 ? (
                  <Badge variant="secondary" className="tabular-nums">
                    through {groups.length} nested{" "}
                    {groups.length === 1 ? "group" : "groups"}
                  </Badge>
                ) : null}
                {shown.length !== people.length ? (
                  <span className="text-muted-foreground">{shown.length} shown</span>
                ) : null}
              </div>

              {people.length > 0 ? (
                <div className="space-y-1.5">
                  <Label htmlFor="member-filter">Filter</Label>
                  <Input
                    id="member-filter"
                    className="font-dn"
                    value={text}
                    placeholder="part of a name"
                    onChange={(e) => setText(e.target.value)}
                  />
                </div>
              ) : null}

              <ul className="divide-y divide-border rounded-md border border-border">
                {shown.map((m) => (
                  <li key={m.dn} className="flex items-center gap-2 px-3 py-1.5">
                    <button
                      type="button"
                      className="min-w-0 flex-1 truncate text-left font-dn text-sm hover:underline"
                      title={m.dn}
                      onClick={() => onNavigate(m.dn)}
                    >
                      {m.rdn ?? rdnOf(m.dn)}
                    </button>
                    {m.direct ? (
                      <Badge variant="outline" className="shrink-0">direct</Badge>
                    ) : (
                      // The chain, innermost group first, because that is the
                      // one you would edit to remove them.
                      <span
                        className="shrink-0 truncate font-dn text-xs text-muted-foreground"
                        title={(m.via ?? []).join(" → ")}
                      >
                        via {(m.via ?? []).map((v) => rdnOf(v)).join(" → ")}
                      </span>
                    )}
                  </li>
                ))}
                {shown.length === 0 && !query.isPending ? (
                  <li className="px-3 py-4 text-sm text-muted-foreground">
                    {people.length === 0
                      ? "This group contains nobody."
                      : "Nothing here matches that."}
                  </li>
                ) : null}
              </ul>
            </>
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
