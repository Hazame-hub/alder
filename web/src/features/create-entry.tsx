import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, ArrowRight, Check, Loader2 } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { AttributeKind, ChangeRequest, ObjectView } from "@/lib/api";
import { buildAddChange, escapeRDNValue } from "@/lib/create";
import { cn } from "@/lib/utils";
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
import { ChangeDialog } from "@/components/change-dialog";
import { AddAttribute, AttributeEditor } from "@/components/attribute-editor";

/**
 * Creating an entry, in steps, from the schema.
 *
 * Two things it deliberately is not. It is not a template: every field on the
 * last step is generated from what the chosen classes require and permit, so it
 * offers the same controls the editor does — a Boolean gets TRUE/FALSE, a
 * DN-valued attribute gets the entry picker, and each field carries the
 * schema's own description. And it has no finish button of its own: the last
 * step hands a ChangeRequest to the same confirmation dialog every other write
 * in the application goes through, with the same LDIF, the same Ansible tab and
 * the same Stage button. A wizard here is a way of composing a change record
 * and nothing more.
 *
 * Going back preserves everything typed, because a wizard that discards your
 * work when you check something is worse than no wizard.
 */

type Step = "kind" | "class" | "details";

const steps: [Step, string][] = [
  ["kind", "Kind"],
  ["class", "Object classes"],
  ["details", "Name and attributes"],
];

export function CreateEntryDialog({
  parentDN,
  open,
  onOpenChange,
  onCreated,
}: {
  parentDN: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (dn: string) => void;
}) {
  const [step, setStep] = useState<Step>("kind");
  /** The view whose classes are on offer, or null for the whole schema. */
  const [kind, setKind] = useState<ObjectView | null>(null);
  const [browsingAll, setBrowsingAll] = useState(false);
  const [structural, setStructural] = useState("");
  const [auxiliary, setAuxiliary] = useState<string[]>([]);
  const [rdnAttr, setRdnAttr] = useState("");
  const [rdnValue, setRdnValue] = useState("");
  const [values, setValues] = useState<Record<string, string[]>>({});
  const [added, setAdded] = useState<string[]>([]);
  const [change, setChange] = useState<ChangeRequest | null>(null);

  useEffect(() => {
    if (!open) return;
    setStep("kind");
    setKind(null);
    setBrowsingAll(false);
    setStructural("");
    setAuxiliary([]);
    setRdnAttr("");
    setRdnValue("");
    setValues({});
    setAdded([]);
    setChange(null);
  }, [open]);

  const views = useQuery({
    queryKey: ["views"],
    enabled: open,
    staleTime: Infinity,
    queryFn: async () => unwrap(await api.GET("/views")),
  });

  const schema = useQuery({
    queryKey: ["schema"],
    enabled: open && browsingAll,
    queryFn: async () => unwrap(await api.GET("/schema")),
  });

  const classes = useMemo(
    () => [structural, ...auxiliary].filter((c) => c !== ""),
    [structural, auxiliary],
  );

  // What an entry of these classes must and may hold, with the schema's opinion
  // about each attribute. This is what makes the last step a generated form
  // rather than a list of names beside text boxes.
  const requirements = useQuery({
    queryKey: ["requirements", classes],
    enabled: open && structural !== "",
    queryFn: async () =>
      unwrap(await api.GET("/schema/requirements", { params: { query: { class: classes } } })),
  });

  const kindOf = (name: string): AttributeKind | undefined =>
    requirements.data?.kinds.find((k) => k.name.toLowerCase() === name.toLowerCase());

  const must = (requirements.data?.requirements.must ?? []).filter(
    (n) => n.toLowerCase() !== "objectclass",
  );
  const may = requirements.data?.requirements.may ?? [];

  // The naming attribute defaults to the first required one that is not the
  // object class, which is what a directory almost always names entries by.
  useEffect(() => {
    if (rdnAttr === "" && must.length > 0) setRdnAttr(must[0] as string);
  }, [must, rdnAttr]);

  const availableToAdd = useMemo(() => {
    const taken = new Set([
      ...must.map((n) => n.toLowerCase()),
      ...added.map((n) => n.toLowerCase()),
    ]);
    return may.filter((n) => !taken.has(n.toLowerCase())).sort();
  }, [may, added, must]);

  const candidate = buildAddChange({
    parentDN,
    structural,
    auxiliary,
    rdnAttr,
    rdnValue,
    attributes: [...must, ...added],
    values,
  });
  const stepIndex = steps.findIndex(([id]) => id === step);

  const canAdvance =
    step === "kind"
      ? kind !== null || browsingAll
      : step === "class"
        ? structural !== ""
        : false;

  return (
    <>
      <Dialog open={open && change === null} onOpenChange={onOpenChange}>
        <DialogContent wide>
          <DialogHeader>
            <DialogTitle>New entry</DialogTitle>
            <DialogDescription>
              under <span className="font-dn">{parentDN}</span>
            </DialogDescription>
          </DialogHeader>

          <StepBar current={stepIndex} />

          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
            {step === "kind" ? (
              <KindStep
                views={views.data?.views ?? []}
                loading={views.isPending}
                selected={kind}
                browsingAll={browsingAll}
                onPick={(v) => {
                  setKind(v);
                  setBrowsingAll(false);
                  setStructural("");
                  setStep("class");
                }}
                onBrowseAll={() => {
                  setKind(null);
                  setBrowsingAll(true);
                  setStructural("");
                  setStep("class");
                }}
              />
            ) : step === "class" ? (
              <ClassStep
                offered={
                  browsingAll
                    ? (schema.data?.objectClasses ?? [])
                        .filter((c) => c.kind === "STRUCTURAL")
                        .map((c) => c.name)
                        .sort()
                    : (kind?.createClasses ?? [])
                }
                loading={browsingAll && schema.isPending}
                auxiliaries={(schema.data?.objectClasses ?? [])
                  .filter((c) => c.kind === "AUXILIARY")
                  .map((c) => c.name)
                  .sort()}
                needAuxiliaries={() => setBrowsingAll(true)}
                structural={structural}
                auxiliary={auxiliary}
                onStructural={(name) => {
                  setStructural(name);
                  // The naming attribute follows the class, so a class change
                  // must not leave the previous class's choice behind.
                  setRdnAttr("");
                }}
                onAuxiliary={setAuxiliary}
              />
            ) : (
              <DetailsStep
                parentDN={parentDN}
                requirementsPending={requirements.isPending}
                rdnAttr={rdnAttr}
                rdnValue={rdnValue}
                onRdnAttr={setRdnAttr}
                onRdnValue={setRdnValue}
                must={must}
                added={added}
                availableToAdd={availableToAdd}
                onAdd={(name) => setAdded((a) => [...a, name])}
                onRemove={(name) => {
                  setAdded((a) => a.filter((n) => n !== name));
                  setValues((v) => {
                    const next = { ...v };
                    delete next[name];
                    return next;
                  });
                }}
                values={values}
                onValues={(name, next) => setValues((v) => ({ ...v, [name]: next }))}
                kindOf={kindOf}
                pickerBase={parentDN}
              />
            )}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            {stepIndex > 0 ? (
              <Button
                variant="outline"
                onClick={() => setStep(steps[stepIndex - 1]?.[0] as Step)}
              >
                <ArrowLeft />
                Back
              </Button>
            ) : null}
            {step === "details" ? (
              <Button disabled={!candidate} onClick={() => setChange(candidate)}>
                <Check />
                Review the change
              </Button>
            ) : (
              <Button
                disabled={!canAdvance}
                onClick={() => setStep(steps[stepIndex + 1]?.[0] as Step)}
              >
                Next
                <ArrowRight />
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ChangeDialog
        change={change}
        open={change !== null}
        onOpenChange={(o) => {
          // Closing the confirmation returns to the form rather than throwing
          // the work away: it is the natural place to land after reading the
          // LDIF and deciding something needs changing.
          if (!o) setChange(null);
        }}
        title="Create this entry"
        onApplied={(result) => {
          setChange(null);
          onOpenChange(false);
          onCreated(result.storedDn ?? result.dn);
        }}
      />
    </>
  );
}

function StepBar({ current }: { current: number }) {
  return (
    <ol className="flex shrink-0 items-center gap-1 border-b border-border px-5 pb-2 text-xs">
      {steps.map(([id, label], i) => (
        <li key={id} className="flex items-center gap-1">
          {i > 0 ? <span className="text-muted-foreground/50">›</span> : null}
          <span
            className={cn(
              "rounded px-1.5 py-0.5",
              i === current
                ? "bg-accent font-medium text-accent-foreground"
                : i < current
                  ? "text-foreground"
                  : "text-muted-foreground",
            )}
          >
            {label}
          </span>
        </li>
      ))}
    </ol>
  );
}

function KindStep({
  views,
  loading,
  selected,
  browsingAll,
  onPick,
  onBrowseAll,
}: {
  views: ObjectView[];
  loading: boolean;
  selected: ObjectView | null;
  browsingAll: boolean;
  onPick: (v: ObjectView) => void;
  onBrowseAll: () => void;
}) {
  if (loading) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Reading the schema…
      </p>
    );
  }
  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        What kind of entry is this? Each option narrows the object classes on
        the next step to the ones this directory actually defines for it.
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        {views.map((v) => (
          <button
            key={v.id}
            type="button"
            onClick={() => onPick(v)}
            className={cn(
              "rounded-lg border p-3 text-left transition-colors",
              selected?.id === v.id
                ? "border-primary bg-primary/8"
                : "border-border hover:bg-accent/60",
            )}
          >
            <div className="font-medium">{singular(v.label)}</div>
            {v.description ? (
              <div className="mt-0.5 text-xs text-muted-foreground">{v.description}</div>
            ) : null}
            <div className="mt-1.5 flex flex-wrap gap-1">
              {(v.createClasses ?? []).slice(0, 4).map((c) => (
                <Badge key={c} variant="secondary" className="font-dn text-[0.68rem]">
                  {c}
                </Badge>
              ))}
              {(v.createClasses ?? []).length > 4 ? (
                <Badge variant="outline" className="text-[0.68rem]">
                  +{(v.createClasses ?? []).length - 4}
                </Badge>
              ) : null}
            </div>
          </button>
        ))}
        <button
          type="button"
          onClick={onBrowseAll}
          className={cn(
            "rounded-lg border p-3 text-left transition-colors",
            browsingAll ? "border-primary bg-primary/8" : "border-border hover:bg-accent/60",
          )}
        >
          <div className="font-medium">Something else</div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            Choose from every structural class this server publishes.
          </div>
        </button>
      </div>
    </div>
  );
}

function ClassStep({
  offered,
  loading,
  auxiliaries,
  needAuxiliaries,
  structural,
  auxiliary,
  onStructural,
  onAuxiliary,
}: {
  offered: string[];
  loading: boolean;
  auxiliaries: string[];
  needAuxiliaries: () => void;
  structural: string;
  auxiliary: string[];
  onStructural: (name: string) => void;
  onAuxiliary: (names: string[]) => void;
}) {
  const [filter, setFilter] = useState("");
  const shown = offered.filter((c) => c.toLowerCase().includes(filter.toLowerCase()));

  useEffect(() => {
    // The auxiliary list comes from the full schema, which is only fetched when
    // it is needed. Asking for it here keeps the first step's request cheap.
    needAuxiliaries();
  }, [needAuxiliaries]);

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label>Structural class</Label>
        <p className="text-xs text-muted-foreground">
          Every entry has exactly one. It decides what the entry may hold, and it
          cannot be changed afterwards without recreating the entry.
        </p>
        {offered.length > 8 ? (
          <Input
            value={filter}
            placeholder="filter"
            className="font-dn"
            onChange={(e) => setFilter(e.target.value)}
          />
        ) : null}
        {loading ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Reading the schema…
          </p>
        ) : (
          <div className="flex max-h-56 flex-wrap gap-1.5 overflow-y-auto rounded-md border border-border p-2">
            {shown.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => onStructural(c)}
                className={cn(
                  "rounded-md border px-2 py-0.5 font-mono text-xs transition-colors",
                  structural === c
                    ? "border-primary bg-primary/12 text-primary"
                    : "border-border hover:bg-accent",
                )}
              >
                {c}
              </button>
            ))}
            {shown.length === 0 ? (
              <p className="p-1 text-sm text-muted-foreground">
                Nothing here matches that.
              </p>
            ) : null}
          </div>
        )}
      </div>

      <div className="space-y-2">
        <Label>Auxiliary classes</Label>
        <p className="text-xs text-muted-foreground">
          Optional. Each one adds attributes the entry may then hold.
        </p>
        <div className="flex max-h-32 flex-wrap gap-1.5 overflow-y-auto rounded-md border border-border p-2">
          {auxiliaries.map((c) => {
            const on = auxiliary.includes(c);
            return (
              <button
                key={c}
                type="button"
                onClick={() =>
                  onAuxiliary(on ? auxiliary.filter((n) => n !== c) : [...auxiliary, c])
                }
                className={cn(
                  "rounded-md border px-2 py-0.5 font-mono text-xs transition-colors",
                  on
                    ? "border-primary bg-primary/12 text-primary"
                    : "border-border hover:bg-accent",
                )}
              >
                {c}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function DetailsStep({
  parentDN,
  requirementsPending,
  rdnAttr,
  rdnValue,
  onRdnAttr,
  onRdnValue,
  must,
  added,
  availableToAdd,
  onAdd,
  onRemove,
  values,
  onValues,
  kindOf,
  pickerBase,
}: {
  parentDN: string;
  requirementsPending: boolean;
  rdnAttr: string;
  rdnValue: string;
  onRdnAttr: (a: string) => void;
  onRdnValue: (v: string) => void;
  must: string[];
  added: string[];
  availableToAdd: string[];
  onAdd: (name: string) => void;
  onRemove: (name: string) => void;
  values: Record<string, string[]>;
  onValues: (name: string, next: string[]) => void;
  kindOf: (name: string) => AttributeKind | undefined;
  pickerBase: string;
}) {
  if (requirementsPending) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Working out what this entry needs…
      </p>
    );
  }

  const fallback = (name: string): AttributeKind => ({
    name,
    kind: "string",
    known: false,
  });

  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label>Name</Label>
        <div className="flex items-center gap-2">
          <select
            value={rdnAttr}
            onChange={(e) => onRdnAttr(e.target.value)}
            className="h-9 rounded-md border border-input bg-card px-2 font-dn text-sm shadow-xs focus:outline-none focus:ring-2 focus:ring-ring"
          >
            {[...new Set([rdnAttr, ...must, ...availableToAdd])]
              .filter((n) => n !== "")
              .map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
          </select>
          <span className="text-muted-foreground">=</span>
          <Input
            value={rdnValue}
            className="font-dn"
            placeholder="the value that names this entry"
            onChange={(e) => onRdnValue(e.target.value)}
          />
        </div>
        {rdnValue.trim() !== "" ? (
          <p className="font-dn text-xs text-muted-foreground">
            {rdnAttr}={escapeRDNValue(rdnValue)},{parentDN}
          </p>
        ) : null}
      </div>

      {must.length ? (
        <div className="space-y-3">
          <Label>Required</Label>
          {must.map((name) => (
            <AttributeEditor
              key={name}
              name={name}
              kind={kindOf(name) ?? fallback(name)}
              required
              values={
                values[name] ??
                // The naming value is already decided above; showing it here
                // pre-filled saves typing it twice and keeps the two in step.
                (name.toLowerCase() === rdnAttr.toLowerCase() ? [rdnValue] : [""])
              }
              pickerBase={pickerBase}
              onChange={(next: string[]) => onValues(name, next)}
            />
          ))}
        </div>
      ) : null}

      {added.length ? (
        <div className="space-y-3">
          <Label>Optional</Label>
          {added.map((name) => (
            <AttributeEditor
              key={name}
              name={name}
              kind={kindOf(name) ?? fallback(name)}
              required={false}
              values={values[name] ?? [""]}
              pickerBase={pickerBase}
              isNew
              onChange={(next: string[]) => onValues(name, next)}
              onRemove={() => onRemove(name)}
            />
          ))}
        </div>
      ) : null}

      <AddAttribute available={availableToAdd} onAdd={onAdd} />
    </div>
  );
}

/** singular turns a view's plural label into the thing being created. */
function singular(label: string): string {
  if (label.endsWith("units")) return label.slice(0, -1);
  if (label.endsWith("s")) return label.slice(0, -1);
  return label;
}
