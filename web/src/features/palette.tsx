import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CornerDownLeft, FileSearch, Search } from "lucide-react";
import { api, ApiFailure, unwrap } from "@/lib/api";
import type { Destination, ResolveResult } from "@/lib/api";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";

/**
 * One box that takes a DN, a filter, or a name.
 *
 * The target user lives in a terminal and can already name what they want.
 * Making them click down a tree to reach an entry whose DN is on their
 * clipboard is the papercut this removes.
 *
 * The server does the parsing. `internal/dn` and `internal/filter` are the
 * authorities on what these strings are, and a regex here would drift from
 * them the first time either grew a case — the same argument that keeps the
 * LDIF preview server-side.
 *
 * Nothing here guesses. "cn=platform" is a valid one-component DN *and* almost
 * certainly a request to find something called platform, so both destinations
 * come back and you pick.
 */
export function JumpPalette({ onEntry, onSearch }: {
  onEntry: (dn: string) => void;
  onSearch: (filter: string, base: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");

  // Ctrl+K / Cmd+K, which is what this gesture means everywhere else.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const query = useQuery<ResolveResult, ApiFailure>({
    queryKey: ["resolve", text],
    enabled: open && text.trim() !== "",
    queryFn: async () =>
      unwrap(await api.GET("/resolve", { params: { query: { q: text.trim() } } })),
  });

  const go = (d: Destination) => {
    setOpen(false);
    setText("");
    if (d.kind === "entry" && d.dn) {
      onEntry(d.dn);
      return;
    }
    if (d.kind === "search" && d.filter) {
      onSearch(d.filter, d.base ?? "");
    }
  };

  const destinations = query.data?.destinations ?? [];

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="top-[20%] translate-y-0 gap-0 p-0">
        <DialogTitle className="sr-only">Jump to</DialogTitle>

        <div className="flex items-center gap-2 border-b border-border px-3">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <Input
            autoFocus
            value={text}
            placeholder="A DN, a filter, or a name"
            className="border-0 font-dn shadow-none focus-visible:ring-0"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              // Enter takes the first destination, which is the DN when the
              // input is one — the unambiguous case, and the common one.
              if (e.key === "Enter" && destinations[0]) go(destinations[0]);
            }}
          />
        </div>

        <ul className="max-h-72 overflow-y-auto p-1">
          {destinations.map((d, i) => (
            <li key={i}>
              <button
                type="button"
                className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent"
                onClick={() => go(d)}
              >
                {d.kind === "entry" ? (
                  <FileSearch className="size-4 shrink-0 text-muted-foreground" />
                ) : (
                  <Search className="size-4 shrink-0 text-muted-foreground" />
                )}
                <span className="min-w-0 flex-1 truncate font-dn">{d.label}</span>
                {i === 0 ? (
                  <CornerDownLeft className="size-3.5 shrink-0 text-muted-foreground" />
                ) : null}
              </button>
            </li>
          ))}

          {text.trim() !== "" && !query.isPending && destinations.length === 0 ? (
            <li className="px-2 py-3 text-sm text-muted-foreground">
              That is not a DN, and not a filter yet.
            </li>
          ) : null}

          {text.trim() === "" ? (
            <li className="px-2 py-3 text-xs text-muted-foreground">
              Paste a DN to open it, type an RFC 4515 filter to run it, or type a
              name to look for it.
            </li>
          ) : null}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
