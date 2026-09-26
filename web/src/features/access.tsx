import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { KeyRound, Loader2 } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { components } from "@/lib/api.gen";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ErrorNote } from "@/components/change-dialog";
import { safeText } from "@/lib/display";

type AccessReport = components["schemas"]["AccessReport"];
type AccessRule = components["schemas"]["AccessRule"];

/**
 * The access rules that bear on this entry.
 *
 * "Why can't I write this?" is the question, and this is deliberately not an
 * answer to it: Alder does not evaluate access control, and a screen that
 * looked like it did would be believed. What it gives instead is the thing an
 * operator actually needs at two in the morning -- the rules, where each one is
 * written, in the order the server consults them, with the ones bearing on this
 * entry marked and the raw value always visible.
 *
 * Nothing here writes. Access control is the one thing in a directory that can
 * lock every administrator out of it, and editing it is out of scope on the
 * record.
 */
export function AccessButton({ dn }: { dn: string }) {
  const [open, setOpen] = useState(false);

  const report = useQuery<AccessReport, ApiFailure>({
    queryKey: ["access", dn],
    enabled: open,
    retry: false,
    queryFn: async () => unwrap(await api.GET("/access", { params: { query: { dn } } })),
  });

  const rules = report.data?.rules ?? [];
  const applying = rules.filter((r) => r.applies === "yes").length;

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        title="The access control rules the server holds for this entry"
      >
        <KeyRound />
        Access
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>Access control</DialogTitle>
            <DialogDescription>
              <span className="font-dn">{safeText(dn)}</span>
            </DialogDescription>
          </DialogHeader>

          {report.isPending ? (
            <p className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Reading the rules the server holds.
            </p>
          ) : null}
          {report.isError ? <ErrorNote title="The rules could not be read" error={report.error} /> : null}

          {report.data ? (
            <div className="space-y-3">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span>
                  {rules.length} rule{rules.length === 1 ? "" : "s"}, {applying} bearing on this entry
                </span>
                {(report.data.styles ?? []).map((style) => (
                  <Badge key={style} variant="outline">
                    {safeText(style)}
                  </Badge>
                ))}
              </div>

              {report.data.effective ? (
                <Verdict effective={report.data.effective} />
              ) : report.data.rightsNote ? (
                <p className="rounded-md border p-3 text-xs text-muted-foreground">
                  {safeText(report.data.rightsNote)}
                </p>
              ) : null}

              <p className="rounded-md border border-warning/40 bg-warning/10 p-3 text-xs text-warning-tint-foreground">
                {report.data.disclaimer}
              </p>

              {(report.data.unread ?? []).map((unread) => (
                <p key={unread.where} className="rounded-md border p-3 text-xs">
                  <span className="font-dn">{safeText(unread.where)}</span> could not be read, so this is
                  not the whole answer — {safeText(unread.reason)}.
                </p>
              ))}

              {rules.length === 0 && (report.data.unread ?? []).length === 0 ? (
                <p className="rounded-md border p-3 text-sm text-muted-foreground">
                  This server holds no access rules Alder can read: none on this entry or above it, and none
                  in its configuration tree.
                </p>
              ) : null}

              <ol className="space-y-2">
                {rules.map((rule, i) => (
                  <RuleRow key={`${rule.source}:${rule.index ?? i}:${i}`} rule={rule} />
                ))}
              </ol>
            </div>
          ) : null}

          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

/**
 * What the server itself says this identity may do here.
 *
 * It goes above the rules and it is marked as the server's, because it is the
 * one thing on this screen that is not Alder reading text: the directory
 * computed it with the same code that will refuse the operation. Where a
 * server cannot answer, the note in its place says so rather than leaving the
 * rules to look like a verdict.
 */
function Verdict({ effective }: { effective: NonNullable<AccessReport["effective"]> }) {
  const [all, setAll] = useState(false);
  const attributes = effective.attributes ?? [];
  // The attributes worth reading first are the ones that are not the same
  // answer as everything else: a list of eighty "rsc" rows buries the "none".
  const denied = attributes.filter((a) => !a.words?.length);
  const shown = all ? attributes : denied.length > 0 ? denied : attributes.slice(0, 8);

  return (
    <div className="space-y-2 rounded-md border border-success/40 bg-success/5 p-3">
      <div className="text-sm font-medium">
        What the server says {effective.subject ? "this identity" : "you"} may do here
      </div>
      {effective.subject ? (
        <div className="font-dn text-xs [overflow-wrap:anywhere]">{safeText(effective.subject)}</div>
      ) : null}
      <p className="text-xs">
        <span className="text-muted-foreground">this entry: </span>
        {effective.entryWords?.length ? (
          safeText(effective.entryWords.join(", "))
        ) : (
          <span className="text-muted-foreground">nothing</span>
        )}
        <code className="ml-1.5 font-mono text-[11px] text-muted-foreground">{safeText(effective.entry)}</code>
      </p>
      {attributes.length > 0 ? (
        <>
          <ul className="grid gap-x-4 gap-y-0.5 text-xs sm:grid-cols-2">
            {shown.map((a) => (
              <li key={a.name} className="flex flex-wrap items-baseline gap-1.5">
                <code className="font-mono">{safeText(a.name)}</code>
                <span className={a.words?.length ? "text-muted-foreground" : "text-destructive"}>
                  {a.words?.length ? safeText(a.words.join(", ")) : "nothing"}
                </span>
              </li>
            ))}
          </ul>
          {attributes.length > shown.length ? (
            <Button variant="ghost" size="sm" onClick={() => setAll(true)}>
              Show all {attributes.length} attributes
            </Button>
          ) : null}
        </>
      ) : null}
      <p className="text-xs text-muted-foreground">
        The directory's own answer, from the Get Effective Rights control — not Alder reading the rules
        below.
      </p>
    </div>
  );
}

type Look = { label: string; variant: "success" | "outline" | "warning" };

const APPLIES_LOOK: Record<"yes" | "maybe" | "no", Look> = {
  yes: { label: "bears on this entry", variant: "success" },
  maybe: { label: "may bear on this entry", variant: "warning" },
  no: { label: "elsewhere", variant: "outline" },
};

function RuleRow({ rule }: { rule: AccessRule }) {
  const look: Look = APPLIES_LOOK[rule.applies] ?? APPLIES_LOOK.maybe;
  return (
    <li className={`rounded-md border p-3 ${rule.applies === "yes" ? "" : "opacity-70"}`}>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        {rule.index !== undefined ? (
          <Badge variant="secondary" title="The order the server consults its rules in">
            {rule.index}
          </Badge>
        ) : null}
        <Badge variant={look.variant}>{look.label}</Badge>
        {rule.inherited ? (
          <Badge variant="outline" title="Written on an entry above this one">
            inherited
          </Badge>
        ) : null}
        {rule.name ? <span className="font-medium">{safeText(rule.name)}</span> : null}
        <span className="ml-auto font-dn text-xs text-muted-foreground [overflow-wrap:anywhere]">
          {safeText(rule.source)}
        </span>
      </div>

      {rule.why ? <p className="mt-1 text-xs text-muted-foreground">{safeText(rule.why)}</p> : null}

      {rule.target && (rule.target.dn || rule.target.attributes?.length || rule.target.filter) ? (
        <p className="mt-2 text-xs">
          <span className="text-muted-foreground">about </span>
          {rule.target.dn ? (
            <>
              <span className="font-dn">{safeText(rule.target.dn)}</span>
              {rule.target.scope ? (
                <span className="text-muted-foreground"> ({safeText(rule.target.scope)})</span>
              ) : null}
            </>
          ) : (
            <span className="text-muted-foreground">every entry</span>
          )}
          {rule.target.attributes?.length ? (
            <>
              <span className="text-muted-foreground">, attributes </span>
              <code className="font-mono">{safeText(rule.target.attributes.join(", "))}</code>
            </>
          ) : null}
          {rule.target.filter ? (
            <>
              <span className="text-muted-foreground">, matching </span>
              <code className="font-mono">{safeText(rule.target.filter)}</code>
            </>
          ) : null}
        </p>
      ) : null}

      {rule.grants?.length ? (
        <ul className="mt-2 space-y-0.5 text-xs">
          {rule.grants.map((grant, i) => (
            <li key={i} className="flex flex-wrap items-center gap-1.5">
              <Badge
                variant={grant.kind === "deny" ? "destructive" : grant.kind === "allow" ? "success" : "secondary"}
              >
                {grant.kind === "level" ? "level" : grant.kind}
              </Badge>
              <span className="font-dn [overflow-wrap:anywhere]">{safeText(grant.subject)}</span>
              <span className="text-muted-foreground">→</span>
              <code className="font-mono">{safeText(grant.access) || "(not stated)"}</code>
            </li>
          ))}
        </ul>
      ) : null}

      <pre className="mt-2 overflow-x-auto rounded bg-muted/50 p-2 font-mono text-[11px] leading-relaxed">
        {safeText(rule.raw)}
      </pre>
      {!rule.parsed ? (
        <p className="mt-1 text-xs text-warning-tint-foreground">
          Alder did not take this rule apart; what is above is the rule itself, as the server holds it.
        </p>
      ) : null}
    </li>
  );
}
