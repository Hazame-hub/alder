import { useQuery } from "@tanstack/react-query";
import { ArrowDownToLine } from "lucide-react";
import { api, unwrap } from "@/lib/api";
import { serverLabel } from "@/lib/reference";
import { safeText } from "@/lib/display";

/**
 * Which server a write goes to, said above the LDIF.
 *
 * For as long as a session was one directory there was only one place a
 * change could go, and the dialogs showed the LDIF and not where. With a
 * reference server attached there are two servers on the screen (B6), and a
 * dialog that does not say which one it writes to is a write to the wrong
 * server waiting to happen -- even though only one of them can be written
 * to. Shown always, because it is true always, and cheap.
 */
export function AppliesTo() {
  const session = useQuery({
    queryKey: ["session"],
    queryFn: async () => unwrap(await api.GET("/session")),
  });
  const where = serverLabel(session.data);
  if (!where) return null;
  return (
    <p className="flex items-center gap-1.5 text-xs text-muted-foreground" data-testid="applies-to">
      <ArrowDownToLine className="size-3.5" />
      Applies to <span className="font-dn font-medium text-foreground">{safeText(where)}</span>
    </p>
  );
}
