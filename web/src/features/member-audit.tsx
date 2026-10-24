import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { MemberAudit, MemberAuditGroup } from "@/lib/api";
import {
  allNotFound,
  cleanupFor,
  groupsWithFindings,
  memberDn,
  valueKey,
  withoutFound,
  wouldBeEmpty,
  type Recheck,
} from "@/lib/member-audit";
import { stageChanges, type StageOutcome } from "@/lib/stage-deletes";
import { safeText } from "@/lib/display";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/misc";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ErrorNote } from "@/components/change-dialog";

/**
 * Members nobody can find (docs/proposals/group-membership-tools.md, Part 2).
 *
 * The report says, every time, who did not find them: a directory answers "no
 * such object" for an entry that was deleted and for one this bind may not see
 * alike, so nothing here says "deleted". The clean-up is chosen, checked again,
 * then staged; the changeset plans and reviews it like any other change.
 */

const reasonLabel: Record<string, string> = {
  outside_contexts: "outside every naming context",
  below_referral: "below a referral, which is not followed",
  not_a_dn: "not a DN",
};

function bindLabel(bind: string): string {
  return bind === "" ? "an anonymous bind" : bind;
}

/** The most groups one audit reads: below a larger base, it is refused. */
const maxGroups = 2000;

/** At most this many second reads in flight at once. */
const recheckConcurrency = 8;

async function recheck(chosen: Set<string>): Promise<Map<string, Recheck>> {
  const byDn = new Map<string, string[]>();
  for (const key of chosen) {
    const value = key.split("\n")[2] ?? "";
    const dn = memberDn(value);
    byDn.set(dn, [...(byDn.get(dn) ?? []), key]);
  }
  const answers = new Map<string, Recheck>();
  const dns = [...byDn.keys()];
  for (let i = 0; i < dns.length; i += recheckConcurrency) {
    await Promise.all(
      dns.slice(i, i + recheckConcurrency).map(async (dn) => {
        let answer: Recheck;
        try {
          await unwrap(await api.GET("/entry", { params: { query: { dn, includeOperational: false } } }));
          answer = "found";
        } catch (err) {
          answer = err instanceof ApiFailure && err.status === 404 ? "not-found" : "unknown";
        }
        for (const key of byDn.get(dn) ?? []) answers.set(key, answer);
      }),
    );
  }
  return answers;
}

export function MemberAuditDialog({
  open,
  onOpenChange,
  base,
  readOnly,
  onOpenEntry,
  onStaged,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  base: string;
  readOnly: boolean;
  onOpenEntry: (dn: string) => void;
  onStaged: (outcome: StageOutcome) => void;
}) {
  const audit = useQuery<MemberAudit, ApiFailure>({
    queryKey: ["member-audit", base],
    enabled: open && base !== "",
    staleTime: 0,
    gcTime: 0,
    retry: false,
    queryFn: async () => unwrap(await api.POST("/members/audit", { body: { base, limit: maxGroups } })),
  });
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [confirming, setConfirming] = useState(false);
  const [checking, setChecking] = useState(false);

  const close = (next: boolean) => {
    if (!next) {
      setChosen(new Set());
      setConfirming(false);
    }
    onOpenChange(next);
  };

  const cleanup = useMemo(() => (audit.data ? cleanupFor(audit.data, chosen) : null), [audit.data, chosen]);

  const toggle = (key: string) =>
    setChosen((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const stage = async () => {
    if (!audit.data) return;
    setChecking(true);
    try {
      const answers = await recheck(chosen);
      const { kept, found, unknown } = withoutFound(chosen, answers);
      const plan = cleanupFor(audit.data, kept);
      const outcome = stageChanges(plan.items, {
        noun: "change",
        nothing: "Every chosen value was found or could not be read again, so nothing was staged.",
      });
      const notes = [
        found.length > 0 ? `Found again and left in place: ${found.join("; ")}.` : "",
        unknown.length > 0 ? `Could not be read again, so left in place: ${unknown.join("; ")}.` : "",
      ].filter((n) => n !== "");
      onStaged({ ...outcome, message: [outcome.message, ...notes].join(" ") });
      close(false);
    } finally {
      setChecking(false);
    }
  };

  const data = audit.data;
  const findings = data ? groupsWithFindings(data) : [];
  const unchecked = data ? data.groups.filter((g) => g.notChecked.length > 0) : [];
  const empties = data ? data.groups.filter(wouldBeEmpty) : [];
  const noPerson = data ? data.groups.filter((g) => g.noPersonThroughNesting) : [];
  const entriesRead = data ? data.read.reduce((n, r) => n + r.entries, 0) : 0;

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent wide className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{confirming ? "Stage the clean-up" : "Check membership"}</DialogTitle>
          <DialogDescription>
            Groups below <span className="font-dn">{safeText(base)}</span>, and the members they
            name that this bind could not find. Nothing is written from here.
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4 text-sm">
          {audit.isPending && audit.fetchStatus !== "idle" ? (
            <p className="flex items-center gap-2 text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Reading the groups and the entries they name…
            </p>
          ) : audit.isError ? (
            <ErrorNote title="The audit was not made" error={audit.error} />
          ) : data && confirming && cleanup ? (
            <ConfirmCleanup audit={data} cleanup={cleanup} />
          ) : data ? (
            <>
              <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2" data-testid="audit-bind">
                <p className="font-medium">
                  Not found by <span className="font-dn">{safeText(bindLabel(data.bind))}</span>.
                </p>
                <p>
                  An entry this bind may not see is reported the same way as one that was deleted.
                </p>
              </div>
              <p className="text-xs text-muted-foreground">
                {data.groups.length} {data.groups.length === 1 ? "group" : "groups"} read, and{" "}
                {entriesRead} {entriesRead === 1 ? "entry" : "entries"} in {data.read.length}{" "}
                {data.read.length === 1 ? "search" : "searches"}, at{" "}
                {new Date(data.readAt).toLocaleString()}.
                {data.referrals.length > 0
                  ? ` ${data.referrals.length} ${data.referrals.length === 1 ? "referral was" : "referrals were"} returned and not followed.`
                  : ""}
              </p>

              <section className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="font-medium">Members not found</h3>
                  {!readOnly && findings.length > 0 ? (
                    <Button size="sm" variant="outline" onClick={() => setChosen(allNotFound(data))}>
                      Select all not found
                    </Button>
                  ) : null}
                  {chosen.size > 0 ? (
                    <Button size="sm" variant="ghost" onClick={() => setChosen(new Set())}>
                      Clear
                    </Button>
                  ) : null}
                </div>
                {findings.length === 0 ? (
                  <p className="text-muted-foreground">Every member these groups name was found.</p>
                ) : (
                  <ul className="space-y-3">
                    {findings.map((g) => (
                      <li key={g.dn} className="rounded-md border border-border px-3 py-2">
                        <p className="font-dn text-xs">{safeText(g.dn)}</p>
                        <ul className="mt-1 space-y-1">
                          {g.notFound.map((v) => {
                            const key = valueKey(g.dn, v.attribute, v.value);
                            return (
                              <li key={key} className="flex items-center gap-2">
                                {readOnly ? null : (
                                  <Checkbox
                                    checked={chosen.has(key)}
                                    onCheckedChange={() => toggle(key)}
                                    aria-label={`Remove ${v.value} from ${g.dn}`}
                                  />
                                )}
                                <span className="text-xs text-muted-foreground">{v.attribute}</span>
                                <span className="font-dn">{safeText(v.value)}</span>
                              </li>
                            );
                          })}
                        </ul>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              {empties.length > 0 ? (
                <section className="space-y-1">
                  <h3 className="font-medium">Groups that would be left empty</h3>
                  <p className="text-xs text-muted-foreground">
                    If every value not found were removed. A group whose classes require a member
                    keeps its last one: delete the group instead, from its own page.
                  </p>
                  <ul className="space-y-1">
                    {empties.map((g) => (
                      <EmptyGroup key={g.dn} group={g} onOpen={() => onOpenEntry(g.dn)} />
                    ))}
                  </ul>
                </section>
              ) : null}

              {noPerson.length > 0 ? (
                <section className="space-y-1">
                  <h3 className="font-medium">Groups left with no person through nesting</h3>
                  <p className="text-xs text-muted-foreground">
                    Their remaining members are only groups that would themselves be empty or hold no
                    person. Worked out from this same read, not from a fresh walk.
                  </p>
                  <ul className="space-y-0.5">
                    {noPerson.map((g) => (
                      <li key={g.dn} className="font-dn text-xs">
                        {safeText(g.dn)}
                      </li>
                    ))}
                  </ul>
                </section>
              ) : null}

              {unchecked.length > 0 ? (
                <section className="space-y-1">
                  <h3 className="font-medium">
                    Values not checked{" "}
                    <Badge variant="outline">{unchecked.reduce((n, g) => n + g.notChecked.length, 0)}</Badge>
                  </h3>
                  <ul className="space-y-1">
                    {unchecked.map((g) =>
                      g.notChecked.map((v) => (
                        <li key={valueKey(g.dn, v.attribute, v.value)} className="text-xs">
                          <span className="font-dn">{safeText(v.value)}</span>{" "}
                          <span className="text-muted-foreground">
                            in {v.attribute} of <span className="font-dn">{safeText(g.dn)}</span>:{" "}
                            {reasonLabel[v.reason] ?? v.reason}
                            {v.referral ? <> (<span className="font-dn">{safeText(v.referral)}</span>)</> : null}
                          </span>
                        </li>
                      )),
                    )}
                  </ul>
                </section>
              ) : null}

              {data.cycles.length > 0 ? (
                <section className="space-y-1">
                  <h3 className="font-medium">Groups that contain themselves</h3>
                  <ul className="space-y-0.5">
                    {data.cycles.map((dn) => (
                      <li key={dn} className="font-dn text-xs">
                        {safeText(dn)}
                      </li>
                    ))}
                  </ul>
                </section>
              ) : null}
            </>
          ) : null}
        </div>

        <DialogFooter>
          {confirming ? (
            <>
              <Button variant="ghost" onClick={() => setConfirming(false)} disabled={checking}>
                Back
              </Button>
              <Button onClick={() => void stage()} disabled={checking || (cleanup?.items.length ?? 0) === 0}>
                {checking ? <Loader2 className="animate-spin" /> : null}
                Check again and stage
              </Button>
            </>
          ) : (
            <>
              <Button variant="ghost" onClick={() => close(false)}>
                Close
              </Button>
              {readOnly ? null : (
                <Button onClick={() => setConfirming(true)} disabled={chosen.size === 0}>
                  Review the clean-up: {chosen.size} {chosen.size === 1 ? "value" : "values"}
                </Button>
              )}
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function EmptyGroup({ group, onOpen }: { group: MemberAuditGroup; onOpen: () => void }) {
  return (
    <li className="flex flex-wrap items-center gap-2 text-xs">
      <span className="font-dn">{safeText(group.dn)}</span>
      {group.classesRequireMember ? (
        <Badge variant="warning">requires a member: the plan would refuse removing the last</Badge>
      ) : (
        <Badge variant="outline">the removal would leave it empty</Badge>
      )}
      <Button size="sm" variant="ghost" className="h-6" onClick={onOpen}>
        Open the group
      </Button>
    </li>
  );
}

function ConfirmCleanup({
  audit,
  cleanup,
}: {
  audit: MemberAudit;
  cleanup: ReturnType<typeof cleanupFor>;
}) {
  return (
    <div className="space-y-3">
      <p>
        {cleanup.values} {cleanup.values === 1 ? "value" : "values"} in {cleanup.items.length}{" "}
        {cleanup.items.length === 1 ? "change" : "changes"}, one per group and attribute. Each value
        is read once more first, and any now found is left in place and named. The changeset then
        plans and shows the changes as LDIF before anything is sent.
      </p>
      {cleanup.emptied.length > 0 ? (
        <p>
          This leaves {cleanup.emptied.length === 1 ? "one group" : `${cleanup.emptied.length} groups`}{" "}
          with no members, which {cleanup.emptied.length === 1 ? "its classes allow" : "their classes allow"}:{" "}
          {cleanup.emptied.map(safeText).join("; ")}.
        </p>
      ) : null}
      {cleanup.leftOut.length > 0 ? (
        <div className="space-y-1">
          <p className="font-medium">Left out</p>
          <ul className="space-y-1">
            {cleanup.leftOut.map((l) => (
              <li key={l.group} className="text-xs">
                <span className="font-dn">{safeText(l.group)}</span>: {l.reason}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2" data-testid="cleanup-caveat">
        Not found by <span className="font-dn">{safeText(bindLabel(audit.bind))}</span>; an entry this
        bind may not see is removed the same way.
      </p>
    </div>
  );
}
