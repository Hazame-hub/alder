import { useMemo, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  ChevronsUpDown,
  Copy,
  Eye,
  MoreVertical,
  Pencil,
  Plus,
  Trash2,
} from "lucide-react";
import type { AttributeTypeSummary, ObjectClassSummary } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/**
 * The schema as a table.
 *
 * The list beside the detail pane is the right shape for reading one
 * definition. It is the wrong shape for the question this answers: what does
 * this directory define, where did it come from, and which of it is mine. A
 * thousand attribute types in a column two hundred pixels wide cannot answer
 * that; sortable columns can.
 *
 * The provenance column is deliberately not a "shipped or custom" flag. One of
 * the two servers Alder targets discards X-ORIGIN when it loads a schema file,
 * so on that server such a flag would be a guess — and a column that guesses on
 * one of two supported servers is worse than a column that is absent. What is
 * shown is what the server published: the collection holding the definition
 * where the schema lives in configuration entries, the extension where the
 * server keeps one, and nothing where it published neither.
 */

export type SchemaRow = {
  id: string;
  name: string;
  oid: string;
  desc?: string;
  origin?: string;
  /** The kind badge for a class, or the syntax label for an attribute type. */
  tag?: string;
  extra?: string;
  obsolete?: boolean;
};

type SortKey = "name" | "tag" | "origin";
type Sort = { key: SortKey; dir: "asc" | "desc" } | null;

export function objectClassRows(classes: ObjectClassSummary[]): SchemaRow[] {
  return classes.map((c) => ({
    id: c.name,
    name: c.name,
    oid: c.oid,
    desc: c.desc,
    origin: c.origin,
    tag: c.kind,
    extra: (c.superiors ?? []).join(", "),
    obsolete: c.obsolete,
  }));
}

export function attributeTypeRows(attrs: AttributeTypeSummary[]): SchemaRow[] {
  return attrs.map((a) => ({
    id: a.name,
    name: a.name,
    oid: a.oid,
    desc: a.desc,
    origin: a.origin,
    tag: a.syntaxLabel,
    extra: a.singleValue ? "single-valued" : "",
    obsolete: a.obsolete,
  }));
}

export function SchemaTable({
  rows,
  extraLabel,
  canEdit,
  editableReason,
  onOpen,
  onEdit,
  onDelete,
  onCreate,
}: {
  rows: SchemaRow[];
  /** Heading for the column that is superiors, or single-valuedness. */
  extraLabel: string;
  canEdit: boolean;
  /** Why editing is unavailable, in the server's own words. */
  editableReason?: string;
  onOpen: (id: string) => void;
  onEdit: (id: string) => void;
  onDelete: (id: string) => void;
  onCreate: () => void;
}) {
  const [sort, setSort] = useState<Sort>({ key: "name", dir: "asc" });

  // The column only exists where something published a provenance. On a server
  // that publishes none, an empty column would suggest the information was
  // missing rather than never offered.
  const hasOrigin = useMemo(() => rows.some((r) => r.origin), [rows]);

  const sorted = useMemo(() => {
    if (!sort) return rows;
    const factor = sort.dir === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      const av = (a[sort.key] ?? "").toString();
      const bv = (b[sort.key] ?? "").toString();
      if (av === "" && bv === "") return 0;
      if (av === "") return 1;
      if (bv === "") return -1;
      return factor * av.localeCompare(bv, undefined, { sensitivity: "base" });
    });
  }, [rows, sort]);

  const toggle = (key: SortKey) =>
    setSort((prev) => {
      if (prev?.key !== key) return { key, dir: "asc" };
      if (prev.dir === "asc") return { key, dir: "desc" };
      return null;
    });

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border px-4 py-2 text-sm">
        <span className="font-medium tabular-nums">
          {rows.length} {rows.length === 1 ? "definition" : "definitions"}
        </span>
        {canEdit ? (
          <Button size="sm" variant="outline" onClick={onCreate}>
            <Plus />
            New definition
          </Button>
        ) : editableReason ? (
          <span className="text-xs text-muted-foreground">{editableReason}</span>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        <table className="w-full border-collapse text-sm">
          <thead className="sticky top-0 z-10 bg-background">
            <tr className="border-b border-border text-left">
              <Header label="Name" sortKey="name" sort={sort} onSort={toggle} />
              <Header label="Kind" sortKey="tag" sort={sort} onSort={toggle} />
              <th className="px-3 py-1.5 font-medium">{extraLabel}</th>
              {hasOrigin ? (
                <Header label="From" sortKey="origin" sort={sort} onSort={toggle} />
              ) : null}
              <th className="px-3 py-1.5 font-medium">Description</th>
              <th className="w-9 px-2 py-1.5" />
            </tr>
          </thead>
          <tbody>
            {sorted.map((row) => (
              <tr
                key={row.id}
                className="border-b border-border/60 transition-colors hover:bg-accent/50"
              >
                <td className="max-w-64 px-3 py-1.5 align-middle">
                  <button
                    type="button"
                    className="block max-w-full truncate text-left font-dn font-medium hover:underline"
                    title={row.name}
                    onClick={() => onOpen(row.id)}
                  >
                    {row.name}
                  </button>
                  {/*
                    The OID under the name, for the same reason the entry table
                    puts the attribute name under its heading: the name is the
                    convenience and the OID is the identity.
                  */}
                  <span className="block truncate font-dn text-[0.68rem] text-muted-foreground">
                    {row.oid}
                  </span>
                </td>
                <td className="px-3 py-1.5 align-middle">
                  <span className="flex flex-wrap items-center gap-1">
                    {row.tag ? (
                      <Badge variant="secondary" className="font-normal">
                        {row.tag}
                      </Badge>
                    ) : (
                      <span className="text-muted-foreground/50">—</span>
                    )}
                    {row.obsolete ? <Badge variant="warning">obsolete</Badge> : null}
                  </span>
                </td>
                <td className="max-w-48 truncate px-3 py-1.5 align-middle font-dn text-xs" title={row.extra}>
                  {row.extra || <span className="text-muted-foreground/50">—</span>}
                </td>
                {hasOrigin ? (
                  <td className="max-w-40 truncate px-3 py-1.5 align-middle" title={row.origin}>
                    {row.origin ? (
                      <span className="font-dn text-xs">{row.origin}</span>
                    ) : (
                      <span className="text-muted-foreground/50">—</span>
                    )}
                  </td>
                ) : null}
                <td className="max-w-96 truncate px-3 py-1.5 align-middle" title={row.desc}>
                  {row.desc || <span className="text-muted-foreground/50">—</span>}
                </td>
                <td className="px-2 py-1.5 align-middle">
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${row.name}`}>
                        <MoreVertical />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem onSelect={() => onOpen(row.id)}>
                        <Eye />
                        View the definition
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        onSelect={() => void navigator.clipboard.writeText(row.oid)}
                      >
                        <Copy />
                        Copy the OID
                      </DropdownMenuItem>
                      {canEdit ? (
                        <>
                          <DropdownMenuItem onSelect={() => onEdit(row.id)}>
                            <Pencil />
                            Edit
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem destructive onSelect={() => onDelete(row.id)}>
                            <Trash2 />
                            Remove…
                          </DropdownMenuItem>
                        </>
                      ) : null}
                    </DropdownMenuContent>
                  </DropdownMenu>
                </td>
              </tr>
            ))}
          </tbody>
        </table>

        {rows.length === 0 ? (
          <p className="p-6 text-sm text-muted-foreground">
            Nothing here matches that. The filter searches names, OIDs and
            descriptions.
          </p>
        ) : null}
      </div>
    </div>
  );
}

function Header({
  label,
  sortKey,
  sort,
  onSort,
}: {
  label: string;
  sortKey: SortKey;
  sort: Sort;
  onSort: (k: SortKey) => void;
}) {
  const active = sort?.key === sortKey;
  const Icon = !active ? ChevronsUpDown : sort.dir === "asc" ? ArrowUp : ArrowDown;
  return (
    <th className="px-3 py-1.5 font-medium">
      <button
        type="button"
        className="flex items-center gap-1"
        onClick={() => onSort(sortKey)}
        aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
      >
        {label}
        <Icon
          className={cn("size-3", active ? "text-foreground" : "text-muted-foreground/40")}
        />
      </button>
    </th>
  );
}
