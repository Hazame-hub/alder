import { useMemo, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  ChevronsUpDown,
  Copy,
  Eye,
  FileDown,
  MoreVertical,
  Pencil,
  Trash2,
} from "lucide-react";
import type { SearchResultEntry } from "@/lib/api";
import { displayText, rdnOf } from "@/lib/values";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/misc";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/**
 * The table half of the browser.
 *
 * A tree is the right shape for a directory's structure and the wrong shape for
 * a flat container holding four thousand people. This renders the same entries
 * as rows, with the attributes worth comparing as columns.
 *
 * Two things it deliberately does not do. It does not fetch: it renders the
 * page it is handed, so the paging rules that apply everywhere else apply here
 * unchanged. And it does not pretend the sort is a server-side one — the
 * ordering is over the rows actually loaded, and the header says so whenever
 * the result was cut short, because a "first alphabetically" that is really
 * "first alphabetically out of the hundred that arrived" is a lie a table
 * tells very convincingly.
 */

export type EntryTableColumn = {
  attribute: string;
  label: string;
  desc?: string;
};

export type EntryTableProps = {
  columns: EntryTableColumn[];
  entries: SearchResultEntry[];
  /** True when the search stopped early. Drives the caveat on the sort. */
  truncated?: boolean;
  readOnly?: boolean;
  /** Shown when there are no entries at all. */
  empty?: React.ReactNode;
  onOpen: (dn: string) => void;
  onEdit?: (dn: string) => void;
  onDelete?: (dn: string) => void;
  onExport?: (dn: string) => void;
  /** Offered only when given; receives every selected DN. */
  onStageDeletes?: (dns: string[]) => void;
};

type Sort = { key: string; dir: "asc" | "desc" } | null;

/**
 * The DN column's sort key.
 *
 * Underscores are not legal in an attribute type name — RFC 4512 allows only
 * letters, digits and hyphens — so this cannot collide with a column.
 */
const dnKey = "__dn";

export function EntryTable({
  columns,
  entries,
  truncated,
  readOnly,
  empty,
  onOpen,
  onEdit,
  onDelete,
  onExport,
  onStageDeletes,
}: EntryTableProps) {
  const [sort, setSort] = useState<Sort>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const rows = useMemo(() => sortRows(entries, sort), [entries, sort]);

  const toggleSort = (key: string) =>
    setSort((prev) => {
      if (prev?.key !== key) return { key, dir: "asc" };
      if (prev.dir === "asc") return { key, dir: "desc" };
      // Third click clears it, so the server's own order is reachable again.
      return null;
    });

  const toggleRow = (dn: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(dn)) next.delete(dn);
      else next.add(dn);
      return next;
    });

  const allOnPage = rows.length > 0 && rows.every((e) => selected.has(e.dn));
  const someOnPage = rows.some((e) => selected.has(e.dn));

  const toggleAll = () =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (allOnPage) rows.forEach((e) => next.delete(e.dn));
      else rows.forEach((e) => next.add(e.dn));
      return next;
    });

  const selectable = onStageDeletes !== undefined && readOnly !== true;

  if (entries.length === 0) {
    return <div className="p-6 text-sm text-muted-foreground">{empty}</div>;
  }

  return (
    // h-full, not just flex-col: the scrolling happens in the div below, and a
    // child that sizes to its content gives that div no height to scroll
    // within. Without it the table renders all 200 rows and clips the ones past
    // the fold, with no scrollbar and no way to reach them.
    <div className="flex h-full min-h-0 flex-col">
      {selectable && selected.size > 0 ? (
        <div className="flex flex-wrap items-center gap-3 border-b border-border bg-accent/40 px-3 py-2 text-sm">
          <span className="font-medium tabular-nums">
            {selected.size} selected
          </span>
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              onStageDeletes?.([...selected]);
              setSelected(new Set());
            }}
          >
            <Trash2 />
            Stage {selected.size === 1 ? "deletion" : "deletions"}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            Clear
          </Button>
          <span className="text-xs text-muted-foreground">
            Staged, not applied. The changeset shows every deletion as one LDIF
            document before anything is sent.
          </span>
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-auto">
        <table className="w-full border-collapse text-sm">
          <thead className="sticky top-0 z-10 bg-background">
            <tr className="border-b border-border text-left">
              {selectable ? (
                <th className="w-9 px-3 py-2">
                  <Checkbox
                    checked={allOnPage ? true : someOnPage ? "indeterminate" : false}
                    onCheckedChange={toggleAll}
                    aria-label="Select every row listed"
                  />
                </th>
              ) : null}

              <SortHeader
                label="Entry"
                attribute="dn"
                sortKey={dnKey}
                sort={sort}
                onSort={toggleSort}
              />
              {columns.map((col) => (
                <SortHeader
                  key={col.attribute}
                  label={col.label}
                  attribute={col.attribute}
                  desc={col.desc}
                  sortKey={col.attribute}
                  sort={sort}
                  onSort={toggleSort}
                />
              ))}
              <th className="w-9 px-2 py-2" />
            </tr>
          </thead>

          <tbody>
            {rows.map((entry) => {
              const isSelected = selected.has(entry.dn);
              return (
                <tr
                  key={entry.dn}
                  className={cn(
                    "border-b border-border/60 transition-colors hover:bg-accent/50",
                    isSelected && "bg-primary/8",
                  )}
                >
                  {selectable ? (
                    <td className="px-3 py-1.5 align-middle">
                      <Checkbox
                        checked={isSelected}
                        onCheckedChange={() => toggleRow(entry.dn)}
                        aria-label={`Select ${entry.dn}`}
                      />
                    </td>
                  ) : null}

                  <td className="max-w-md px-3 py-1.5 align-middle">
                    <button
                      type="button"
                      className="block max-w-full truncate text-left font-dn font-medium hover:underline"
                      title={entry.dn}
                      onClick={() => onOpen(entry.dn)}
                    >
                      {entry.rdn ?? rdnOf(entry.dn)}
                    </button>
                    <span
                      className="block max-w-full truncate font-dn text-xs text-muted-foreground"
                      title={entry.dn}
                    >
                      {entry.dn}
                    </span>
                  </td>

                  {columns.map((col) => (
                    <td
                      key={col.attribute}
                      className="max-w-56 truncate px-3 py-1.5 align-middle"
                      title={cellTitle(entry, col.attribute)}
                    >
                      {cellText(entry, col.attribute)}
                    </td>
                  ))}

                  <td className="px-2 py-1.5 align-middle">
                    <RowMenu
                      dn={entry.dn}
                      readOnly={readOnly}
                      onOpen={onOpen}
                      onEdit={onEdit}
                      onDelete={onDelete}
                      onExport={onExport}
                    />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {sort && truncated ? (
        <p className="border-t border-border px-3 py-2 text-xs text-muted-foreground">
          Sorted over the {entries.length} entries that were returned, not over
          the whole directory — the search stopped early. Narrow the filter to
          sort something complete.
        </p>
      ) : null}
    </div>
  );
}

function SortHeader({
  label,
  attribute,
  desc,
  sortKey,
  sort,
  onSort,
}: {
  label: string;
  attribute: string;
  desc?: string;
  sortKey: string;
  sort: Sort;
  onSort: (key: string) => void;
}) {
  const active = sort?.key === sortKey;
  const Icon = !active ? ChevronsUpDown : sort.dir === "asc" ? ArrowUp : ArrowDown;
  return (
    <th className="px-3 py-1.5 font-medium">
      <button
        type="button"
        className="group flex w-full flex-col items-start gap-0 text-left"
        onClick={() => onSort(sortKey)}
        title={desc ?? undefined}
        aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
      >
        <span className="flex items-center gap-1">
          {label}
          <Icon
            className={cn(
              "size-3",
              active ? "text-foreground" : "text-muted-foreground/40",
            )}
          />
        </span>
        {/*
          The real attribute name, always. A heading that reads "Email" and
          nothing else teaches somebody that their directory has a field called
          Email, and the next thing they do is write that in a filter.
        */}
        <span className="font-dn text-[0.68rem] font-normal leading-tight text-muted-foreground">
          {attribute}
        </span>
      </button>
    </th>
  );
}

function RowMenu({
  dn,
  readOnly,
  onOpen,
  onEdit,
  onDelete,
  onExport,
}: {
  dn: string;
  readOnly?: boolean;
  onOpen: (dn: string) => void;
  onEdit?: (dn: string) => void;
  onDelete?: (dn: string) => void;
  onExport?: (dn: string) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${dn}`}>
          <MoreVertical />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => onOpen(dn)}>
          <Eye />
          Open the entry
        </DropdownMenuItem>
        {onEdit && !readOnly ? (
          <DropdownMenuItem onSelect={() => onEdit(dn)}>
            <Pencil />
            Edit
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuItem onSelect={() => void navigator.clipboard.writeText(dn)}>
          <Copy />
          Copy the DN
        </DropdownMenuItem>
        {onExport ? (
          <DropdownMenuItem onSelect={() => onExport(dn)}>
            <FileDown />
            Export as LDIF
          </DropdownMenuItem>
        ) : null}
        {onDelete && !readOnly ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem destructive onSelect={() => onDelete(dn)}>
              <Trash2 />
              Delete…
            </DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * A cell shows the first value and says how many more there are.
 *
 * Joining them all reads well for two and wraps a row to six lines for a group
 * with two hundred members. The count is the honest summary, and the entry
 * panel holds the rest.
 */
function cellText(entry: SearchResultEntry, attribute: string): React.ReactNode {
  const attr = attributeOf(entry, attribute);
  if (!attr) return <span className="text-muted-foreground/50">—</span>;
  if (attr.withheld) {
    return (
      <Badge variant="outline" className="font-normal">
        withheld
      </Badge>
    );
  }
  const first = attr.values[0];
  if (first === undefined) return <span className="text-muted-foreground/50">—</span>;
  const extra = attr.values.length - 1;
  return (
    <span className="flex items-baseline gap-1.5">
      <span className="truncate">{displayText(first)}</span>
      {extra > 0 ? (
        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
          +{extra}
        </span>
      ) : null}
    </span>
  );
}

function cellTitle(entry: SearchResultEntry, attribute: string): string | undefined {
  const attr = attributeOf(entry, attribute);
  if (!attr || attr.withheld) return undefined;
  return attr.values.map(displayText).join("\n") || undefined;
}

/** Attribute names are case-insensitive, and options are not part of the name. */
function attributeOf(entry: SearchResultEntry, attribute: string) {
  const wanted = attribute.toLowerCase();
  return entry.attributes?.find(
    (a) => (a.name.split(";")[0] ?? a.name).toLowerCase() === wanted,
  );
}

function sortValue(entry: SearchResultEntry, key: string): string {
  if (key === dnKey) return entry.rdn ?? rdnOf(entry.dn);
  const attr = attributeOf(entry, key);
  const first = attr?.values[0];
  return first ? displayText(first) : "";
}

function sortRows(entries: SearchResultEntry[], sort: Sort): SearchResultEntry[] {
  if (!sort) return entries;
  const factor = sort.dir === "asc" ? 1 : -1;
  return [...entries].sort((a, b) => {
    const av = sortValue(a, sort.key);
    const bv = sortValue(b, sort.key);
    // An entry missing the attribute sorts last in both directions, so
    // reversing the order never fills the top of the table with blanks.
    if (av === "" && bv === "") return 0;
    if (av === "") return 1;
    if (bv === "") return -1;
    // numeric so uidNumber 9 comes before 10, sensitivity so cn sorts the way
    // the directory matches it: case-insensitively.
    return (
      factor * av.localeCompare(bv, undefined, { numeric: true, sensitivity: "base" })
    );
  });
}
