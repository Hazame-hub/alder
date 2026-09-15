import { useMemo, useState } from "react";
import { AlertTriangle, ArrowRight, ChevronDown, ChevronRight, ListChecks } from "lucide-react";
import type { components } from "@/lib/api.gen";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui";
import { changeset } from "@/lib/changeset";
import { safeText } from "@/lib/display";
import {
  filterSchemaItems,
  isChosen,
  missingDependencies,
  nonDestructiveKeys,
  orderedSchemaChanges,
  schemaItemLabel,
  schemaSelectable,
  type Actionable,
  type DiffKind,
  type SchemaDiffItem,
  type SchemaElementKind,
} from "@/lib/schema-diff";

type Diff = components["schemas"]["Diff"];
type Reference = components["schemas"]["SchemaReference"];

const PAGE = 50;

export const SCHEMA_KIND_LOOK: Record<
  DiffKind,
  {
    label: string;
    variant: "success" | "destructive" | "secondary" | "outline" | "warning";
  }
> = {
  added: { label: "Added", variant: "success" },
  removed: { label: "Removed", variant: "destructive" },
  modified: { label: "Modified", variant: "secondary" },
  renamed: { label: "Renamed", variant: "secondary" },
  metadata_only: { label: "Metadata only", variant: "outline" },
  unchanged: { label: "Unchanged", variant: "outline" },
  unknown: { label: "Unknown", variant: "warning" },
};

const ELEMENT_LABEL: Record<SchemaElementKind, string> = {
  attributeType: "attribute type",
  objectClass: "object class",
};

const FILTER_KINDS: DiffKind[] = ["added", "modified", "removed", "metadata_only", "unknown", "unchanged"];

function referenceText(r: Reference): string {
  const name = safeText(r.name ?? r.oid);
  return r.oid === ""
    ? `${r.relation} ${ELEMENT_LABEL[r.element]} ${name} (not defined on that side)`
    : `${r.relation} ${ELEMENT_LABEL[r.element]} ${name}`;
}

/**
 * A schema comparison: definitions told apart by OID and compared by meaning.
 *
 * Every string here came from a directory or a file someone sent, so each one
 * goes through safeText -- for display only; what is staged is the change
 * request exactly as Alder derived it.
 */
export function SchemaDiffView({
  diff,
  sourceLabel,
  targetLabel,
  onReviewChangeset,
}: {
  diff: Diff;
  sourceLabel: string;
  targetLabel: string;
  onReviewChangeset: () => void;
}) {
  const schema = diff.schema;
  const [text, setText] = useState("");
  const [elements, setElements] = useState<Set<SchemaElementKind>>(new Set());
  const [kinds, setKinds] = useState<Set<DiffKind>>(new Set());
  const [actionable, setActionable] = useState<Actionable>("all");
  const [page, setPage] = useState(0);
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [deletions, setDeletions] = useState<Set<string>>(new Set());
  const [staged, setStaged] = useState<number | null>(null);

  const items = useMemo(() => schema?.items ?? [], [schema]);
  const visible = useMemo(
    () => filterSchemaItems(items, { text, elements, kinds, actionable }),
    [items, text, elements, kinds, actionable],
  );
  if (!schema) return null;

  const pages = Math.max(1, Math.ceil(visible.length / PAGE));
  const current = Math.min(page, pages - 1);
  const shown = visible.slice(current * PAGE, current * PAGE + PAGE);
  const { ordered, unplaced } = orderedSchemaChanges(schema, chosen, deletions);
  const missing = missingDependencies(items, chosen, deletions);
  const changeCount = ordered.reduce((n, o) => n + o.changes.length, 0);
  const byKey = new Map(items.map((i) => [i.key, i]));
  const nameOf = (key: string) => {
    const item = byKey.get(key);
    return item ? `${ELEMENT_LABEL[item.element]} ${safeText(schemaItemLabel(item))}` : safeText(key);
  };

  const toggle = <T,>(set: Set<T>, value: T, on: boolean) => {
    const next = new Set(set);
    if (on) next.add(value);
    else next.delete(value);
    return next;
  };
  const refilter = <T,>(setter: (f: (s: Set<T>) => Set<T>) => void, value: T) => {
    setter((s) => toggle(s, value, !s.has(value)));
    setPage(0);
  };

  return (
    <div className="space-y-3">
      <div className="text-sm">
        <span className="font-medium">{sourceLabel}</span> <ArrowRight className="inline size-3.5" />{" "}
        <span className="font-medium">{targetLabel}</span>
        <span className="text-muted-foreground"> · schema, compared by OID</span>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead className="text-muted-foreground">
            <tr>
              <th className="py-1 pr-3 text-left font-normal"> </th>
              {["compared", "added", "modified", "removed", "metadata only", "unchanged", "unknown"].map((h) => (
                <th key={h} className="px-2 py-1 text-right font-normal">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {(
              [
                ["Attribute types", schema.attributeTypes],
                ["Object classes", schema.objectClasses],
              ] as const
            ).map(([label, c]) => (
              <tr key={label} className="border-t">
                <td className="py-1 pr-3 font-medium">{label}</td>
                {[c.compared, c.added, c.modified, c.removed, c.metadataOnly, c.unchanged, c.unknown].map((n, i) => (
                  <td key={i} className="px-2 py-1 text-right tabular-nums">
                    {n}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {!diff.complete ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
          <div className="mb-1 flex items-center gap-1.5 font-medium">
            <AlertTriangle className="size-4" />
            This comparison is incomplete
          </div>
          <ul className="ml-5 list-disc space-y-0.5">
            {(diff.reasons ?? []).map((r, i) => (
              <li key={`${r.code}${i}`}>
                <code className="font-mono text-xs">{r.code}</code> — {safeText(r.detail)}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-xs">
            A definition could not be parsed. It is shown as unknown, and no removal is offered.
          </p>
        </div>
      ) : null}
      {diff.crossVendor ? (
        <p className="text-xs text-muted-foreground">
          The two sides come from different server products, or one did not identify itself. Server-defined schema and
          X- extensions can differ for that reason alone.
        </p>
      ) : null}
      <p className="text-xs text-muted-foreground">
        A difference only in X- extensions is <em>metadata only</em>: shown, and never proposed as a change.
      </p>

      <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
        <Input
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setPage(0);
          }}
          placeholder="NAME or OID contains"
          className="font-mono text-xs"
          aria-label="Filter by NAME or OID"
        />
        <select
          value={actionable}
          onChange={(e) => {
            setActionable(e.target.value as Actionable);
            setPage(0);
          }}
          className="h-9 rounded-md border border-input bg-transparent px-2 text-sm"
          aria-label="Filter by whether a change is offered"
        >
          <option value="all">Actionable or not</option>
          <option value="actionable">A change is offered</option>
          <option value="not_actionable">No change is offered</option>
        </select>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        {(["attributeType", "objectClass"] as const).map((el) => (
          <button
            key={el}
            type="button"
            aria-pressed={elements.has(el)}
            onClick={() => refilter(setElements, el)}
            className={`rounded-md border px-2 py-1 text-xs ${elements.has(el) ? "border-primary bg-primary/10" : ""}`}
          >
            {el === "objectClass" ? "object classes" : "attribute types"}
          </button>
        ))}
        <span className="mx-1 h-4 w-px bg-border" />
        {FILTER_KINDS.map((k) => (
          <button
            key={k}
            type="button"
            aria-pressed={kinds.has(k)}
            onClick={() => refilter(setKinds, k)}
            className={`rounded-md border px-2 py-1 text-xs ${kinds.has(k) ? "border-primary bg-primary/10" : ""}`}
          >
            {SCHEMA_KIND_LOOK[k].label.toLowerCase()}
          </button>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 px-3 py-2 text-sm">
        <span>
          {changeCount} change{changeCount === 1 ? "" : "s"} selected
        </span>
        <Button
          size="sm"
          variant="outline"
          onClick={() => setChosen(new Set([...chosen, ...nonDestructiveKeys(items, visible)]))}
        >
          Select every non-destructive change shown
        </Button>
        <Button size="sm" variant="ghost" onClick={() => (setChosen(new Set()), setDeletions(new Set()))}>
          Clear
        </Button>
        <Button
          size="sm"
          className="ml-auto"
          disabled={changeCount === 0 || missing.length > 0 || unplaced.length > 0}
          onClick={() => {
            for (const { item, changes } of ordered) {
              const label = `${SCHEMA_KIND_LOOK[item.kind].label.toLowerCase()} ${ELEMENT_LABEL[item.element]} ${safeText(schemaItemLabel(item))}`;
              for (const c of changes) changeset.add(c, label);
            }
            setStaged(changeCount);
            setChosen(new Set());
            setDeletions(new Set());
          }}
        >
          <ListChecks />
          Stage into changeset
        </Button>
      </div>
      {missing.length > 0 ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
          <div className="mb-1 font-medium">
            <code className="font-mono text-xs">dependency_required</code> — select these too, or leave them out
          </div>
          <ul className="ml-5 list-disc space-y-0.5 text-xs">
            {missing.map((m) => (
              <li key={`${m.key}>${m.needs}`}>
                {nameOf(m.key)} needs {nameOf(m.needs)}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {unplaced.length > 0 ? (
        <p className="text-sm text-destructive">
          The comparison gave no order for {unplaced.map(nameOf).join(", ")}, so nothing can be staged.
        </p>
      ) : null}
      {staged !== null ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm">
          {staged} change{staged === 1 ? "" : "s"} staged in dependency order. They are planned against the directory,
          and reviewed, before anything is applied.
          <Button size="sm" variant="outline" onClick={onReviewChangeset}>
            Review the changeset
          </Button>
        </div>
      ) : null}

      <ol className="divide-y rounded-md border">
        {shown.length === 0 ? <li className="p-3 text-sm text-muted-foreground">No differences match.</li> : null}
        {shown.map((index) => (
          <SchemaItemRow
            key={(items[index] as SchemaDiffItem).key}
            item={items[index] as SchemaDiffItem}
            expanded={open.has((items[index] as SchemaDiffItem).key)}
            onExpand={(key) => setOpen((s) => toggle(s, key, !s.has(key)))}
            chosen={chosen.has((items[index] as SchemaDiffItem).key)}
            onChoose={(key, on) => setChosen((s) => toggle(s, key, on))}
            deleting={deletions.has((items[index] as SchemaDiffItem).key)}
            onDelete={(key, on) => setDeletions((s) => toggle(s, key, on))}
            selectedAll={(key) => {
              const item = byKey.get(key);
              return item !== undefined && isChosen(item, chosen, deletions);
            }}
            nameOf={nameOf}
          />
        ))}
      </ol>

      {pages > 1 ? (
        <div className="flex items-center justify-between text-sm">
          <Button size="sm" variant="outline" disabled={current === 0} onClick={() => setPage(current - 1)}>
            Previous
          </Button>
          <span className="text-muted-foreground">
            Page {current + 1} of {pages} · {visible.length} differences
          </span>
          <Button size="sm" variant="outline" disabled={current >= pages - 1} onClick={() => setPage(current + 1)}>
            Next
          </Button>
        </div>
      ) : null}
    </div>
  );
}

function SchemaItemRow({
  item,
  expanded,
  onExpand,
  chosen,
  onChoose,
  deleting,
  onDelete,
  selectedAll,
  nameOf,
}: {
  item: SchemaDiffItem;
  expanded: boolean;
  onExpand: (key: string) => void;
  chosen: boolean;
  onChoose: (key: string, on: boolean) => void;
  deleting: boolean;
  onDelete: (key: string, on: boolean) => void;
  selectedAll: (key: string) => boolean;
  nameOf: (key: string) => string;
}) {
  const candidate = item.candidate;
  const destructive = candidate?.destructive === true;
  const selectable = schemaSelectable(item);
  const label = safeText(schemaItemLabel(item));
  const needs = (candidate?.requires ?? []).filter((k) => (chosen || deleting) && !selectedAll(k));
  return (
    <li className="px-3 py-2 text-sm">
      <div className="flex min-w-0 items-center gap-2">
        {selectable && !destructive ? (
          <Checkbox
            checked={chosen}
            onCheckedChange={(v) => onChoose(item.key, v === true)}
            aria-label={`Select the change to ${ELEMENT_LABEL[item.element]} ${label}`}
          />
        ) : null}
        <button
          type="button"
          onClick={() => onExpand(item.key)}
          aria-expanded={expanded}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
        >
          {expanded ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
          <Badge variant={SCHEMA_KIND_LOOK[item.kind].variant} className="shrink-0">
            {SCHEMA_KIND_LOOK[item.kind].label}
          </Badge>
          <span className="shrink-0 text-xs text-muted-foreground">{ELEMENT_LABEL[item.element]}</span>
          <span className="truncate font-mono text-xs" title={`${label} ${safeText(item.oid)}`}>
            {label}
            {item.names?.length ? <span className="text-muted-foreground"> · {safeText(item.oid)}</span> : null}
          </span>
        </button>
      </div>

      {destructive ? (
        selectable ? (
          <label className="mt-1 ml-6 flex items-center gap-2 text-xs text-destructive">
            <Checkbox checked={deleting} onCheckedChange={(v) => onDelete(item.key, v === true)} />
            Include removing this definition in the plan
          </label>
        ) : (
          <p className="mt-1 ml-6 text-xs text-muted-foreground">
            Removal not offered: <code className="font-mono">{candidate?.blocked}</code>
          </p>
        )
      ) : candidate?.blocked ? (
        <p className="mt-1 ml-6 text-xs text-muted-foreground">
          No change offered: <code className="font-mono">{candidate.blocked}</code>
        </p>
      ) : null}
      {destructive && candidate?.impact?.length ? (
        <p className="mt-1 ml-6 text-xs text-warning-tint-foreground">
          Impact:{" "}
          {candidate.impact.map((p) => (
            <code key={p} className="mr-1 font-mono">
              {p}
            </code>
          ))}
        </p>
      ) : null}
      {item.problems?.length ? (
        <div className="mt-1 ml-6 flex flex-wrap gap-1">
          {item.problems.map((p) => (
            <Badge key={p} variant="warning">
              {p}
            </Badge>
          ))}
        </div>
      ) : null}
      {needs.length > 0 ? (
        <p className="mt-1 ml-6 text-xs text-warning-tint-foreground">
          Needs {needs.map(nameOf).join(", ")} selected too.
        </p>
      ) : null}

      {expanded ? (
        <div className="mt-2 ml-6 space-y-2 text-xs">
          <div className="font-mono text-muted-foreground">
            {safeText(item.oid)}
            {item.names?.length ? ` · NAME ${item.names.map((n) => safeText(n)).join(", ")}` : ""}
          </div>
          {item.kind === "added" ? (
            <p className="text-muted-foreground">Defined in the target and not in the source.</p>
          ) : null}
          {item.kind === "removed" ? (
            <p className="text-muted-foreground">Defined in the source and not in the target.</p>
          ) : null}
          {(item.fields ?? []).map((f) => (
            <div key={f.field}>
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="font-mono font-medium">{safeText(f.field)}</span>
                <Badge variant={f.category === "core" ? "secondary" : "outline"}>{f.category}</Badge>
              </div>
              <div className="mt-0.5 space-y-0.5 font-mono [overflow-wrap:anywhere]">
                {(f.source ?? []).map((v, i) => (
                  <div key={`s${i}`} className="text-destructive">
                    − {safeText(v)}
                  </div>
                ))}
                {(f.target ?? []).map((v, i) => (
                  <div key={`t${i}`} className="text-emerald-700 dark:text-emerald-400">
                    + {safeText(v)}
                  </div>
                ))}
              </div>
            </div>
          ))}
          {item.requires?.length ? (
            <div>
              <div className="font-medium">Refers to</div>
              <ul className="ml-4 list-disc">
                {item.requires.map((r, i) => (
                  <li key={i}>{referenceText(r)}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {item.requiredBy?.length ? (
            <div>
              <div className="font-medium">Referred to by, in the source</div>
              <ul className="ml-4 list-disc">
                {item.requiredBy.map((r, i) => (
                  <li key={i}>{referenceText(r)}</li>
                ))}
              </ul>
            </div>
          ) : null}
          {item.sourceCollection || item.targetCollection ? (
            <div className="text-muted-foreground">
              Held in {safeText(item.sourceCollection ?? item.targetCollection)}
            </div>
          ) : null}
          {item.sourceDefinition || item.targetDefinition ? (
            <details>
              <summary className="cursor-pointer text-muted-foreground">Definition text</summary>
              {item.sourceDefinition ? (
                <pre className="mt-1 whitespace-pre-wrap rounded bg-muted/40 p-2 font-mono [overflow-wrap:anywhere]">
                  source: {safeText(item.sourceDefinition)}
                </pre>
              ) : null}
              {item.targetDefinition ? (
                <pre className="mt-1 whitespace-pre-wrap rounded bg-muted/40 p-2 font-mono [overflow-wrap:anywhere]">
                  target: {safeText(item.targetDefinition)}
                </pre>
              ) : null}
            </details>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}
