import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Eye, Loader2, ServerCog, X } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { SessionInfo } from "@/lib/api";
import { referenceKey, serverLabel, useReference } from "@/lib/reference";
import { safeText } from "@/lib/display";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui";
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
 * A second server inside one session (B6).
 *
 * The comparisons Alder is best at are between two servers -- a replica and
 * its primary, staging and production -- and a session was one directory, so
 * every one of them meant a disconnect in the middle of the work or a file
 * carried between two connections. The reference is a second connection,
 * attached here and only here, held read-only by the server: it can be read
 * beside the session's own directory everywhere a comparison is made, and
 * nothing can be written to it.
 *
 * The chip is quieter than the connection beside it on purpose. It is the
 * server you look at, not the one you change.
 */
export function ReferenceChip({ session }: { session: SessionInfo }) {
  const reference = useReference();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);

  const detach = useMutation({
    mutationKey: ["detach-reference"],
    mutationFn: async () => unwrap(await api.DELETE("/session/reference")),
    onSettled: () => {
      queryClient.setQueryData(referenceKey, null);
      // Anything read from the reference is now a reading of a server this
      // session no longer holds.
      queryClient.removeQueries({ predicate: (q) => q.queryKey.includes("reference") && q.queryKey[0] !== "reference" });
    },
  });

  if (!reference) {
    return (
      <>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setOpen(true)}
          title="Connect to a second server, read-only, to compare this one with"
        >
          <Eye />
          Add a reference
        </Button>
        {open ? (
          <AttachReferenceDialog
            session={session}
            onOpenChange={setOpen}
            onAttached={(info) => {
              setOpen(false);
              queryClient.setQueryData(referenceKey, info);
            }}
          />
        ) : null}
      </>
    );
  }

  return (
    <div
      className="flex items-center gap-1 rounded-md border border-dashed border-border px-2 py-1 text-xs text-muted-foreground"
      data-testid="reference-chip"
      title="A second server held for comparison. Nothing can be written to it."
    >
      <Eye className="size-3.5" />
      <span>reference:</span>
      <span className="font-dn text-foreground">{safeText(serverLabel(reference))}</span>
      <Badge variant="outline" className="px-1.5">
        read-only
      </Badge>
      {reference.verified === false ? (
        <Badge variant="destructive" className="px-1.5">
          unverified TLS
        </Badge>
      ) : null}
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="Detach the reference"
        title="Detach the reference"
        onClick={() => detach.mutate()}
        disabled={detach.isPending}
      >
        <X />
      </Button>
    </div>
  );
}

function AttachReferenceDialog({
  session,
  onOpenChange,
  onAttached,
}: {
  session: SessionInfo;
  onOpenChange: (open: boolean) => void;
  onAttached: (info: SessionInfo) => void;
}) {
  // Host starts empty for the reason the capture-from dialog gives: the
  // point is the *other* server, and a form pre-filled with this one attaches
  // the wrong thing when somebody hurries. How it is reached and who binds
  // are usually the same across a pair, and are carried over.
  const [host, setHost] = useState("");
  const [port, setPort] = useState(String(session.port ?? 636));
  const [bindDn, setBindDn] = useState(session.bindDn ?? "");
  const [password, setPassword] = useState("");
  const [configBindDn, setConfigBindDn] = useState("");
  const [configPassword, setConfigPassword] = useState("");
  const [caCertificate, setCaCertificate] = useState("");
  const [insecure, setInsecure] = useState(session.verified === false);

  const attach = useMutation<SessionInfo, ApiFailure>({
    mutationKey: ["attach-reference"],
    mutationFn: async () =>
      unwrap(
        await api.POST("/session/reference", {
          body: {
            host: host.trim(),
            port: Number(port),
            tls: (session.tls ?? "ldaps") as "ldaps" | "starttls" | "plaintext",
            bindDn: bindDn.trim() || undefined,
            bindPassword: password || undefined,
            configBindDn: configBindDn.trim() || undefined,
            configBindPassword: configPassword || undefined,
            caCertificate: caCertificate.trim() || undefined,
            insecureSkipVerify: insecure,
          },
        }),
      ),
    onSuccess: onAttached,
  });

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="screen-shell max-w-2xl">
        <DialogHeader className="border-b border-border pb-4">
          <p className="screen-kicker">A second server, for comparison</p>
          <DialogTitle className="text-xl tracking-tight">Add a reference server</DialogTitle>
          <DialogDescription>
            Read beside <span className="font-dn">{safeText(serverLabel(session))}</span> in snapshots,
            entry comparisons, replication and schema. Nothing can be written to it.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex flex-wrap gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="ref-host">Host</Label>
              <Input
                id="ref-host"
                className="w-64"
                value={host}
                placeholder="the other server"
                onChange={(e) => setHost(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="ref-port">Port</Label>
              <Input
                id="ref-port"
                type="number"
                className="w-28"
                value={port}
                onChange={(e) => setPort(e.target.value)}
              />
            </div>
          </div>

          <div className="flex flex-wrap gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="ref-bind">Bind DN</Label>
              <Input
                id="ref-bind"
                className="font-dn w-80"
                value={bindDn}
                onChange={(e) => setBindDn(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="ref-pw">Password</Label>
              <Input
                id="ref-pw"
                type="password"
                className="w-56"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
          </div>

          <div className="flex flex-wrap gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="ref-cfg">Configuration bind DN</Label>
              <Input
                id="ref-cfg"
                className="font-dn w-80"
                value={configBindDn}
                placeholder="cn=admin,cn=config"
                onChange={(e) => setConfigBindDn(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="ref-cfg-pw">Configuration password</Label>
              <Input
                id="ref-cfg-pw"
                type="password"
                className="w-56"
                value={configPassword}
                onChange={(e) => setConfigPassword(e.target.value)}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="ref-ca">CA certificate (PEM, optional)</Label>
            <textarea
              id="ref-ca"
              className="h-20 w-full rounded-md border border-input bg-transparent p-2 font-mono text-xs"
              value={caCertificate}
              placeholder="-----BEGIN CERTIFICATE-----"
              onChange={(e) => setCaCertificate(e.target.value)}
            />
          </div>

          <label className="flex items-start gap-2.5 text-sm">
            <Checkbox
              checked={insecure}
              onCheckedChange={(v) => setInsecure(v === true)}
              className="mt-0.5"
            />
            <span>
              Do not verify the certificate
              <span className="block text-xs text-muted-foreground">
                For a self-signed certificate on a test server. Leave it off against anything
                you care about.
              </span>
            </span>
          </label>

          <p className="rounded-md border border-warning/40 bg-warning/10 p-3 text-xs text-warning-tint-foreground">
            What happens to what you type: Alder opens a second connection and keeps this password
            in its memory, beside the one you connected with, for as long as this session lasts.
            It is never written to disk or to a log and never sent back to the browser. Detaching,
            disconnecting, connecting again or the session timing out closes the connection and
            discards the password. Nothing can be applied to the reference from Alder: every change
            still goes to <span className="font-dn">{safeText(serverLabel(session))}</span>.
          </p>

          {attach.isError ? (
            <ErrorNote title="The reference could not be attached" error={attach.error} />
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={host.trim() === "" || attach.isPending} onClick={() => attach.mutate()}>
            {attach.isPending ? <Loader2 className="animate-spin" /> : <ServerCog />}
            Attach read-only
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
