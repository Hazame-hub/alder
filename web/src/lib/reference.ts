import { useQuery } from "@tanstack/react-query";
import { api, ApiFailure, unwrap } from "./api";
import type { SessionInfo } from "./api";

/**
 * The session's reference server (B6), or null when none is attached.
 *
 * A second, read-only connection held under the same session for comparison.
 * Keyed apart from ["session"] on purpose: disconnecting drops every query but
 * the session's, and a reference that outlived its session on screen would be
 * a server the header claims to hold and does not.
 */
export const referenceKey = ["reference"] as const;

export function useReference(): SessionInfo | null {
  const reference = useQuery({
    queryKey: referenceKey,
    queryFn: async () => {
      try {
        return unwrap(await api.GET("/session/reference"));
      } catch (e) {
        // None attached is a normal state, not a failure to show.
        if (e instanceof ApiFailure && e.status === 404) return null;
        throw e;
      }
    },
    retry: false,
  });
  return reference.data ?? null;
}

/** host:port, the way every label for a server in this interface reads. */
export function serverLabel(info: Pick<SessionInfo, "host" | "port"> | null | undefined): string {
  if (!info?.host) return "";
  return info.port ? `${info.host}:${info.port}` : info.host;
}
