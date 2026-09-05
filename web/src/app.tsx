import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import {
  Database,
  FileUp,
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
import { ChangesetView } from "@/features/changeset";
import { ObjectListPanel } from "@/features/objects";
import { OverviewPanel } from "@/features/overview";
import { isDirectoryView, type AppSearch, type AppView } from "@/lib/route";
import { useChangeset } from "@/lib/changeset";
import { SourceLink } from "@/components/source-link";

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
        <TopBar info={info} view={view} onView={(v) => go({ view: v })} />
        {isDirectoryView(view) ? (
          <DirectoryNav view={view} onView={(v) => go({ view: v })} />
        ) : null}

        <div className="flex min-h-0 flex-1">
          {view === "overview" ? (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <OverviewPanel
                info={info}
                onBrowse={(dn) => openEntry(dn)}
                onView={(v) => go({ view: v })}
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
                onOpenEntry={openEntry}
                onReviewChangeset={() => go({ view: "changeset" })}
              />
            </main>
          ) : view === "schema" ? (
            <main className="min-w-0 flex-1">
              <SchemaBrowser />
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
              />
            </main>
          ) : view === "changeset" ? (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <ChangesetView onBrowse={openEntry} />
            </main>
          ) : (
            <main className="min-w-0 flex-1 overflow-y-auto">
              <ImportPanel />
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

function TopBar({
  info,
  view,
  onView,
}: {
  info: SessionInfo;
  view: AppView;
  onView: (v: AppView) => void;
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

  const disconnect = async () => {
    await api.DELETE("/session");
    await queryClient.invalidateQueries();
    queryClient.setQueryData(["session"], { connected: false });
  };

  const staged = useChangeset();

  const tabs: [AppView, string, typeof Database][] = [
    ["overview", "Overview", Gauge],
    ["browse", "Directory", Database],
    ["search", "Search", SearchIcon],
    ["schema", "Schema", Layers],
    ["changeset", "Changeset", ListChecks],
    ["import", "Import", FileUp],
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

      <div className="ml-auto flex items-center gap-2">
        {info.readOnly ? <Badge variant="outline">read-only</Badge> : null}
        {info.verified === false ? (
          <Badge variant="destructive" className="gap-1">
            <ShieldAlert className="size-3" />
            unverified TLS
          </Badge>
        ) : null}
        <div className="hidden text-right text-xs leading-tight sm:block">
          <div className="font-dn">
            {info.host}:{info.port}
          </div>
          <div className="text-muted-foreground">
            {info.bindDn ? (
              <span className="font-dn">{info.bindDn}</span>
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
        <Button variant="ghost" size="sm" onClick={() => void disconnect()}>
          <LogOut />
          Disconnect
        </Button>
      </div>
    </header>
  );
}
