import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, ServerCog } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { SessionInfo } from "@/lib/api";
import { Button } from "@/components/ui/button";
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
 * Capturing the server you are not connected to.
 *
 * A configuration snapshot exists to be compared against another server, and
 * Alder could only ever capture the one it was connected to. So the
 * comparison an operator actually wants -- this replica against the primary
 * -- meant capturing one, disconnecting, connecting to the other, and
 * comparing: an audit measured forty-six interactions before the drift was
 * on screen, and lost its first capture to the disconnect on the way.
 *
 * The credentials typed here are sent once, with the request, and are not
 * kept: no session is opened on that server, nothing is stored, and the
 * connection is closed as soon as the configuration has been read. The
 * server says the same in its own words, and the form says it here because
 * the person typing a production bind password is entitled to know before
 * they type it rather than after.
 */

type Captured = { raw: string; host: string; port: number };

export function CaptureFromAnotherServer({
  into,
  session,
  onCaptured,
}: {
  into: "A" | "B";
  session: SessionInfo | undefined;
  onCaptured: (result: Captured) => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <ServerCog />
        Capture another server into {into}
      </Button>
      {open ? (
        <CaptureFromDialog
          into={into}
          session={session}
          onOpenChange={setOpen}
          onCaptured={(result) => {
            setOpen(false);
            onCaptured(result);
          }}
        />
      ) : null}
    </>
  );
}

function CaptureFromDialog({
  into,
  session,
  onOpenChange,
  onCaptured,
}: {
  into: "A" | "B";
  session: SessionInfo | undefined;
  onOpenChange: (open: boolean) => void;
  onCaptured: (result: Captured) => void;
}) {
  // Host and port start empty: the whole point is the *other* server, and a
  // form pre-filled with the one you are on is a form that captures the
  // wrong thing when somebody hurries. Everything that is usually the same
  // between two servers in a pair -- how they are reached, who you bind as
  // -- is carried over.
  const [host, setHost] = useState("");
  const [port, setPort] = useState(String(session?.port ?? 636));
  const [bindDn, setBindDn] = useState(session?.bindDn ?? "");
  const [password, setPassword] = useState("");
  const [configBindDn, setConfigBindDn] = useState("");
  const [configPassword, setConfigPassword] = useState("");
  const [insecure, setInsecure] = useState(session?.verified === false);

  const capture = useMutation<Captured, ApiFailure>({
    mutationKey: ["capture-from"],
    mutationFn: async () => {
      const raw = (await unwrap(
        await api.POST("/snapshots/capture/from", {
          body: {
            kind: "config",
            from: {
              host: host.trim(),
              port: Number(port),
              tls: (session?.tls ?? "ldaps") as "ldaps" | "starttls" | "plaintext",
              bindDn: bindDn.trim() || undefined,
              bindPassword: password || undefined,
              configBindDn: configBindDn.trim() || undefined,
              configBindPassword: configPassword || undefined,
              insecureSkipVerify: insecure,
            },
          },
          parseAs: "text",
        }),
      )) as unknown as string;
      return { raw, host: host.trim(), port: Number(port) };
    },
    onSuccess: onCaptured,
  });

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="screen-shell max-w-2xl">
        <DialogHeader className="border-b border-border pb-4">
          <p className="screen-kicker">The other side of the comparison</p>
          <DialogTitle className="text-xl tracking-tight">
            Capture another server into snapshot {into}
          </DialogTitle>
          <DialogDescription>
            Its configuration is read once and returned. Nothing is written to it.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="flex flex-wrap gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="from-host">Host</Label>
              <Input
                id="from-host"
                className="w-64"
                value={host}
                placeholder="the other server"
                onChange={(e) => setHost(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="from-port">Port</Label>
              <Input
                id="from-port"
                type="number"
                className="w-28"
                value={port}
                onChange={(e) => setPort(e.target.value)}
              />
            </div>
          </div>

          <div className="flex flex-wrap gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="from-bind">Bind DN</Label>
              <Input
                id="from-bind"
                className="font-dn w-80"
                value={bindDn}
                onChange={(e) => setBindDn(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="from-pw">Password</Label>
              <Input
                id="from-pw"
                type="password"
                className="w-56"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
          </div>

          <div className="flex flex-wrap gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="from-cfg">Configuration bind DN</Label>
              <Input
                id="from-cfg"
                className="font-dn w-80"
                value={configBindDn}
                placeholder="cn=admin,cn=config"
                onChange={(e) => setConfigBindDn(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="from-cfg-pw">Configuration password</Label>
              <Input
                id="from-cfg-pw"
                type="password"
                className="w-56"
                value={configPassword}
                onChange={(e) => setConfigPassword(e.target.value)}
              />
            </div>
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
            What happens to what you type: it is sent with this one request, used to open a
            connection, read that server's configuration, and closed. Alder does not keep it,
            does not open a session on that server, and never writes it to a log or to disk.
            You stay connected to the directory you are on, and no change can be applied to the
            other one from here.
          </p>

          {capture.isError ? (
            <ErrorNote title="That server could not be captured" error={capture.error} />
          ) : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            disabled={host.trim() === "" || capture.isPending}
            onClick={() => capture.mutate()}
          >
            {capture.isPending ? <Loader2 className="animate-spin" /> : <ServerCog />}
            Capture into {into}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
