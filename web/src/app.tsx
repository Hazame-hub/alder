import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Database,
  FileUp,
  Layers,
  Loader2,
  LogOut,
  Moon,
  Search as SearchIcon,
  ShieldAlert,
  Sun,
} from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { SessionInfo } from "@/lib/api";
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

type View = "browse" | "schema" | "search" | "import";

export function App() {
  const queryClient = useQueryClient();
  const [view, setView] = useState<View>("browse");
  const [selectedDN, setSelectedDN] = useState<string | null>(null);

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
          setView("browse");
        }}
      />
    );
  }

  const info = session.data;
  const openEntry = (dn: string) => {
    setSelectedDN(dn);
    setView("browse");
  };

  return (
    <TooltipProvider delayDuration={300}>
      <div className="flex h-full flex-col">
        <TopBar info={info} view={view} onView={setView} />

        <div className="flex min-h-0 flex-1">
          {view === "browse" ? (
            <>
              <aside className="flex w-72 shrink-0 flex-col border-r border-border">
                <div className="border-b border-border px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Directory
                </div>
                <div className="min-h-0 flex-1 overflow-auto px-1">
                  <Tree selectedDN={selectedDN} onSelect={setSelectedDN} />
                </div>
              </aside>
              <main className="min-w-0 flex-1">
                {selectedDN ? (
                  <EntryPanel
                    dn={selectedDN}
                    readOnly={info.readOnly === true}
                    onNavigate={openEntry}
                    onDeleted={(parent) => setSelectedDN(parent || null)}
                  />
                ) : (
                  <p className="p-8 text-sm text-muted-foreground">
                    Pick an entry from the tree.
                  </p>
                )}
              </main>
            </>
          ) : view === "schema" ? (
            <main className="min-w-0 flex-1">
              <SchemaBrowser />
            </main>
          ) : view === "search" ? (
            <main className="min-w-0 flex-1">
              <SearchPanel
                initialBase={searchBaseFor(info, selectedDN)}
                onOpenEntry={openEntry}
              />
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

function TopBar({
  info,
  view,
  onView,
}: {
  info: SessionInfo;
  view: View;
  onView: (v: View) => void;
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

  const tabs: [View, string, typeof Database][] = [
    ["browse", "Browse", Database],
    ["search", "Search", SearchIcon],
    ["schema", "Schema", Layers],
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
              view === id
                ? "bg-accent font-medium text-accent-foreground"
                : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
            )}
          >
            <Icon className="size-4" />
            {label}
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
        <Button variant="ghost" size="sm" onClick={() => void disconnect()}>
          <LogOut />
          Disconnect
        </Button>
      </div>
    </header>
  );
}
