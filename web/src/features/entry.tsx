import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Ban,
  Download,
  Eye,
  EyeOff,
  Loader2,
  Lock,
  Pencil,
  Plus,
  KeyRound,
  Copy,
  Search as SearchIconAlias,
  Tag,
  Trash2,
  X,
} from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type {
  AttributeValue,
  ChangeMod,
  ChangeRequest,
  EntryAttribute,
  EntryView,
  SessionInfo,
} from "@/lib/api";
import {
  displayText,
  formatGeneralizedTime,
  inputType,
  multiline,
  parentOf,
  rdnOf,
  textValue,
} from "@/lib/values";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input, Textarea } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Checkbox,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui";
import { ChangeDialog, ErrorNote } from "@/components/change-dialog";
import { DnPicker } from "@/components/dn-picker";
import { CopyEntryDialog, SetPasswordDialog } from "@/features/entry-dialogs";
import { CopyButton, LdifBlock } from "@/components/ldif-block";

export function EntryPanel({
  dn,
  onNavigate,
  onDeleted,
  readOnly,
}: {
  dn: string;
  onNavigate: (dn: string) => void;
  onDeleted: (parentDN: string) => void;
  readOnly: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const [showLdif, setShowLdif] = useState(false);

  const entry = useQuery({
    queryKey: ["entry", dn],
    queryFn: async () =>
      unwrap(await api.GET("/entry", { params: { query: { dn } } })),
    // Refetching under an open editor is deliberate. The editor holds its own
    // frozen baseline, so a refetch can no longer disturb what is being typed,
    // and it is the only thing that can notice somebody else changing the entry
    // underneath. Turning it off while editing would make the drift warning
    // below unreachable, which is the opposite of what it is for.
    refetchOnWindowFocus: true,
  });

  // Leaving edit mode when the DN changes prevents an edit begun on one entry
  // from being applied to another.
  useEffect(() => setEditing(false), [dn]);

  if (entry.isPending) {
    return (
      <div className="flex items-center gap-2 p-8 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Reading {dn}
      </div>
    );
  }
  if (entry.isError) {
    return (
      <div className="p-6">
        <ErrorNote title="This entry could not be read" error={entry.error as ApiFailure} />
      </div>
    );
  }

  const data = entry.data;

  return (
    <div className="flex h-full flex-col">
      <EntryHeader
        entry={data}
        editing={editing}
        readOnly={readOnly}
        onEdit={() => setEditing(true)}
        onCancelEdit={() => setEditing(false)}
        onNavigate={onNavigate}
        onDeleted={onDeleted}
        showLdif={showLdif}
        onToggleLdif={() => setShowLdif((v) => !v)}
      />

      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        {showLdif ? (
          <div className="mb-5">
            <LdifBlock
              text={data.ldif ?? ""}
              filename={`${rdnOf(data.dn).replace(/[^\w.-]/g, "-")}.ldif`}
            />
            <p className="mt-2 text-xs text-muted-foreground">
              The entry as an LDIF content record. Sensitive attributes are
              omitted; use Export if you need them.
            </p>
          </div>
        ) : null}

        {editing ? (
          // Keyed by DN so that navigating to another entry starts a fresh
          // editor rather than carrying the previous one's baseline across.
          <EntryEditor
            key={data.dn}
            entry={data}
            onDone={() => setEditing(false)}
            onNavigate={onNavigate}
          />
        ) : (
          <EntryReader entry={data} onNavigate={onNavigate} />
        )}
      </div>
    </div>
  );
}

/* --- header --------------------------------------------------------------- */

function EntryHeader({
  entry,
  editing,
  readOnly,
  onEdit,
  onCancelEdit,
  onNavigate,
  onDeleted,
  showLdif,
  onToggleLdif,
}: {
  entry: EntryView;
  editing: boolean;
  readOnly: boolean;
  onEdit: () => void;
  onCancelEdit: () => void;
  onNavigate: (dn: string) => void;
  onDeleted: (parentDN: string) => void;
  showLdif: boolean;
  onToggleLdif: () => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [deleteChange, setDeleteChange] = useState<ChangeRequest | null>(null);
  const [adding, setAdding] = useState(false);
  const [copying, setCopying] = useState(false);
  const [settingPassword, setSettingPassword] = useState(false);

  // Only offer a password control where the server can actually perform the
  // operation, rather than offering one that can only fail.
  const queryClient = useQueryClient();
  const canSetPassword =
    queryClient.getQueryData<SessionInfo>(["session"])?.capabilities
      ?.passwordModify === true;

  return (
    <div className="border-b border-border px-5 py-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-base font-semibold">{rdnOf(entry.dn)}</h2>
            {entry.requirements?.structural ? (
              <Badge variant="secondary">{entry.requirements.structural}</Badge>
            ) : (
              <Badge variant="warning">no single structural class</Badge>
            )}
            {entry.hasChildren ? <Badge variant="outline">has children</Badge> : null}
          </div>
          <div className="mt-1 flex items-center gap-1.5">
            <p className="truncate font-dn text-muted-foreground" title={entry.dn}>
              {entry.dn}
            </p>
            <CopyButton text={entry.dn} />
          </div>
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-1.5">
          <Button variant="ghost" size="sm" onClick={onToggleLdif}>
            {showLdif ? <EyeOff /> : <Eye />}
            LDIF
          </Button>
          <ExportButton dn={entry.dn} />
          {readOnly ? (
            <Badge variant="outline" className="gap-1">
              <Lock className="size-3" />
              read-only
            </Badge>
          ) : editing ? (
            <Button variant="outline" size="sm" onClick={onCancelEdit}>
              <X />
              Cancel
            </Button>
          ) : (
            <>
              <Button variant="outline" size="sm" onClick={() => setAdding(true)}>
                <Plus />
                Child
              </Button>
              <Button variant="outline" size="sm" onClick={() => setCopying(true)}>
                <Copy />
                Copy
              </Button>
              {canSetPassword ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => setSettingPassword(true)}
                >
                  <KeyRound />
                  Password
                </Button>
              ) : null}
              <Button variant="outline" size="sm" onClick={() => setRenaming(true)}>
                <Tag />
                Rename
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="text-destructive hover:bg-destructive/10"
                onClick={() =>
                  setDeleteChange({ dn: entry.dn, type: "delete" })
                }
              >
                <Trash2 />
                Delete
              </Button>
              <Button size="sm" onClick={onEdit}>
                <Pencil />
                Edit
              </Button>
            </>
          )}
        </div>
      </div>

      {entry.requirements?.unknown?.length ? (
        <p className="mt-2 rounded-md border border-warning/40 bg-warning/10 px-2.5 py-1.5 text-xs text-warning-tint-foreground">
          The schema does not define these object classes on this entry:{" "}
          <span className="font-mono">{entry.requirements.unknown.join(", ")}</span>.
          Their attributes cannot be checked.
        </p>
      ) : null}

      <RenameDialog
        entry={entry}
        open={renaming}
        onOpenChange={setRenaming}
        onRenamed={onNavigate}
      />
      <NewEntryDialog
        parentDN={entry.dn}
        open={adding}
        onOpenChange={setAdding}
        onCreated={onNavigate}
      />
      <CopyEntryDialog
        entry={entry}
        open={copying}
        onOpenChange={setCopying}
        onCreated={onNavigate}
      />
      <SetPasswordDialog
        dn={entry.dn}
        open={settingPassword}
        onOpenChange={setSettingPassword}
      />
      <ChangeDialog
        change={deleteChange}
        open={deleteChange !== null}
        onOpenChange={(open) => !open && setDeleteChange(null)}
        title="Delete this entry"
        destructive
        onApplied={() => onDeleted(parentOf(entry.dn))}
      />
    </div>
  );
}

function ExportButton({ dn }: { dn: string }) {
  const [open, setOpen] = useState(false);
  const [scope, setScope] = useState<"base" | "one" | "sub">("base");
  const [operational, setOperational] = useState(false);
  const [sensitive, setSensitive] = useState(false);

  const download = () => {
    const params = new URLSearchParams({
      dn,
      scope,
      includeOperational: String(operational),
      includeSensitive: String(sensitive),
    });
    // A plain navigation, so the browser's own download handling applies and
    // the session cookie goes with it.
    window.location.href = `/api/v1/export/ldif?${params.toString()}`;
    setOpen(false);
  };

  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        <Download />
        Export
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Export as LDIF</DialogTitle>
            <DialogDescription className="font-dn">{dn}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 px-5 py-4">
            <div className="space-y-1.5">
              <Label>Scope</Label>
              <Select value={scope} onValueChange={(v) => setScope(v as typeof scope)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="base">This entry only</SelectItem>
                  <SelectItem value="one">This entry's children</SelectItem>
                  <SelectItem value="sub">The whole subtree</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <label className="flex items-start gap-2.5 text-sm">
              <Checkbox
                checked={operational}
                onCheckedChange={(v) => setOperational(v === true)}
                className="mt-0.5"
              />
              <span>
                Include operational attributes
                <span className="block text-xs text-muted-foreground">
                  The directory owns these and will refuse to import them back.
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2.5 text-sm">
              <Checkbox
                checked={sensitive}
                onCheckedChange={(v) => setSensitive(v === true)}
                className="mt-0.5"
              />
              <span>
                Include sensitive attributes
                <span className="block text-xs text-muted-foreground">
                  Password hashes and similar. An export ends up in tickets and
                  repositories; leave this off unless you are recreating the
                  entry elsewhere.
                </span>
              </span>
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button onClick={download}>
              <Download />
              Download
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

/* --- read mode ------------------------------------------------------------ */

function EntryReader({
  entry,
  onNavigate,
}: {
  entry: EntryView;
  onNavigate: (dn: string) => void;
}) {
  const groups = useMemo(() => groupAttributes(entry.attributes), [entry.attributes]);

  return (
    <div className="space-y-6">
      {groups.map((group) =>
        group.items.length ? (
          <section key={group.title}>
            <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              {group.title}
            </h3>
            <dl className="divide-y divide-border rounded-lg border border-border">
              {group.items.map((attr) => (
                <AttributeRow key={attr.name} attr={attr} onNavigate={onNavigate} />
              ))}
            </dl>
          </section>
        ) : null,
      )}
    </div>
  );
}

function AttributeRow({
  attr,
  onNavigate,
}: {
  attr: EntryAttribute;
  onNavigate: (dn: string) => void;
}) {
  const [revealed, setRevealed] = useState(false);

  return (
    <div className="grid grid-cols-1 gap-1 px-3 py-2 sm:grid-cols-[minmax(11rem,15rem)_1fr] sm:gap-4">
      <dt className="flex items-start gap-1.5 pt-0.5">
        <span
          className="font-dn font-medium"
          title={attr.kind.desc ?? attr.kind.syntaxLabel ?? undefined}
        >
          {attr.name}
        </span>
        {attr.required ? (
          <span className="text-destructive" title="Required by an object class">
            *
          </span>
        ) : null}
        {attr.kind.known === false ? (
          <Badge variant="warning" title="Not defined in this server's schema">
            ?
          </Badge>
        ) : null}
        {attr.kind.readOnly ? <Lock className="mt-0.5 size-3 text-muted-foreground" /> : null}
      </dt>
      <dd className="min-w-0 space-y-1">
        {attr.withheld ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Ban className="size-3.5" />
            <span>
              {attr.valueCount === 1
                ? "set, and withheld"
                : `${attr.valueCount ?? 0} values, withheld`}
            </span>
            <span className="text-xs">
              (a secret; Alder never sends it to the browser)
            </span>
          </div>
        ) : (
          attr.values.map((value, i) => (
            <ValueDisplay
              key={i}
              value={value}
              kindName={attr.kind.kind}
              revealed={revealed}
              onReveal={() => setRevealed(true)}
              onNavigate={onNavigate}
            />
          ))
        )}
        {attr.values.length === 0 && !attr.withheld ? (
          <span className="text-sm italic text-muted-foreground">no values</span>
        ) : null}
      </dd>
    </div>
  );
}

function ValueDisplay({
  value,
  kindName,
  revealed,
  onReveal,
  onNavigate,
}: {
  value: AttributeValue;
  kindName: string;
  revealed: boolean;
  onReveal: () => void;
  onNavigate: (dn: string) => void;
}) {
  if (kindName === "dn" && value.text) {
    return (
      <button
        type="button"
        className="block max-w-full truncate text-left font-dn text-primary hover:underline"
        onClick={() => onNavigate(value.text as string)}
        title={`Go to ${value.text}`}
      >
        {value.text}
      </button>
    );
  }

  if (kindName === "image" && value.base64) {
    return (
      <img
        src={`data:image/jpeg;base64,${value.base64}`}
        alt=""
        className="max-h-32 rounded-md border border-border"
      />
    );
  }

  if (value.base64 !== undefined) {
    return (
      <div className="flex items-center gap-2">
        <span className="font-dn text-muted-foreground">{displayText(value)}</span>
        {revealed ? (
          <code className="max-w-full truncate rounded bg-muted px-1.5 py-0.5 font-mono text-[11px]">
            {value.base64.slice(0, 96)}
            {value.base64.length > 96 ? "…" : ""}
          </code>
        ) : (
          <Button variant="ghost" size="sm" onClick={onReveal} className="h-6 text-xs">
            show base64
          </Button>
        )}
      </div>
    );
  }

  const text = value.text ?? "";
  if (kindName === "time") {
    const pretty = formatGeneralizedTime(text);
    if (pretty) {
      return (
        <span className="text-sm">
          {pretty} <span className="font-dn text-muted-foreground">({text})</span>
        </span>
      );
    }
  }
  if (kindName === "boolean") {
    return (
      <Badge variant={text.toUpperCase() === "TRUE" ? "success" : "outline"}>
        {text}
      </Badge>
    );
  }

  return <span className="block whitespace-pre-wrap break-words font-dn">{text}</span>;
}

function groupAttributes(attributes: EntryAttribute[]) {
  return [
    {
      title: "Required",
      items: attributes.filter((a) => a.required && !a.kind.operational),
    },
    {
      title: "Optional",
      items: attributes.filter((a) => !a.required && !a.kind.operational),
    },
    {
      title: "Operational — the directory owns these",
      items: attributes.filter((a) => a.kind.operational),
    },
  ];
}

/* --- edit mode ------------------------------------------------------------ */

type Draft = Record<string, string[]>;

function EntryEditor({
  entry,
  onDone,
  onNavigate,
}: {
  entry: EntryView;
  onDone: () => void;
  onNavigate: (dn: string) => void;
}) {
  const editable = useMemo(
    () =>
      entry.attributes.filter(
        (a) => !a.kind.operational && !a.kind.readOnly && !a.withheld,
      ),
    [entry.attributes],
  );

  // An attribute whose values cannot round-trip as text is shown but not
  // edited. Offering a text box for a JPEG is how a JPEG gets destroyed.
  const binaryAttrs = editable.filter((a) => a.values.some((v) => v.base64 !== undefined));
  const textAttrs = editable.filter((a) => !a.values.some((v) => v.base64 !== undefined));

  // The baseline is captured once, when editing begins, and never updated.
  //
  // Deriving it from the live query instead meant that any background refetch
  // -- a window focus is enough -- produced a new attributes array and silently
  // reset the form, discarding whatever the user had typed. The entry the user
  // started from is the entry the change is computed against; drift is
  // reported below rather than applied behind their back.
  const queryClient = useQueryClient();
  const pickerBase = useMemo(() => {
    const info = queryClient.getQueryData<SessionInfo>(["session"]);
    const contexts = info?.capabilities?.namingContexts ?? [];
    return (
      contexts.find((c) => entry.dn.toLowerCase().endsWith(c.toLowerCase())) ??
      contexts[0]
    );
  }, [queryClient, entry.dn]);

  const [original] = useState<Draft>(() => snapshot(entry.attributes));
  const [draft, setDraft] = useState<Draft>(() => snapshot(entry.attributes));
  const [added, setAdded] = useState<string[]>([]);
  const [change, setChange] = useState<ChangeRequest | null>(null);

  // If the entry changed in the directory while it was being edited, say so.
  // Applying regardless is legitimate -- a replace says what the attribute ends
  // up as -- but the user should know they are overwriting someone.
  const drifted = useMemo(() => {
    const current = snapshot(entry.attributes);
    const names = new Set([...Object.keys(original), ...Object.keys(current)]);
    return [...names].filter(
      (name) => JSON.stringify(original[name] ?? []) !== JSON.stringify(current[name] ?? []),
    );
  }, [entry.attributes, original]);

  const available = useMemo(() => {
    const present = new Set(
      [...entry.attributes.map((a) => a.name.toLowerCase()), ...added.map((a) => a.toLowerCase())],
    );
    const candidates = [
      ...(entry.requirements?.must ?? []),
      ...(entry.requirements?.may ?? []),
    ];
    return candidates.filter((name) => !present.has(name.toLowerCase())).sort();
  }, [entry.attributes, entry.requirements, added]);

  const mods = useMemo(
    () => computeMods(original, draft, added),
    [original, draft, added],
  );

  const kindOf = (name: string) =>
    entry.attributes.find((a) => a.name.toLowerCase() === name.toLowerCase())?.kind;

  return (
    <div className="space-y-5">
      <div className="rounded-md border border-primary/30 bg-primary/6 px-3 py-2 text-sm">
        Editing. Nothing is sent until you review the LDIF and confirm it.
      </div>

      {drifted.length ? (
        <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-warning-tint-foreground">
          This entry changed in the directory since you started editing:{" "}
          <span className="font-mono">{drifted.join(", ")}</span>. Your edits are
          intact; applying them will overwrite the newer values.
        </div>
      ) : null}

      <div className="space-y-4">
        {textAttrs.map((attr) => (
          <AttributeEditor
            key={attr.name}
            name={attr.name}
            kind={attr.kind}
            required={attr.required === true}
            values={draft[attr.name] ?? []}
            pickerBase={pickerBase}
            onChange={(values) => setDraft((d) => ({ ...d, [attr.name]: values }))}
          />
        ))}

        {added.map((name) => (
          <AttributeEditor
            key={name}
            name={name}
            kind={
              kindOf(name) ?? {
                name,
                kind: "string" as const,
                known: false,
              }
            }
            required={(entry.requirements?.must ?? []).includes(name)}
            values={draft[name] ?? [""]}
            pickerBase={pickerBase}
            isNew
            onRemove={() => {
              setAdded((a) => a.filter((n) => n !== name));
              setDraft((d) => {
                const next = { ...d };
                delete next[name];
                return next;
              });
            }}
            onChange={(values) => setDraft((d) => ({ ...d, [name]: values }))}
          />
        ))}
      </div>

      {binaryAttrs.length ? (
        <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
          Not editable here because their values are binary:{" "}
          <span className="font-mono">
            {binaryAttrs.map((a) => a.name).join(", ")}
          </span>
          . Change them by importing LDIF, where the encoding is explicit.
        </div>
      ) : null}

      <AddAttribute
        available={available}
        onAdd={(name) => {
          setAdded((a) => [...a, name]);
          setDraft((d) => ({ ...d, [name]: [""] }));
        }}
      />

      <div className="sticky bottom-0 flex items-center justify-between gap-3 border-t border-border bg-background/95 py-3 backdrop-blur">
        <p className="text-sm text-muted-foreground">
          {mods.length === 0
            ? "Nothing has changed yet."
            : `${mods.length} modification${mods.length === 1 ? "" : "s"} pending.`}
        </p>
        <div className="flex gap-2">
          <Button variant="outline" onClick={onDone}>
            Cancel
          </Button>
          <Button
            disabled={mods.length === 0}
            onClick={() => setChange({ dn: entry.dn, type: "modify", mods })}
          >
            Review {mods.length || ""} change{mods.length === 1 ? "" : "s"}
          </Button>
        </div>
      </div>

      <ChangeDialog
        change={change}
        open={change !== null}
        onOpenChange={(open) => !open && setChange(null)}
        onApplied={(result) => {
          onDone();
          onNavigate(result.dn);
        }}
      />
    </div>
  );
}

function AttributeEditor({
  name,
  kind,
  required,
  values,
  onChange,
  onRemove,
  isNew,
  pickerBase,
}: {
  name: string;
  kind: EntryAttribute["kind"];
  required: boolean;
  values: string[];
  onChange: (values: string[]) => void;
  onRemove?: () => void;
  isNew?: boolean;
  /** Search base for the DN picker, when this attribute holds DNs. */
  pickerBase?: string;
}) {
  const single = kind.singleValue === true;
  const asText = multiline(kind, values.map(textValue));
  // A DN-valued attribute gets a picker. Typing a full distinguished name by
  // hand is the step where "add this person to that group" goes wrong.
  const isDn = kind.kind === "dn" && pickerBase !== undefined;
  const [picking, setPicking] = useState<number | null>(null);

  const setAt = (i: number, v: string) => {
    const next = [...values];
    next[i] = v;
    onChange(next);
  };

  return (
    <div
      className={cn(
        "rounded-lg border p-3",
        isNew ? "border-primary/40 bg-primary/5" : "border-border",
      )}
    >
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Label className="font-dn">
          {name}
          {required ? <span className="ml-0.5 text-destructive">*</span> : null}
        </Label>
        {single ? <Badge variant="outline">single-valued</Badge> : null}
        {kind.known === false ? (
          <Badge variant="warning">not in the schema</Badge>
        ) : kind.syntaxLabel ? (
          <Badge variant="secondary">{kind.syntaxLabel}</Badge>
        ) : null}
        {kind.sensitive ? <Badge variant="destructive">secret</Badge> : null}
        {onRemove ? (
          <Button
            variant="ghost"
            size="icon-sm"
            className="ml-auto"
            onClick={onRemove}
            aria-label={`Remove ${name}`}
          >
            <X />
          </Button>
        ) : null}
      </div>

      <div className="space-y-2">
        {values.map((value, i) => (
          <div key={i} className="flex items-start gap-2">
            {kind.kind === "boolean" ? (
              <Select value={value || "TRUE"} onValueChange={(v) => setAt(i, v)}>
                <SelectTrigger className="max-w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="TRUE">TRUE</SelectItem>
                  <SelectItem value="FALSE">FALSE</SelectItem>
                </SelectContent>
              </Select>
            ) : asText ? (
              <Textarea
                value={value}
                rows={3}
                className="font-dn"
                onChange={(e) => setAt(i, e.target.value)}
              />
            ) : (
              <Input
                value={value}
                type={inputType(kind)}
                maxLength={kind.maxLength}
                className="font-dn"
                onChange={(e) => setAt(i, e.target.value)}
              />
            )}
            {values.length > 1 ? (
              <Button
                variant="ghost"
                size="icon"
                onClick={() => onChange(values.filter((_, j) => j !== i))}
                aria-label="Remove this value"
              >
                <X />
              </Button>
            ) : null}
          </div>
        ))}
      </div>

      <div className="mt-2 flex items-center gap-1">
        {!single ? (
          <Button variant="ghost" size="sm" onClick={() => onChange([...values, ""])}>
            <Plus />
            Add a value
          </Button>
        ) : null}
        {isDn ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setPicking(single ? 0 : values.length)}
          >
            <SearchIconAlias />
            Find an entry
          </Button>
        ) : null}
      </div>

      {isDn && picking !== null ? (
        <DnPicker
          open
          onOpenChange={(o) => !o && setPicking(null)}
          baseDn={pickerBase as string}
          title={`Choose a value for ${name}`}
          suggestedFilter={suggestedFilterFor(name)}
          onPick={(dn) => {
            const next = [...values];
            next[picking] = dn;
            // Picking into the slot past the end is how "add another" works.
            onChange(next.map((v) => v ?? ""));
            setPicking(null);
          }}
        />
      ) : null}
    </div>
  );
}

/**
 * suggestedFilterFor narrows the picker to what the attribute usually holds.
 *
 * It is a starting point shown in an editable box, not a rule: a group can
 * legitimately contain another group, and the user can widen it.
 */
function suggestedFilterFor(attribute: string): string {
  switch (attribute.toLowerCase()) {
    case "member":
    case "uniquemember":
    case "memberof":
      return "(|(objectClass=person)(objectClass=groupOfNames))";
    case "manager":
    case "secretary":
    case "owner":
      return "(objectClass=person)";
    default:
      return "(objectClass=*)";
  }
}

function AddAttribute({
  available,
  onAdd,
}: {
  available: string[];
  onAdd: (name: string) => void;
}) {
  const [value, setValue] = useState("");
  if (available.length === 0) return null;
  return (
    <div className="flex items-end gap-2 rounded-lg border border-dashed border-border p-3">
      <div className="flex-1 space-y-1.5">
        <Label>Add an attribute</Label>
        <p className="text-xs text-muted-foreground">
          Only attributes this entry's object classes permit are offered.
        </p>
        <Select value={value} onValueChange={setValue}>
          <SelectTrigger className="max-w-sm">
            <SelectValue placeholder={`${available.length} available`} />
          </SelectTrigger>
          <SelectContent>
            {available.map((name) => (
              <SelectItem key={name} value={name}>
                {name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <Button
        variant="outline"
        disabled={!value}
        onClick={() => {
          onAdd(value);
          setValue("");
        }}
      >
        <Plus />
        Add
      </Button>
    </div>
  );
}

/**
 * snapshot reduces an entry's attributes to the editable text values.
 *
 * Operational, read-only, withheld and binary-valued attributes are excluded
 * deliberately. Excluding binary ones matters: their values arrive as base64
 * and would map to empty strings here, which computeMods would then read as
 * "the user cleared this attribute" and turn into a delete.
 */
function snapshot(attributes: EntryAttribute[]): Draft {
  const out: Draft = {};
  for (const a of attributes) {
    if (a.kind.operational || a.kind.readOnly || a.withheld) continue;
    if (a.values.some((v) => v.base64 !== undefined)) continue;
    out[a.name] = a.values.map((v) => v.text ?? "");
  }
  return out;
}

/**
 * computeMods diffs the draft against the original values.
 *
 * An attribute that gained values only becomes an `add` of those values, and
 * one that lost values only becomes a `delete` of those. Anything else -- a
 * mixed edit, a reordering, a single-valued attribute -- becomes a `replace`
 * carrying the whole new set, and an emptied attribute becomes a valueless
 * `delete`.
 *
 * The narrow operations are not a cosmetic choice. Replacing is destructive
 * under concurrency: adding one person to a fifty-person group by replacing the
 * whole member list silently removes anyone another administrator added since
 * the entry was read, and group membership is the most concurrently edited
 * attribute a directory has. An `add` of the one value both administrators
 * intended succeeds for both.
 *
 * It also makes the LDIF say what was meant. "add: member" with one line is the
 * change; fifty-one lines of "replace: member" is the same change buried in its
 * own context.
 */
export function computeMods(original: Draft, draft: Draft, added: string[]): ChangeMod[] {
  const mods: ChangeMod[] = [];
  const names = new Set([...Object.keys(original), ...added]);

  for (const name of names) {
    const before = (original[name] ?? []).filter((v) => v !== "");
    const after = (draft[name] ?? []).filter((v) => v !== "");

    if (sameValues(before, after)) continue;

    if (after.length === 0) {
      mods.push({ op: "delete", name });
      continue;
    }

    const gained = after.filter((v) => !before.includes(v));
    const lost = before.filter((v) => !after.includes(v));

    if (gained.length > 0 && lost.length === 0) {
      mods.push({ op: "add", name, values: gained.map(textValue) });
      continue;
    }
    if (lost.length > 0 && gained.length === 0) {
      mods.push({ op: "delete", name, values: lost.map(textValue) });
      continue;
    }

    // A mixed edit, or a reordering with the same members. Replace states the
    // end result, which is the only thing that describes it honestly.
    mods.push({ op: "replace", name, values: after.map(textValue) });
  }
  return mods;
}

function sameValues(a: string[], b: string[]) {
  const left = a.filter((v) => v !== "");
  const right = b.filter((v) => v !== "");
  if (left.length !== right.length) return false;
  return left.every((v, i) => v === right[i]);
}

/* --- rename --------------------------------------------------------------- */

function RenameDialog({
  entry,
  open,
  onOpenChange,
  onRenamed,
}: {
  entry: EntryView;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRenamed: (dn: string) => void;
}) {
  const [newRdn, setNewRdn] = useState(rdnOf(entry.dn));
  const [deleteOld, setDeleteOld] = useState(true);
  const [newSuperior, setNewSuperior] = useState("");
  const [change, setChange] = useState<ChangeRequest | null>(null);

  useEffect(() => {
    if (open) {
      setNewRdn(rdnOf(entry.dn));
      setNewSuperior("");
      setDeleteOld(true);
    }
  }, [open, entry.dn]);

  return (
    <>
      <Dialog open={open && change === null} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rename or move</DialogTitle>
            <DialogDescription className="font-dn">{entry.dn}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4 px-5 py-4">
            <div className="space-y-1.5">
              <Label htmlFor="newrdn">New RDN</Label>
              <Input
                id="newrdn"
                value={newRdn}
                className="font-dn"
                onChange={(e) => setNewRdn(e.target.value)}
                placeholder="cn=new name"
              />
              <p className="text-xs text-muted-foreground">
                One RDN, written as <code className="font-mono">attribute=value</code>.
              </p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="newsuperior">New parent (optional)</Label>
              <Input
                id="newsuperior"
                value={newSuperior}
                className="font-dn"
                onChange={(e) => setNewSuperior(e.target.value)}
                placeholder={parentOf(entry.dn)}
              />
              <p className="text-xs text-muted-foreground">
                Leave empty to rename in place. Fill it in to move the entry.
              </p>
            </div>
            <label className="flex items-start gap-2.5 text-sm">
              <Checkbox
                checked={deleteOld}
                onCheckedChange={(v) => setDeleteOld(v === true)}
                className="mt-0.5"
              />
              <span>
                Remove the old RDN value
                <span className="block text-xs text-muted-foreground">
                  With this off, the entry keeps its previous naming value as an
                  ordinary attribute.
                </span>
              </span>
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              disabled={!newRdn.includes("=")}
              onClick={() =>
                setChange({
                  dn: entry.dn,
                  type: "modrdn",
                  newRdn,
                  deleteOldRdn: deleteOld,
                  ...(newSuperior ? { newSuperior } : {}),
                })
              }
            >
              Review the change
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ChangeDialog
        change={change}
        open={change !== null}
        onOpenChange={(o) => {
          if (!o) {
            setChange(null);
            onOpenChange(false);
          }
        }}
        title="Rename this entry"
        onApplied={(result) => onRenamed(result.dn)}
      />
    </>
  );
}

/* --- create --------------------------------------------------------------- */

export function NewEntryDialog({
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
  const [structural, setStructural] = useState("");
  const [auxiliary, setAuxiliary] = useState<string[]>([]);
  const [rdn, setRdn] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [change, setChange] = useState<ChangeRequest | null>(null);

  const schema = useQuery({
    queryKey: ["schema"],
    enabled: open,
    queryFn: async () => unwrap(await api.GET("/schema")),
  });

  const detail = useQuery({
    queryKey: ["objectclass", structural],
    enabled: open && structural !== "",
    queryFn: async () =>
      unwrap(
        await api.GET("/schema/objectclasses/{name}", {
          params: { path: { name: structural } },
        }),
      ),
  });

  useEffect(() => {
    if (open) {
      setStructural("");
      setAuxiliary([]);
      setRdn("");
      setValues({});
    }
  }, [open]);

  const structurals = (schema.data?.objectClasses ?? []).filter(
    (c) => c.kind === "STRUCTURAL",
  );
  const auxiliaries = (schema.data?.objectClasses ?? []).filter(
    (c) => c.kind === "AUXILIARY",
  );

  const must = useMemo(() => {
    const set = new Set<string>([
      ...(detail.data?.must ?? []),
      ...(detail.data?.inheritedMust ?? []),
    ]);
    set.delete("objectClass");
    return [...set].sort();
  }, [detail.data]);

  const build = (): ChangeRequest | null => {
    if (!rdn.includes("=") || !structural) return null;
    const attributes = [
      {
        name: "objectClass",
        values: ["top", structural, ...auxiliary].map(textValue),
      },
      ...must
        .filter((name) => (values[name] ?? "").trim() !== "")
        .map((name) => ({ name, values: [textValue(values[name] as string)] })),
    ];
    // The RDN's own attribute has to be present with the RDN's value; a
    // directory rejects an entry whose naming attribute is missing, and making
    // the user type it twice is a papercut with no upside.
    const [rdnAttr, ...rdnRest] = rdn.split("=");
    const rdnValue = rdnRest.join("=");
    const attrName = (rdnAttr ?? "").trim();
    if (attrName && !attributes.some((a) => a.name.toLowerCase() === attrName.toLowerCase())) {
      attributes.push({ name: attrName, values: [textValue(rdnValue)] });
    }
    return { dn: `${rdn},${parentDN}`, type: "add", attributes };
  };

  const candidate = build();

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

          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
            <div className="space-y-1.5">
              <Label>Structural object class</Label>
              <Select value={structural} onValueChange={setStructural}>
                <SelectTrigger>
                  <SelectValue placeholder="Choose one" />
                </SelectTrigger>
                <SelectContent>
                  {structurals.map((c) => (
                    <SelectItem key={c.oid} value={c.name}>
                      {c.name}
                      {c.desc ? ` — ${c.desc}` : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                Every entry has exactly one. It decides what the entry may hold.
              </p>
            </div>

            {structural ? (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor="new-rdn">RDN</Label>
                  <Input
                    id="new-rdn"
                    value={rdn}
                    className="font-dn"
                    placeholder="cn=example"
                    onChange={(e) => setRdn(e.target.value)}
                  />
                  {rdn.includes("=") ? (
                    <p className="font-dn text-xs text-muted-foreground">
                      {rdn},{parentDN}
                    </p>
                  ) : null}
                </div>

                <div className="space-y-1.5">
                  <Label>Auxiliary classes (optional)</Label>
                  <div className="flex max-h-32 flex-wrap gap-1.5 overflow-y-auto rounded-md border border-border p-2">
                    {auxiliaries.map((c) => {
                      const on = auxiliary.includes(c.name);
                      return (
                        <button
                          key={c.oid}
                          type="button"
                          onClick={() =>
                            setAuxiliary((prev) =>
                              on
                                ? prev.filter((n) => n !== c.name)
                                : [...prev, c.name],
                            )
                          }
                          className={cn(
                            "rounded-md border px-2 py-0.5 font-mono text-xs transition-colors",
                            on
                              ? "border-primary bg-primary/12 text-primary"
                              : "border-border hover:bg-accent",
                          )}
                        >
                          {c.name}
                        </button>
                      );
                    })}
                  </div>
                </div>

                {must.length ? (
                  <div className="space-y-2">
                    <Label>Required attributes</Label>
                    {must.map((name) => (
                      <div key={name} className="flex items-center gap-2">
                        <span className="w-40 shrink-0 font-dn text-sm">{name}</span>
                        <Input
                          className="font-dn"
                          value={values[name] ?? ""}
                          onChange={(e) =>
                            setValues((v) => ({ ...v, [name]: e.target.value }))
                          }
                        />
                      </div>
                    ))}
                    <p className="text-xs text-muted-foreground">
                      From the object class chain. The RDN's own attribute is
                      filled in for you.
                    </p>
                  </div>
                ) : null}
              </>
            ) : null}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button disabled={!candidate} onClick={() => setChange(candidate)}>
              Review the change
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ChangeDialog
        change={change}
        open={change !== null}
        onOpenChange={(o) => {
          if (!o) {
            setChange(null);
            onOpenChange(false);
          }
        }}
        title="Create this entry"
        onApplied={(result) => onCreated(result.dn)}
      />
    </>
  );
}
