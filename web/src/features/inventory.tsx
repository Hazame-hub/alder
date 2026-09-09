import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { BarChart3, Loader2 } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { InventoryResponse } from "@/lib/api";
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
import { displayText } from "@/lib/values";

/**
 * What values does this attribute actually hold.
 *
 * The question that finds a team spelled "platfrm" with four people on it, or
 * a cost centre nobody has used since a reorganisation. A directory owner can
 * ask it today only by exporting the subtree and counting in a shell.
 *
 * Everything on screen is scoped to the entries examined and says so. A tally
 * over a bounded search that reads as a description of the directory is a
 * confident wrong answer, and this feature is only worth having if it is
 * trusted.
 */
export function InventoryButton({ base, attribute }: {
  base: string;
  attribute?: string;
}) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        className="text-muted-foreground"
        onClick={() => setOpen(true)}
        title="Tally what values an attribute holds under here"
      >
        <BarChart3 />
        Values
      </Button>
      {open ? (
        <InventoryDialog
          base={base}
          initial={attribute ?? ""}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </>
  );
}

function InventoryDialog({ base, initial, onClose }: {
  base: string;
  initial: string;
  onClose: () => void;
}) {
  const [attribute, setAttribute] = useState(initial);
  const [limit, setLimit] = useState(1000);

  const run = useMutation<InventoryResponse, ApiFailure>({
    mutationFn: async () =>
      unwrap(
        await api.POST("/inventory", {
          body: {
            baseDn: base,
            scope: "sub",
            attribute: attribute.trim(),
            limit,
            // The rows rendered. The counts behind them stay exact, and the
            // tail comes back as a remainder rather than being dropped.
            maxValues: 200,
          },
        }),
      ),
  });

  const data = run.data;
  // Every value held by exactly one entry, and as many distinct values as
  // entries: this is an identifier, not a category, and a tally of it is a
  // list rather than an answer.
  const looksLikeAnIdentifier =
    data !== undefined &&
    data.withValue > 1 &&
    data.distinctValues === data.withValue;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent wide>
        <DialogHeader>
          <DialogTitle>Values</DialogTitle>
          <DialogDescription>
            What one attribute holds under <span className="font-dn">{base}</span>,
            and how many entries carry each.
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-5 py-4">
          <div className="grid gap-3 sm:grid-cols-[1fr_8rem_auto] sm:items-end">
            <div className="space-y-1.5">
              <Label htmlFor="inventory-attribute">Attribute</Label>
              <Input
                id="inventory-attribute"
                className="font-dn"
                value={attribute}
                placeholder="departmentNumber"
                onChange={(e) => setAttribute(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && attribute.trim() && run.mutate()}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="inventory-limit">Examine</Label>
              <Input
                id="inventory-limit"
                type="number"
                min={1}
                max={10000}
                value={limit}
                onChange={(e) => setLimit(Number(e.target.value) || 1000)}
              />
            </div>
            <Button
              disabled={attribute.trim() === "" || run.isPending}
              onClick={() => run.mutate()}
            >
              {run.isPending ? <Loader2 className="animate-spin" /> : <BarChart3 />}
              Tally
            </Button>
          </div>

          {run.isError ? (
            <ErrorNote title="That attribute could not be tallied" error={run.error} />
          ) : null}

          {data ? (
            <>
              {data.truncated ? (
                <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-warning-tint-foreground">
                  The search stopped at {data.limit} entries, so entries exist
                  under this base that were not examined. Everything below
                  describes the {data.examined} that were — raise the limit, or
                  narrow the base, before reading it as the whole picture.
                </p>
              ) : null}

              <div className="flex flex-wrap items-center gap-2 text-sm">
                <Badge variant="outline" className="tabular-nums">
                  {data.examined} entries examined
                </Badge>
                <Badge variant="secondary" className="tabular-nums">
                  {data.withValue} hold it
                </Badge>
                <Badge variant="outline" className="tabular-nums">
                  {data.withoutValue} do not
                </Badge>
                <Badge variant="secondary" className="tabular-nums">
                  {data.distinctValues} distinct
                </Badge>
                {data.singletonValues > 0 ? (
                  <Badge variant="warning" className="tabular-nums">
                    {data.singletonValues} held by one entry
                  </Badge>
                ) : null}
              </div>

              {looksLikeAnIdentifier ? (
                <p className="rounded-md border border-border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
                  Every entry holds a different value, so this attribute
                  identifies entries rather than grouping them — a tally of it
                  is a list, not an answer.
                </p>
              ) : null}

              <ul className="divide-y divide-border rounded-md border border-border">
                {data.values.map((row, i) => (
                  <li key={i} className="flex items-center gap-3 px-3 py-1.5">
                    <span className="min-w-0 flex-1 truncate font-dn text-sm">
                      {displayText(row.value)}
                    </span>
                    {row.entries === 1 ? (
                      // The typo signal: one entry among hundreds is usually a
                      // misspelling of a value that many hold.
                      <Badge variant="warning" className="shrink-0 tabular-nums">
                        1 entry
                      </Badge>
                    ) : (
                      <span className="shrink-0 tabular-nums text-sm text-muted-foreground">
                        {row.entries} entries
                      </span>
                    )}
                  </li>
                ))}
                {data.values.length === 0 ? (
                  <li className="px-3 py-4 text-sm text-muted-foreground">
                    No entry under here holds that attribute.
                  </li>
                ) : null}
              </ul>

              {data.otherValues > 0 ? (
                <p className="text-xs text-muted-foreground">
                  {data.otherValues} further distinct{" "}
                  {data.otherValues === 1 ? "value is" : "values are"} held by{" "}
                  {data.otherEntries}{" "}
                  {data.otherEntries === 1 ? "entry" : "entries"}, counted here
                  but not listed.
                </p>
              ) : null}
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
