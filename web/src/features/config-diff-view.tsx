import { useMemo, useState } from "react";
import { ListChecks } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { changeset } from "@/lib/changeset";
import { safeText } from "@/lib/display";
import {
  ACTION_LOOK,
  configSections,
  filterConfigItems,
  stageableChanges,
  valueText,
  type ConfigDiffItem,
  type DiffKind,
} from "@/lib/config-diff";
import type { components } from "@/lib/api.gen";

type Diff = components["schemas"]["Diff"];

const KIND_LABEL: Record<DiffKind, string> = {
  added: "Added",
  removed: "Removed",
  modified: "Modified",
  renamed: "Renamed",
  unchanged: "Unchanged",
  unknown: "Unknown",
  metadata_only: "Metadata",
};

const PAGE = 50;

/**
 * A configuration comparison.
 *
 * Configuration belongs to the server's software, so this view never suggests
 * that one server's setting is another's. When the two sides are different
 * providers there is nothing to compare setting by setting, and the view says
 * exactly that instead of listing differences that would all be artefacts.
 *
 * A difference is staged only when the server said Alder already changes that
 * setting through the ordinary plan. Everything else is shown and left alone.
 */
export function ConfigDiffView({
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
  const config = diff.config;
  const [section, setSection] = useState<string | "all">("all");
  const [kinds, setKinds] = useState<Set<DiffKind>>(new Set());
  const [actionableOnly, setActionableOnly] = useState(false);
  const [hideSensitive, setHideSensitive] = useState(false);
  const [text, setText] = useState("");
  const [shown, setShown] = useState(PAGE);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [staged, setStaged] = useState<number | null>(null);

  const visible = useMemo(
    () => (config ? filterConfigItems(config.items, { section, kinds, actionableOnly, hideSensitive, text }) : []),
    [config, section, kinds, actionableOnly, hideSensitive, text],
  );
  if (!config) return null;

  if (config.providerMismatch) {
    return (
      <section className="space-y-3 rounded-lg border p-4">
        <h3 className="font-medium">These configuration models are provider-specific</h3>
        <p className="text-sm">
          {sourceLabel} is {config.source.provider} configuration and {targetLabel} is {config.target.provider}{" "}
          configuration. Their settings are not the same settings. Alder does not translate configuration between
          servers, and does not compare them setting by setting, because every difference it listed would be an
          artefact of the comparison rather than a fact about either server.
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          {[
            { label: sourceLabel, summary: config.source },
            { label: targetLabel, summary: config.target },
          ].map(({ label, summary }) => (
            <div key={label} className="rounded-md border p-3 text-sm">
              <div className="font-medium">{label}</div>
              <div className="text-xs text-muted-foreground">
                {summary.provider}
                {summary.vendor ? ` · ${safeText(summary.vendor)}` : ""} · {summary.settings} settings in{" "}
                {summary.resources} resources · {summary.completeness}
              </div>
              <ul className="mt-2 space-y-0.5 text-xs">
                {summary.sections.map((s) => (
                  <li key={s.section} className="flex justify-between gap-2">
                    <span>{s.section}</span>
                    <span className="text-muted-foreground">{s.settings}</span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </section>
    );
  }

  const stage = () => {
    const changes = stageableChanges(config, selected);
    const result = changeset.addMany(
      changes.map(({ item, change }) => ({ change, label: `config ${item.key} on ${item.dn ?? ""}` })),
    );
    setStaged(result.staged);
  };
  const actionableSelected = stageableChanges(config, selected).length;

  return (
    <section className="space-y-4 rounded-lg border p-4">
      <div>
        <h3 className="font-medium">
          {config.provider} configuration · {sourceLabel} → {targetLabel}
        </h3>
        <p className="text-xs text-muted-foreground">
          Added means set on the target and not on the source. Configuration is this server's own: nothing here is
          portable to another product.
        </p>
      </div>

      <div className="flex flex-wrap gap-1.5 text-xs">
        {(
          [
            [config.counts.added, "added"],
            [config.counts.modified, "modified"],
            [config.counts.removed, "removed"],
            [config.counts.unchanged, "unchanged"],
            [config.counts.unknown, "unknown"],
          ] as [number, string][]
        )
          .filter(([n]) => n > 0)
          .map(([n, label]) => (
            <span key={label} className="rounded-md border px-2 py-1">
              {n} {label}
            </span>
          ))}
        <span className="rounded-md border px-2 py-1">{config.counts.actionable} Alder can change</span>
      </div>

      {!config.complete ? (
        <p className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
          Part of a configuration could not be read, so this is not the whole answer. What is missing is unknown, not
          removed.
        </p>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setShown(PAGE);
          }}
          placeholder="Setting, resource, entry or value contains"
          className="max-w-xs font-mono text-xs"
          aria-label="Filter settings"
        />
        <select
          value={section}
          onChange={(e) => {
            setSection(e.target.value);
            setShown(PAGE);
          }}
          className="h-9 rounded-md border border-input bg-transparent px-2 text-sm"
          aria-label="Filter by section"
        >
          <option value="all">Every section</option>
          {configSections(config).map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
        {(["added", "modified", "removed"] as DiffKind[]).map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => {
              const next = new Set(kinds);
              if (next.has(k)) next.delete(k);
              else next.add(k);
              setKinds(next);
              setShown(PAGE);
            }}
            className={`rounded-md border px-2 py-1 text-xs ${kinds.has(k) ? "border-primary" : ""}`}
          >
            {KIND_LABEL[k]}
          </button>
        ))}
        <label className="flex items-center gap-1.5 text-sm">
          <input type="checkbox" checked={actionableOnly} onChange={(e) => setActionableOnly(e.target.checked)} />
          Only what Alder can change
        </label>
        <label className="flex items-center gap-1.5 text-sm">
          <input type="checkbox" checked={hideSensitive} onChange={(e) => setHideSensitive(e.target.checked)} />
          Hide withheld
        </label>
        <span className="text-xs text-muted-foreground">
          {visible.length} of {config.items.length}
        </span>
      </div>

      {selected.size > 0 ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border px-3 py-2 text-sm">
          {actionableSelected} change{actionableSelected === 1 ? "" : "s"} from {selected.size} selected setting
          {selected.size === 1 ? "" : "s"}
          <Button size="sm" onClick={stage} disabled={actionableSelected === 0}>
            <ListChecks />
            Stage for review
          </Button>
          {staged !== null ? (
            <>
              <span>{staged} staged.</span>
              <Button size="sm" variant="outline" onClick={onReviewChangeset}>
                Review the changeset
              </Button>
            </>
          ) : null}
        </div>
      ) : null}

      <ol className="divide-y rounded-md border">
        {visible.slice(0, shown).map((item) => (
          <ConfigRow
            key={item.id}
            item={item}
            selected={selected.has(item.id)}
            onToggle={() => {
              const next = new Set(selected);
              if (next.has(item.id)) next.delete(item.id);
              else next.add(item.id);
              setSelected(next);
              setStaged(null);
            }}
          />
        ))}
      </ol>
      {visible.length > shown ? (
        <Button variant="outline" size="sm" onClick={() => setShown(shown + PAGE)}>
          Show {Math.min(PAGE, visible.length - shown)} more
        </Button>
      ) : null}

      <p className="text-xs text-muted-foreground">
        Nothing here changes the server. A setting Alder can change is staged into the changeset, planned against the
        directory and reviewed like every other change; recovery is not available for configuration changes.
      </p>
    </section>
  );
}

function ConfigRow({
  item,
  selected,
  onToggle,
}: {
  item: ConfigDiffItem;
  selected: boolean;
  onToggle: () => void;
}) {
  const look = ACTION_LOOK[item.actionable];
  const selectable = item.actionable === "writable";
  return (
    <li className="px-3 py-2 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <input
          type="checkbox"
          checked={selected}
          disabled={!selectable}
          onChange={onToggle}
          aria-label={`Select ${item.key}`}
        />
        <Badge variant="outline">{KIND_LABEL[item.kind]}</Badge>
        <code className="font-mono text-xs">{safeText(item.key)}</code>
        <span className="text-xs text-muted-foreground">{safeText(item.section)}</span>
        {item.resource ? (
          <span className="font-dn text-xs [overflow-wrap:anywhere]">{safeText(item.resourceLabel || item.resource)}</span>
        ) : null}
        <Badge variant={look.variant}>{look.label}</Badge>
        {item.sensitive ? <Badge variant="secondary">withheld</Badge> : null}
        {item.operational ? <Badge variant="secondary">machine detail</Badge> : null}
      </div>
      <div className="mt-1 ml-6 space-y-0.5 font-mono text-xs">
        <div className="text-destructive">- {safeText(valueText(item, "source"))}</div>
        <div className="text-success-foreground">+ {safeText(valueText(item, "target"))}</div>
        {item.dn ? <div className="font-dn text-muted-foreground">{safeText(item.dn)}</div> : null}
        {item.comparison === "raw" ? (
          <div className="text-muted-foreground">compared as text: nothing here parses this value</div>
        ) : null}
        {(item.problems ?? []).map((p) => (
          <div key={p} className="text-warning-tint-foreground">
            {safeText(p)}
          </div>
        ))}
      </div>
    </li>
  );
}
