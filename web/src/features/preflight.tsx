import { useMemo, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, ClipboardCheck, Upload } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ErrorNote } from "@/components/change-dialog";
import { safeText } from "@/lib/display";
import {
  CATEGORY_LABEL,
  CLASSIFICATION_LOOK,
  causeChain,
  claimedArtifact,
  filterFindings,
  OVERALL_LOOK,
  SOURCE_LABEL,
  subjectLine,
  type PreflightCategory,
  type PreflightClassification,
  type PreflightFinding,
  type PreflightReport,
} from "@/lib/preflight";

type Loaded = {
  /** The artifact exactly as the file held it. It is sent as these bytes and never edited. */
  raw: string;
  filename: string;
  claim: { label: string; objects: number } | null;
};

const PAGE = 200;

/**
 * Migration preflight.
 *
 * An artifact from anywhere -- a package, a schema snapshot, a data snapshot --
 * read against the directory this session is connected to. The report says
 * what would carry across and why not. There is no button here that migrates,
 * applies or stages anything: a preflight is analysis, and what the operator
 * does about it goes through the Packages view, a comparison, and a plan.
 */
export function PreflightPanel() {
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [report, setReport] = useState<PreflightReport | null>(null);
  const [openError, setOpenError] = useState<string | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const open = (file: File) => {
    setOpenError(null);
    const reader = new FileReader();
    reader.onload = () => {
      const raw = String(reader.result ?? "");
      let document: unknown;
      try {
        document = JSON.parse(raw);
      } catch {
        setOpenError("The file is not JSON, so it is not an Alder change package or snapshot.");
        return;
      }
      const safe = file.name.replace(/[^A-Za-z0-9._-]+/g, "-").slice(0, 80) || "artifact.json";
      setLoaded({ raw, filename: safe, claim: claimedArtifact(document) });
      setReport(null);
    };
    reader.readAsText(file);
  };

  const run = useMutation<PreflightReport, ApiFailure>({
    mutationFn: async () =>
      unwrap(await api.POST("/preflight", { body: { artifact: JSON.parse(loaded?.raw ?? "{}") } })),
    onSuccess: setReport,
  });

  return (
    <div className="mx-auto max-w-5xl space-y-5 p-6">
      <header>
        <h2 className="text-lg font-semibold">Migration preflight</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Read a change package, a schema snapshot or a data snapshot against this directory, and see what
          would carry across: what is already here, what could be added, what needs something first, what
          contradicts this directory, and what could not be seen. A preflight writes nothing, changes nothing
          in the file, and rewrites no DN.
        </p>
      </header>

      <section className="space-y-3 rounded-lg border p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 className="font-medium">Artifact</h3>
            {loaded ? (
              <p className="text-sm">
                {loaded.claim ? `${loaded.claim.label} · ${loaded.claim.objects} object${loaded.claim.objects === 1 ? "" : "s"}` : "Not a recognised artifact"}
                <span className="ml-2 font-mono text-xs text-muted-foreground">{safeText(loaded.filename)}</span>
              </p>
            ) : (
              <p className="text-sm text-muted-foreground">No artifact open.</p>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" onClick={() => fileInput.current?.click()}>
              <Upload />
              Open artifact
            </Button>
            <Button onClick={() => run.mutate()} disabled={!loaded || run.isPending}>
              <ClipboardCheck />
              Run preflight
            </Button>
          </div>
        </div>
        <input
          ref={fileInput}
          type="file"
          accept=".json,application/json"
          className="hidden"
          aria-label="Artifact file"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) open(file);
            e.target.value = "";
          }}
        />
        {openError ? (
          <p className="rounded-md border border-destructive/40 bg-destructive/8 p-3 text-sm text-destructive">{openError}</p>
        ) : null}
        {run.isError ? <ErrorNote title="The preflight could not run" error={run.error} /> : null}
      </section>

      {report ? <ReportView report={report} /> : null}
    </div>
  );
}

function ReportView({ report }: { report: PreflightReport }) {
  const look = OVERALL_LOOK[report.overall];
  const [category, setCategory] = useState<PreflightCategory | "all">("all");
  const [classification, setClassification] = useState<PreflightClassification | "all">("all");
  const [attentionOnly, setAttentionOnly] = useState(true);
  const [text, setText] = useState("");
  const [shown, setShown] = useState(PAGE);
  const [focus, setFocus] = useState<string | null>(null);

  const findings = useMemo(
    () => filterFindings(report, { category, classification, attentionOnly, text }),
    [report, category, classification, attentionOnly, text],
  );

  const target = [safeText(report.target.namingContexts.join(", ")), report.target.vendor ? safeText(report.target.vendor) : ""]
    .filter(Boolean)
    .join(" · ");
  const source = [SOURCE_LABEL[report.source.type] ?? report.source.type, report.source.vendor ? `from ${safeText(report.source.vendor)}` : ""]
    .filter(Boolean)
    .join(" ");

  const reveal = (id: string) => {
    setCategory("all");
    setClassification("all");
    setAttentionOnly(false);
    setText("");
    setShown(report.findings.length);
    setFocus(id);
    requestAnimationFrame(() => document.getElementById(`finding-${id}`)?.scrollIntoView({ block: "center" }));
  };

  return (
    <section className="space-y-4 rounded-lg border p-4">
      <div className="space-y-1">
        <p className="text-xs text-muted-foreground">
          {source} → {target || "this directory"}
          {report.source.title ? ` · ${safeText(report.source.title)}` : ""} · checksum {safeText(report.source.integrity)}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={look.variant} className="text-sm">
            {look.label}
          </Badge>
          <span className="text-sm">{look.summary}</span>
        </div>
        {!report.complete && report.reasons.length > 0 ? (
          <p className="text-xs text-warning-tint-foreground">
            Not complete: <code className="font-mono">{safeText(report.reasons.join(", "))}</code>
          </p>
        ) : null}
        {report.target.crossVendor ? (
          <p className="text-xs text-muted-foreground">
            Source and target are different products. That alone decides nothing; every finding rests on a concrete difference.
          </p>
        ) : null}
      </div>

      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        {report.sections.map((s) => (
          <button
            key={s.category}
            type="button"
            onClick={() => {
              setCategory(s.category);
              setShown(PAGE);
            }}
            className={`rounded-md border p-2 text-left text-xs hover:bg-muted ${category === s.category ? "border-primary" : ""}`}
          >
            <div className="mb-1 font-medium">{CATEGORY_LABEL[s.category]}</div>
            {(
              [
                [s.counts.portable, "portable"],
                [s.counts.alreadySatisfied, "already present"],
                [s.counts.prerequisiteRequired, "need prerequisites"],
                [s.counts.incompatible, "incompatible"],
                [s.counts.unsupported, "unsupported"],
                [s.counts.unknown, "unknown"],
                [s.counts.excluded, "not migrated"],
              ] as [number, string][]
            )
              .filter(([n]) => n > 0)
              .map(([n, label]) => (
                <div key={label}>
                  {n} {label}
                </div>
              ))}
          </button>
        ))}
      </div>

      {report.capabilities.length > 0 ? (
        <div className="text-xs">
          <div className="mb-1 font-medium">Capabilities this artifact needs</div>
          <ul className="space-y-0.5">
            {report.capabilities.map((c) => (
              <li key={c.capability}>
                <Badge variant={c.available ? "outline" : "destructive"}>{c.available ? "available" : "not available"}</Badge>{" "}
                <code className="font-mono">{safeText(c.capability)}</code> — {safeText(c.requiredBy)}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setShown(PAGE);
          }}
          placeholder="Code, DN, OID or text contains"
          className="max-w-xs font-mono text-xs"
          aria-label="Filter findings by text"
        />
        <select
          value={category}
          onChange={(e) => {
            setCategory(e.target.value as PreflightCategory | "all");
            setShown(PAGE);
          }}
          className="h-9 rounded-md border border-input bg-transparent px-2 text-sm"
          aria-label="Filter findings by section"
        >
          <option value="all">Every section</option>
          {(Object.keys(CATEGORY_LABEL) as PreflightCategory[]).map((c) => (
            <option key={c} value={c}>
              {CATEGORY_LABEL[c]}
            </option>
          ))}
        </select>
        <select
          value={classification}
          onChange={(e) => {
            setClassification(e.target.value as PreflightClassification | "all");
            setShown(PAGE);
          }}
          className="h-9 rounded-md border border-input bg-transparent px-2 text-sm"
          aria-label="Filter findings by status"
        >
          <option value="all">Every status</option>
          {(Object.keys(CLASSIFICATION_LOOK) as PreflightClassification[]).map((c) => (
            <option key={c} value={c}>
              {CLASSIFICATION_LOOK[c].label}
            </option>
          ))}
        </select>
        <label className="flex items-center gap-1.5 text-sm">
          <input type="checkbox" checked={attentionOnly} onChange={(e) => setAttentionOnly(e.target.checked)} />
          Only what needs attention
        </label>
        <span className="text-xs text-muted-foreground">
          {findings.length} of {report.findings.length} findings
        </span>
      </div>

      {findings.length === 0 ? (
        <p className="text-sm text-muted-foreground">No finding matches.</p>
      ) : (
        <ol className="divide-y rounded-md border">
          {findings.slice(0, shown).map((f) => (
            <FindingRow key={f.id} report={report} finding={f} focused={focus === f.id} onReveal={reveal} />
          ))}
        </ol>
      )}
      {findings.length > shown ? (
        <Button variant="outline" size="sm" onClick={() => setShown(shown + PAGE)}>
          Show {Math.min(PAGE, findings.length - shown)} more
        </Button>
      ) : null}

      <div className="rounded-md border p-3 text-xs">
        <div className="mb-1 font-medium">Not evaluated</div>
        <ul className="ml-5 list-disc space-y-0.5">
          {report.notEvaluated.map((n) => (
            <li key={n.area}>
              <span className="font-medium">{safeText(n.area.replaceAll("_", " "))}</span> — {safeText(n.reason)}
            </li>
          ))}
        </ul>
      </div>
      <p className="text-xs text-muted-foreground">
        Nothing has been written and the artifact is unchanged. A preflight is not a plan: to act on this, open the
        package in Packages, or compare snapshots, and plan the changes there.
      </p>
    </section>
  );
}

function FindingRow({
  report,
  finding: f,
  focused,
  onReveal,
}: {
  report: PreflightReport;
  finding: PreflightFinding;
  focused: boolean;
  onReveal: (id: string) => void;
}) {
  const [open, setOpen] = useState(focused);
  const look = CLASSIFICATION_LOOK[f.classification];
  const chain = open ? causeChain(report, f.id) : [];
  return (
    <li id={`finding-${f.id}`} className={`px-3 py-2 text-sm ${focused ? "bg-muted" : ""}`}>
      <button type="button" className="flex w-full flex-wrap items-center gap-2 text-left" onClick={() => setOpen(!open)}>
        {open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
        <span className="font-mono text-xs text-muted-foreground">{f.id}</span>
        <Badge variant={look.variant}>{look.label}</Badge>
        <code className="font-mono text-xs">{f.code}</code>
        <span className="font-dn text-xs [overflow-wrap:anywhere]">{safeText(subjectLine(f))}</span>
      </button>
      {open ? (
        <div className="mt-2 ml-6 space-y-1.5 text-xs">
          <p>{safeText(f.explanation)}</p>
          {f.target ? (
            <p className="text-muted-foreground">
              Target: <code className="font-mono">{safeText(f.target.fact)}</code>
              {f.target.detail ? ` — ${safeText(f.target.detail)}` : ""}
            </p>
          ) : null}
          <div className="flex flex-wrap gap-1">
            {f.blocksPortability ? <Badge variant="destructive">blocks portability</Badge> : null}
            {f.blocksPlan ? <Badge variant="warning">a plan would refuse it</Badge> : null}
            {f.manualAction ? <Badge variant="warning">manual action</Badge> : null}
            {f.validationStatus ? <Badge variant="outline">validation: {f.validationStatus}</Badge> : null}
          </div>
          {f.prerequisites?.length ? (
            <div>
              <div className="font-medium">Needs first</div>
              <ul className="ml-4 list-disc">
                {f.prerequisites.map((p, index) => (
                  <li key={index}>
                    {p.type} <span className="font-dn">{safeText([p.element, p.oid, p.name, p.dn, p.capability].filter(Boolean).join(" "))}</span>
                    {p.providedBy ? (
                      <>
                        {" "}
                        — supplied by{" "}
                        <button type="button" className="underline" onClick={() => onReveal(p.providedBy as string)}>
                          {p.providedBy}
                        </button>
                      </>
                    ) : null}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {chain.length > 0 ? (
            <div>
              <div className="font-medium">Because of</div>
              <ol className="ml-4 space-y-0.5">
                {chain.map((c) => (
                  <li key={c.id}>
                    ↳{" "}
                    <button type="button" className="font-mono underline" onClick={() => onReveal(c.id)}>
                      {c.id}
                    </button>{" "}
                    <code className="font-mono">{c.code}</code> {safeText(subjectLine(c))}
                  </li>
                ))}
              </ol>
            </div>
          ) : null}
        </div>
      ) : null}
    </li>
  );
}
