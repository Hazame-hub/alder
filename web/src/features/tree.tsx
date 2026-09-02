import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ChevronRight,
  Folder,
  Globe,
  Loader2,
  Server,
  User,
  Users,
  FileText,
} from "lucide-react";
import { api, unwrap } from "@/lib/api";
import type { TreeNode } from "@/lib/api";
import { ancestorsOf } from "@/lib/values";
import { cn } from "@/lib/utils";

/**
 * Tree is the lazy-loading DIT browser.
 *
 * One level is fetched per expanded node, never a subtree. A directory can hold
 * a million entries under one OU and a browser that fetches eagerly to draw a
 * sidebar is a browser that takes the directory down.
 */
export function Tree({
  selectedDN,
  onSelect,
}: {
  selectedDN: string | null;
  onSelect: (dn: string) => void;
}) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  // Selecting an entry from search or a link should reveal it in the tree, so
  // every ancestor of the selection is expanded automatically.
  useEffect(() => {
    if (!selectedDN) return;
    setExpanded((prev) => {
      const next = new Set(prev);
      for (const ancestor of ancestorsOf(selectedDN).slice(0, -1)) {
        next.add(ancestor);
      }
      return next;
    });
  }, [selectedDN]);

  const roots = useQuery({
    queryKey: ["tree", null],
    queryFn: async () => unwrap(await api.GET("/tree", { params: { query: {} } })),
  });

  const toggle = (dn: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(dn)) next.delete(dn);
      else next.add(dn);
      return next;
    });

  if (roots.isPending) {
    return (
      <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Reading the naming contexts…
      </div>
    );
  }
  if (roots.isError) {
    return (
      <p className="p-4 text-sm text-destructive">
        The tree could not be loaded. {(roots.error as Error).message}
      </p>
    );
  }

  return (
    <div className="py-1">
      {roots.data.nodes.map((node) => (
        <TreeItem
          key={node.dn}
          node={node}
          depth={0}
          expanded={expanded}
          onToggle={toggle}
          selectedDN={selectedDN}
          onSelect={onSelect}
        />
      ))}
    </div>
  );
}

function TreeItem({
  node,
  depth,
  expanded,
  onToggle,
  selectedDN,
  onSelect,
}: {
  node: TreeNode;
  depth: number;
  expanded: Set<string>;
  onToggle: (dn: string) => void;
  selectedDN: string | null;
  onSelect: (dn: string) => void;
}) {
  const isOpen = expanded.has(node.dn);
  const isSelected = selectedDN === node.dn;

  const children = useQuery({
    queryKey: ["tree", node.dn],
    enabled: isOpen && node.hasChildren,
    queryFn: async () =>
      unwrap(await api.GET("/tree", { params: { query: { dn: node.dn, limit: 500 } } })),
  });

  const Icon = useMemo(() => iconFor(node), [node]);

  return (
    <div>
      <div
        className={cn(
          "group flex cursor-pointer items-center gap-1 rounded-md py-1 pr-2 text-sm transition-colors",
          isSelected
            ? "bg-primary/12 text-foreground"
            : "hover:bg-accent/70 text-foreground/90",
        )}
        style={{ paddingLeft: `${depth * 12 + 4}px` }}
        onClick={() => onSelect(node.dn)}
        role="treeitem"
        aria-expanded={node.hasChildren ? isOpen : undefined}
        aria-selected={isSelected}
      >
        <button
          type="button"
          className={cn(
            "flex size-4 shrink-0 items-center justify-center rounded",
            node.hasChildren ? "hover:bg-accent" : "invisible",
          )}
          onClick={(e) => {
            e.stopPropagation();
            onToggle(node.dn);
          }}
          aria-label={isOpen ? "Collapse" : "Expand"}
        >
          {children.isFetching && isOpen ? (
            <Loader2 className="size-3 animate-spin text-muted-foreground" />
          ) : (
            <ChevronRight
              className={cn(
                "size-3.5 text-muted-foreground transition-transform",
                isOpen && "rotate-90",
              )}
            />
          )}
        </button>
        <Icon
          className={cn(
            "size-3.5 shrink-0",
            isSelected ? "text-primary" : "text-muted-foreground",
          )}
        />
        <span className="truncate font-dn" title={node.dn}>
          {node.rdn}
        </span>
      </div>

      {isOpen ? (
        <div role="group">
          {children.data?.nodes.map((child) => (
            <TreeItem
              key={child.dn}
              node={child}
              depth={depth + 1}
              expanded={expanded}
              onToggle={onToggle}
              selectedDN={selectedDN}
              onSelect={onSelect}
            />
          ))}
          {children.data?.truncated ? (
            <p
              className="py-1 text-xs italic text-muted-foreground"
              style={{ paddingLeft: `${(depth + 1) * 12 + 26}px` }}
            >
              more children than were listed
            </p>
          ) : null}
          {children.isError ? (
            <p
              className="py-1 text-xs text-destructive"
              style={{ paddingLeft: `${(depth + 1) * 12 + 26}px` }}
            >
              {(children.error as Error).message}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/**
 * iconFor picks an icon from the structural object class.
 *
 * This is the one place a class name is matched literally, and it is purely
 * decorative: an unrecognised class gets the generic icon and nothing else
 * changes.
 */
function iconFor(node: TreeNode) {
  if (node.isNamingContext) return Globe;
  const structural = (node.structural ?? "").toLowerCase();
  if (structural.includes("organizationalunit") || structural === "container") {
    return Folder;
  }
  if (structural.includes("person") || structural === "account") return User;
  if (structural.includes("group") || structural.includes("role")) return Users;
  if (structural.includes("domain") || structural.includes("organization")) {
    return Server;
  }
  return FileText;
}
