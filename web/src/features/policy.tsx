import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { LockOpen, Loader2, ShieldAlert, Timer } from "lucide-react";
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
import { ChangeDialog, ErrorNote } from "@/components/change-dialog";
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
        <DialogContent className="screen-shell max-w-3xl">
          <DialogHeader className="border-b border-border pb-4">
            <p className="screen-kicker">Account protection</p>
            <DialogTitle className="text-xl tracking-tight">Password policy and account state</DialogTitle>
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
                <div className="space-y-2 rounded-md border border-destructive/40 bg-destructive/8 p-3">
                  <p className="flex items-start gap-2 text-sm text-destructive">
                    <ShieldAlert className="mt-0.5 size-4 shrink-0" />
                    <span>{safeText(state.lockedDetail) || "This account is locked."}</span>
                  </p>
                  {/* The screen that knows the answer can now act on it. It
                      used to hand the reader to the import screen to type an
                      LDIF modify by hand -- seventeen interactions between
                      knowing and fixing, measured in an audit. */}
                  {report.data.unlock ? (
                    <UnlockAction dn={dn} unlock={report.data.unlock} />
                  ) : (
                    <p className="text-xs text-destructive/80">
                      Alder does not recognise this kind of lock, so it offers no change for it —
                      clearing it is a change to make by hand.
                    </p>
                  )}
                </div>
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

/**
 * The unlock, offered where the badge is -- in the entry header, beside the
 * red "account locked".
 *
 * The badge is read from attributes already in hand, so it costs nothing and
 * appears before anything is opened. The change that clears the lock is not:
 * it is derived by the server from the state it read, so this asks for it.
 * That is one request on a locked entry and none on any other, which is the
 * right trade for the audit's headline -- the screen that announced the
 * problem offered nothing to do about it, and the cure was seventeen
 * interactions away in a screen nobody was told to open.
 *
 * Renders nothing at all until the report says there is a lock it knows how
 * to clear. A button that appears and then fails is worse than no button:
 * the Policy dialog is where a lock Alder does not recognise gets explained,
 * and it says so in words.
 */
export function EntryUnlockButton({ dn, readOnly }: { dn: string; readOnly: boolean }) {
  const report = useQuery<PolicyReport, ApiFailure>({
    queryKey: ["policy", dn],
    // The same key the dialog uses, so opening it afterwards is free and one
    // invalidation refreshes both.
    enabled: !readOnly,
    retry: false,
    queryFn: async () => unwrap(await api.GET("/policy", { params: { query: { dn } } })),
  });
  const unlock = report.data?.unlock;
  if (readOnly || !unlock) return null;
  return <UnlockAction dn={dn} unlock={unlock} compact />;
}

/**
 * Clear the lock, through the same plan and the same review as every other
 * write.
 *
 * The change itself is the server's: the report carries it, derived from the
 * attribute the report already had to read in order to say the account is
 * locked. Nothing here builds LDIF, which is the rule -- one code path builds
 * what gets sent, and what the operator confirms is rendered from it.
 */
function UnlockAction({
  dn,
  unlock,
  compact,
}: {
  dn: string;
  unlock: NonNullable<PolicyReport["unlock"]>;
  /** In the header, where there is room for the button and not the sentence. */
  compact?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const queryClient = useQueryClient();
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setOpen(true)}
          title={
            compact ? safeText(`${unlock.why} — ${unlock.attributes.join(", ")}`) : undefined
          }
        >
          <LockOpen />
          {compact ? "Unlock" : "Unlock this account"}
        </Button>
        {compact ? null : (
          <span className="text-xs text-destructive/80">
            {safeText(unlock.why)} — {safeText(unlock.attributes.join(", "))}
          </span>
        )}
      </div>
      <ChangeDialog
        change={unlock.change}
        open={open}
        onOpenChange={setOpen}
        title="Unlock this account"
        onApplied={() => {
          // The lock is what this dialog is about, so the dialog has to be
          // wrong the moment it is gone.
          void queryClient.invalidateQueries({ queryKey: ["policy", dn] });
          void queryClient.invalidateQueries({ queryKey: ["entry"] });
        }}
      />
    </>
  );
}
