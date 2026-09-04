import { useEffect, useState } from "react";
import type { ChangeRequest, EntryView } from "@/lib/api";
import { parentOf, rdnOf, textValue } from "@/lib/values";
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
import { ChangeDialog } from "@/components/change-dialog";

/**
 * SetPasswordDialog changes an entry's password.
 *
 * The password goes to /changes/apply and nowhere else. The preview shows a
 * notice rather than LDIF, because the server performs an RFC 3062 extended
 * operation, not a modification — rendering "replace: userPassword" would
 * describe an operation that does not happen, and the point of the preview is
 * that it describes the one that does.
 */
export function SetPasswordDialog({
  dn,
  open,
  onOpenChange,
}: {
  dn: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [change, setChange] = useState<ChangeRequest | null>(null);

  useEffect(() => {
    if (open) {
      setPassword("");
      setConfirm("");
    }
  }, [open]);

  const mismatch = confirm !== "" && password !== confirm;
  const ready = password !== "" && password === confirm;

  const clear = () => {
    setPassword("");
    setConfirm("");
  };

  return (
    <>
      <Dialog
        open={open && change === null}
        onOpenChange={(o) => {
          if (!o) clear();
          onOpenChange(o);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Set password</DialogTitle>
            <DialogDescription className="font-dn">{dn}</DialogDescription>
          </DialogHeader>

          <div className="space-y-4 px-5 py-4">
            <div className="space-y-1.5">
              <Label htmlFor="new-password">New password</Label>
              <Input
                id="new-password"
                type="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirm-password">Confirm</Label>
              <Input
                id="confirm-password"
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
              {mismatch ? (
                <p className="text-xs text-destructive">These do not match.</p>
              ) : null}
            </div>
            <p className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
              Alder asks the directory to set the password rather than writing a
              hash itself, so the server chooses the scheme and applies its own
              password policy. That also means this change has no LDIF: the next
              screen shows the equivalent ldappasswd command instead.
            </p>
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => {
                clear();
                onOpenChange(false);
              }}
            >
              Cancel
            </Button>
            <Button
              disabled={!ready}
              onClick={() =>
                setChange({ dn, type: "setpassword", newPassword: password })
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
        title="Set this password"
        onOpenChange={(o) => {
          if (!o) {
            setChange(null);
            clear();
            onOpenChange(false);
          }
        }}
      />
    </>
  );
}

/**
 * CopyEntryDialog creates a new entry from an existing one.
 *
 * Anything the directory owns is left behind — operational attributes, and
 * anything NO-USER-MODIFICATION — because those describe the original, not the
 * copy. Sensitive attributes cannot be copied at all: their values were never
 * sent to the browser, which is the point. The dialog says so rather than
 * silently producing an account with no password.
 */
export function CopyEntryDialog({
  entry,
  open,
  onOpenChange,
  onCreated,
}: {
  entry: EntryView;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: (dn: string) => void;
}) {
  const [newRdn, setNewRdn] = useState("");
  const [parent, setParent] = useState("");
  const [change, setChange] = useState<ChangeRequest | null>(null);

  useEffect(() => {
    if (open) {
      setNewRdn("");
      setParent(parentOf(entry.dn));
    }
  }, [open, entry.dn]);

  const withheld = entry.attributes.filter((a) => a.withheld);

  const build = (): ChangeRequest | null => {
    if (!newRdn.includes("=") || !parent) return null;
    const [rdnAttrRaw, ...rest] = newRdn.split("=");
    const rdnAttr = (rdnAttrRaw ?? "").trim();
    const rdnValue = rest.join("=");
    if (!rdnAttr || !rdnValue) return null;

    const attributes = entry.attributes
      .filter((a) => !a.kind.operational && !a.kind.readOnly && !a.withheld)
      .map((a) =>
        a.name.toLowerCase() === rdnAttr.toLowerCase()
          ? { name: a.name, values: [textValue(rdnValue)] }
          : { name: a.name, values: a.values },
      );

    // The naming attribute must be present carrying the new value, even when
    // the original was named by a different attribute.
    if (!attributes.some((a) => a.name.toLowerCase() === rdnAttr.toLowerCase())) {
      attributes.push({ name: rdnAttr, values: [textValue(rdnValue)] });
    }
    return { dn: `${newRdn},${parent}`, type: "add", attributes };
  };

  const candidate = build();

  return (
    <>
      <Dialog open={open && change === null} onOpenChange={onOpenChange}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Copy this entry</DialogTitle>
            <DialogDescription className="font-dn">{entry.dn}</DialogDescription>
          </DialogHeader>

          <div className="space-y-4 px-5 py-4">
            <div className="space-y-1.5">
              <Label htmlFor="copy-rdn">New RDN</Label>
              <Input
                id="copy-rdn"
                value={newRdn}
                autoFocus
                className="font-dn"
                placeholder={rdnOf(entry.dn)}
                onChange={(e) => setNewRdn(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="copy-parent">Parent</Label>
              <Input
                id="copy-parent"
                value={parent}
                className="font-dn"
                onChange={(e) => setParent(e.target.value)}
              />
            </div>
            {newRdn.includes("=") ? (
              <p className="font-dn text-xs text-muted-foreground">
                {newRdn},{parent}
              </p>
            ) : null}
            {withheld.length ? (
              <p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning-tint-foreground">
                Not copied, because their values were never sent to this browser:{" "}
                <span className="font-mono">
                  {withheld.map((a) => a.name).join(", ")}
                </span>
                . Set a password on the copy afterwards.
              </p>
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
        title="Create this copy"
        onOpenChange={(o) => {
          if (!o) {
            setChange(null);
            onOpenChange(false);
          }
        }}
        // A copy is an add, so the server may store it under a name of its own.
        onApplied={(result) => onCreated(result.storedDn ?? result.dn)}
      />
    </>
  );
}
