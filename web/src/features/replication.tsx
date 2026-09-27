import { useMutation } from "@tanstack/react-query";
import { ArrowDownToLine, ArrowUpFromLine, Loader2, Radio } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { ApiFailure } from "@/lib/api";
import type { components } from "@/lib/api.gen";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ErrorNote } from "@/components/change-dialog";
import { safeText } from "@/lib/display";

type ReplicationReport = components["schemas"]["ReplicationReport"];
type ReplicatedSuffix = components["schemas"]["ReplicatedSuffix"];
type ReplicationLink = components["schemas"]["ReplicationLink"];
type ReplicationCursor = components["schemas"]["ReplicationCursor"];

/**
 * What this server records about its own replication.
 *
 * Read on request rather than on load, for the same reason the entry counts
 * on this page are: it is a search of the configuration tree, and the overview
 * is the page that opens instantly.
 *
 * The three questions, in the order an operator asks them: is this a supplier
 * or a consumer, who is it linked to, and how far along is it. The third is
 * the only one both servers answer the same way, and it is the one that tells
 * you whether replication is actually working — so it is not buried.
 */
export function ReplicationCard() {
  const report = useMutation<ReplicationReport, ApiFailure>({
    mutationFn: async () => unwrap(await api.GET("/replication", {})),
  });

  return (
    <section className="rounded-lg border border-border">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2 text-sm font-medium">
        <Radio className="size-4 text-muted-foreground" />
        Replication
        {report.data ? <RoleBadge role={report.data.role} /> : null}
      </header>

      {!report.data && !report.isError ? (
        <div className="space-y-1 px-4 py-3">
          <Button
            variant="outline"
            size="sm"
            onClick={() => report.mutate()}
            disabled={report.isPending}
          >
            {report.isPending ? <Loader2 className="animate-spin" /> : <Radio />}
            Read this server's replication
          </Button>
          <p className="text-xs text-muted-foreground">
            Whether this server supplies or receives changes, who it is linked to, and how far
            along it is. Reads the configuration tree, so it is asked for rather than loaded.
          </p>
        </div>
      ) : null}

      {report.isError ? (
        <div className="px-4 py-3">
          <ErrorNote title="Replication could not be read" error={report.error} />
        </div>
      ) : null}

      {report.data ? (
        <div className="space-y-3 px-4 py-3">
          <p className="text-sm">{safeText(report.data.why)}</p>

          {report.data.suffixes.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Nothing in this server's configuration sets up a link to another server.
            </p>
          ) : (
            report.data.suffixes.map((suffix) => <SuffixBlock key={suffix.dn} suffix={suffix} />)
          )}

          {(report.data.unread ?? []).map((unread) => (
            <p key={unread.where} className="rounded-md border p-3 text-xs">
              <span className="font-dn">{safeText(unread.where)}</span> could not be read, so this
              is not the whole answer — {safeText(unread.reason)}.
            </p>
          ))}

          <p className="rounded-md border border-warning/40 bg-warning/10 p-3 text-xs text-warning-tint-foreground">
            {safeText(report.data.disclaimer)}
          </p>
        </div>
      ) : null}
    </section>
  );
}

function RoleBadge({ role }: { role: ReplicationReport["role"] }) {
  const look: Record<string, { label: string; variant: "success" | "outline" | "warning" }> = {
    supplier: { label: "supplier", variant: "success" },
    consumer: { label: "consumer", variant: "outline" },
    both: { label: "supplier and consumer", variant: "success" },
    none: { label: "not replicated", variant: "outline" },
  };
  const it = look[role] ?? { label: role, variant: "outline" as const };
  return <Badge variant={it.variant}>{it.label}</Badge>;
}

function SuffixBlock({ suffix }: { suffix: ReplicatedSuffix }) {
  return (
    <div className="rounded-md border p-3">
      <div className="flex flex-wrap items-baseline gap-2">
        <span className="font-dn text-sm [overflow-wrap:anywhere]">{safeText(suffix.dn)}</span>
        {suffix.serverId ? (
          <span className="text-xs text-muted-foreground">
            this server is id <span className="font-medium">{safeText(suffix.serverId)}</span>
          </span>
        ) : null}
      </div>

      {suffix.cursors.length > 0 ? (
        <div className="mt-2">
          <div className="text-xs font-medium">How far along this server is</div>
          <ul className="mt-0.5 space-y-0.5">
            {suffix.cursors.map((cursor) => (
              <li key={cursor.origin + cursor.raw} className="text-xs">
                <CursorLine cursor={cursor} />
              </li>
            ))}
          </ul>
          {/* The one sentence that makes the numbers above usable. Comparing
              them between two servers is the whole method, and nobody guesses
              that from a timestamp on its own. */}
          <p className="mt-1 text-xs text-muted-foreground">
            Open this page against the other server and compare: the same numbers mean the two are
            in step.
          </p>
        </div>
      ) : null}

      {suffix.links.length > 0 ? (
        <ul className="mt-2 space-y-1.5">
          {suffix.links.map((link) => (
            <li key={link.dn + link.name}>
              <LinkRow link={link} />
            </li>
          ))}
        </ul>
      ) : null}

      {(suffix.notes ?? []).map((note) => (
        <p key={note} className="mt-1.5 text-xs text-muted-foreground">
          {safeText(note)}
        </p>
      ))}
    </div>
  );
}

function CursorLine({ cursor }: { cursor: ReplicationCursor }) {
  return (
    <span className="flex flex-wrap items-baseline gap-1.5">
      <span className="text-muted-foreground">from server {safeText(cursor.origin)}:</span>
      <span className="font-medium">{formatMoment(cursor.at)}</span>
      <span className="text-muted-foreground">({ago(cursor.at)})</span>
      <code className="font-mono text-[11px] text-muted-foreground [overflow-wrap:anywhere]">
        {safeText(cursor.raw)}
      </code>
    </span>
  );
}

function LinkRow({ link }: { link: ReplicationLink }) {
  const incoming = link.direction === "incoming";
  const Icon = incoming ? ArrowDownToLine : ArrowUpFromLine;
  return (
    <div className="rounded-md border px-2.5 py-2">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Icon className="size-3.5 text-muted-foreground" />
        <span className="font-medium">{safeText(link.name)}</span>
        <span className="text-muted-foreground">{incoming ? "from" : "to"}</span>
        <span className="font-dn [overflow-wrap:anywhere]">{safeText(link.peer) || "unnamed"}</span>
        {link.transport ? <Badge variant="outline">{safeText(link.transport)}</Badge> : null}
        <StateBadge state={link.state} />
        {link.inProgress ? <Badge variant="outline">updating now</Badge> : null}
      </div>
      {link.bindDn ? (
        <div className="mt-0.5 text-xs text-muted-foreground">
          binds as <span className="font-dn">{safeText(link.bindDn)}</span>
        </div>
      ) : null}
      {link.status ? (
        <div className="mt-0.5 text-xs [overflow-wrap:anywhere]">{safeText(link.status)}</div>
      ) : null}
      {link.lastUpdate ? (
        <div className="mt-0.5 text-xs text-muted-foreground">
          last exchange {formatMoment(link.lastUpdate)} ({ago(link.lastUpdate)})
        </div>
      ) : null}
      {(link.notes ?? []).map((note) => (
        <div key={note} className="mt-0.5 text-xs text-muted-foreground">
          {safeText(note)}
        </div>
      ))}
    </div>
  );
}

function StateBadge({ state }: { state: ReplicationLink["state"] }) {
  switch (state) {
    case "ok":
      return <Badge variant="success">last exchange succeeded</Badge>;
    case "working":
      return <Badge variant="outline">in progress</Badge>;
    case "failing":
      return <Badge variant="warning">last exchange failed</Badge>;
    default:
      return <Badge variant="outline">no status recorded</Badge>;
  }
}

/** A moment in the reader's own zone, because a directory writes UTC and an
 * operator reads a clock on the wall. */
export function formatMoment(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return safeText(iso);
  return at.toLocaleString();
}

/** How long ago, in the words a person uses. Anything older than a day is
 * given in days: the exact hour stopped mattering a long time before that. */
export function ago(iso: string, now: Date = new Date()): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "";
  const seconds = Math.round((now.getTime() - at.getTime()) / 1000);
  if (seconds < 0) return "in the future by this server's clock";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.round(hours / 24)}d ago`;
}
