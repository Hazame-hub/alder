import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bookmark, Loader2, Pencil, ShieldCheck, Trash2, TriangleAlert } from "lucide-react";
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
import { safeText } from "@/lib/display";

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
  const [remembered, setRemembered] = useState(() => recent.load());

  const [host, setHost] = useState(remembered?.host ?? "");
  const [port, setPort] = useState(remembered?.port ?? 636);
  const [portTouched, setPortTouched] = useState(remembered !== null);
  const [tls, setTls] = useState<Tls>(remembered?.tls ?? "ldaps");
  const [bindDn, setBindDn] = useState(remembered?.bindDn ?? "");
  const [bindPassword, setBindPassword] = useState("");
  const [configBindDn, setConfigBindDn] = useState(remembered?.configBindDn ?? "");
  const [configBindPassword, setConfigBindPassword] = useState("");
  const [showConfig, setShowConfig] = useState(false);
  const [insecure, setInsecure] = useState(remembered?.insecureSkipVerify ?? false);
  const [showCa, setShowCa] = useState(false);
  const [caCertificate, setCaCertificate] = useState(remembered?.caCertificate ?? "");
  const [serverName, setServerName] = useState(remembered?.serverName ?? "");

  // The form as it would be remembered: what a successful connection records
  // and what "Save" keeps under a name. Never a password.
  const current = (): recent.RecentConnection => ({
    host: host.trim(),
    port,
    tls,
    bindDn: bindDn.trim(),
    configBindDn: configBindDn.trim(),
    serverName: serverName.trim(),
    caCertificate: caCertificate.trim(),
    insecureSkipVerify: insecure,
  });

  // fill puts a connection in the form, or empties it. Both passwords are
  // cleared every time: a password typed for one directory must not ride along
  // to the next one picked, and switching from prod to dev with prod's admin
  // password still in the box is exactly the slip a saved list makes easy.
  const fill = (c: recent.RecentConnection | null) => {
    setHost(c?.host ?? "");
    setPort(c?.port ?? 636);
    setPortTouched(c !== null);
    setTls(c?.tls ?? "ldaps");
    setBindDn(c?.bindDn ?? "");
    setBindPassword("");
    setConfigBindDn(c?.configBindDn ?? "");
    setConfigBindPassword("");
    setServerName(c?.serverName ?? "");
    setCaCertificate(c?.caCertificate ?? "");
    setInsecure(c?.insecureSkipVerify ?? false);
    if (c?.configBindDn) setShowConfig(true);
    if (c?.caCertificate || c?.serverName || c?.insecureSkipVerify) setShowCa(true);
    connect.reset();
  };

  const [saved, setSaved] = useState(() => recent.listSaved());

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
            configBindDn: configBindDn.trim() || undefined,
            configBindPassword: configBindPassword || undefined,
            insecureSkipVerify: insecure,
            caCertificate: caCertificate.trim() || undefined,
            serverName: serverName.trim() || undefined,
          },
        }),
      ),
    onSuccess: (info) => {
      // The password is dropped from component state the moment the session
      // exists. The server holds it; this form has no further use for it, and
      // it is deliberately not among the fields current() remembers.
      setBindPassword("");
      recent.save(current());
      void queryClient.invalidateQueries();
      onConnected(info);
    },
  });

  const changeTls = (next: Tls) => {
    setTls(next);
    if (!portTouched) setPort(defaultPorts[next]);
  };

  return (
    <div className="connect-screen grid min-h-full place-items-center p-6">
      <div className="screen-shell w-full max-w-xl">
        <div className="mb-6 flex items-center gap-3">
          <AlderMark className="size-9" />
          <div>
            <h1 className="text-xl font-semibold tracking-tight">Alder</h1>
            <p className="text-sm text-muted-foreground">
              A directory engineering tool whose output is code.
            </p>
          </div>
        </div>

        <SavedConnections saved={saved} onChange={setSaved} current={current()} onPick={fill} />

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

          {/*
            Collapsed by default, and worth being explicit about why it exists.
            A directory keeps its configuration beside its data, and the account
            that administers a suffix normally has no rights there. Without a
            second identity, reaching the configuration means connecting as the
            configuration administrator and giving up access to the data — which
            on a server that stores its schema in its configuration makes schema
            editing and entry browsing mutually exclusive.
          */}
          <details
            open={showConfig || (remembered?.configBindDn ?? "") !== ""}
            onToggle={(e) => setShowConfig((e.currentTarget as HTMLDetailsElement).open)}
            className="rounded-md border border-border"
          >
            <summary className="cursor-pointer select-none px-3 py-2 text-sm font-medium">
              Configuration access
            </summary>
            <div className="space-y-3 border-t border-border p-3">
              <p className="text-xs text-muted-foreground">
                Optional, and only needed where the account above has no rights
                in the server's own configuration tree. Supplying it lets one
                session browse the data as the directory administrator and the
                configuration — including the schema, on servers that keep it
                there — as the configuration administrator. Requests are routed
                by DN; neither identity borrows the other's rights.
              </p>
              <div className="space-y-1.5">
                <Label htmlFor="configbinddn">Configuration bind DN</Label>
                <Input
                  id="configbinddn"
                  value={configBindDn}
                  placeholder="cn=admin,cn=config"
                  className="font-dn"
                  autoComplete="off"
                  onChange={(e) => setConfigBindDn(e.target.value)}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="configpassword">Configuration password</Label>
                <Input
                  id="configpassword"
                  type="password"
                  value={configBindPassword}
                  autoComplete="off"
                  onChange={(e) => setConfigBindPassword(e.target.value)}
                />
                <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
                  <ShieldCheck className="mt-0.5 size-3.5 shrink-0" />
                  Held exactly like the bind password: in the server's memory for
                  this session, and nowhere else.
                </p>
              </div>
            </div>
          </details>

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
            Last connected to{" "}
            <span className="font-dn">{safeText(recent.describe(remembered))}</span>.
            Passwords are never remembered.{" "}
            <button
              type="button"
              className="underline underline-offset-2 hover:text-foreground"
              onClick={() => {
                recent.forget();
                setRemembered(null);
                fill(null);
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

type SavedMode = "idle" | "save" | "rename" | "delete";

/**
 * SavedConnections is the named list above the form: primary, replica, dev,
 * prod. Picking one fills the form; the password is still typed every time.
 *
 * It lives in this browser's localStorage, beside the last connection and under
 * the same rules (see lib/recent-connection.ts): no password of either kind and
 * no server state. Alder is stateless, and a list kept on the server would be
 * state to own, expire and secure. The names are the user's own text, but
 * storage is writable by anything on the origin, so they are shown through
 * safeText like any other string Alder did not write itself.
 */
export function SavedConnections({
  saved,
  onChange,
  current,
  onPick,
}: {
  saved: recent.SavedConnection[];
  onChange: (list: recent.SavedConnection[]) => void;
  current: recent.RecentConnection;
  onPick: (c: recent.RecentConnection) => void;
}) {
  const [picked, setPicked] = useState<string | null>(null);
  const [mode, setMode] = useState<SavedMode>("idle");
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<string | null>(null);

  const entry = saved.find((s) => s.name === picked) ?? null;
  const edited = entry !== null && !recent.same(entry.connection, current);

  const begin = (next: SavedMode) => {
    setError(null);
    setMode(next);
    // Saving starts from the picked name, so saving an edited entry under the
    // same name is how it is updated. With nothing picked, the host is a
    // reasonable first guess at a name.
    setDraft(next === "save" ? (entry?.name ?? current.host) : (entry?.name ?? ""));
  };

  const finish = (result: recent.SavedResult, name: string | null) => {
    if (!result.ok) {
      setError(recent.failureText(result.reason));
      return;
    }
    onChange(result.list);
    // Saving under an existing name in another case keeps the new spelling, so
    // look the entry up again rather than trusting what was typed.
    const kept = name === null ? null : result.list.find((s) => s.name.toLowerCase() === name.toLowerCase());
    setPicked(kept?.name ?? null);
    setMode("idle");
    setError(null);
  };

  const commit = () => {
    const name = recent.cleanName(draft);
    if (mode === "save") {
      finish(recent.saveNamed(name, current), name);
    } else if (mode === "rename" && entry) {
      finish(recent.renameSaved(entry.name, name), name);
    } else if (mode === "delete" && entry) {
      finish(recent.deleteSaved(entry.name), null);
    }
  };

  const cancel = () => {
    setMode("idle");
    setError(null);
  };

  return (
    <section
      aria-label="Saved connections"
      className="mb-4 space-y-2 rounded-lg border border-border bg-card p-3 shadow-sm"
    >
      <div className="flex items-center gap-2">
        <Select
          value={entry?.name ?? ""}
          disabled={saved.length === 0}
          onValueChange={(name) => {
            const chosen = saved.find((s) => s.name === name);
            if (!chosen) return;
            setPicked(chosen.name);
            cancel();
            onPick(chosen.connection);
          }}
        >
          <SelectTrigger aria-label="Saved connection" className="min-w-0 flex-1">
            <SelectValue
              placeholder={saved.length === 0 ? "No saved connections yet" : "Choose a saved connection"}
            />
          </SelectTrigger>
          <SelectContent>
            {saved.map((s) => (
              <SelectItem key={s.name} value={s.name}>
                {safeText(s.name)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!current.host}
          title={current.host ? "Save the form under a name" : "Enter a host first"}
          onClick={() => begin("save")}
        >
          <Bookmark />
          Save
        </Button>
        {entry ? (
          <>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label="Rename saved connection"
              title="Rename"
              onClick={() => begin("rename")}
            >
              <Pencil />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label="Delete saved connection"
              title="Delete"
              onClick={() => begin("delete")}
            >
              <Trash2 />
            </Button>
          </>
        ) : null}
      </div>

      {mode === "save" || mode === "rename" ? (
        <div className="flex items-center gap-2">
          <Input
            aria-label={mode === "save" ? "Name for this connection" : "New name"}
            value={draft}
            maxLength={recent.MAX_NAME}
            placeholder="prod, replica, dev…"
            autoFocus
            className="h-8 min-w-0 flex-1"
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              // This sits outside the connect form, so Enter would otherwise
              // do nothing. Make it do the obvious thing, and Escape back out.
              if (e.key === "Enter") {
                e.preventDefault();
                commit();
              } else if (e.key === "Escape") {
                cancel();
              }
            }}
          />
          <Button type="button" size="sm" onClick={commit}>
            {mode === "save" ? "Save" : "Rename"}
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={cancel}>
            Cancel
          </Button>
        </div>
      ) : null}

      {mode === "delete" && entry ? (
        <div className="flex items-center gap-2 text-sm">
          <span className="min-w-0 flex-1 truncate">
            Delete <span className="font-medium">{safeText(entry.name)}</span> from this browser?
          </span>
          <Button type="button" variant="destructive" size="sm" onClick={commit}>
            Delete
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={cancel}>
            Cancel
          </Button>
        </div>
      ) : null}

      {error ? <p className="text-xs text-destructive">{error}</p> : null}

      <p className="text-xs text-muted-foreground">
        {entry ? (
          <>
            <span className="font-dn">{safeText(recent.describe(entry.connection))}</span>
            {edited ? " — edited since it was picked; Save to update it." : "."}{" "}
          </>
        ) : null}
        Kept in this browser only, never with a password.
      </p>
    </section>
  );
}
