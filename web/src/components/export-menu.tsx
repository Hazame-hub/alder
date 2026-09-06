import { Download, FileCode, FileDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

/**
 * Taking a table away with you, in either of the two shapes it comes in.
 *
 * The entry page has had the choice since the playbook export shipped; the
 * tables did not, which left the more useful half unreachable. "The thirty
 * people I just searched for" is the set worth enforcing — a single entry
 * rarely is — so the format choice mattered more here than where it landed
 * first.
 *
 * A menu rather than the entry page's dialog, because there is only one
 * decision to make. The base, scope and filter are not choices here: they are
 * the search that is already on screen, which is the whole point of exporting
 * from a table rather than from a subtree that happens to contain it.
 */
export function ExportMenu({
  dn,
  scope,
  filter,
  limit,
  label = "Export",
  disabled = false,
}: {
  dn: string;
  scope: "base" | "one" | "sub";
  filter: string;
  limit: number;
  label?: string;
  disabled?: boolean;
}) {
  const go = (format: "ldif" | "ansible") => {
    const params = new URLSearchParams({ dn, scope, filter, limit: String(limit) });
    // A plain navigation, so the browser's own download handling applies and
    // the session cookie goes with it.
    window.location.href = `/api/v1/export/${format}?${params.toString()}`;
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground"
          disabled={disabled}
          title="Export what this table is showing"
        >
          <Download />
          {label}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel>Export what this table is showing</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => go("ldif")}>
          <FileDown />
          <span>
            As LDIF
            <span className="block text-xs text-muted-foreground">
              The entries as they are, whole.
            </span>
          </span>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => go("ansible")}>
          <FileCode />
          <span>
            As an Ansible playbook
            <span className="block text-xs text-muted-foreground">
              Tasks that enforce them. Running it against a directory whose
              entries differ will change them.
            </span>
          </span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
