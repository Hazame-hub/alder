import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangle, ListChecks, Package, Upload } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ErrorNote } from "@/components/change-dialog";
import { DownloadButton } from "@/components/ldif-block";
import { changeset, useChangeset } from "@/lib/changeset";
import { safeText } from "@/lib/display";
import {
  changeLine,
  orderedItems,
  readyChanges,
  STATUS_LOOK,
  unmetAssumptions,
  type PackageInspection,
  type PackageValidation,
} from "@/lib/change-package";

type Loaded = {
  /** The package exactly as it was built or uploaded. What travels is these bytes. */
  raw: string;
  inspection: PackageInspection;
  filename: string;
  origin: "exported" | "uploaded";
};

/**
 * Change packages.
 *
 * A package says what someone means to change. It is created here, carried
 * away as a file, and brought back -- to this directory or another one -- to be
 * validated against whatever is there now. Validation prepares changes; the
 * changeset plans them; the plan is what gets reviewed and applied. There is no
 * button here that writes anything, and no "promote": promotion is bringing the
 * same file to the next directory and doing this again.
 */
export function PackagesPanel({ onReviewChangeset }: { onReviewChangeset: () => void }) {
  const staged = useChangeset();
  const [title, setTitle] = useState("");
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [validation, setValidation] = useState<PackageValidation | null>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [stagedCount, setStagedCount] = useState<number | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const inspect = async (raw: string): Promise<PackageInspection> => {
    let document: unknown;
    try {
      document = JSON.parse(raw);
    } catch {
      throw new Error("The file is not JSON, so it is not an Alder change package.");
    }
    return unwrap(await api.POST("/packages/inspect", { body: document as never }));
  };

  const exportPackage = useMutation<Loaded, ApiFailure>({
    mutationFn: async () => {
      const raw = unwrap(
        await api.POST("/packages/build", {
          body: {
            title: title.trim() || undefined,
            method: "changeset",
            changes: staged.map((s) => s.change),
          },
          parseAs: "text",
        }),
      ) as unknown as string;
      const inspection = await inspect(raw);
      return { raw, inspection, origin: "exported", filename: `alder-change-package-${inspection.packageId.slice(0, 8)}.json` };
    },
    onSuccess: (next) => {
      setLoaded(next);
      setValidation(null);
      setStagedCount(null);
    },
  });

  const upload = (file: File) => {
    setUploadError(null);
    const reader = new FileReader();
    reader.onload = async () => {
      const raw = String(reader.result ?? "");
      try {
        const inspection = await inspect(raw);
        const safe = file.name.replace(/[^A-Za-z0-9._-]+/g, "-").slice(0, 80) || "package.json";
        setLoaded({ raw, inspection, filename: safe, origin: "uploaded" });
        setValidation(null);
        setStagedCount(null);
      } catch (e) {
        setUploadError(e instanceof Error ? e.message : String(e));
      }
    };
    reader.readAsText(file);
  };

  const validate = useMutation<PackageValidation, ApiFailure>({
    mutationFn: async () =>
      unwrap(
        await api.POST("/packages/validate", {
          body: { package: JSON.parse(loaded?.raw ?? "{}") },
        }),
      ),
    onSuccess: (next) => {
      setValidation(next);
      setStagedCount(null);
    },
  });

  const stage = () => {
    if (!validation) return;
    const ready = readyChanges(validation);
    const result = changeset.addMany(
      ready.map(({ item, change }) => ({ change, label: `package ${item.id}: ${item.label ?? item.kind}` })),
    );
    setStagedCount(result.staged);
  };

  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold">Change packages</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          A package describes what you mean to change, so the same intent can be carried to another
          directory. It holds no password and no plan: each directory is asked what the intent means
          there, and the changes that are ready are staged, planned and reviewed like every other
          change. Nothing is stored on the server.
        </p>
      </header>

      <section className="space-y-3 rounded-lg border p-4">
        <h3 className="font-medium">Make one, or open one</h3>
        <div className="grid gap-2 sm:grid-cols-[1fr_auto_auto]">
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="What this package is for"
            aria-label="Package title"
          />
          <Button onClick={() => exportPackage.mutate()} disabled={exportPackage.isPending || staged.length === 0}>
            <Package />
            Export {staged.length} staged change{staged.length === 1 ? "" : "s"}
          </Button>
          <Button variant="outline" onClick={() => fileInput.current?.click()}>
            <Upload />
            Open a package
          </Button>
        </div>
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
        {staged.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            The changeset is empty. Stage changes from the editor, an import or a comparison, and they can
            be exported as a package.
          </p>
        ) : null}
        {exportPackage.isError ? <ErrorNote title="The package could not be built" error={exportPackage.error} /> : null}
        {uploadError ? (
          <p className="rounded-md border border-destructive/40 bg-destructive/8 p-3 text-sm text-destructive">
            {uploadError}
          </p>
        ) : null}
      </section>

      {loaded ? (
        <PackageCard
          loaded={loaded}
          onValidate={() => validate.mutate()}
          validating={validate.isPending}
          error={validate.isError ? validate.error : null}
        />
      ) : null}

      {validation ? (
        <ValidationView
          validation={validation}
          onStage={stage}
          stagedCount={stagedCount}
          onReviewChangeset={onReviewChangeset}
        />
      ) : null}
    </div>
  );
}

function PackageCard({
  loaded,
  onValidate,
  validating,
  error,
}: {
  loaded: Loaded;
  onValidate: () => void;
  validating: boolean;
  error: ApiFailure | null;
}) {
  const i = loaded.inspection;
  const c = i.counts;
  return (
    <section className="space-y-3 rounded-lg border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="font-medium">{safeText(i.title) || "Untitled package"}</h3>
          <p className="font-mono text-xs text-muted-foreground">
            {safeText(i.packageId)} · created {safeText(i.createdAt)} · {loaded.origin}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <DownloadButton text={loaded.raw} filename={loaded.filename} label="Download" mime="application/json" />
          <Button onClick={onValidate} disabled={validating}>
            Validate against this directory
          </Button>
        </div>
      </div>
      {i.description ? <p className="text-sm">{safeText(i.description)}</p> : null}
      <div className="flex flex-wrap gap-1 text-sm">
        <Badge variant="outline">{c.changes} changes</Badge>
        <Badge variant="outline">{c.schema} schema</Badge>
        <Badge variant="outline">{c.data} data</Badge>
        {c.destructive > 0 ? <Badge variant="destructive">{c.destructive} destructive</Badge> : null}
        <Badge variant={i.integrity === "verified" ? "success" : "warning"}>checksum {i.integrity}</Badge>
      </div>
      {c.omitted > 0 ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
          <div className="mb-1 font-medium">
            {c.omitted} change{c.omitted === 1 ? "" : "s"} could not be packaged
          </div>
          <ul className="ml-5 list-disc space-y-0.5 text-xs">
            {i.omitted.map((o, index) => (
              <li key={index}>
                <code className="font-mono">{o.reason}</code>
                {o.subject ? ` — ${safeText(o.subject)}` : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <ol className="divide-y rounded-md border text-sm">
        {i.order.map((id) => {
          const change = i.changes.find((ch) => ch.id === id);
          if (!change) return null;
          return (
            <li key={id} className="flex flex-wrap items-center gap-2 px-3 py-2">
              <span className="font-mono text-xs text-muted-foreground">{safeText(change.id)}</span>
              {change.destructive ? <Badge variant="destructive">destructive</Badge> : null}
              <span className="font-dn text-xs [overflow-wrap:anywhere]">{safeText(changeLine(change))}</span>
              {change.dependsOn?.length ? (
                <span className="text-xs text-muted-foreground">after {safeText(change.dependsOn.join(", "))}</span>
              ) : null}
            </li>
          );
        })}
      </ol>
      {error ? <ErrorNote title="The package could not be validated" error={error} /> : null}
    </section>
  );
}

function ValidationView({
  validation,
  onStage,
  stagedCount,
  onReviewChangeset,
}: {
  validation: PackageValidation;
  onStage: () => void;
  stagedCount: number | null;
  onReviewChangeset: () => void;
}) {
  const c = validation.counts;
  const unmet = unmetAssumptions(validation);
  const rows: [number, string][] = [
    [c.ready, "ready"],
    [c.alreadySatisfied, "already satisfied"],
    [c.noOp, "nothing to do"],
    [c.conflict, "conflict"],
    [c.dependencyMissing, "dependency missing"],
    [c.unsupported, "unsupported here"],
    [c.targetIncompatible, "not for this directory"],
    [c.unknown, "unknown"],
  ];
  return (
    <section className="space-y-3 rounded-lg border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-medium">
          Against {safeText(validation.target.namingContexts.join(", ")) || "this directory"}
          {validation.target.vendor ? ` · ${safeText(validation.target.vendor)}` : ""}
        </h3>
        <div className="flex items-center gap-2">
          <Button onClick={onStage} disabled={c.ready === 0}>
            <ListChecks />
            Stage {c.ready} ready change{c.ready === 1 ? "" : "s"}
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5 text-xs">
        {rows
          .filter(([n]) => n > 0)
          .map(([n, label]) => (
            <span key={label} className="rounded-md border px-2 py-1">
              {n} {label}
            </span>
          ))}
      </div>

      {unmet.length > 0 ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
          <div className="mb-1 flex items-center gap-1.5 font-medium">
            <AlertTriangle className="size-4" />
            This package assumes something this directory does not have
          </div>
          <ul className="ml-5 list-disc space-y-0.5 text-xs">
            {unmet.map((a, index) => (
              <li key={index}>
                {a.kind} <span className="font-dn">{safeText(a.value)}</span>
                {a.detail ? ` — ${safeText(a.detail)}` : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {stagedCount !== null ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm">
          {stagedCount} change{stagedCount === 1 ? "" : "s"} staged, in the order this directory needs them. They are
          planned against the directory, and reviewed, before anything is applied.
          <Button size="sm" variant="outline" onClick={onReviewChangeset}>
            Review the changeset
          </Button>
        </div>
      ) : null}

      <ol className="divide-y rounded-md border">
        {orderedItems(validation).map((item) => {
          const look = STATUS_LOOK[item.status];
          return (
            <li key={item.id} className="px-3 py-2 text-sm">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={look.variant}>{look.label}</Badge>
                <span className="font-mono text-xs text-muted-foreground">{safeText(item.id)}</span>
                <span className="text-xs text-muted-foreground">{item.kind}</span>
                {item.destructive ? <Badge variant="destructive">destructive</Badge> : null}
                {item.label ? <span className="truncate text-xs">{safeText(item.label)}</span> : null}
              </div>
              {(item.problems ?? []).map((p, index) => (
                <p key={index} className="mt-1 ml-2 text-xs text-warning-tint-foreground">
                  <code className="font-mono">{p.code}</code>
                  {p.subject ? <span className="font-dn"> {safeText(p.subject)}</span> : null}
                  {p.detail ? ` — ${safeText(p.detail)}` : null}
                </p>
              ))}
            </li>
          );
        })}
      </ol>
      <p className="text-xs text-muted-foreground">
        Nothing has been applied. Staging puts the ready changes in the changeset, where they are planned
        against this directory and reviewed before anything is written.
      </p>
    </section>
  );
}
