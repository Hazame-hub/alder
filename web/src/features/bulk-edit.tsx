import { useMemo, useState } from "react";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { Loader2, UserPlus } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { SearchResultEntry, SessionInfo } from "@/lib/api";
import {
  bulkEditSummary,
  bulkOperations,
  classesOf,
  classSetKey,
  composeBulkEdit,
  isPickedMembership,
  offeredAttributes,
  operationsFor,
  selectionRefusal,
  stageBulkEdit,
  takesOneValue,
  takesValues,
  type BulkEditOrigin,
  type BulkOperation,
  type ClassSetPermits,
} from "@/lib/bulk-edit";
import type { StageOutcome } from "@/lib/stage-deletes";
import { searchBaseFor } from "@/lib/access-subject";
import { safeText } from "@/lib/display";
import { parentOf } from "@/lib/values";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/misc";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui";
import { AttributeEditor } from "@/components/attribute-editor";
import { DnPicker } from "@/components/dn-picker";
import { ErrorNote } from "@/components/change-dialog";

/**
 * One modification over a table's selection (docs/proposals/bulk-attribute-edit.md).
 *
 * The dialog stages and never applies: its only action puts one `modify` per
 * selected entry into the changeset, where the plan says what each would do
 * and the operator reviews the set as one LDIF document.
 */

/**
 * The most distinct sets of object classes a selection may hold. Each set is
 * one schema request; a selection more varied than this is many kinds of
 * entry, and one modification over all of them is not what was meant.
 */
const maxClassSets = 20;

/** How many selected DNs the dialog names before it counts the rest. */
const namedAtFirst = 3;

export function BulkEditDialog({
  open,
  onOpenChange,
  entries,
  onStaged,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The selected rows, as the table holds them. */
  entries: SearchResultEntry[];
  onStaged: (outcome: StageOutcome) => void;
}) {
  const queryClient = useQueryClient();
  const session = queryClient.getQueryData<SessionInfo>(["session"]);
  const dns = useMemo(() => entries.map((e) => e.dn), [entries]);
  const refusal = selectionRefusal(dns, session);

  const sets = useMemo(() => {
    const out = new Map<string, string[]>();
    for (const entry of entries) {
      const classes = classesOf(entry);
      if (classes) out.set(classSetKey(classes), classes);
    }
    return [...out.entries()];
  }, [entries]);
  const tooVaried = sets.length > maxClassSets;
  const unclassed = entries.filter((e) => classesOf(e) === null).length;

  const requirements = useQueries({
    queries: sets.map(([key, classes]) => ({
      queryKey: ["requirements", key],
      enabled: open && !tooVaried && refusal === null,
      queryFn: async () =>
        unwrap(await api.GET("/schema/requirements", { params: { query: { class: classes } } })),
    })),
  });
  const loading = requirements.some((r) => r.isPending && r.fetchStatus !== "idle");
  const failed = requirements.find((r) => r.isError)?.error as ApiFailure | undefined;
  const permits: ClassSetPermits[] = requirements.flatMap((r, i) =>
    r.data ? [{ key: sets[i]![0], requirements: r.data.requirements, kinds: r.data.kinds }] : [],
  );

  const [widen, setWiden] = useState(false);
  const offered = offeredAttributes(entries, permits, widen);
  const [attribute, setAttribute] = useState("");
  const chosen = offered.find((o) => o.kind.name.toLowerCase() === attribute.toLowerCase());
  const operations = chosen ? operationsFor(chosen.kind) : [];
  const [op, setOp] = useState<BulkOperation>("add");
  const effectiveOp = operations.includes(op) ? op : (operations[0] ?? "add");
  const [values, setValues] = useState<string[]>([""]);
  const [picking, setPicking] = useState(false);
  const membership = chosen ? isPickedMembership(chosen.kind.name) : false;

  const pickerBase = searchBaseFor(dns[0] ?? "", session?.capabilities?.namingContexts);

  const composition =
    chosen && refusal === null
      ? composeBulkEdit(dns, chosen.kind, { attribute: chosen.kind.name, op: effectiveOp, values })
      : null;
  const notPermitted = chosen ? entries.length - chosen.permittedBy : 0;

  const reset = () => {
    setAttribute("");
    setOp("add");
    setValues([""]);
    setWiden(false);
  };
  const close = (next: boolean) => {
    if (!next) reset();
    onOpenChange(next);
  };

  const stage = () => {
    if (!composition?.ok || !chosen) return;
    onStaged(
      stageBulkEdit(
        { attribute: chosen.kind.name, op: effectiveOp, values },
        composition.items,
      ),
    );
    close(false);
  };

  const parents = [...new Set(dns.map(parentOf))];

  return (
    <>
      <Dialog open={open} onOpenChange={close}>
        <DialogContent wide className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>Edit an attribute on {entries.length === 1 ? "one entry" : `${entries.length} entries`}</DialogTitle>
            <DialogDescription>
              {entries.length} {entries.length === 1 ? "entry" : "entries"} selected
              {parents.length === 1 ? (
                <>
                  {" "}
                  from <span className="font-dn">{safeText(parents[0] ?? "")}</span>
                </>
              ) : null}
              : {dns.slice(0, namedAtFirst).map(safeText).join("; ")}
              {dns.length > namedAtFirst ? `, and ${dns.length - namedAtFirst} more` : ""}.
            </DialogDescription>
          </DialogHeader>

          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4 text-sm">
            {refusal ? (
              <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2">{refusal}</p>
            ) : tooVaried ? (
              <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2">
                The selection holds {sets.length} different sets of object classes. One
                modification is offered over at most {maxClassSets}: select entries of fewer kinds.
              </p>
            ) : failed ? (
              <ErrorNote title="The schema could not be read" error={failed} />
            ) : loading ? (
              <p className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Reading what these entries&apos; classes permit
              </p>
            ) : (
              <>
                <div className="space-y-1.5">
                  <Label>Attribute</Label>
                  <Select
                    value={chosen ? chosen.kind.name : ""}
                    onValueChange={(v) => {
                      setAttribute(v);
                      setValues([""]);
                    }}
                  >
                    <SelectTrigger aria-label="Attribute">
                      <SelectValue placeholder="Choose an attribute" />
                    </SelectTrigger>
                    <SelectContent>
                      {offered.map((o) => (
                        <SelectItem key={o.kind.name} value={o.kind.name}>
                          {o.kind.name}
                          {o.permittedBy < entries.length ? ` (${o.permittedBy} of ${entries.length})` : ""}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <label className="flex items-center gap-2 text-xs text-muted-foreground">
                    <Checkbox
                      checked={widen}
                      onCheckedChange={(v) => setWiden(v === true)}
                      aria-label="Show attributes only some entries permit"
                    />
                    Show attributes only some entries permit
                  </label>
                  {offered.length === 0 ? (
                    <p className="text-xs text-muted-foreground">
                      {unclassed > 0
                        ? `${unclassed} selected ${unclassed === 1 ? "row carries" : "rows carry"} no object class, so nothing is known to be permitted on every entry.`
                        : "No attribute is permitted on every selected entry."}{" "}
                      Passwords, attributes the server owns and objectClass are never offered.
                    </p>
                  ) : null}
                  {chosen && notPermitted > 0 ? (
                    <p className="text-xs text-warning-tint-foreground">
                      {notPermitted} {notPermitted === 1 ? "entry's classes do" : "entries' classes do"} not
                      permit {chosen.kind.name}; the plan marks{" "}
                      {notPermitted === 1 ? "it" : "them"} invalid and nothing is sent for{" "}
                      {notPermitted === 1 ? "it" : "them"}.
                    </p>
                  ) : null}
                </div>

                {chosen ? (
                  <div className="space-y-1.5">
                    <Label>Operation</Label>
                    <Select
                      value={effectiveOp}
                      onValueChange={(v) => {
                        const next = v as BulkOperation;
                        setOp(next);
                        if (takesOneValue(next)) setValues((prev) => prev.slice(0, 1));
                      }}
                    >
                      <SelectTrigger aria-label="Operation">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {bulkOperations
                          .filter((o) => operations.includes(o.op))
                          .map((o) => (
                            <SelectItem key={o.op} value={o.op}>
                              {o.label}
                            </SelectItem>
                          ))}
                      </SelectContent>
                    </Select>
                    <p className="text-xs text-muted-foreground">
                      {bulkOperations.find((o) => o.op === effectiveOp)?.meaning}
                      {membership
                        ? " A membership list is never replaced here, and the member comes from the directory, one at a time."
                        : ""}
                    </p>
                  </div>
                ) : null}

                {chosen && takesValues(effectiveOp) ? (
                  membership ? (
                    <div className="space-y-1.5">
                      <Label>Member</Label>
                      <div className="flex flex-wrap items-center gap-2">
                        {values[0] ? (
                          <span className="font-dn" data-testid="bulk-member">
                            {safeText(values[0])}
                          </span>
                        ) : (
                          <span className="text-muted-foreground">No entry chosen yet.</span>
                        )}
                        <Button size="sm" variant="outline" onClick={() => setPicking(true)}>
                          <UserPlus />
                          {values[0] ? "Choose another" : "Choose an entry"}
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <AttributeEditor
                      name={chosen.kind.name}
                      // One field for an add or a removal: see takesOneValue.
                      kind={takesOneValue(effectiveOp) ? { ...chosen.kind, singleValue: true } : chosen.kind}
                      required={false}
                      values={values}
                      onChange={setValues}
                      pickerBase={chosen.kind.kind === "dn" ? pickerBase : undefined}
                    />
                  )
                ) : null}

                {composition?.ok && composition.warning ? (
                  <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2">
                    {composition.warning}
                  </p>
                ) : null}
                {composition?.ok && composition.namedBy.length > 0 ? (
                  <p className="rounded-md border border-border bg-muted/40 px-3 py-2">
                    {composition.namedBy.length}{" "}
                    {composition.namedBy.length === 1 ? "entry is" : "entries are"} named by{" "}
                    <span className="font-dn">{safeText(chosen?.kind.name ?? "")}</span>;{" "}
                    {composition.namedBy.length === 1 ? "it is" : "they are"} not changed:{" "}
                    <span className="font-dn">
                      {composition.namedBy.slice(0, namedAtFirst).map(safeText).join("; ")}
                    </span>
                    {composition.namedBy.length > namedAtFirst
                      ? `, and ${composition.namedBy.length - namedAtFirst} more`
                      : ""}
                    .
                  </p>
                ) : null}
                {composition && !composition.ok && values.some((v) => v !== "") ? (
                  <p className="text-xs text-warning-tint-foreground">{composition.message}</p>
                ) : null}
              </>
            )}
          </div>

          <DialogFooter>
            <span className="mr-auto text-xs text-muted-foreground">
              Staged, not applied. The changeset plans every entry and shows the LDIF first.
            </span>
            <Button variant="outline" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button disabled={!composition?.ok} onClick={stage}>
              Stage{" "}
              {composition?.ok
                ? `${composition.items.length} ${composition.items.length === 1 ? "change" : "changes"}`
                : "changes"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <DnPicker
        open={picking}
        onOpenChange={setPicking}
        baseDn={pickerBase}
        title={`Choose the ${chosen?.kind.name ?? "member"}`}
        onPick={(dn) => setValues([dn])}
      />
    </>
  );
}

/** Above a changeset that is one bulk edit and nothing else. */
export function BulkEditHeading({ origin }: { origin: BulkEditOrigin }) {
  return (
    <div className="space-y-0.5 rounded-md border bg-muted/40 px-3 py-2 text-sm">
      <div className="font-medium">{safeText(bulkEditSummary(origin))}</div>
      <div className="text-muted-foreground">
        One change per selected entry. Check the plan: an entry that already
        matches is unchanged, one that is gone is a conflict, and one whose
        classes do not permit the attribute is invalid. None of those is sent.
      </div>
      {origin.entries > 0 ? (
        <Badge variant="outline" className="font-normal">
          set when staged; nothing re-runs the search
        </Badge>
      ) : null}
    </div>
  );
}
