import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { GitCompare, Loader2 } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { AttributeComparison, EntryComparison } from "@/lib/api";
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
import { Checkbox } from "@/components/ui";
import { ErrorNote } from "@/components/change-dialog";
import { displayText, rdnOf } from "@/lib/values";

/**
 * Why does this account work and that one not.
 *
 * Two things make this more than a text diff, and both come from the server.
 * Each side is annotated from its *own* object classes, so an attribute one
 * entry's classes require and it does not hold shows up — and that absence is
 * frequently the whole answer, while being invisible to any diff of what is
 * present. And a sensitive attribute is compared on presence alone: that a
 * password is set is not the secret, but whether two entries hold the same one
 * is not a question this product answers.
 */
export function CompareButton({ dn }: { dn: string }) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        title="Compare this entry against another, attribute by attribute"
      >
        <GitCompare />
        Compare
      </Button>
      {open ? <CompareDialog left={dn} onClose={() => setOpen(false)} /> : null}
    </>
  );
}

function CompareDialog({ left, onClose }: { left: string; onClose: () => void }) {
  const [typed, setTyped] = useState("");
  const [right, setRight] = useState("");
  const [operational, setOperational] = useState(true);
  const [onlyDifferences, setOnlyDifferences] = useState(true);

  const query = useQuery<EntryComparison, ApiFailure>({
    queryKey: ["compare", left, right, operational],
    enabled: right !== "",
    queryFn: async () =>
      unwrap(
        await api.GET("/compare", {
          params: { query: { left, right, includeOperational: operational } },
        }),
      ),
  });

  const rows = query.data?.attributes ?? [];
  const shown = onlyDifferences ? rows.filter((r) => r.status !== "same") : rows;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent wide>
        <DialogHeader>
          <DialogTitle>Compare</DialogTitle>
          <DialogDescription>
            <span className="font-dn">{left}</span> against another entry.
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-5 py-4">
          <div className="space-y-1.5">
            <Label htmlFor="compare-right">Compare against</Label>
            <div className="flex gap-2">
              <Input
                id="compare-right"
                className="font-dn"
                value={typed}
                placeholder="uid=someone,ou=people,dc=example,dc=test"
                onChange={(e) => setTyped(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && setRight(typed.trim())}
              />
              <Button
                disabled={typed.trim() === ""}
                onClick={() => setRight(typed.trim())}
              >
                Compare
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              The DN of the entry to compare against — usually already on your
              clipboard from the one you were just looking at.
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-4">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={onlyDifferences}
                onCheckedChange={(v) => setOnlyDifferences(v === true)}
              />
              Only what differs
            </label>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={operational}
                onCheckedChange={(v) => setOperational(v === true)}
              />
              <span>
                Include what the directory owns
                <span className="block text-xs text-muted-foreground">
                  Mostly noise — but a lock is operational, and is sometimes the
                  whole answer.
                </span>
              </span>
            </label>
          </div>

          {query.isPending && right !== "" ? (
            <p className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Reading both entries…
            </p>
          ) : null}

          {query.error ? (
            <ErrorNote title="Could not compare these entries" error={query.error} />
          ) : null}

          {query.data ? (
            <>
              {/* A difference in structural class explains most of the rows
                  under it, so it is read first. */}
              <div className="grid gap-2 sm:grid-cols-2">
                {[query.data.left, query.data.right].map((side, i) => (
                  <div key={i} className="rounded-md border border-border px-3 py-2">
                    <div className="truncate font-dn text-sm" title={side.dn}>
                      {side.rdn ?? rdnOf(side.dn)}
                    </div>
                    <div className="mt-0.5 text-xs text-muted-foreground">
                      {side.structural ? (
                        <span className="font-dn">{side.structural}</span>
                      ) : (
                        <span className="text-warning-tint-foreground">
                          no single structural class
                        </span>
                      )}
                    </div>
                  </div>
                ))}
              </div>

              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge variant="outline" className="tabular-nums">
                  {query.data.counts.same} same
                </Badge>
                <Badge variant="warning" className="tabular-nums">
                  {query.data.counts.differs} differ
                </Badge>
                <Badge variant="secondary" className="tabular-nums">
                  {query.data.counts.leftOnly} only on the left
                </Badge>
                <Badge variant="secondary" className="tabular-nums">
                  {query.data.counts.rightOnly} only on the right
                </Badge>
                {/* Only when there are any: an operator comparing two entries
                    that hold no secrets should not be shown a zero. */}
                {query.data.counts.withheld > 0 ? (
                  <Badge variant="outline" className="tabular-nums">
                    {query.data.counts.withheld} withheld
                  </Badge>
                ) : null}
              </div>

              <p className="text-xs text-muted-foreground">
                Values are compared byte for byte, except DNs, which are compared
                as DNs — so the directory may still consider two values here the
                same under the attribute&apos;s matching rule.
              </p>

              <ul className="divide-y divide-border rounded-md border border-border">
                {shown.map((row) => (
                  <CompareRow key={row.name} row={row} />
                ))}
                {shown.length === 0 ? (
                  <li className="px-3 py-4 text-sm text-muted-foreground">
                    {rows.length === 0
                      ? "Nothing to compare."
                      : "These two entries are identical in every attribute compared."}
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

function CompareRow({ row }: { row: AttributeComparison }) {
  return (
    <li className="px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-dn text-sm font-medium">{row.name}</span>
        <StatusBadge row={row} />
        {row.left.required && !row.left.present ? (
          <Badge variant="warning">required on the left, and absent</Badge>
        ) : null}
        {row.right.required && !row.right.present ? (
          <Badge variant="warning">required on the right, and absent</Badge>
        ) : null}
        {row.left.present && !row.left.permitted ? (
          <Badge variant="warning">not permitted on the left</Badge>
        ) : null}
        {row.right.present && !row.right.permitted ? (
          <Badge variant="warning">not permitted on the right</Badge>
        ) : null}
      </div>

      {row.withheld ? (
        <p className="mt-1 text-xs text-muted-foreground">
          Withheld. {row.left.present ? "Set" : "Not set"} on the left,{" "}
          {row.right.present ? "set" : "not set"} on the right
          {row.status === "withheld"
            ? " — whether they hold the same value is not reported."
            : "."}
        </p>
      ) : (
        <div className="mt-1 grid gap-0.5">
          {(row.values ?? []).map((v, i) => (
            <div key={i} className="flex items-start gap-2 text-xs">
              <span
                className={
                  v.side === "both"
                    ? "w-14 shrink-0 text-muted-foreground"
                    : "w-14 shrink-0 font-medium text-warning-tint-foreground"
                }
              >
                {v.side === "both" ? "both" : v.side}
              </span>
              <span className="min-w-0 flex-1 break-all font-dn">
                {displayText(v.value)}
              </span>
            </div>
          ))}
        </div>
      )}
    </li>
  );
}

function StatusBadge({ row }: { row: AttributeComparison }) {
  switch (row.status) {
    case "differs":
      return <Badge variant="warning">differs</Badge>;
    case "leftOnly":
      return <Badge variant="secondary">only on the left</Badge>;
    case "rightOnly":
      return <Badge variant="secondary">only on the right</Badge>;
    // Its own badge, never "same". The server did not compare these values, so
    // nothing here may suggest they match.
    case "withheld":
      return <Badge variant="outline">withheld</Badge>;
    default:
      return <Badge variant="outline">same</Badge>;
  }
}
