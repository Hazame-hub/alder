import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import {
  Activity,
  Check,
  Hash,
  Lock,
  Minus,
  ServerCog,
  ShieldAlert,
  ShieldCheck,
  Loader2,
} from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { ApiFailure, CountResult, SessionInfo } from "@/lib/api";
import type { AppView } from "@/lib/route";
import { displayText } from "@/lib/values";
import { Button } from "@/components/ui/button";
import { ErrorNote } from "@/components/change-dialog";

/**
 * What this session is connected to, and what it can do there.
 *
 * Everything on this page is something the server said. The connection details
 * and every capability come from the RootDSE read at connect time, which has
 * already happened — so the page costs nothing to open.
 *
 * Two things it deliberately does not do. It does not count entries on load:
 * counting a naming context is a search, and on a real suffix an expensive one,
 * so it is a button per context and the answer says "at least" when it stopped
 * early. And it invents nothing — a server that publishes no monitoring entry
 * gets no monitoring section, rather than a row of dashes implying something
 * failed.
 */

export function OverviewPanel({
  info,
  onBrowse,
  onView,
}: {
  info: SessionInfo;
  onBrowse: (dn: string) => void;
  onView: (v: AppView) => void;
}) {
  const caps = info.capabilities;
  const contexts = caps?.namingContexts ?? [];

  return (
    <div className="mx-auto max-w-5xl space-y-5 px-6 py-6">
      <header className="space-y-1">
        <h2 className="text-lg font-semibold">
          {info.host}:{info.port}
        </h2>
        <p className="text-sm text-muted-foreground">
          {info.vendorName ? (
            <>
              {info.vendorName}
              {info.vendorVersion ? ` · ${info.vendorVersion}` : ""}
            </>
          ) : (
            "The server does not publish a vendor name."
          )}
        </p>
      </header>

      <div className="grid gap-4 md:grid-cols-2">
        <Card title="Connection" icon={ServerCog}>
          <Row label="Transport">
            <span className="font-dn">{info.tls ?? "unknown"}</span>
          </Row>
          <Row label="Certificate">
            {info.verified === false ? (
              <span className="flex items-center gap-1.5 text-warning-tint-foreground">
                <ShieldAlert className="size-3.5" />
                not verified for this session
              </span>
            ) : (
              <span className="flex items-center gap-1.5">
                <ShieldCheck className="size-3.5" />
                verified
              </span>
            )}
          </Row>
          <Row label="Bound as">
            <span className="font-dn">{info.bindDn || "anonymous"}</span>
          </Row>
          {info.readOnly ? (
            <Row label="This instance">
              <span className="flex items-center gap-1.5">
                <Lock className="size-3.5" />
                started read-only; every write is refused here
              </span>
            </Row>
          ) : null}
        </Card>

        <Card title="What this session can do" icon={Check}>
          <Flag on={caps?.paging === true} label="Paged results">
            Without it a large search cannot be walked, and results are whatever
            the server's own size limit allowed.
          </Flag>
          <Flag on={caps?.passwordModify === true} label="Password modify (RFC 3062)">
            Where it is offered, a password is set by asking the server rather
            than by writing a hash, so the server chooses the scheme.
          </Flag>
          <Flag on={caps?.serverSort === true} label="Server-side sort" />
          <Flag on={caps?.whoAmI === true} label="Who am I (RFC 4532)" />
        </Card>

        <Card title="Schema" icon={Hash}>
          <Row label="Published at">
            <button
              type="button"
              className="font-dn hover:underline"
              onClick={() => onBrowse(caps?.subschemaSubentry ?? "")}
              disabled={!caps?.subschemaSubentry}
            >
              {caps?.subschemaSubentry || "not published"}
            </button>
          </Row>
          <Row label="Editable">
            <SchemaWritability info={info} onView={onView} />
          </Row>
        </Card>

        <Card title="Configuration" icon={ServerCog}>
          {caps?.config?.dn ? (
            <>
              <Row label="Tree">
                <button
                  type="button"
                  className="font-dn hover:underline"
                  onClick={() => onBrowse(caps.config?.dn as string)}
                >
                  {caps.config.dn}
                </button>
              </Row>
              <Row label="Readable">
                {caps.config.readable ? (
                  <span>
                    yes
                    {caps.config.separateBind ? (
                      <>
                        , as{" "}
                        <span className="font-dn">{caps.config.boundAs}</span>
                      </>
                    ) : null}
                  </span>
                ) : (
                  <span className="text-muted-foreground">
                    {caps.config.reason ||
                      "not with this identity — a configuration account is usually separate"}
                  </span>
                )}
              </Row>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">
              This server announces no configuration tree, and none was found at
              the conventional location.
            </p>
          )}
        </Card>
      </div>

      <NamingContexts contexts={contexts} onBrowse={onBrowse} />

      {caps?.monitor?.readable && caps.monitor.dn ? (
        <MonitorCard dn={caps.monitor.dn} onBrowse={onBrowse} />
      ) : null}
    </div>
  );
}

function SchemaWritability({
  info,
  onView,
}: {
  info: SessionInfo;
  onView: (v: AppView) => void;
}) {
  const write = info.capabilities?.schemaWrite;
  if (!write || write.style === "none") {
    return (
      <span className="text-muted-foreground">
        {write?.unavailable ?? "no writable location was found"}
      </span>
    );
  }
  const targets = write.targets ?? [];
  return (
    <span>
      yes —{" "}
      <button type="button" className="hover:underline" onClick={() => onView("schema")}>
        {targets.length === 1
          ? "one location"
          : `${targets.length} locations, and which one a definition joins matters`}
      </button>
    </span>
  );
}

/**
 * The suffixes this directory holds, and how big they are if you ask.
 *
 * Counting is a button rather than something this page does on load. A count is
 * a subtree search; on a directory with a hundred thousand people, doing three
 * of them because somebody opened a landing page is exactly the kind of thing
 * an admin tool should not do behind your back.
 */
function NamingContexts({
  contexts,
  onBrowse,
}: {
  contexts: string[];
  onBrowse: (dn: string) => void;
}) {
  return (
    <section className="rounded-lg border border-border">
      <header className="border-b border-border px-4 py-2 text-sm font-medium">
        Naming contexts
      </header>
      {contexts.length === 0 ? (
        <p className="px-4 py-3 text-sm text-muted-foreground">
          The server publishes no naming contexts, so there is nothing to browse.
          That usually means the bound identity may not see them.
        </p>
      ) : (
        <ul className="divide-y divide-border">
          {contexts.map((dn) => (
            <ContextRow key={dn} dn={dn} onBrowse={onBrowse} />
          ))}
        </ul>
      )}
    </section>
  );
}

function ContextRow({ dn, onBrowse }: { dn: string; onBrowse: (dn: string) => void }) {
  const count = useMutation<CountResult, ApiFailure>({
    mutationFn: async () =>
      unwrap(await api.GET("/count", { params: { query: { dn } } })),
  });

  return (
    <li className="flex flex-wrap items-center gap-3 px-4 py-2.5">
      <button
        type="button"
        className="min-w-0 flex-1 truncate text-left font-dn text-sm hover:underline"
        title={dn}
        onClick={() => onBrowse(dn)}
      >
        {dn}
      </button>

      {count.data ? (
        <span className="text-sm tabular-nums">
          {count.data.truncated ? "at least " : ""}
          <span className="font-medium">{count.data.count.toLocaleString()}</span>{" "}
          entries
          {count.data.took ? (
            <span className="ml-1.5 text-xs text-muted-foreground">
              in {count.data.took}
            </span>
          ) : null}
        </span>
      ) : count.isError ? (
        <span className="text-sm text-destructive">{count.error.message}</span>
      ) : (
        <Button
          variant="outline"
          size="sm"
          disabled={count.isPending}
          onClick={() => count.mutate()}
        >
          {count.isPending ? <Loader2 className="animate-spin" /> : <Hash />}
          Count entries
        </Button>
      )}
    </li>
  );
}

/**
 * The server's own monitoring entry, shown only where there is one.
 *
 * What it holds is entirely the server's business — the attributes differ
 * completely between the two Alder targets — so this renders whatever is
 * published rather than looking for particular names. A curated list of
 * "interesting" counters would be a list of one server's counters.
 */
function MonitorCard({ dn, onBrowse }: { dn: string; onBrowse: (dn: string) => void }) {
  const [showAll, setShowAll] = useState(false);
  const entry = useQuery({
    queryKey: ["entry", dn],
    queryFn: async () => unwrap(await api.GET("/entry", { params: { query: { dn } } })),
  });

  const attrs = (entry.data?.attributes ?? []).filter(
    (a) => a.name.toLowerCase() !== "objectclass" && !a.withheld,
  );
  const shown = showAll ? attrs : attrs.slice(0, 12);

  return (
    <section className="rounded-lg border border-border">
      <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-2">
        <Activity className="size-4 text-muted-foreground" />
        <span className="text-sm font-medium">Server monitor</span>
        <button
          type="button"
          className="font-dn text-xs text-muted-foreground hover:underline"
          onClick={() => onBrowse(dn)}
        >
          {dn}
        </button>
      </header>

      {entry.isPending ? (
        <p className="flex items-center gap-2 px-4 py-3 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          Reading it…
        </p>
      ) : entry.isError ? (
        <div className="p-4">
          <ErrorNote
            title="The monitoring entry could not be read"
            error={entry.error as ApiFailure}
          />
        </div>
      ) : (
        <>
          <dl className="grid gap-x-6 gap-y-1.5 px-4 py-3 text-sm sm:grid-cols-2">
            {shown.map((a) => (
              <div key={a.name} className="flex items-baseline justify-between gap-3">
                <dt className="truncate font-dn text-xs text-muted-foreground" title={a.name}>
                  {a.name}
                </dt>
                <dd className="shrink-0 truncate tabular-nums" title={a.values.map(displayText).join(", ")}>
                  {a.values.length === 0
                    ? "—"
                    : displayText(a.values[0] as (typeof a.values)[number])}
                  {a.values.length > 1 ? (
                    <span className="ml-1 text-xs text-muted-foreground">
                      +{a.values.length - 1}
                    </span>
                  ) : null}
                </dd>
              </div>
            ))}
          </dl>
          {attrs.length > shown.length ? (
            <div className="px-4 pb-3">
              <Button variant="outline" size="sm" onClick={() => setShowAll(true)}>
                Show all {attrs.length}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}

function Card({
  title,
  icon: Icon,
  children,
}: {
  title: string;
  icon: typeof Check;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-border">
      <header className="flex items-center gap-2 border-b border-border px-4 py-2">
        <Icon className="size-4 text-muted-foreground" />
        <span className="text-sm font-medium">{title}</span>
      </header>
      <dl className="space-y-1.5 px-4 py-3 text-sm">{children}</dl>
    </section>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-3">
      <dt className="w-32 shrink-0 text-xs text-muted-foreground">{label}</dt>
      {/*
        Wrapping, not truncating. Several of these rows carry the sentence
        explaining why something is unavailable — "this session cannot read
        cn=config", "the schema lives in configuration" — and a reason cut off
        at the card edge is the same as no reason at all. A long DN wrapping is
        the lesser problem.
      */}
      <dd className="min-w-0 flex-1 break-words">{children}</dd>
    </div>
  );
}

function Flag({
  on,
  label,
  children,
}: {
  on: boolean;
  label: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-2">
      {on ? (
        <Check className="mt-0.5 size-3.5 shrink-0 text-primary" />
      ) : (
        <Minus className="mt-0.5 size-3.5 shrink-0 text-muted-foreground/60" />
      )}
      <div className="min-w-0">
        <div className={on ? "" : "text-muted-foreground"}>{label}</div>
        {!on && children ? (
          <div className="text-xs text-muted-foreground">{children}</div>
        ) : null}
      </div>
    </div>
  );
}
