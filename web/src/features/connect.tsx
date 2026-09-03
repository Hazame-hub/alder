import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, ShieldCheck, TriangleAlert } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { SessionInfo } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Checkbox,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui";
import { ErrorNote } from "@/components/change-dialog";
import { AlderMark } from "@/components/mark";
import * as recent from "@/lib/recent-connection";

type Tls = "ldaps" | "starttls" | "plaintext";

const defaultPorts: Record<Tls, number> = {
  ldaps: 636,
  starttls: 389,
  plaintext: 389,
};

export function ConnectScreen({ onConnected }: { onConnected: (s: SessionInfo) => void }) {
  const queryClient = useQueryClient();

  // Everything except the password is restored from the last successful
  // connection. Reconnecting to the same directory should not mean finding and
  // re-pasting its CA certificate.
  const [remembered] = useState(() => recent.load());

  const [host, setHost] = useState(remembered?.host ?? "");
  const [port, setPort] = useState(remembered?.port ?? 636);
  const [portTouched, setPortTouched] = useState(remembered !== null);
  const [tls, setTls] = useState<Tls>(remembered?.tls ?? "ldaps");
  const [bindDn, setBindDn] = useState(remembered?.bindDn ?? "");
  const [bindPassword, setBindPassword] = useState("");
  const [insecure, setInsecure] = useState(remembered?.insecureSkipVerify ?? false);
  const [showCa, setShowCa] = useState(false);
  const [caCertificate, setCaCertificate] = useState(remembered?.caCertificate ?? "");
  const [serverName, setServerName] = useState(remembered?.serverName ?? "");

  const connect = useMutation<SessionInfo, ApiFailure>({
    mutationFn: async () =>
      unwrap(
        await api.POST("/session", {
          body: {
            host: host.trim(),
            port,
            tls,
            bindDn: bindDn.trim() || undefined,
            bindPassword: bindPassword || undefined,
            insecureSkipVerify: insecure,
            caCertificate: caCertificate.trim() || undefined,
            serverName: serverName.trim() || undefined,
          },
        }),
      ),
    onSuccess: (info) => {
      // The password is dropped from component state the moment the session
      // exists. The server holds it; this form has no further use for it, and
      // it is deliberately not among the fields remembered below.
      setBindPassword("");
      recent.save({
        host: host.trim(),
        port,
        tls,
        bindDn: bindDn.trim(),
        serverName: serverName.trim(),
        caCertificate: caCertificate.trim(),
        insecureSkipVerify: insecure,
      });
      void queryClient.invalidateQueries();
      onConnected(info);
    },
  });

  const changeTls = (next: Tls) => {
    setTls(next);
    if (!portTouched) setPort(defaultPorts[next]);
  };

  return (
    <div className="grid min-h-full place-items-center p-6">
      <div className="w-full max-w-lg">
        <div className="mb-6 flex items-center gap-3">
          <AlderMark className="size-9" />
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Alder</h1>
            <p className="text-sm text-muted-foreground">
              A directory engineering tool whose output is code.
            </p>
          </div>
        </div>

        <form
          className="space-y-4 rounded-lg border border-border bg-card p-5 shadow-sm"
          onSubmit={(e) => {
            e.preventDefault();
            connect.mutate();
          }}
        >
          <div className="grid gap-3 sm:grid-cols-[1fr_7rem]">
            <div className="space-y-1.5">
              <Label htmlFor="host">Directory host</Label>
              <Input
                id="host"
                value={host}
                required
                autoFocus
                placeholder="ldap.example.test"
                className="font-dn"
                onChange={(e) => setHost(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="port">Port</Label>
              <Input
                id="port"
                type="number"
                min={1}
                max={65535}
                value={port}
                onChange={(e) => {
                  setPortTouched(true);
                  setPort(Number(e.target.value) || 0);
                }}
              />
            </div>
          </div>

          <div className="space-y-1.5">
            <Label>Transport</Label>
            <Select value={tls} onValueChange={(v) => changeTls(v as Tls)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="ldaps">LDAPS — TLS from the start</SelectItem>
                <SelectItem value="starttls">StartTLS — upgrade after connecting</SelectItem>
                <SelectItem value="plaintext">Plaintext — no encryption</SelectItem>
              </SelectContent>
            </Select>
            {tls === "plaintext" ? (
              <p className="flex items-start gap-1.5 text-xs text-destructive">
                <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                Your bind password will cross the network in the clear. Alder
                refuses this unless it was started with
                <code className="mx-1 font-mono">--i-know-this-is-insecure</code>.
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="binddn">Bind DN</Label>
            <Input
              id="binddn"
              value={bindDn}
              placeholder="cn=admin,dc=example,dc=test"
              className="font-dn"
              autoComplete="username"
              onChange={(e) => setBindDn(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Leave empty to bind anonymously.
            </p>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              value={bindPassword}
              autoComplete="current-password"
              onChange={(e) => setBindPassword(e.target.value)}
            />
            <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
              <ShieldCheck className="mt-0.5 size-3.5 shrink-0" />
              Held in the server's memory for this session only. Not written to
              disk, not put in a token, not stored in your browser.
            </p>
          </div>

          <details
            open={showCa || (remembered?.caCertificate ?? "") !== ""}
            onToggle={(e) => setShowCa((e.currentTarget as HTMLDetailsElement).open)}
            className="rounded-md border border-border"
          >
            <summary className="cursor-pointer select-none px-3 py-2 text-sm font-medium">
              Certificate options
            </summary>
            <div className="space-y-3 border-t border-border p-3">
              <div className="space-y-1.5">
                <Label htmlFor="ca">CA certificate (PEM)</Label>
                <Textarea
                  id="ca"
                  rows={4}
                  value={caCertificate}
                  placeholder="-----BEGIN CERTIFICATE-----"
                  className="font-mono text-xs"
                  onChange={(e) => setCaCertificate(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  The supported way to reach a directory behind a private CA.
                </p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="servername">Certificate name override</Label>
                <Input
                  id="servername"
                  value={serverName}
                  className="font-dn"
                  placeholder="ldap.example.test"
                  onChange={(e) => setServerName(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  For a directory reached by IP but certified by name.
                </p>
              </div>
              <label className="flex items-start gap-2.5 text-sm">
                <Checkbox
                  checked={insecure}
                  onCheckedChange={(v) => setInsecure(v === true)}
                  className="mt-0.5"
                />
                <span>
                  Skip certificate verification
                  <span className="block text-xs text-muted-foreground">
                    Per-connection, never a default. The session is marked
                    unverified for as long as it lasts.
                  </span>
                </span>
              </label>
            </div>
          </details>

          {connect.isError ? <ErrorNote title="Could not connect" error={connect.error} /> : null}

          <Button type="submit" className="w-full" disabled={connect.isPending || !host}>
            {connect.isPending ? <Loader2 className="animate-spin" /> : null}
            Connect
          </Button>
        </form>

        {remembered ? (
          <p className="mt-4 text-center text-xs text-muted-foreground">
            Reconnecting to{" "}
            <span className="font-dn">{recent.describe(remembered)}</span>. The
            password is never remembered.{" "}
            <button
              type="button"
              className="underline underline-offset-2 hover:text-foreground"
              onClick={() => {
                recent.forget();
                setHost("");
                setPort(636);
                setPortTouched(false);
                setTls("ldaps");
                setBindDn("");
                setServerName("");
                setCaCertificate("");
                setInsecure(false);
              }}
            >
              Forget it
            </button>
          </p>
        ) : (
          <p className="mt-4 text-center text-xs text-muted-foreground">
            Nothing is written to the directory without showing you the LDIF
            first.
          </p>
        )}
      </div>
    </div>
  );
}
