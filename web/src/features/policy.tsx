import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, ShieldAlert, Timer } from "lucide-react";
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

type PolicyReport = components["schemas"]["PolicyReport"];
type PolicySetting = components["schemas"]["PolicySetting"];

/**
 * The password policy in force on this entry, and what the server records
 * about the account.
 *
 * The sibling of the access view, and the same rule: this says what the server
 * holds and where, and it does not decide whether a bind would succeed. The
 * three things an operator is actually hunting for -- is it locked, when did
 * the password change, and which policy applies to *this* account rather than
 * to everybody -- are each in a different place, and two of them are
 * operational attributes nobody reads by accident.
 */
export function PolicyButton({ dn }: { dn: string }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        title="The password policy in force here, and what the server records about this account"
      >
        <Timer />
        Policy
      </Button>
      <PolicyDialog dn={dn} open={open} onOpenChange={setOpen} />
    </>
  );
}

/** The same report, opened by a refusal that points here. */
export function PolicyDialog({
  dn,
  open,
  onOpenChange,
}: {
  dn: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const setOpen = onOpenChange;

  const report = useQuery<PolicyReport, ApiFailure>({
    queryKey: ["policy", dn],
    enabled: open,
    retry: false,
    queryFn: async () => unwrap(await api.GET("/policy", { params: { query: { dn } } })),
  });

  const state = report.data?.state;
  const policy = report.data?.policy;

  return (
    <>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle>Password policy and account state</DialogTitle>
            <DialogDescription>
              <span className="font-dn">{safeText(dn)}</span>
            </DialogDescription>
          </DialogHeader>

          {report.isPending ? (
            <p className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              Reading the policy and the account's state.
            </p>
          ) : null}
          {report.isError ? <ErrorNote title="The policy could not be read" error={report.error} /> : null}

          {report.data ? (
            <div className="space-y-3">
              {state?.locked ? (
                <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/8 p-3 text-sm text-destructive">
                  <ShieldAlert className="mt-0.5 size-4 shrink-0" />
                  <span>{safeText(state.lockedDetail) || "This account is locked."}</span>
                </p>
              ) : null}
              {state?.mustChange ? (
                <p className="rounded-md border border-warning/40 bg-warning/10 p-3 text-sm text-warning-tint-foreground">
                  The password must be changed at the next bind.
                </p>
              ) : null}

              <div className="rounded-md border p-3">
                <div className="mb-1 text-sm font-medium">What the server records about this account</div>
                {state && state.attributes.length > 0 ? (
                  <SettingList settings={state.attributes} />
                ) : (
                  <p className="text-xs text-muted-foreground">
                    Nothing yet: the server records these as things happen — a password change, a failed
                    bind, a lock — and none has on this account.
                  </p>
                )}
              </div>

              <div className="rounded-md border p-3">
                <div className="mb-1 flex flex-wrap items-center gap-2 text-sm font-medium">
                  The policy in force
                  {policy ? (
                    <Badge variant={policy.source === "entry" ? "success" : "outline"}>
                      {policy.source === "entry"
                        ? "named by this entry"
                        : policy.source === "default"
                          ? "the server's default"
                          : "not found"}
                    </Badge>
                  ) : null}
                </div>
                {policy ? (
                  <>
                    <p className="text-xs text-muted-foreground">{safeText(policy.why)}</p>
                    {policy.dn ? (
                      <p className="mt-0.5 font-dn text-xs [overflow-wrap:anywhere]">{safeText(policy.dn)}</p>
                    ) : null}
                    {policy.settings.length > 0 ? (
                      <div className="mt-2">
                        <SettingList settings={policy.settings} />
                      </div>
                    ) : null}
                  </>
                ) : null}
              </div>

              {(report.data.unread ?? []).map((unread) => (
                <p key={unread.where} className="rounded-md border p-3 text-xs">
                  <span className="font-dn">{safeText(unread.where)}</span> could not be read, so this is
                  not the whole answer — {safeText(unread.reason)}.
                </p>
              ))}

              <p className="rounded-md border border-warning/40 bg-warning/10 p-3 text-xs text-warning-tint-foreground">
                {report.data.disclaimer}
              </p>
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

function SettingList({ settings }: { settings: PolicySetting[] }) {
  return (
    <ul className="grid gap-x-4 gap-y-0.5 text-xs sm:grid-cols-2">
      {settings.map((s) => (
        <li key={s.key} className="flex flex-wrap items-baseline gap-1.5">
          {/* The label and the attribute, both visible. Putting the attribute
              in a tooltip hid it from anyone reading with a screen reader and
              made the accessible name the raw attribute rather than the
              words. */}
          <span>{safeText(s.label) || <code className="font-mono">{safeText(s.key)}</code>}</span>
          <span className="font-medium [overflow-wrap:anywhere]">{safeText(s.values.join(", "))}</span>
          {s.detail ? <span className="text-muted-foreground">({safeText(s.detail)})</span> : null}
          {s.label ? <code className="font-mono text-[11px] text-muted-foreground">{safeText(s.key)}</code> : null}
        </li>
      ))}
    </ul>
  );
}

/**
 * Whether an entry Alder has already read looks locked.
 *
 * Read from the attributes in hand rather than by asking the server again: the
 * one fact worth putting in front of somebody before they open anything is
 * that this account cannot log in, and it should not cost a request.
 */
export function looksLocked(attributes: { name: string; values: { text?: string }[] }[]): boolean {
  for (const attr of attributes) {
    const name = attr.name.toLowerCase();
    if (name === "pwdaccountlockedtime" && attr.values.length > 0) return true;
    if (name === "nsaccountlock" && attr.values.some((v) => (v.text ?? "").toLowerCase() === "true")) {
      return true;
    }
  }
  return false;
}
