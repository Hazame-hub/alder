import { useMemo, useState } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Users } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { ChangeRequest, EntryView, MemberList, SearchResultEntry, SessionInfo } from "@/lib/api";
import {
  composeMembers,
  dnMembershipAttributes,
  looksLikeGroup,
  memberKey,
  nestingFindings,
  type MembersOperation,
} from "@/lib/group-members";
import { searchBaseFor } from "@/lib/access-subject";
import { displayText } from "@/lib/values";
import { safeText } from "@/lib/display";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui";
import { DnPicker } from "@/components/dn-picker";
import { MultiDnPicker, type PickedEntry } from "@/components/multi-dn-picker";
import { ErrorNote } from "@/components/change-dialog";

/**
 * Many members at once (docs/proposals/group-membership-tools.md, Part 1).
 *
 * Two ways in, one record. From a group: *Add members…* picks several entries
 * from searches, *Remove members…* ticks the group's own values. From a table's
 * selection: *Add to a group…* and *Remove from a group…* take one group.
 * Either way the result is one `modify` of that group, reviewed and applied
 * through the ordinary change dialog, holding only the values that change
 * anything.
 */

const groupFilter = "(|(objectClass=groupOfNames)(objectClass=groupOfUniqueNames))";

/** How many DNs the dialog names before it counts the rest. */
const namedAtFirst = 5;

function rowClasses(entry: SearchResultEntry): string[] {
  const attr = entry.attributes?.find((a) => (a.name.split(";")[0] ?? a.name).toLowerCase() === "objectclass");
  return (attr?.values ?? []).map((v) => v.text ?? "").filter((v) => v !== "");
}

/** A table's selected rows, as the dialog takes them. */
export function pickedFromRows(rows: SearchResultEntry[]): PickedEntry[] {
  return rows.map((r) => ({ dn: r.dn, classes: rowClasses(r) }));
}

function valuesOf(entry: EntryView, attribute: string): string[] {
  const folded = attribute.toLowerCase();
  const attr = entry.attributes.find((a) => (a.name.split(";")[0] ?? a.name).toLowerCase() === folded);
  return (attr?.values ?? []).map(displayText).filter((v) => v !== "");
}

function named(dns: string[]): string {
  const shown = dns.slice(0, namedAtFirst).map(safeText).join("; ");
  return dns.length > namedAtFirst ? `${shown}, and ${dns.length - namedAtFirst} more` : shown;
}

async function expand(dn: string): Promise<MemberList> {
  return unwrap(await api.GET("/members", { params: { query: { dn, limit: 2000 } } }));
}

export function GroupMembersDialog({
  open,
  onOpenChange,
  op,
  groupDn,
  preset,
  onReview,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  op: MembersOperation;
  /** The group, when the dialog is opened from it; otherwise it is chosen here. */
  groupDn?: string;
  /** The members, when the dialog is opened from a table's selection. */
  preset?: PickedEntry[];
  /** Hands the one record to the change dialog for its plan and review. */
  onReview: (change: ChangeRequest) => void;
}) {
  const queryClient = useQueryClient();
  const session = queryClient.getQueryData<SessionInfo>(["session"]);
  const contexts = session?.capabilities?.namingContexts;

  const [chosenGroup, setChosenGroup] = useState<string | null>(null);
  const group = groupDn ?? chosenGroup;
  const [pickingGroup, setPickingGroup] = useState(false);
  const [attribute, setAttribute] = useState("");
  const [picked, setPicked] = useState<PickedEntry[]>([]);
  const [ticked, setTicked] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState("");

  const entry = useQuery<EntryView, ApiFailure>({
    queryKey: ["entry", group, "membership"],
    enabled: open && group !== null,
    // Read when the dialog opens, not from a cache: what the record leaves out
    // depends on what the group holds now.
    staleTime: 0,
    gcTime: 0,
    queryFn: async () =>
      unwrap(await api.GET("/entry", { params: { query: { dn: group as string, includeOperational: false } } })),
  });

  const attributes = dnMembershipAttributes(entry.data?.membershipAttributes);
  const chosenAttribute = attributes.find((a) => a.toLowerCase() === attribute.toLowerCase()) ?? attributes[0] ?? "";
  const held = useMemo(
    () => (entry.data && chosenAttribute ? valuesOf(entry.data, chosenAttribute) : []),
    [entry.data, chosenAttribute],
  );

  const members: PickedEntry[] =
    preset ?? (op === "add" ? picked : held.filter((v) => ticked.has(v)).map((dn) => ({ dn, classes: [] })));

  // Nesting, for an add: each picked group's own expansion (a cycle), and the
  // target's (who it already reaches through another group).
  const pickedGroups = op === "add" ? members.filter((m) => looksLikeGroup(m.classes)) : [];
  const insideQueries = useQueries({
    queries: pickedGroups.map((m) => ({
      queryKey: ["members", m.dn, 2000],
      enabled: open,
      queryFn: () => expand(m.dn),
    })),
  });
  const around = useQuery<MemberList, ApiFailure>({
    queryKey: ["members", group, 2000],
    enabled: open && op === "add" && group !== null && members.length > 0,
    queryFn: () => expand(group as string),
  });
  const inside = new Map<string, MemberList>();
  insideQueries.forEach((q, i) => {
    if (q.data) inside.set(memberKey(pickedGroups[i]!.dn), q.data);
  });
  const nestingPending =
    op === "add" && (insideQueries.some((q) => q.isPending) || (members.length > 0 && around.isPending));
  const findings =
    group && op === "add" && !nestingPending
      ? nestingFindings(
          group,
          members.map((m) => ({ dn: m.dn, isGroup: looksLikeGroup(m.classes) })),
          inside,
          around.data,
        )
      : [];
  const blocking = findings.filter((f) => f.kind === "cycle" || f.kind === "unchecked");

  const composition =
    group && chosenAttribute && members.length > 0
      ? composeMembers(
          group,
          chosenAttribute,
          held,
          members.map((m) => m.dn),
          op,
        )
      : null;

  const reset = () => {
    setChosenGroup(null);
    setAttribute("");
    setPicked([]);
    setTicked(new Set());
    setFilter("");
  };
  const close = (next: boolean) => {
    if (!next) reset();
    onOpenChange(next);
  };

  const pickerBase = searchBaseFor(group ?? preset?.[0]?.dn ?? "", contexts);
  const title =
    op === "add"
      ? preset
        ? `Add ${preset.length === 1 ? "one entry" : `${preset.length} entries`} to a group`
        : "Add members"
      : preset
        ? `Remove ${preset.length === 1 ? "one entry" : `${preset.length} entries`} from a group`
        : "Remove members";
  const shownHeld = held.filter((v) => v.toLowerCase().includes(filter.toLowerCase()));
  const canReview = composition?.ok === true && blocking.length === 0 && !nestingPending;

  return (
    <>
      <Dialog open={open && !pickingGroup} onOpenChange={close}>
        <DialogContent wide className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>
              One change to the group, holding only the members that change anything. It is
              planned and shown as LDIF before anything is sent.
            </DialogDescription>
          </DialogHeader>

          <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4 text-sm">
            <div className="space-y-1.5">
              <Label>Group</Label>
              <div className="flex flex-wrap items-center gap-2">
                {group ? (
                  <span className="font-dn" data-testid="members-group">
                    {safeText(group)}
                  </span>
                ) : (
                  <span className="text-muted-foreground">No group chosen yet.</span>
                )}
                {groupDn ? null : (
                  <Button size="sm" variant="outline" onClick={() => setPickingGroup(true)}>
                    <Users />
                    {group ? "Choose another group" : "Choose a group"}
                  </Button>
                )}
              </div>
            </div>

            {group && entry.isPending ? (
              <p className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Reading the group
              </p>
            ) : null}
            {entry.isError ? <ErrorNote title="The group could not be read" error={entry.error} /> : null}
            {entry.data && attributes.length === 0 ? (
              <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2">
                This entry&apos;s classes permit no membership attribute that holds entries
                (<span className="font-dn">member</span> or <span className="font-dn">uniqueMember</span>).
              </p>
            ) : null}

            {attributes.length > 1 ? (
              <div className="space-y-1.5">
                <Label htmlFor="members-attr">Membership attribute</Label>
                <Select value={chosenAttribute} onValueChange={setAttribute}>
                  <SelectTrigger id="members-attr">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {attributes.map((a) => (
                      <SelectItem key={a} value={a}>
                        {a}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            ) : attributes.length === 1 ? (
              <p className="text-xs text-muted-foreground">
                Written to <span className="font-dn">{safeText(chosenAttribute)}</span>, which holds{" "}
                {held.length} {held.length === 1 ? "value" : "values"} now.
              </p>
            ) : null}

            {preset ? (
              <p>
                {preset.length} {preset.length === 1 ? "entry" : "entries"} from the selection:{" "}
                <span className="font-dn">{named(preset.map((p) => p.dn))}</span>.
              </p>
            ) : entry.data && attributes.length > 0 ? (
              op === "add" ? (
                <MultiDnPicker baseDn={pickerBase} chosen={picked} onChosen={setPicked} />
              ) : (
                <div className="space-y-2">
                  <Input
                    aria-label="Filter the members"
                    className="font-dn"
                    value={filter}
                    placeholder="part of a name"
                    onChange={(e) => setFilter(e.target.value)}
                  />
                  <ul className="max-h-64 divide-y divide-border overflow-y-auto rounded-md border border-border">
                    {shownHeld.map((value) => (
                      <li key={value}>
                        <label className="flex cursor-pointer items-center gap-2 px-3 py-1.5 hover:bg-accent/60">
                          <Checkbox
                            checked={ticked.has(value)}
                            aria-label={`Remove ${value}`}
                            onCheckedChange={() =>
                              setTicked((prev) => {
                                const next = new Set(prev);
                                if (next.has(value)) next.delete(value);
                                else next.add(value);
                                return next;
                              })
                            }
                          />
                          <span className="min-w-0 flex-1 truncate font-dn" title={safeText(value)}>
                            {safeText(value)}
                          </span>
                        </label>
                      </li>
                    ))}
                    {shownHeld.length === 0 ? (
                      <li className="px-3 py-4 text-muted-foreground">
                        {held.length === 0 ? "This group holds no members." : "Nothing here matches that."}
                      </li>
                    ) : null}
                  </ul>
                  <p className="text-xs text-muted-foreground">{ticked.size} ticked.</p>
                </div>
              )
            ) : null}

            {nestingPending && members.length > 0 ? (
              <p className="flex items-center gap-2 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Checking nested groups
              </p>
            ) : null}
            {findings.map((f) => (
              <p
                key={`${f.kind}:${f.member}`}
                className={
                  f.kind === "indirect"
                    ? "rounded-md border border-border bg-muted/40 px-3 py-2"
                    : "rounded-md border border-warning/40 bg-warning/10 px-3 py-2"
                }
              >
                {f.kind === "cycle" ? (
                  <>
                    <span className="font-dn">{safeText(f.member)}</span> already contains this group
                    {f.chain.length > 1 ? (
                      <>
                        {" "}
                        through <span className="font-dn">{named(f.chain.slice(1))}</span>
                      </>
                    ) : null}
                    , so adding it would make a cycle. Leave it out.
                  </>
                ) : f.kind === "unchecked" ? (
                  <>
                    <span className="font-dn">{safeText(f.member)}</span> is a group too large, or too
                    hard to read, to check for a cycle. Leave it out, or add it on its own.
                  </>
                ) : (
                  <>
                    <span className="font-dn">{safeText(f.member)}</span> is already in this group through{" "}
                    <span className="font-dn">{named(f.via)}</span>. Adding it directly is still allowed:
                    a direct and an indirect membership are different facts.
                  </>
                )}
              </p>
            ))}

            {composition && composition.leftOut.length > 0 ? (
              <p className="text-xs text-muted-foreground">
                {composition.leftOut.length}{" "}
                {op === "add"
                  ? composition.leftOut.length === 1
                    ? "is already a member and is"
                    : "are already members and are"
                  : composition.leftOut.length === 1
                    ? "is not a member and is"
                    : "are not members and are"}{" "}
                left out: <span className="font-dn">{named(composition.leftOut)}</span>. A record naming
                {op === "add" ? " a value the group holds" : " a value the group lacks"} could be refused whole.
              </p>
            ) : null}
            {composition && !composition.ok ? (
              <p className="text-xs text-warning-tint-foreground">{composition.message}</p>
            ) : null}
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button
              disabled={!canReview}
              onClick={() => {
                if (!composition?.ok) return;
                onReview(composition.change);
                close(false);
              }}
            >
              {composition?.ok
                ? `Review: ${op === "add" ? "add" : "remove"} ${composition.values.length} ${
                    composition.values.length === 1 ? "member" : "members"
                  }`
                : "Review the change"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <DnPicker
        open={open && pickingGroup}
        onOpenChange={setPickingGroup}
        baseDn={pickerBase}
        title="Choose the group"
        suggestedFilter={groupFilter}
        onPick={(dn) => {
          setChosenGroup(dn);
          setAttribute("");
        }}
      />
    </>
  );
}
