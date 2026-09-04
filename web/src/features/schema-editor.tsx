import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ChangeRequest, SchemaWrite, SchemaTarget } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/misc";
import { ErrorNote } from "@/components/change-dialog";

/**
 * The form that authors a schema definition.
 *
 * It does not write anything. It asks the server to build the change, and the
 * change then goes to the ordinary confirmation dialog — same LDIF preview,
 * same Ansible export, same option to stage it in a changeset. Editing the
 * schema is not a special kind of write, and the interface should not suggest
 * that it is.
 *
 * The definition text is rendered by the server rather than assembled here.
 * That is the same rule as the LDIF preview: the bytes the person confirms have
 * to be the bytes the directory receives, and a definition built in the browser
 * and a definition built on the server are two chances to differ.
 */

export type SchemaEditorRequest = {
  kind: "objectClass" | "attributeType";
  op: "add" | "replace" | "delete";
  /** Prefilled when editing; the OID also identifies what to replace. */
  initial?: Partial<DefinitionForm> & { oid?: string };
  /** The stored definition, shown when removing one. */
  raw?: string;
};

type DefinitionForm = {
  oid: string;
  names: string;
  desc: string;
  obsolete: boolean;
  // object class
  superNames: string;
  classKind: "STRUCTURAL" | "ABSTRACT" | "AUXILIARY";
  must: string;
  may: string;
  // attribute type
  superName: string;
  equality: string;
  ordering: string;
  substr: string;
  syntax: string;
  singleValue: boolean;
};

const empty: DefinitionForm = {
  oid: "",
  names: "",
  desc: "",
  obsolete: false,
  superNames: "",
  classKind: "STRUCTURAL",
  must: "",
  may: "",
  superName: "",
  equality: "",
  ordering: "",
  substr: "",
  syntax: "",
  singleValue: false,
};

/** Directory String, the syntax a new text attribute almost always wants. */
const directoryString = "1.3.6.1.4.1.1466.115.121.1.15";

function list(s: string): string[] {
  return s
    .split(/[,\s]+/)
    .map((v) => v.trim())
    .filter(Boolean);
}

export function SchemaEditorDialog({
  request,
  write,
  open,
  onOpenChange,
  onBuilt,
}: {
  request: SchemaEditorRequest | null;
  write: SchemaWrite;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Hands the built change to the confirmation dialog. */
  onBuilt: (change: ChangeRequest, title: string, destructive: boolean) => void;
}) {
  const targets: SchemaTarget[] = write.targets ?? [];
  // Preselected only when there is nothing to choose. With several, the first
  // is the server's own core schema, which is the one collection a definition
  // should almost never join -- so a default here would quietly aim the change
  // at the worst target. The same reason the order is never rearranged applies:
  // Alder does not choose for you.
  const [targetDn, setTargetDn] = useState(targets.length === 1 ? (targets[0]?.dn ?? "") : "");
  const [form, setForm] = useState<DefinitionForm>({ ...empty, ...request?.initial });
  const [raw, setRaw] = useState(request?.raw ?? "");
  const [mode, setMode] = useState<"form" | "raw">("form");

  // Remounting on each open is what keeps the form from showing the previous
  // definition's fields; the key is set by the caller.
  const kind = request?.kind ?? "attributeType";
  const op = request?.op ?? "add";
  const isDelete = op === "delete";

  const build = useMutation({
    mutationFn: async () =>
      unwrap(
        await api.POST("/schema/change", {
          body: {
            targetDn,
            kind,
            op,
            oid: request?.initial?.oid ?? form.oid,
            ...(isDelete
              ? {}
              : mode === "raw"
                ? { definition: raw }
                : kind === "objectClass"
                  ? {
                      objectClass: {
                        oid: form.oid,
                        names: list(form.names),
                        desc: form.desc || undefined,
                        obsolete: form.obsolete || undefined,
                        superNames: list(form.superNames),
                        kind: form.classKind,
                        must: list(form.must),
                        may: list(form.may),
                      },
                    }
                  : {
                      attributeType: {
                        oid: form.oid,
                        names: list(form.names),
                        desc: form.desc || undefined,
                        obsolete: form.obsolete || undefined,
                        superName: form.superName || undefined,
                        equality: form.equality || undefined,
                        ordering: form.ordering || undefined,
                        substr: form.substr || undefined,
                        syntax: form.syntax || undefined,
                        singleValue: form.singleValue || undefined,
                      },
                    }),
          },
        }),
      ),
    onSuccess: (result) => {
      onOpenChange(false);
      onBuilt(
        result.change,
        isDelete
          ? `Remove this ${label(kind)} from the schema`
          : op === "replace"
            ? `Replace this ${label(kind)}`
            : `Add this ${label(kind)} to the schema`,
        isDelete,
      );
    },
  });

  const error = build.error as ApiFailure | null;
  const set = <K extends keyof DefinitionForm>(k: K, v: DefinitionForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }));

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent wide>
        <DialogHeader>
          <DialogTitle>
            {isDelete
              ? `Remove a ${label(kind)}`
              : op === "replace"
                ? `Edit a ${label(kind)}`
                : `New ${label(kind)}`}
          </DialogTitle>
          <DialogDescription>
            Nothing is written yet. Alder will render the definition and show you
            the change before anything is sent.
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
          {/*
            Only worth asking when there is a choice. Where the schema is a
            single entry there is exactly one place a definition can go, and a
            picker with one option is a question with one answer.
          */}
          {targets.length > 1 ? (
            <div className="space-y-1.5">
              <Label>Which collection</Label>
              <Select value={targetDn} onValueChange={setTargetDn}>
                <SelectTrigger>
                  <SelectValue placeholder="Choose one" />
                </SelectTrigger>
                <SelectContent>
                  {targets.map((t) => (
                    <SelectItem key={t.dn} value={t.dn}>
                      {t.name} — {t.attributeTypes} attribute types, {t.objectClasses} classes
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                These load in order, and a definition can only refer to something
                defined before it. The first is usually the server's own core
                schema, which is rarely where your definition belongs, so Alder
                does not choose for you.
              </p>
            </div>
          ) : null}

          {isDelete ? (
            <div className="space-y-2">
              <p className="text-sm">
                This removes the definition from the schema. Entries already using
                it are not changed, and the directory may refuse the removal while
                anything still depends on it.
              </p>
              {request?.raw ? (
                <pre className="overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs">
                  {request.raw}
                </pre>
              ) : null}
            </div>
          ) : (
            <Tabs value={mode} onValueChange={(v) => setMode(v as "form" | "raw")}>
              <TabsList className="mb-3">
                <TabsTrigger value="form">Form</TabsTrigger>
                <TabsTrigger value="raw">Definition</TabsTrigger>
              </TabsList>

              <TabsContent value="form">
                <div className="space-y-3">
                  <Field label="OID" hint="Numeric, from an arc you control.">
                    <Input
                      value={form.oid}
                      placeholder="1.3.6.1.4.1.99999.1.1"
                      onChange={(e) => set("oid", e.target.value)}
                    />
                  </Field>
                  <Field label="Name" hint="One or more, separated by spaces. Letters, digits and hyphens.">
                    <Input
                      value={form.names}
                      placeholder="alderTeam"
                      onChange={(e) => set("names", e.target.value)}
                    />
                  </Field>
                  <Field label="Description">
                    <Input value={form.desc} onChange={(e) => set("desc", e.target.value)} />
                  </Field>

                  {kind === "objectClass" ? (
                    <>
                      <Field label="Kind">
                        <Select
                          value={form.classKind}
                          onValueChange={(v) =>
                            set("classKind", v as DefinitionForm["classKind"])
                          }
                        >
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="STRUCTURAL">
                              STRUCTURAL — what an entry is
                            </SelectItem>
                            <SelectItem value="AUXILIARY">
                              AUXILIARY — added alongside a structural class
                            </SelectItem>
                            <SelectItem value="ABSTRACT">
                              ABSTRACT — only inherited from
                            </SelectItem>
                          </SelectContent>
                        </Select>
                      </Field>
                      <Field label="Superior classes" hint="Usually top, or the class this extends.">
                        <Input
                          value={form.superNames}
                          placeholder="top"
                          onChange={(e) => set("superNames", e.target.value)}
                        />
                      </Field>
                      <Field label="Required attributes (MUST)">
                        <Input value={form.must} onChange={(e) => set("must", e.target.value)} />
                      </Field>
                      <Field label="Optional attributes (MAY)">
                        <Input value={form.may} onChange={(e) => set("may", e.target.value)} />
                      </Field>
                    </>
                  ) : (
                    <>
                      <Field
                        label="Syntax"
                        hint="How the values are shaped. Required, unless a superior supplies one."
                      >
                        <div className="flex gap-2">
                          <Input
                            value={form.syntax}
                            placeholder={directoryString}
                            onChange={(e) => set("syntax", e.target.value)}
                          />
                          <Button
                            type="button"
                            variant="outline"
                            onClick={() => {
                              set("syntax", directoryString);
                              if (!form.equality) set("equality", "caseIgnoreMatch");
                            }}
                          >
                            Text
                          </Button>
                        </div>
                      </Field>
                      <Field label="Superior attribute (SUP)" hint="Inherits syntax and matching from it.">
                        <Input
                          value={form.superName}
                          onChange={(e) => set("superName", e.target.value)}
                        />
                      </Field>
                      <Field
                        label="Equality matching rule"
                        hint="Without one, the directory cannot search on this attribute."
                      >
                        <Input
                          value={form.equality}
                          placeholder="caseIgnoreMatch"
                          onChange={(e) => set("equality", e.target.value)}
                        />
                      </Field>
                      <Field label="Substring matching rule">
                        <Input
                          value={form.substr}
                          placeholder="caseIgnoreSubstringsMatch"
                          onChange={(e) => set("substr", e.target.value)}
                        />
                      </Field>
                      <Field label="Ordering matching rule">
                        <Input
                          value={form.ordering}
                          onChange={(e) => set("ordering", e.target.value)}
                        />
                      </Field>
                      <label className="flex items-center gap-2 text-sm">
                        <input
                          type="checkbox"
                          checked={form.singleValue}
                          onChange={(e) => set("singleValue", e.target.checked)}
                        />
                        Single-valued
                      </label>
                    </>
                  )}

                  <label className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={form.obsolete}
                      onChange={(e) => set("obsolete", e.target.checked)}
                    />
                    Obsolete
                  </label>
                </div>
              </TabsContent>

              <TabsContent value="raw">
                <textarea
                  className="h-52 w-full rounded-md border border-input bg-transparent p-3 font-mono text-xs"
                  value={raw}
                  placeholder="( 1.3.6.1.4.1.99999.1.1 NAME 'alderTeam' EQUALITY caseIgnoreMatch SYNTAX 1.3.6.1.4.1.1466.115.121.1.15 SINGLE-VALUE )"
                  onChange={(e) => setRaw(e.target.value)}
                />
                <p className="mt-2 text-xs text-muted-foreground">
                  An RFC 4512 definition, written out. It is parsed and checked
                  exactly as the form is — this is a shortcut through the form,
                  not around it.
                </p>
              </TabsContent>
            </Tabs>
          )}

          {error ? (
            <ErrorNote title="This definition cannot be used" error={error} />
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            variant={isDelete ? "destructive" : "default"}
            disabled={build.isPending || !targetDn}
            onClick={() => build.mutate()}
          >
            {build.isPending ? <Loader2 className="animate-spin" /> : null}
            Review the change
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  );
}

function label(kind: "objectClass" | "attributeType"): string {
  return kind === "objectClass" ? "object class" : "attribute type";
}
