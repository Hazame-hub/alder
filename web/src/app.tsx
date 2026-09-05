import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Database,
  FileUp,
  FolderTree,
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
import { useChangeset } from "@/lib/changeset";
import { SourceLink } from "@/components/source-link";

/**
 * Sections are what the top bar offers. Directory is the only one with pages of
 * its own, and they sit on a second row rather than behind a menu: Users is a
 * destination, and putting it one click deep inside a dropdown would undo the
 * reason for having it.
 */
type Section = "directory" | "search" | "schema" | "changeset" | "import";

/** A page within Directory. Browse is the tree; the rest are the object views. */
type DirectoryPage = "browse" | ObjectViewId;

export function App() {
  const queryClient = useQueryClient();
  const [section, setSection] = useState<Section>("directory");
  const [page, setPage] = useState<DirectoryPage>("browse");
  const [selectedDN, setSelectedDN] = useState<string | null>(null);
  // Whether the entry panel should arrive already in edit mode. Set only by a
  // caller whose action was Edit, and cleared by every other way of opening one.
  const [openForEdit, setOpenForEdit] = useState(false);

  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => unwrap(await api.GET("/session")),
    // A session can expire while the tab is open; re-checking on focus is what
    // turns that into the connection screen rather than a wall of 401s.
    refetchOnWindowFocus: true,
    retry: false,
  });

  // The first naming context is the natural landing place: it is where the
  // directory the user connected to actually starts.
  useEffect(() => {
    if (!selectedDN && session.data?.connected) {
      const first = session.data.capabilities?.namingContexts?.[0];
      if (first) setSelectedDN(first);
    }
  }, [session.data, selectedDN]);

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
          setSelectedDN(info.capabilities?.namingContexts?.[0] ?? null);
          setSection("directory");
          setPage("browse");
        }}
      />
    );
  }

  const info = session.data;
  const contexts = info.capabilities?.namingContexts ?? [];

  // Opening an entry always lands on the tree, whichever page asked for it. The
  // entry panel is where an entry is read and edited, and it lives there.
  const openEntry = (dn: string, forEdit = false) => {
    setSelectedDN(dn);
    setOpenForEdit(forEdit);
    setSection("directory");
    setPage("browse");
  };

  return (
    <TooltipProvider delayDuration={300}>
      <div className="flex h-full flex-col">
        <TopBar info={info} section={section} onSection={setSection} />
        {section === "directory" ? (
          <DirectoryNav page={page} onPage={setPage} />
        ) : null}

        <div className="flex min-h-0 flex-1">
          {section === "directory" ? (
            page === "browse" ? (
              <>
                <aside className="flex w-72 shrink-0 flex-col border-r border-border">
                  <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    Directory
                  </div>
                  <div className="min-h-0 flex-1 overflow-auto px-1">
                    <Tree
                      selectedDN={selectedDN}
                      onSelect={(dn) => {
                        setSelectedDN(dn);
                        setOpenForEdit(false);
                      }}
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
                      onDeleted={(parent) => setSelectedDN(parent || null)}
                      schemaTargets={(info.capabilities?.schemaWrite?.targets ?? []).map(
                        (t) => t.dn,
                      )}
                      onOpenSchema={() => setSection("schema")}
                    />
                  ) : (
                    <p className="p-8 text-sm text-muted-foreground">
                      Pick an entry from the tree.
                    </p>
                  )}
                </main>
              </>
            ) : (
              <main className="min-w-0 flex-1">
                <ObjectListPanel
                  viewId={page}
                  namingContexts={contexts}
                  readOnly={info.readOnly === true}
                  onOpenEntry={openEntry}
                  onReviewChangeset={() => setSection("changeset")}
                />
              </main>
            )
          ) : section === "schema" ? (
            <main className="min-w-0 flex-1">
              <SchemaBrowser />
            </main>
          ) : section === "search" ? (
            <main className="min-w-0 flex-1">
              <SearchPanel
                initialBase={searchBaseFor(info, selectedDN)}
                onOpenEntry={openEntry}
              />
            </main>
          ) : section === "changeset" ? (
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
  page,
  onPage,
}: {
  page: DirectoryPage;
  onPage: (p: DirectoryPage) => void;
}) {
  const pages: [DirectoryPage, string, typeof Database][] = [
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
          onClick={() => onPage(id)}
          className={cn(
            "flex items-center gap-1.5 rounded-md px-2.5 py-1 text-sm transition-colors",
            page === id
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
  section,
  onSection,
}: {
  info: SessionInfo;
  section: Section;
  onSection: (s: Section) => void;
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

  const tabs: [Section, string, typeof Database][] = [
    ["directory", "Directory", Database],
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
            onClick={() => onSection(id)}
            className={cn(
              "flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-sm transition-colors",
              section === id
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
