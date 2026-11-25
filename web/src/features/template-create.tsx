import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { FolderSearch, Loader2, Plus } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ChangeRequest, EntryTemplate } from "@/lib/api";
import { allocationLine, newLabel, templatesFor } from "@/lib/templates";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
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
import { ChangeDialog, ErrorNote } from "@/components/change-dialog";
import { DnPicker } from "@/components/dn-picker";
import { safeText } from "@/lib/display";

export const templatesKey = ["templates"] as const;

/** The templates this Alder offers, checked against this directory. */
export function useTemplates() {
  return useQuery({
    queryKey: templatesKey,
    queryFn: async () => unwrap(await api.GET("/templates")),
    // Templates are read when Alder starts and the schema does not change
    // under a session.
    staleTime: Infinity,
  });
}

/**
 * "New user" / "New group": the button on a view's header, offered when a
 * template for that view fits this server.
 */
export function NewFromTemplateButton({
  view,
  defaultParent,
  onCreated,
}: {
  view: string;
  defaultParent: string;
  onCreated: (dn: string) => void;
}) {
  const templates = useTemplates();
  const [open, setOpen] = useState(false);
  const offered = templatesFor(templates.data?.templates, view);
  if (offered.length === 0) return null;
  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <Plus />
        {newLabel(view)}
      </Button>
      <TemplateCreateDialog
        templates={offered}
        defaultParent={defaultParent}
        open={open}
        onOpenChange={setOpen}
        onCreated={onCreated}
      />
    </>
  );
}

/**
 * Creating an entry from a template.
 *
 * Like the creation wizard, it composes an add change and hands it to the
 * same confirmation dialog every write goes through; it has no finish button
 * of its own. What it adds is the number: the server finds the next free
 * uidNumber or gidNumber when the form is reviewed, and the review says which
 * number and why. Closing the review comes back here with everything typed.
 */
export function TemplateCreateDialog({
  templates,
  defaultParent,
  open,
  onOpenChange,
  onCreated,
}: {
  templates: EntryTemplate[];
  defaultParent: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (dn: string) => void;
}) {
  const usable = templates.filter((t) => t.usable);
  const [name, setName] = useState("");
  const [parent, setParent] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [picking, setPicking] = useState<string | null>(null);
  const [change, setChange] = useState<ChangeRequest | null>(null);
  const [allocated, setAllocated] = useState("");

  const chosen = templates.find((t) => t.name === name) ?? usable[0];

  useEffect(() => {
    if (!open) return;
    const first = usable[0];
    setName(first?.name ?? "");
    setParent(first?.parent ?? defaultParent);
    setValues(defaults(first));
    setChange(null);
    // Only when the dialog opens: a template list refetched while it is open
    // must not empty what has been typed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const pick = (t: EntryTemplate) => {
    setName(t.name);
    setParent(t.parent ?? parent ?? defaultParent);
    // Values typed for a field the other template also has are kept.
    setValues((prev) => ({ ...defaults(t), ...keep(prev, t) }));
  };

  const render = useMutation<
    { change: ChangeRequest; line: string },
    ApiFailure
  >({
    mutationFn: async () => {
      const res = unwrap(
        await api.POST("/templates/render", {
          body: { template: chosen?.name ?? "", parent: parent.trim(), values: nonEmpty(values) },
        }),
      );
      return { change: res.change, line: allocationLine(res) };
    },
    onSuccess: ({ change, line }) => {
      setAllocated(line);
      setChange(change);
    },
  });

  const missing = (chosen?.fields ?? []).some((f) => f.required && !(values[f.name] ?? "").trim());

  return (
    <>
      <Dialog open={open && change === null} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{chosen ? `New ${chosen.title.toLowerCase()}` : "New entry"}</DialogTitle>
            <DialogDescription>
              From a template. You review the exact change, with any number it takes from the
              directory, before anything is written.
            </DialogDescription>
          </DialogHeader>

          <form
            className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (!missing && chosen) render.mutate();
            }}
          >
            {templates.length > 1 ? (
              <fieldset className="space-y-1.5">
                <legend className="mb-1.5 text-sm font-medium">Template</legend>
                {templates.map((t) => (
                  <label
                    key={t.name}
                    className={cn(
                      "flex cursor-pointer items-start gap-2 rounded-md border p-2.5 text-sm",
                      t.name === chosen?.name ? "border-primary bg-accent/40" : "border-border",
                      !t.usable && "cursor-not-allowed opacity-70",
                    )}
                  >
                    <input
                      type="radio"
                      name="template"
                      className="mt-1"
                      checked={t.name === chosen?.name}
                      disabled={!t.usable}
                      onChange={() => pick(t)}
                    />
                    <span>
                      <span className="font-medium">{safeText(t.title)}</span>
                      {t.description ? (
                        <span className="block text-xs text-muted-foreground">{safeText(t.description)}</span>
                      ) : null}
                      {!t.usable && t.problems?.length ? (
                        <span className="block text-xs text-warning-tint-foreground">
                          Not usable here: {safeText(t.problems.join("; "))}
                        </span>
                      ) : null}
                    </span>
                  </label>
                ))}
              </fieldset>
            ) : chosen?.description ? (
              <p className="text-sm text-muted-foreground">{safeText(chosen.description)}</p>
            ) : null}

            {chosen ? (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor="template-parent">Create under</Label>
                  <div className="flex gap-2">
                    <Input
                      id="template-parent"
                      className="font-dn"
                      value={parent}
                      onChange={(e) => setParent(e.target.value)}
                      required
                    />
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      aria-label="Pick the parent entry"
                      onClick={() => setPicking("__parent")}
                    >
                      <FolderSearch />
                    </Button>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    The new entry is named {chosen.rdn}=… under this entry.
                  </p>
                </div>

                {chosen.fields.map((f) => (
                  <div key={f.name} className="space-y-1.5">
                    <Label htmlFor={`template-field-${f.name}`}>
                      {f.label}
                      {f.required ? <span className="text-destructive"> *</span> : null}
                    </Label>
                    <div className="flex gap-2">
                      <Input
                        id={`template-field-${f.name}`}
                        value={values[f.name] ?? ""}
                        // Text, not type=number: a number input's wheel and
                        // arrow keys change an ID while somebody scrolls.
                        inputMode={f.kind === "number" ? "numeric" : undefined}
                        pattern={f.kind === "number" ? "[0-9]*" : undefined}
                        className={f.kind === "dn" ? "font-dn" : undefined}
                        required={f.required}
                        onChange={(e) => setValues((prev) => ({ ...prev, [f.name]: e.target.value }))}
                      />
                      {f.kind === "dn" ? (
                        <Button
                          type="button"
                          variant="outline"
                          size="icon"
                          aria-label={`Pick ${f.label}`}
                          onClick={() => setPicking(f.name)}
                        >
                          <FolderSearch />
                        </Button>
                      ) : null}
                    </div>
                    {f.help ? <p className="text-xs text-muted-foreground">{safeText(f.help)}</p> : null}
                  </div>
                ))}

                {chosen.allocates.length > 0 ? (
                  <p className="rounded-md border border-dashed p-2.5 text-xs text-muted-foreground">
                    {chosen.allocates.map((a) => a.attribute).join(" and ")}{" "}
                    {chosen.allocates.length === 1 ? "is" : "are"} the next free number in{" "}
                    {chosen.allocates.map((a) => `${a.from}–${a.to}`).join(" and ")}, found in the
                    directory when you review. It is not reserved: the review shows the number, and
                    applying is what takes it.
                  </p>
                ) : null}
              </>
            ) : (
              <p className="text-sm text-muted-foreground">
                No template for this view fits this directory's schema.
              </p>
            )}

            {render.error ? <ErrorNote title="The change could not be prepared" error={render.error} /> : null}
            <button type="submit" hidden />
          </form>

          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button disabled={!chosen || missing || !parent.trim() || render.isPending} onClick={() => render.mutate()}>
              {render.isPending ? <Loader2 className="animate-spin" /> : null}
              Review
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <DnPicker
        open={picking !== null}
        onOpenChange={(o) => {
          if (!o) setPicking(null);
        }}
        baseDn={defaultParent}
        title={picking === "__parent" ? "Create under" : "Pick an entry"}
        suggestedFilter={picking === "__parent" ? "(objectClass=organizationalUnit)" : "(objectClass=person)"}
        onPick={(dn) => {
          if (picking === "__parent") setParent(dn);
          else if (picking) setValues((prev) => ({ ...prev, [picking]: dn }));
          setPicking(null);
        }}
      />

      <ChangeDialog
        change={change}
        open={change !== null}
        onOpenChange={(o) => {
          // Back to the form, with everything typed: the natural place to land
          // after reading the change and deciding something is wrong.
          if (!o) setChange(null);
        }}
        title={allocated ? `Create this entry (${allocated})` : "Create this entry"}
        onApplied={(result) => {
          setChange(null);
          onOpenChange(false);
          onCreated(result.storedDn ?? result.dn);
        }}
      />
    </>
  );
}

function defaults(t: EntryTemplate | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of t?.fields ?? []) out[f.name] = f.default ?? "";
  return out;
}

function keep(prev: Record<string, string>, t: EntryTemplate): Record<string, string> {
  const out: Record<string, string> = {};
  for (const f of t.fields) {
    const v = prev[f.name];
    if (v) out[f.name] = v;
  }
  return out;
}

function nonEmpty(values: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(values)) {
    if (v.trim() !== "") out[k] = v;
  }
  return out;
}
