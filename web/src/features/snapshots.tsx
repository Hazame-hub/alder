import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowRight,
  Camera,
  ChevronDown,
  ChevronRight,
  ListChecks,
  Loader2,
  Upload,
} from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { components } from "@/lib/api.gen";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui";
import { ErrorNote } from "@/components/change-dialog";
import { DownloadButton } from "@/components/ldif-block";
import { changeset } from "@/lib/changeset";
import {
  filterItems,
  itemDn,
  nonDestructive,
  selectable,
  selectedChanges,
  type DiffItem,
  type DiffKind,
} from "@/lib/diff-selection";
import { safeText } from "@/lib/display";

type Snapshot = components["schemas"]["Snapshot"];
type Inspection = components["schemas"]["SnapshotInspection"];
type Diff = components["schemas"]["Diff"];
type SnapshotValue = components["schemas"]["SnapshotValue"];

type Slot = {
  name: "A" | "B";
  raw: string;
  doc: Snapshot;
  inspection: Inspection;
  filename: string;
  origin: "captured" | "uploaded";
  /** The compact JSON the server is sent, which is what its request limit counts. */
  bytes: number;
};

type SideChoice = "live" | "A" | "B";

const PAGE = 50;

/** The server's request body limit (`alder serve`). */
const MAX_BODY = 16 << 20;

const mb = (bytes: number) => (bytes / (1 << 20)).toFixed(1);

/**
 * What a snapshot too large to send says about itself, unverified. Only a
 * capture the server has just produced is ever described this way.
 */
function unread(doc: Snapshot): Inspection {
  return {
    version: doc.version,
    kind: doc.kind,
    createdAt: doc.createdAt,
    source: doc.source,
    operationalAttributes: doc.operationalAttributes,
    schemaAvailable: doc.schemaAvailable,
    excluded: doc.excluded,
    entryCount: doc.entryCount,
    checksum: doc.checksum ?? "",
    integrity: "unverified",
  };
}

const KIND_LOOK: Record<DiffKind, { label: string; variant: "success" | "destructive" | "secondary" | "outline" | "warning" }> = {
  added: { label: "Added", variant: "success" },
  removed: { label: "Removed", variant: "destructive" },
  modified: { label: "Modified", variant: "secondary" },
  renamed: { label: "Renamed", variant: "secondary" },
  unchanged: { label: "Unchanged", variant: "outline" },
  unknown: { label: "Unknown", variant: "warning" },
};

/**
 * Snapshots and comparisons.
 *
 * A snapshot is a file the operator keeps; nothing is stored on the server. A
 * comparison says what differs, in a stated direction. When the source is the
 * live directory, a difference can be turned into change requests -- and those
 * are only staged into the changeset, which plans them against the directory
 * as it is and applies them through the same review as every other write.
 */
export function SnapshotsPanel({ onReviewChangeset }: { onReviewChangeset: () => void }) {
  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => unwrap(await api.GET("/session")),
  });
  const defaultBase = session.data?.capabilities?.namingContexts?.[0] ?? "";

  const [slots, setSlots] = useState<Partial<Record<"A" | "B", Slot>>>({});
  const [base, setBase] = useState<string | null>(null);
  const [scope, setScope] = useState<"base" | "one" | "sub">("sub");
  const [filter, setFilter] = useState("");
  const [operational, setOperational] = useState(false);
  const [into, setInto] = useState<"A" | "B">("A");
  const [source, setSource] = useState<SideChoice>("live");
  const [target, setTarget] = useState<SideChoice>("A");
  const fileInput = useRef<HTMLInputElement>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);

  const inspect = async (
    raw: string,
    captured: boolean,
  ): Promise<{ doc: Snapshot; inspection: Inspection; bytes: number }> => {
    let doc: Snapshot;
    try {
      doc = JSON.parse(raw) as Snapshot;
    } catch {
      throw new Error("The file is not JSON, so it is not an Alder snapshot.");
    }
    const bytes = new TextEncoder().encode(JSON.stringify(doc)).length;
    if (bytes > MAX_BODY) {
      if (!captured) {
        throw new Error(
          `The snapshot is ${mb(bytes)} MB, and the server reads at most ${mb(MAX_BODY)} MB in one request, so it cannot be inspected or compared here.`,
        );
      }
      // Keep what was just captured, so it can still be downloaded.
      return { doc, bytes, inspection: unread(doc) };
    }
    const inspection = unwrap(await api.POST("/snapshots/inspect", { body: doc }));
    return { doc, inspection, bytes };
  };

  const capture = useMutation<Slot, ApiFailure>({
    mutationFn: async () => {
      const raw = unwrap(
        await api.POST("/snapshots/capture", {
          body: {
            base: (base ?? defaultBase).trim(),
            scope,
            filter: filter.trim() || undefined,
            operationalAttributes: operational,
          },
          parseAs: "text",
        }),
      ) as unknown as string;
      const { doc, inspection, bytes } = await inspect(raw, true);
      const stamp = inspection.createdAt.replace(/[-:]/g, "").replace(/\.\d+/, "");
      return { name: into, raw, doc, inspection, bytes, origin: "captured", filename: `alder-snapshot-${stamp}.json` };
    },
    onSuccess: (slot) => setSlots((s) => ({ ...s, [slot.name]: slot })),
  });

  const upload = (file: File) => {
    setUploadError(null);
    const reader = new FileReader();
    reader.onload = async () => {
      const raw = String(reader.result ?? "");
      try {
        const { doc, inspection, bytes } = await inspect(raw, false);
        // The name shown is only ever a label; nothing is written anywhere by it.
        const safe = file.name.replace(/[^A-Za-z0-9._-]+/g, "-").slice(0, 80) || "snapshot.json";
        setSlots((s) => ({ ...s, [into]: { name: into, raw, doc, inspection, bytes, origin: "uploaded", filename: safe } }));
      } catch (e) {
        setUploadError(e instanceof Error ? e.message : String(e));
      }
    };
    reader.readAsText(file);
  };

  const sideBody = (choice: SideChoice) =>
    choice === "live" ? { live: {} } : { snapshot: slots[choice]?.doc as Snapshot };
  const requestBytes = (["A", "B"] as const)
    .filter((n) => n === source || n === target)
    .reduce((sum, n) => sum + (slots[n]?.bytes ?? 0), 0);
  const compareProblem =
    source === target
      ? "Choose two different states."
      : source !== "live" && !slots[source]
        ? `Snapshot ${source} is empty.`
        : target !== "live" && !slots[target]
          ? `Snapshot ${target} is empty.`
          : requestBytes > MAX_BODY
            ? `Together that is ${mb(requestBytes)} MB to send, and the server reads at most ${mb(MAX_BODY)} MB in one request.`
            : null;

  const compare = useMutation<Diff, ApiFailure>({
    mutationFn: async () =>
      unwrap(await api.POST("/diff", { body: { source: sideBody(source), target: sideBody(target) } })),
  });

  const sideLabel = (choice: SideChoice) => (choice === "live" ? "The directory now" : `Snapshot ${choice}`);

  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold">Snapshots</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Capture a subtree as a versioned snapshot you keep, compare it with another snapshot or with
          the directory as it is now, and stage selected differences as changes. Nothing is stored on the
          server, and nothing is applied from here.
        </p>
      </header>

      <section className="space-y-3 rounded-lg border p-4">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="font-medium">Load into</span>
          {(["A", "B"] as const).map((n) => (
            <Button key={n} size="sm" variant={into === n ? "default" : "outline"} onClick={() => setInto(n)}>
              Snapshot {n}
            </Button>
          ))}
        </div>
        <div className="grid gap-2 sm:grid-cols-[1fr_auto_1fr]">
          <Input
            value={base ?? defaultBase}
            onChange={(e) => setBase(e.target.value)}
            placeholder="Base DN"
            className="font-dn"
            aria-label="Base DN"
          />
          <select
            value={scope}
            onChange={(e) => setScope(e.target.value as "base" | "one" | "sub")}
            className="h-9 rounded-md border border-input bg-transparent px-2 text-sm"
            aria-label="Scope"
          >
            <option value="sub">Whole subtree</option>
            <option value="one">One level</option>
            <option value="base">Base entry</option>
          </select>
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter, default (objectClass=*)"
            className="font-mono text-xs"
            aria-label="Filter"
          />
        </div>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={operational} onCheckedChange={(v) => setOperational(v === true)} />
          Include operational attributes
          <span className="text-xs text-muted-foreground">
            (they change on every write, so they are left out by default)
          </span>
        </label>
        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={() => capture.mutate()} disabled={capture.isPending || !(base ?? defaultBase).trim()}>
            {capture.isPending ? <Loader2 className="animate-spin" /> : <Camera />}
            Capture into snapshot {into}
          </Button>
          <Button variant="outline" onClick={() => fileInput.current?.click()}>
            <Upload />
            Upload into snapshot {into}
          </Button>
          <input
            ref={fileInput}
            type="file"
            accept=".json,application/json"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) upload(file);
              e.target.value = "";
            }}
          />
        </div>
        {capture.isError ? <ErrorNote title="The snapshot could not be captured" error={capture.error} /> : null}
        {uploadError ? (
          <p className="rounded-md border border-destructive/40 bg-destructive/8 p-3 text-sm text-destructive">{uploadError}</p>
        ) : null}

        <div className="grid gap-3 sm:grid-cols-2">
          {(["A", "B"] as const).map((n) => (
            <SlotCard key={n} name={n} slot={slots[n]} />
          ))}
        </div>
      </section>

      <section className="space-y-3 rounded-lg border p-4">
        <h3 className="font-medium">Compare</h3>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <SideSelect label="Source" value={source} onChange={setSource} slots={slots} />
          <ArrowRight className="size-4 text-muted-foreground" />
          <SideSelect label="Target" value={target} onChange={setTarget} slots={slots} />
          <Button onClick={() => compare.mutate()} disabled={compare.isPending || compareProblem !== null}>
            {compare.isPending ? <Loader2 className="animate-spin" /> : null}
            Compare
          </Button>
          {compareProblem ? <span className="text-xs text-muted-foreground">{compareProblem}</span> : null}
        </div>
        <p className="text-xs text-muted-foreground">
          <strong>Added</strong> means in the target and not in the source. Changes can be proposed only when
          the source is the directory now: they move it toward the target.
        </p>
        {compare.isError ? <ErrorNote title="The comparison failed" error={compare.error} /> : null}
        {compare.data ? (
          <DiffView
            diff={compare.data}
            sourceLabel={sideLabel(source)}
            targetLabel={sideLabel(target)}
            onReviewChangeset={onReviewChangeset}
          />
        ) : null}
      </section>
    </div>
  );
}

function SlotCard({ name, slot }: { name: "A" | "B"; slot?: Slot }) {
  if (!slot) {
    return (
      <div className="rounded-md border border-dashed p-3 text-sm text-muted-foreground">Snapshot {name} is empty.</div>
    );
  }
  const i = slot.inspection;
  return (
    <div className="space-y-1 rounded-md border p-3 text-sm">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium">Snapshot {name}</span>
        <DownloadButton text={slot.raw} filename={slot.filename} label="Download" mime="application/json" />
      </div>
      <div className="truncate font-dn text-xs" title={safeText(i.source.base)}>
        {i.source.scope} of {safeText(i.source.base)}
      </div>
      <div className="text-xs text-muted-foreground">
        {i.entryCount} entries · {i.source.vendor ?? "server not identified"} · {slot.origin} {i.createdAt}
      </div>
      <div className="flex flex-wrap gap-1">
        <Badge variant={i.integrity === "verified" ? "success" : "warning"}>checksum {i.integrity}</Badge>
        {i.operationalAttributes ? <Badge variant="outline">operational attributes</Badge> : null}
        {!i.schemaAvailable ? <Badge variant="warning">no schema</Badge> : null}
        {slot.bytes > MAX_BODY ? <Badge variant="warning">too large to compare here</Badge> : null}
      </div>
    </div>
  );
}

function SideSelect({
  label,
  value,
  onChange,
  slots,
}: {
  label: string;
  value: SideChoice;
  onChange: (v: SideChoice) => void;
  slots: Partial<Record<"A" | "B", Slot>>;
}) {
  return (
    <label className="flex items-center gap-1.5">
      <span className="text-muted-foreground">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value as SideChoice)}
        className="h-9 rounded-md border border-input bg-transparent px-2 text-sm"
      >
        <option value="live">The directory now</option>
        <option value="A">Snapshot A{slots.A ? "" : " (empty)"}</option>
        <option value="B">Snapshot B{slots.B ? "" : " (empty)"}</option>
      </select>
    </label>
  );
}

function valueText(v: SnapshotValue): string {
  if (v.text !== undefined) return v.text;
  const bytes = Math.floor(((v.base64 ?? "").length * 3) / 4);
  return `«${bytes} bytes of binary data»`;
}

function DiffView({
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
  const [kinds, setKinds] = useState<Set<DiffKind>>(new Set());
  const [dn, setDn] = useState("");
  const [attribute, setAttribute] = useState("");
  const [page, setPage] = useState(0);
  const [open, setOpen] = useState<Set<number>>(new Set());
  const [chosen, setChosen] = useState<Set<number>>(new Set());
  const [deletions, setDeletions] = useState<Set<number>>(new Set());
  const [staged, setStaged] = useState<number | null>(null);

  const visible = useMemo(() => filterItems(diff.items, { kinds, dn, attribute }), [diff.items, kinds, dn, attribute]);
  const pages = Math.max(1, Math.ceil(visible.length / PAGE));
  const current = Math.min(page, pages - 1);
  const shown = visible.slice(current * PAGE, current * PAGE + PAGE);
  const changes = selectedChanges(diff.items, chosen, deletions);
  const c = diff.counts;

  const toggle = (set: Set<number>, index: number, on: boolean) => {
    const next = new Set(set);
    if (on) next.add(index);
    else next.delete(index);
    return next;
  };

  return (
    <div className="space-y-3">
      <div className="text-sm">
        <span className="font-medium">{sourceLabel}</span> <ArrowRight className="inline size-3.5" />{" "}
        <span className="font-medium">{targetLabel}</span>
        <span className="text-muted-foreground"> · {c.compared} entries compared</span>
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        {(["added", "modified", "removed", "renamed", "unknown"] as DiffKind[]).map((k) => {
          const n = c[k];
          const active = kinds.has(k);
          return (
            <button
              key={k}
              type="button"
              aria-pressed={active}
              onClick={() => {
                setKinds((s) => {
                  const next = new Set(s);
                  if (next.has(k)) next.delete(k);
                  else next.add(k);
                  return next;
                });
                setPage(0);
              }}
              className={`rounded-md border px-2 py-1 text-xs ${active ? "border-primary bg-primary/10" : ""}`}
            >
              {n} {KIND_LOOK[k].label.toLowerCase()}
            </button>
          );
        })}
        <span className="text-xs text-muted-foreground">{c.unchanged} unchanged</span>
      </div>

      {!diff.complete ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
          <div className="mb-1 flex items-center gap-1.5 font-medium">
            <AlertTriangle className="size-4" />
            This comparison is incomplete
          </div>
          <ul className="ml-5 list-disc space-y-0.5">
            {(diff.reasons ?? []).map((r) => (
              <li key={r.code}>
                <code className="font-mono text-xs">{r.code}</code> — {r.detail}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-xs">What could not be seen is shown as unknown, and no deletion is offered.</p>
        </div>
      ) : null}
      {diff.crossVendor ? (
        <p className="text-xs text-muted-foreground">
          The two sides come from different server products, or one did not identify itself. Server-specific
          attributes can differ for that reason alone.
        </p>
      ) : null}
      {diff.comparedByBytes?.length || diff.ruleDifferences?.length ? (
        <p className="text-xs text-muted-foreground">
          Compared byte for byte, with no shared equality rule:{" "}
          <span className="font-mono">{[...(diff.comparedByBytes ?? []), ...(diff.ruleDifferences ?? [])].join(", ")}</span>
        </p>
      ) : null}

      <div className="grid gap-2 sm:grid-cols-2">
        <Input
          value={dn}
          onChange={(e) => {
            setDn(e.target.value);
            setPage(0);
          }}
          placeholder="DN contains"
          className="font-dn"
          aria-label="Filter by DN"
        />
        <Input
          value={attribute}
          onChange={(e) => {
            setAttribute(e.target.value);
            setPage(0);
          }}
          placeholder="Attribute name contains"
          className="font-mono text-xs"
          aria-label="Filter by attribute"
        />
      </div>

      <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 px-3 py-2 text-sm">
        <span>
          {changes.length} change{changes.length === 1 ? "" : "s"} selected
        </span>
        <Button
          size="sm"
          variant="outline"
          onClick={() => setChosen(new Set([...chosen, ...nonDestructive(diff.items, visible)]))}
        >
          Select every non-destructive change shown
        </Button>
        <Button size="sm" variant="ghost" onClick={() => (setChosen(new Set()), setDeletions(new Set()))}>
          Clear
        </Button>
        <Button
          size="sm"
          className="ml-auto"
          disabled={changes.length === 0}
          onClick={() => {
            diff.items.forEach((item, index) => {
              if (!selectable(item)) return;
              const included = item.candidate?.destructive ? deletions.has(index) : chosen.has(index);
              if (!included) return;
              for (const change of item.candidate?.changes ?? []) {
                changeset.add(change, `${KIND_LOOK[item.kind].label.toLowerCase()} ${itemDn(item)}`);
              }
            });
            setStaged(changes.length);
            setChosen(new Set());
            setDeletions(new Set());
          }}
        >
          <ListChecks />
          Stage into changeset
        </Button>
      </div>
      {staged !== null ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm">
          {staged} change{staged === 1 ? "" : "s"} staged. They are planned against the directory, and reviewed,
          before anything is applied.
          <Button size="sm" variant="outline" onClick={onReviewChangeset}>
            Review the changeset
          </Button>
        </div>
      ) : null}

      <ol className="divide-y rounded-md border">
        {shown.length === 0 ? (
          <li className="p-3 text-sm text-muted-foreground">No differences match.</li>
        ) : null}
        {shown.map((index) => {
          const item = diff.items[index] as DiffItem;
          const candidate = item.candidate;
          const expanded = open.has(index);
          const destructive = candidate?.destructive === true;
          return (
            <li key={index} className="px-3 py-2 text-sm">
              <div className="flex min-w-0 items-center gap-2">
                {selectable(item) && !destructive ? (
                  <Checkbox
                    checked={chosen.has(index)}
                    onCheckedChange={(v) => setChosen((s) => toggle(s, index, v === true))}
                    aria-label={`Select the change to ${itemDn(item)}`}
                  />
                ) : null}
                <button
                  type="button"
                  onClick={() => setOpen((s) => toggle(s, index, !s.has(index)))}
                  aria-expanded={expanded}
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                >
                  {expanded ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
                  <Badge variant={KIND_LOOK[item.kind].variant} className="shrink-0">
                    {KIND_LOOK[item.kind].label}
                  </Badge>
                  <span className="truncate font-dn" title={safeText(itemDn(item))}>
                    {safeText(item.kind === "renamed" ? `${item.sourceDn} → ${item.targetDn}` : itemDn(item))}
                  </span>
                </button>
              </div>

              {destructive ? (
                selectable(item) ? (
                  <label className="mt-1 ml-6 flex items-center gap-2 text-xs text-destructive">
                    <Checkbox
                      checked={deletions.has(index)}
                      onCheckedChange={(v) => setDeletions((s) => toggle(s, index, v === true))}
                    />
                    Include deleting this entry in the plan
                  </label>
                ) : (
                  <p className="mt-1 ml-6 text-xs text-muted-foreground">
                    Deletion not offered: <code className="font-mono">{candidate?.blocked}</code>
                  </p>
                )
              ) : candidate?.blocked ? (
                <p className="mt-1 ml-6 text-xs text-muted-foreground">
                  No change offered: <code className="font-mono">{candidate.blocked}</code>
                </p>
              ) : null}
              {item.reason ? (
                <p className="mt-1 ml-6 text-xs text-warning-tint-foreground">
                  Unknown: <code className="font-mono">{item.reason}</code>
                </p>
              ) : null}

              {expanded ? (
                <div className="mt-2 ml-6 space-y-2">
                  {item.kind === "added" ? (
                    <p className="text-xs text-muted-foreground">In the target and not in the source.</p>
                  ) : null}
                  {item.kind === "removed" ? (
                    <p className="text-xs text-muted-foreground">In the source and not in the target.</p>
                  ) : null}
                  {(item.attributes ?? []).map((a) => (
                    <div key={a.name} className="text-xs">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="font-mono font-medium">{safeText(a.name)}</span>
                        <span className="text-muted-foreground">{a.kind}</span>
                        {a.operational ? <Badge variant="outline">operational</Badge> : null}
                        {a.comparedByBytes ? <Badge variant="outline">by bytes</Badge> : null}
                        {a.unknownReason ? <Badge variant="warning">{a.unknownReason}</Badge> : null}
                      </div>
                      {a.sensitive ? (
                        <div className="text-muted-foreground">
                          values withheld: {a.withheldSource ?? 0} → {a.withheldTarget ?? 0}
                        </div>
                      ) : null}
                      <div className="mt-0.5 space-y-0.5 font-dn [overflow-wrap:anywhere]">
                        {(a.removed ?? []).map((v, i) => (
                          <div key={`r${i}`} className="text-destructive">
                            − {safeText(valueText(v))}
                          </div>
                        ))}
                        {a.removedOmitted ? (
                          <div className="text-muted-foreground">… and {a.removedOmitted} more removed</div>
                        ) : null}
                        {(a.added ?? []).map((v, i) => (
                          <div key={`a${i}`} className="text-emerald-700 dark:text-emerald-400">
                            + {safeText(valueText(v))}
                          </div>
                        ))}
                        {a.addedOmitted ? (
                          <div className="text-muted-foreground">… and {a.addedOmitted} more added</div>
                        ) : null}
                      </div>
                    </div>
                  ))}
                </div>
              ) : null}
            </li>
          );
        })}
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
