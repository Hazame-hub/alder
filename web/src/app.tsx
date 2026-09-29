import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import {
  Database,
  FileUp,
  Camera,
  ClipboardCheck,
  Package,
  FolderTree,
  Gauge,
  Layers,
  ListChecks,
  Loader2,
  LogOut,
  Moon,
  Search as SearchIcon,
  ShieldAlert,
  Sun,
  User,
  Users,
} from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { ObjectViewId, SessionInfo } from "@/lib/api";
import { cn } from "@/lib/utils";
import { jumpShortcut } from "@/lib/shortcut";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { TooltipProvider } from "@/components/ui";
import { AlderMark } from "@/components/mark";
import { ConnectScreen } from "@/features/connect";
import { Tree } from "@/features/tree";
import { EntryPanel } from "@/features/entry";
import { SchemaBrowser } from "@/features/schema";
import { SearchPanel } from "@/features/search";
import { ImportPanel } from "@/features/import";
import { SnapshotsPanel } from "@/features/snapshots";
import { PackagesPanel } from "@/features/packages";
import { PreflightPanel } from "@/features/preflight";
import { JumpPalette } from "@/features/palette";
import { ChangesetView } from "@/features/changeset";
import { ObjectListPanel } from "@/features/objects";
import { OverviewPanel } from "@/features/overview";
import { isDirectoryView, type AppSearch, type AppView } from "@/lib/route";
import { useChangeset } from "@/lib/changeset";
import { setNavigator } from "@/lib/navigate";
import { SourceLink } from "@/components/source-link";
import { safeText } from "@/lib/display";
import { resetHealth, useDirectoryHealth } from "@/lib/directory-health";

/**
 * The top bar offers destinations, and the URL says which one you are on.
 *
 * Directory is the only section with pages of its own, and they sit on a second
 * row rather than behind a menu: Users is a destination, and putting it one
 * click deep inside a dropdown would undo the reason for having it.
 */

export function App() {
  const queryClient = useQueryClient();
  const search = useSearch({ strict: false }) as AppSearch;
  const navigate = useNavigate();

  const view: AppView = search.view ?? "overview";
  const selectedDN = search.dn ?? null;
  const openForEdit = search.edit === true;

  /** Change part of the location, leaving the rest of it alone. */
  const go = (next: Partial<AppSearch>) =>
    void navigate({ to: "/", search: { ...search, ...next } });

  // The error note can offer somewhere to look when the directory refuses a
  // change, and it is rendered too deep to hand a callback to. See
  // lib/navigate for why this one thing is a module store.
  setNavigator(go);

  // Whether the jump palette is open. Up here with the other hooks rather
  // than beside the palette it belongs to: the connection screen and the
  // loading spinner both return before that point, and a hook below an early
  // return is a hook count that changes between renders.
  const [jumpOpen, setJumpOpen] = useState(false);

  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => unwrap(await api.GET("/session")),
    // A session can expire while the tab is open; re-checking on focus is what
    // turns that into the connection screen rather than a wall of 401s.
    refetchOnWindowFocus: true,
    retry: false,
  });

  // Browsing with no entry chosen lands on the first naming context, which is
  // where the directory actually starts. Done here rather than in the URL's
  // defaults because it needs the connection to have answered first.
  useEffect(() => {
    if (view !== "browse" || selectedDN || !session.data?.connected) return;
    const first = session.data.capabilities?.namingContexts?.[0];
    if (first) go({ dn: first });
    // go is stable enough for this; re-running on every render would loop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, selectedDN, session.data]);

  if (session.isPending) {
    return (
      <div className="grid h-full place-items-center text-sm text-muted-foreground">
        <Loader2 className="size-5 animate-spin" />
      </div>
    );
  }

  if (!session.data?.connected) {
    return (
      <ConnectScreen
        onConnected={(info) => {
          queryClient.setQueryData(["session"], info);
          // Connecting lands on the overview: it is the one page that answers
          // "what am I connected to, and what can I do here".
          go({ view: "overview", dn: undefined, edit: undefined });
        }}
      />
    );
  }

  const info = session.data;
  const contexts = info.capabilities?.namingContexts ?? [];

  // Opening an entry always lands on the tree, whichever page asked for it. The
  // entry panel is where an entry is read and edited, and it lives there.
  const openEntry = (dn: string, forEdit = false) =>
    go({ view: "browse", dn, edit: forEdit ? true : undefined });

  return (
    <TooltipProvider delayDuration={300}>
      <div className="flex h-full flex-col">
        <TopBar
          info={info}
          view={view}
          onView={(v) => go({ view: v })}
          onJump={() => setJumpOpen(true)}
        />
        {isDirectoryView(view) ? (
          <DirectoryNav view={view} onView={(v) => go({ view: v })} />
        ) : null}

        <JumpPalette
          open={jumpOpen}
          onOpenChange={setJumpOpen}
          onEntry={openEntry}
          onSearch={(filter, base) =>
            go({ view: "search", filter, base, scope: "sub" })
          }
        />

        <div className="flex min-h-0 flex-1">
          {view === "overview" ? (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <OverviewPanel
                info={info}
                onBrowse={(dn) => openEntry(dn)}
                onView={(v) => go({ view: v })}
                onCompareConfig={() => go({ view: "snapshots", capture: "config" })}
              />
            </main>
          ) : view === "browse" ? (
            <>
              <aside className="flex w-72 shrink-0 flex-col border-r border-border">
                <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Directory
                </div>
                <div className="min-h-0 flex-1 overflow-auto px-1">
                  <Tree
                    selectedDN={selectedDN}
                    onSelect={(dn) => go({ dn, edit: undefined })}
                  />
                </div>
              </aside>
              <main className="min-w-0 flex-1">
                {selectedDN ? (
                  <EntryPanel
                    dn={selectedDN}
                    readOnly={info.readOnly === true}
                    onNavigate={openEntry}
                    startEditing={openForEdit}
                    onDeleted={(parent) => go({ dn: parent || undefined, edit: undefined })}
                    schemaTargets={(info.capabilities?.schemaWrite?.targets ?? []).map(
                      (t) => t.dn,
                    )}
                    onOpenSchema={() => go({ view: "schema" })}
                    onSearch={(filter, base) =>
                      go({ view: "search", filter, base, scope: "sub" })
                    }
                    onReviewChangeset={() => go({ view: "changeset" })}
                  />
                ) : (
                  <p className="p-8 text-sm text-muted-foreground">
                    Pick an entry from the tree.
                  </p>
                )}
              </main>
            </>
          ) : isDirectoryView(view) ? (
            <main className="min-w-0 flex-1">
              <ObjectListPanel
                viewId={view as ObjectViewId}
                namingContexts={contexts}
                readOnly={info.readOnly === true}
                // The same URL parameter the search page uses, so a link to a
                // bigger page of users is a link like any other.
                limit={search.limit}
                onLimitChange={(limit) => go({ limit })}
                onOpenEntry={openEntry}
                onReviewChangeset={() => go({ view: "changeset" })}
              />
            </main>
          ) : view === "schema" ? (
            <main className="min-w-0 flex-1">
              <SchemaBrowser initialQuery={search.find} />
            </main>
          ) : view === "search" ? (
            <main className="min-w-0 flex-1">
              <SearchPanel
                base={search.base ?? searchBaseFor(info, selectedDN)}
                scope={search.scope ?? "sub"}
                filter={search.filter ?? "(objectClass=*)"}
                limit={search.limit ?? 100}
                readOnly={info.readOnly === true}
                onChange={(next) => go(next)}
                onOpenEntry={openEntry}
                onReviewChangeset={() => go({ view: "changeset" })}
              />
            </main>
          ) : view === "changeset" ? (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <ChangesetView onBrowse={openEntry} />
            </main>
          ) : view === "snapshots" ? (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <SnapshotsPanel
                onReviewChangeset={() => go({ view: "changeset" })}
                openWith={search.capture}
              />
            </main>
          ) : view === "packages" ? (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <PackagesPanel onReviewChangeset={() => go({ view: "changeset" })} />
            </main>
          ) : view === "preflight" ? (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <PreflightPanel />
            </main>
          ) : (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <ImportPanel onReviewChangeset={() => go({ view: "changeset" })} />
            </main>
          )}
        </div>
      </div>
    </TooltipProvider>
  );
}

/**
 * searchBaseFor picks the naming context the current selection sits in.
 *
 * Defaulting to the selected entry itself reads well until the selection is a
 * leaf, at which point a subtree search from it can only ever return that one
 * entry. Starting from its suffix is what the user meant by "search"; they can
 * narrow the base from there.
 */
function searchBaseFor(info: SessionInfo, selectedDN: string | null): string {
  const contexts = info.capabilities?.namingContexts ?? [];
  if (selectedDN) {
    const containing = contexts.find((ctx) =>
      selectedDN.toLowerCase().endsWith(ctx.toLowerCase()),
    );
    if (containing) return containing;
  }
  return contexts[0] ?? selectedDN ?? "";
}

function DirectoryNav({
  view,
  onView,
}: {
  view: AppView;
  onView: (v: AppView) => void;
}) {
  const pages: [AppView, string, typeof Database][] = [
    ["browse", "Browse", FolderTree],
    ["users", "Users", User],
    ["groups", "Groups", Users],
    ["organizationalUnits", "Organizational units", Database],
  ];
  return (
    <nav className="flex shrink-0 items-center gap-0.5 border-b border-border px-3 py-1">
      {pages.map(([id, label, Icon]) => (
        <button
          key={id}
          type="button"
          onClick={() => onView(id)}
          className={cn(
            "flex items-center gap-1.5 rounded-md px-2.5 py-1 text-sm transition-colors",
            view === id
              ? "bg-accent font-medium text-accent-foreground"
              : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
          )}
        >
          <Icon className="size-3.5" />
          {label}
        </button>
      ))}
    </nav>
  );
}

/**
 * The way in to the jump palette, for somebody who does not know it exists.
 *
 * The palette is the fastest thing in the application and for three releases
 * nothing said so: a UI audit found Ctrl+K by guessing, having first clicked
 * down the tree to reach an entry whose DN was already on the clipboard. A
 * keystroke with no affordance is a feature only its author has.
 *
 * Shaped like the field it opens rather than an icon, because the gesture it
 * teaches is "type what you are looking for" -- an icon teaches nothing and
 * has to be hovered to find out. The key is printed on it so the second visit
 * costs no click at all, which is the point of advertising a shortcut rather
 * than just a button.
 */
export function JumpButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="ml-auto flex w-56 items-center gap-2 rounded-md border border-border bg-background/60 px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:border-foreground/25 hover:text-foreground"
    >
      <SearchIcon className="size-4 shrink-0" />
      <span className="truncate">Jump to…</span>
      <kbd className="ml-auto rounded border border-border px-1.5 py-0.5 font-sans text-[11px] leading-none text-muted-foreground">
        {jumpShortcut()}
      </kbd>
    </button>
  );
}

/**
 * Whether the directory is answering, in the one place that claims it is.
 *
 * The header says "Bound as cn=Directory Manager" and it reads that from
 * GET /session, which is an object in Alder's own memory: measured with the
 * harness 389 DS paused, it answered 200 in 1.6ms and reported a healthy bind
 * while every directory call was timing out at thirty seconds. A header that
 * reports health it has not checked is worse than one that reports nothing,
 * because it sends the reader looking for their own mistake.
 *
 * So this watches what the requests are actually doing. It appears only when
 * something has failed upstream, and it offers the cure, which the interface
 * never used to suggest: disconnect and connect again.
 */
function DirectoryHealth({ onReconnect }: { onReconnect: () => void }) {
  const health = useDirectoryHealth();
  if (health.kind === "ok") return null;
  // Still going. Said while it is happening rather than after it has failed:
  // the operation timeout is thirty seconds, so the honest version of this
  // badge has to appear long before the failure does.
  if (health.kind === "waiting") {
    return (
      <Badge
        variant="warning"
        className="gap-1"
        title="A request to the directory has not come back yet. Alder gives one thirty seconds before it gives up."
      >
        <Loader2 className="size-3 animate-spin" />
        waiting for the directory — {health.seconds}s
      </Badge>
    );
  }
  return (
    <div className="flex items-center gap-1.5">
      <Badge variant="destructive" className="gap-1" title={safeText(health.message)}>
        <ShieldAlert className="size-3" />
        the directory is not answering
      </Badge>
      <Button variant="outline" size="sm" onClick={onReconnect}>
        Reconnect
      </Button>
    </div>
  );
}

function TopBar({
  info,
  view,
  onView,
  onJump,
}: {
  info: SessionInfo;
  view: AppView;
  onView: (v: AppView) => void;
  onJump: () => void;
}) {
  const queryClient = useQueryClient();
  const [dark, setDark] = useState(
    () =>
      document.documentElement.classList.contains("dark") ||
      window.matchMedia("(prefers-color-scheme: dark)").matches,
  );

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
  }, [dark]);

  /**
   * Let go of the session.
   *
   * Three things were wrong with the obvious version, and an audit hit all
   * three at once: five clicks on Disconnect, nothing on screen, and
   * `DELETE /api/v1/session` answering 503 in the network log.
   *
   * It awaited `invalidateQueries()` before saying "disconnected", and that
   * refetches every live query -- against the directory you are trying to
   * stop talking to. With the directory slow, each one takes the full
   * operation timeout, so the screen sat there. Nothing is refetched now;
   * the cache is dropped, because leaving is not a reason to read anything.
   *
   * It called the API outside the query and mutation caches, so the failure
   * handler that 1.29 put in one place never saw it. It goes through a
   * mutation now, keyed, like every other write.
   *
   * And the request itself was queued behind the in-flight gate that exists
   * to protect the directory, though it touches no directory at all. That
   * one is fixed on the server, in internal/api/limit.go.
   */
  const disconnect = useMutation({
    mutationKey: ["disconnect"],
    mutationFn: async () => unwrap(await api.DELETE("/session")),
    onSettled: () => {
      // Whatever the server said. If the delete failed, the credentials are
      // still in Alder's memory and the session may still be open -- but
      // this tab is done with it, and the honest thing on screen is the
      // connection form rather than a directory we have stopped using. The
      // failure itself reaches the console and the header through the
      // mutation cache, which is the point of routing it that way.
      queryClient.setQueryData(["session"], { connected: false });
      queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== "session" });
      resetHealth();
    },
  });

  const staged = useChangeset();

  const tabs: [AppView, string, typeof Database][] = [
    ["overview", "Overview", Gauge],
    ["browse", "Directory", Database],
    ["search", "Search", SearchIcon],
    ["schema", "Schema", Layers],
    ["changeset", "Changeset", ListChecks],
    ["import", "Import", FileUp],
    ["snapshots", "Snapshots & drift", Camera],
    ["packages", "Packages", Package],
    ["preflight", "Preflight", ClipboardCheck],
  ];

  return (
    <header className="flex shrink-0 flex-wrap items-center gap-3 border-b border-border px-3 py-2">
      <div className="flex items-center gap-2">
        <AlderMark className="size-6" />
        <span className="font-semibold tracking-tight">Alder</span>
      </div>

      <nav className="flex items-center gap-0.5">
        {tabs.map(([id, label, Icon]) => (
          <button
            key={id}
            type="button"
            onClick={() => onView(id)}
            className={cn(
              "flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm transition-colors",
              (id === "browse" ? isDirectoryView(view) : view === id)
                ? "bg-accent font-medium text-accent-foreground"
                : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
            )}
          >
            <Icon className="size-4" />
            {label}
            {id === "changeset" && staged.length > 0 ? (
              <Badge variant="success" className="ml-0.5 px-1.5 tabular-nums">
                {staged.length}
              </Badge>
            ) : null}
          </button>
        ))}
      </nav>

      <JumpButton onClick={onJump} />

      <div className="flex items-center gap-2">
        {info.readOnly ? <Badge variant="outline">read-only</Badge> : null}
        {info.verified === false ? (
          <Badge variant="destructive" className="gap-1">
            <ShieldAlert className="size-3" />
            unverified TLS
          </Badge>
        ) : null}
        <DirectoryHealth onReconnect={() => disconnect.mutate()} />
        <div className="hidden text-right text-xs leading-tight sm:block">
          <div className="font-dn">
            {safeText(info.host)}:{info.port}
          </div>
          <div className="text-muted-foreground">
            {info.bindDn ? (
              <span className="font-dn">{safeText(info.bindDn)}</span>
            ) : (
              "anonymous"
            )}
            {info.vendorName ? ` · ${info.vendorName}` : ""}
          </div>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={() => setDark((d) => !d)}
          aria-label={dark ? "Switch to light" : "Switch to dark"}
        >
          {dark ? <Sun /> : <Moon />}
        </Button>
        <SourceLink />
        <Button variant="ghost" size="sm" onClick={() => disconnect.mutate()}>
          <LogOut />
          Disconnect
        </Button>
      </div>
    </header>
  );
}
