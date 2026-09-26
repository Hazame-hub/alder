import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowLeftRight,
  ArrowRight,
  Camera,
  ChevronDown,
  ChevronRight,
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
import { ReviewActions } from "@/components/review-actions";
import { bench, useBench, type AnySnapshot, type SideChoice, type Slot } from "@/lib/snapshot-bench";
import {
  filterItems,
  itemDn,
  nonDestructive,
  selectable,
  selectedChangeItems,
  selectedChanges,
  type DiffItem,
  type DiffKind,
} from "@/lib/diff-selection";
import { safeText } from "@/lib/display";
import { formatInstant } from "@/lib/values";
import { SCHEMA_KIND_LOOK, SchemaDiffView } from "@/features/schema-diff-view";
import { ConfigDiffView } from "@/features/config-diff-view";
import { SIGNATURE_LOOK, signerLine, worthShowing, type DocumentSignature } from "@/lib/signature";

type Inspection = components["schemas"]["SnapshotInspection"];
type Diff = components["schemas"]["Diff"];
type SnapshotValue = components["schemas"]["SnapshotValue"];

const PAGE = 50;

/** The server's request body limit (`alder serve`). */
const MAX_BODY = 16 << 20;

const mb = (bytes: number) => (bytes / (1 << 20)).toFixed(1);

/**
 * What a snapshot too large to send says about itself, unverified. Only a
 * capture the server has just produced is ever described this way.
 */
function unread(doc: AnySnapshot): Inspection {
  if (doc.kind === "config") {
    return {
      version: doc.version,
      kind: doc.kind,
      createdAt: doc.createdAt,
      source: {
        base: doc.source.root,
        scope: "sub",
        filter: "(objectClass=*)",
        vendor: doc.source.vendor,
        vendorVersion: doc.source.vendorVersion,
      },
      operationalAttributes: false,
      schemaAvailable: false,
      excluded: [],
      entryCount: doc.counts.settings,
      checksum: doc.checksum ?? "",
      integrity: "unverified",
    };
  }
  if (doc.kind === "schema") {
    return {
      version: doc.version,
      kind: doc.kind,
      createdAt: doc.createdAt,
      source: {
        base: doc.source.subschemaEntry,
        scope: "base",
        filter: "(objectClass=subschema)",
        vendor: doc.source.vendor,
        vendorVersion: doc.source.vendorVersion,
      },
      operationalAttributes: true,
      schemaAvailable: true,
      excluded: [],
      entryCount: 1,
      checksum: doc.checksum ?? "",
      integrity: "unverified",
      schema: {
        subschemaEntry: doc.source.subschemaEntry,
        completeness: doc.completeness,
        counts: doc.counts,
        collections: doc.source.collections,
      },
    };
  }
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

const KIND_LOOK = SCHEMA_KIND_LOOK;

/**
 * Snapshots and comparisons.
 *
 * A snapshot is a file the operator keeps; nothing is stored on the server. A
 * comparison says what differs, in a stated direction. When the source is the
 * live directory, a difference can be turned into change requests -- and those
 * are only staged into the changeset, which plans them against the directory
 * as it is and applies them through the same review as every other write.
 */
export function SnapshotsPanel({
  onReviewChangeset,
  openWith,
}: {
  onReviewChangeset: () => void;
  /** Which kind to open on, when the link that led here asked for one. */
  openWith?: "data" | "schema" | "config";
}) {
  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => unwrap(await api.GET("/session")),
  });
  const defaultBase = session.data?.capabilities?.namingContexts?.[0] ?? "";

  // The documents, the two sides and the last comparison live in a store with
  // the lifetime of the tab, so that following "Review the changeset" and
  // coming back finds the comparison still here. See lib/snapshot-bench.
  const { slots, source, target, comparison } = useBench();
  const [base, setBase] = useState<string | null>(null);
  const [scope, setScope] = useState<"base" | "one" | "sub">("sub");
  const [filter, setFilter] = useState("");
  const [operational, setOperational] = useState(false);
  const [captureKind, setCaptureKind] = useState<"data" | "schema" | "config">(openWith ?? "data");
  const [schemaTarget, setSchemaTarget] = useState("");
  const schemaTargets = session.data?.capabilities?.schemaWrite?.targets ?? [];
  const [into, setInto] = useState<"A" | "B">("A");
  // The form that produced a comparison is furniture once the comparison is on
  // the screen, and it is tall. It folds away, and opens again on request or
  // when there is nothing to read.
  const [showCapture, setShowCapture] = useState(() => bench.state().comparison === null);
  // Two ways to name the same two sides is one too many. With a single
  // document loaded there is only one sensible pair, so the selects appear on
  // request; with two, or once asked for, they are back.
  const [chooseSides, setChooseSides] = useState(false);
  const setSource = (choice: SideChoice) => bench.setSide("source", choice);
  const setTarget = (choice: SideChoice) => bench.setSide("target", choice);
  const fileInput = useRef<HTMLInputElement>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);

  const inspect = async (
    raw: string,
    captured: boolean,
  ): Promise<{ doc: AnySnapshot; inspection: Inspection; bytes: number }> => {
    let doc: AnySnapshot;
    try {
      doc = JSON.parse(raw) as AnySnapshot;
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
    if (doc.kind === "config") {
      // Configuration documents are summarised from the document itself; the
      // comparison is where one is decoded strictly and its checksum checked.
      return { doc, inspection: unread(doc), bytes };
    }
    const inspection = unwrap(await api.POST("/snapshots/inspect", { body: doc }));
    return { doc, inspection, bytes };
  };

  const capture = useMutation<Slot, ApiFailure>({
    mutationFn: async () => {
      const raw = unwrap(
        await api.POST("/snapshots/capture", {
          body:
            captureKind === "schema" || captureKind === "config"
              ? { kind: captureKind }
              : {
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
      const prefix =
        captureKind === "schema" ? "alder-schema-snapshot" : captureKind === "config" ? "alder-config-snapshot" : "alder-snapshot";
      return { name: into, raw, doc, inspection, bytes, origin: "captured", filename: `${prefix}-${stamp}.json` };
    },
    onSuccess: (slot) => bench.load(slot),
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
        bench.load({ name: into, raw, doc, inspection, bytes, origin: "uploaded", filename: safe });
      } catch (e) {
        setUploadError(e instanceof Error ? e.message : String(e));
      }
    };
    reader.readAsText(file);
  };

  const kindOf = (choice: SideChoice) => (choice === "live" ? null : (slots[choice]?.doc.kind ?? null));
  const schemaComparison = kindOf(source) === "schema" || kindOf(target) === "schema";
  const configComparison = kindOf(source) === "config" || kindOf(target) === "config";
  // A live side reads whatever the other side is a snapshot of. Where the
  // server keeps schema in several entries, a live source also says which one
  // added definitions go to.
  const sideBody = (choice: SideChoice) =>
    choice === "live"
      ? {
          live: configComparison
            ? { kind: "config" as const }
            : schemaComparison && choice === source && schemaTarget
              ? { schemaTarget }
              : {},
        }
      : { snapshot: slots[choice]?.doc as AnySnapshot };
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
          : source !== "live" && target !== "live" && kindOf(source) !== kindOf(target)
            ? "These snapshots are of different kinds; data, schema and configuration are each compared only with their own."
            : requestBytes > MAX_BODY
            ? `Together that is ${mb(requestBytes)} MB to send, and the server reads at most ${mb(MAX_BODY)} MB in one request.`
            : null;

  const sideLabel = (choice: SideChoice) => (choice === "live" ? "The directory now" : `Snapshot ${choice}`);
  // With one document loaded there is one pair worth comparing -- it against
  // the directory -- so the two selects stay out of the way until a second
  // document, or the operator, gives them something to decide.
  const loaded = (["A", "B"] as const).filter((n) => slots[n]).length;
  const pickSides = chooseSides || loaded > 1;

  const compare = useMutation<Diff, ApiFailure>({
    mutationFn: async () =>
      unwrap(await api.POST("/diff", { body: { source: sideBody(source), target: sideBody(target) } })),
    // The result is kept in the store with the two sides as they were named
    // when it was made, so it survives a visit to the changeset.
    onSuccess: (diff) => {
      bench.setComparison({ diff, sourceLabel: sideLabel(source), targetLabel: sideLabel(target) });
      setShowCapture(false);
    },
  });

  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold">Snapshots &amp; drift</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Capture a subtree, the schema, or the server's own configuration as a versioned snapshot you keep;
          compare it with another snapshot or with the directory as it is now; and review the differences you
          select as changes. This is where a drift is found and put back. Nothing is stored on the server.
        </p>
      </header>

      <section className="space-y-3 rounded-lg border p-4">
        {!showCapture ? (
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <Button size="sm" variant="outline" onClick={() => setShowCapture(true)}>
              <Camera />
              Capture or upload another snapshot
            </Button>
          </div>
        ) : null}
        <div className={showCapture ? "flex flex-wrap items-center gap-2 text-sm" : "hidden"}>
          <span className="font-medium">Load into</span>
          {(["A", "B"] as const).map((n) => (
            <Button key={n} size="sm" variant={into === n ? "default" : "outline"} onClick={() => setInto(n)}>
              Snapshot {n}
            </Button>
          ))}
          <span className="ml-3 font-medium">Capture</span>
          {(["data", "schema", "config"] as const).map((k) => (
            <Button key={k} size="sm" variant={captureKind === k ? "default" : "outline"} onClick={() => setCaptureKind(k)}>
              {k === "data" ? "Directory data" : k === "schema" ? "Schema" : "Configuration"}
            </Button>
          ))}
        </div>
        {!showCapture ? null : captureKind === "data" ? (
        <>
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
        </>
        ) : captureKind === "config" ? (
          <p className="text-xs text-muted-foreground">
            Captures this server's own configuration, as its software arranges it: settings, the databases,
            overlays, plugins and backends they belong to, and whether each is something Alder can change.
            Secrets are withheld, runtime counters are not configuration and are left out, and the schema has its
            own kind. A configuration is only ever compared with another capture of the same server software.
          </p>
        ) : (
          <p className="text-xs text-muted-foreground">
            Captures the schema the server publishes: attribute types and object classes for comparison, and
            syntaxes, matching rules and the rest as context. Server configuration has its own kind.
          </p>
        )}
        <div className={showCapture ? "flex flex-wrap items-center gap-2" : "hidden"}>
          <Button
            onClick={() => capture.mutate()}
            disabled={capture.isPending || (captureKind === "data" && !(base ?? defaultBase).trim())}
          >
            {capture.isPending ? <Loader2 className="animate-spin" /> : <Camera />}
            Capture {captureKind === "schema" ? "the schema " : captureKind === "config" ? "the configuration " : ""}into snapshot{" "}
            {into}
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
          {pickSides ? (
            <>
              <SideSelect label="Source" value={source} onChange={setSource} slots={slots} />
              <ArrowRight className="size-4 text-muted-foreground" />
              <SideSelect label="Target" value={target} onChange={setTarget} slots={slots} />
            </>
          ) : (
            <>
              <span>
                <span className="font-medium">{sideLabel(source)}</span>{" "}
                <ArrowRight className="inline size-4 text-muted-foreground" />{" "}
                <span className="font-medium">{sideLabel(target)}</span>
              </span>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  const wasSource = source;
                  setSource(target);
                  setTarget(wasSource);
                }}
              >
                <ArrowLeftRight />
                Swap
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setChooseSides(true)}>
                Choose sides
              </Button>
            </>
          )}
          <Button onClick={() => compare.mutate()} disabled={compare.isPending || compareProblem !== null}>
            {compare.isPending ? <Loader2 className="animate-spin" /> : null}
            Compare
          </Button>
          {compareProblem ? <span className="text-xs text-muted-foreground">{compareProblem}</span> : null}
        </div>
        {schemaComparison && source === "live" && schemaTargets.length > 1 ? (
          <label className="flex flex-wrap items-center gap-1.5 text-sm">
            <span className="text-muted-foreground">Add new definitions to</span>
            <select
              value={schemaTarget}
              onChange={(e) => setSchemaTarget(e.target.value)}
              className="h-9 rounded-md border border-input bg-transparent px-2 text-sm"
              aria-label="Schema entry for added definitions"
            >
              <option value="">No entry chosen</option>
              {schemaTargets.map((t) => (
                <option key={t.dn} value={t.dn}>
                  {safeText(t.name)}
                </option>
              ))}
            </select>
            <span className="text-xs text-muted-foreground">
              The server keeps schema in several entries; without a choice, additions are not offered.
            </span>
          </label>
        ) : null}
        <p className="text-xs text-muted-foreground">
          <strong>Added</strong> means in the target and not in the source. Changes can be proposed only when
          the source is the directory now: they move it toward the target.
        </p>
        {compare.isError ? <ErrorNote title="The comparison failed" error={compare.error} /> : null}
        {comparison ? (
          comparison.diff.kind === "config" ? (
            <ConfigDiffView
              diff={comparison.diff}
              sourceLabel={comparison.sourceLabel}
              targetLabel={comparison.targetLabel}
              onReviewChangeset={onReviewChangeset}
              onApplied={() => compare.mutate()}
            />
          ) : comparison.diff.kind === "schema" ? (
            <SchemaDiffView
              diff={comparison.diff}
              sourceLabel={comparison.sourceLabel}
              targetLabel={comparison.targetLabel}
              onReviewChangeset={onReviewChangeset}
              onApplied={() => compare.mutate()}
            />
          ) : (
            <DiffView
              diff={comparison.diff}
              sourceLabel={comparison.sourceLabel}
              targetLabel={comparison.targetLabel}
              onReviewChangeset={onReviewChangeset}
              onApplied={() => compare.mutate()}
            />
          )
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
  if (slot.doc.kind === "config") {
    // A configuration document describes itself, and in its own words: these
    // are settings on one server's software, not entries in a subtree.
    const c = slot.doc;
    return (
      <div className="space-y-1 rounded-md border p-3 text-sm">
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium">Snapshot {name} · configuration</span>
          <DownloadButton text={slot.raw} filename={slot.filename} label="Download" mime="application/json" />
        </div>
        <div className="truncate font-dn text-xs" title={safeText(c.source.root)}>
          {c.source.provider} configuration at {safeText(c.source.root)}
        </div>
        <div className="text-xs text-muted-foreground">
          {c.counts.settings} settings · {c.counts.resources} resources ·{" "}
          {safeText(c.source.vendor) || "server not identified"} · <Captured slot={slot} at={c.createdAt} />
        </div>
        <div className="flex flex-wrap gap-1">
          {c.counts.withheld > 0 ? <Badge variant="outline">{c.counts.withheld} withheld</Badge> : null}
          {c.counts.operational > 0 ? <Badge variant="outline">{c.counts.operational} machine details</Badge> : null}
          {c.completeness === "partial" ? <Badge variant="warning">partial</Badge> : null}
          {slot.bytes > MAX_BODY ? <Badge variant="warning">too large to compare here</Badge> : null}
        </div>
      </div>
    );
  }
  if (i.schema) {
    const s = i.schema;
    return (
      <div className="space-y-1 rounded-md border p-3 text-sm">
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium">Snapshot {name} · schema</span>
          <DownloadButton text={slot.raw} filename={slot.filename} label="Download" mime="application/json" />
        </div>
        <div className="truncate font-dn text-xs" title={safeText(s.subschemaEntry)}>
          schema at {safeText(s.subschemaEntry)}
        </div>
        <div className="text-xs text-muted-foreground">
          {s.counts.attributeTypes} attribute types · {s.counts.objectClasses} object classes ·{" "}
          {safeText(i.source.vendor) || "server not identified"} · <Captured slot={slot} at={i.createdAt} />
        </div>
        <div className="flex flex-wrap gap-1">
          <Badge variant={i.integrity === "verified" ? "success" : "warning"}>checksum {i.integrity}</Badge>
          {s.completeness === "partial" ? (
            <Badge variant="warning">partial: {s.counts.unparsed} unparsed</Badge>
          ) : null}
          {s.collections ? <Badge variant="outline">collections</Badge> : null}
          <SignatureBadge signature={i.signature} />
          {slot.bytes > MAX_BODY ? <Badge variant="warning">too large to compare here</Badge> : null}
        </div>
      </div>
    );
  }
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
        {i.entryCount} entries · {i.source.vendor ?? "server not identified"} ·{" "}
        <Captured slot={slot} at={i.createdAt} />
      </div>
      <div className="flex flex-wrap gap-1">
        <Badge variant={i.integrity === "verified" ? "success" : "warning"}>checksum {i.integrity}</Badge>
        {i.operationalAttributes ? <Badge variant="outline">operational attributes</Badge> : null}
        {!i.schemaAvailable ? <Badge variant="warning">no schema</Badge> : null}
        <SignatureBadge signature={i.signature} />
        {slot.bytes > MAX_BODY ? <Badge variant="warning">too large to compare here</Badge> : null}
      </div>
    </div>
  );
}

/**
 * When a snapshot was taken, and how it got here.
 *
 * The card used to say "uploaded <createdAt>", which names the moment the
 * document was captured and labels it as the moment it was loaded. For a
 * drift investigation the capture time is the one that matters.
 */
function Captured({ slot, at }: { slot: Slot; at: string }) {
  return (
    <span title={at}>
      captured {formatInstant(at)}
      {slot.origin === "uploaded" ? " · loaded from file" : ""}
    </span>
  );
}

/**
 * What a document's signature amounted to, where the server said anything.
 * An unsigned document shows nothing: most documents are unsigned, and a badge
 * on every one of them would say only that the world is normal.
 */
function SignatureBadge({ signature }: { signature?: DocumentSignature }) {
  if (!worthShowing(signature)) return null;
  const look = SIGNATURE_LOOK[signature.status];
  const who = (signature.signers ?? []).map(signerLine).join("; ");
  return (
    <Badge variant={look.variant} title={who ? `${look.title}\n${who}` : look.title}>
      {look.label}
      {who ? ` · ${safeText(who)}` : ""}
    </Badge>
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
  onApplied,
}: {
  diff: Diff;
  sourceLabel: string;
  targetLabel: string;
  onReviewChangeset: () => void;
  /** A change was applied from here, so the comparison is out of date. */
  onApplied?: () => void;
}) {
  const [kinds, setKinds] = useState<Set<DiffKind>>(new Set());
  const [dn, setDn] = useState("");
  const [attribute, setAttribute] = useState("");
  const [page, setPage] = useState(0);
  const [open, setOpen] = useState<Set<number>>(new Set());
  const [chosen, setChosen] = useState<Set<number>>(new Set());
  const [deletions, setDeletions] = useState<Set<number>>(new Set());

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
        {(["added", "modified", "removed", "renamed", "unknown"] as const).map((k) => {
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
        <div className="ml-auto">
          <ReviewActions
            changes={selectedChangeItems(diff.items, chosen, deletions).map(({ item, change }) => ({
              change,
              label: `${KIND_LOOK[item.kind].label.toLowerCase()} ${itemDn(item)}`,
            }))}
            onReviewChangeset={onReviewChangeset}
            onApplied={onApplied}
            onStaged={() => {
              setChosen(new Set());
              setDeletions(new Set());
            }}
            destructive={deletions.size > 0}
          />
        </div>
      </div>

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
