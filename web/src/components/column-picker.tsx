import { useState } from "react";
import { Columns3, Plus, X } from "lucide-react";
import type { ObjectViewColumn } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/**
 * Choosing what a table shows.
 *
 * The candidates come from the server, computed over every class the view's
 * filter matches. That is deliberate and it is the whole reason this is not a
 * list assembled in the browser: the walk has to go *down* the class tree, not
 * along the superior chain, or it drops `mail` from Users — `person` does not
 * permit it and `inetOrgPerson` does. Alder has fixed that bug once already,
 * and a second copy of the rule here is how it would come back.
 *
 * Chosen columns keep the order they were added, because that is the order they
 * appear in, and shuffling them alphabetically would take away the only control
 * anybody has over the shape of the table.
 */
export function ColumnPicker({
  available,
  chosen,
  onChange,
  onReset,
}: {
  available: ObjectViewColumn[];
  chosen: ObjectViewColumn[];
  onChange: (next: ObjectViewColumn[]) => void;
  /** Back to what the server suggested. Absent when already there. */
  onReset?: () => void;
}) {
  const [query, setQuery] = useState("");

  const taken = new Set(chosen.map((c) => c.attribute.toLowerCase()));
  const offered = available.filter(
    (c) =>
      !taken.has(c.attribute.toLowerCase()) &&
      (query.trim() === "" ||
        c.attribute.toLowerCase().includes(query.trim().toLowerCase()) ||
        c.label.toLowerCase().includes(query.trim().toLowerCase())),
  );

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="text-muted-foreground">
          <Columns3 />
          Columns
          <span className="tabular-nums">({chosen.length})</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-80 p-0">
        <div className="space-y-2 border-b border-border p-2">
          <p className="text-xs text-muted-foreground">
            Showing, in this order. Removing the last one leaves the entry
            column, which is always there.
          </p>
          <div className="flex flex-wrap gap-1">
            {chosen.map((c) => (
              <button
                key={c.attribute}
                type="button"
                title={c.desc ?? c.attribute}
                onClick={() =>
                  onChange(chosen.filter((k) => k.attribute !== c.attribute))
                }
                className="flex items-center gap-1 rounded-md border border-primary/40 bg-primary/10 px-1.5 py-0.5 font-dn text-xs"
              >
                {c.attribute}
                <X className="size-3 opacity-60" />
              </button>
            ))}
            {chosen.length === 0 ? (
              <span className="text-xs text-muted-foreground">
                No attribute columns.
              </span>
            ) : null}
          </div>
          {onReset ? (
            <Button variant="ghost" size="sm" className="h-6 px-1.5 text-xs" onClick={onReset}>
              Back to the suggested columns
            </Button>
          ) : null}
        </div>

        <div className="p-2">
          <Input
            value={query}
            placeholder="attribute or heading"
            className="h-8"
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>

        <div className="max-h-64 overflow-y-auto px-2 pb-2">
          {offered.map((c) => (
            <button
              key={c.attribute}
              type="button"
              onClick={() => onChange([...chosen, c])}
              className={cn(
                "flex w-full items-baseline gap-2 rounded-sm px-1.5 py-1 text-left text-sm",
                "hover:bg-accent focus:bg-accent focus:outline-none",
              )}
            >
              <Plus className="size-3 shrink-0 text-muted-foreground" />
              <span className="min-w-0">
                <span className="block truncate">{c.label}</span>
                {/*
                  The real attribute name under the heading, as everywhere else:
                  a picker that offers only "Email" teaches somebody their
                  directory has a field called Email.
                */}
                <span className="block truncate font-dn text-[0.68rem] text-muted-foreground">
                  {c.attribute}
                  {c.desc ? ` — ${c.desc}` : ""}
                </span>
              </span>
            </button>
          ))}
          {offered.length === 0 ? (
            <p className="px-1.5 py-2 text-sm text-muted-foreground">
              {available.length === 0
                ? "This server published nothing to choose from."
                : query.trim() !== ""
                  ? "Nothing here matches that."
                  : "Every attribute these classes permit is already shown."}
            </p>
          ) : null}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
