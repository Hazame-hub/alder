import { useSyncExternalStore } from "react";
import { ApiFailure } from "@/lib/api";

/**
 * Whether the directory behind this session is answering.
 *
 * A session is an in-memory object on the server, so `GET /session` keeps
 * returning `connected: true` in a millisecond while the directory it names is
 * unreachable. Measured, with the harness 389 DS paused: `/session` answered
 * 200 in 1.6ms and reported a healthy bind, while `POST /search` took **thirty
 * seconds** to come back 502. The header went on saying "Bound as
 * cn=Directory Manager" throughout.
 *
 * So the header cannot ask the session whether the directory is alive. What it
 * can do is watch what every other request is doing, which is what this is: a
 * module store the query client writes to on every failure and every success,
 * and the header reads.
 *
 * A module store rather than query state because it is cross-cutting — any
 * request on any screen contributes, and the header must see it without every
 * view threading a callback upward. The same deliberate exception the
 * navigator makes, and for the same reason.
 */

export type Health =
  /** Nothing has failed since the last success. */
  | { kind: "ok" }
  /** The directory answered badly, or did not answer. */
  | { kind: "unreachable"; status: number; message: string; at: number };

let health: Health = { kind: "ok" };
const listeners = new Set<() => void>();

function publish(next: Health) {
  if (next.kind === health.kind && next.kind === "ok") return;
  health = next;
  listeners.forEach((l) => l());
}

/**
 * Record that a request failed.
 *
 * Only an upstream failure counts. A 403 is the directory working correctly
 * and refusing; a 404 is an answer. Reporting those as "the directory is not
 * answering" would cry wolf on the ordinary business of the day, and a health
 * indicator that cries wolf gets ignored — which is worse than not having one.
 */
export function recordFailure(error: unknown) {
  if (!(error instanceof ApiFailure)) {
    // A failure reaching Alder itself rather than the directory: the page is
    // still loaded, so the server or the network between is the problem.
    publish({ kind: "unreachable", status: 0, message: "Alder itself did not answer.", at: Date.now() });
    return;
  }
  if (error.status === 502 || error.status === 504 || error.code === "upstream") {
    publish({
      kind: "unreachable",
      status: error.status,
      message: error.detail || error.message,
      at: Date.now(),
    });
  }
}

/** Record that a request succeeded, which clears the state. */
export function recordSuccess() {
  if (health.kind !== "ok") publish({ kind: "ok" });
  health = { kind: "ok" };
}

/** Reset, for a disconnect and for tests. */
export function resetHealth() {
  health = { kind: "ok" };
  listeners.forEach((l) => l());
}

export function subscribeHealth(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function readHealth(): Health {
  return health;
}

/** The health of the directory behind this session, for the header. */
export function useDirectoryHealth(): Health {
  return useSyncExternalStore(subscribeHealth, readHealth, readHealth);
}

/**
 * Whether to retry a failed request.
 *
 * Never an answer the server gave. An ApiFailure means Alder answered, and its
 * answer is the answer — retrying a 502 from an unreachable directory costs
 * another full operation timeout and changes nothing. That mattered more than
 * it looks: the timeout is thirty seconds, so the default policy of two
 * retries meant **ninety seconds** before a screen was allowed to say anything
 * had gone wrong, and nobody waits ninety seconds.
 *
 * A failure that is not an ApiFailure never reached Alder at all, and that is
 * worth one retry.
 */
export function shouldRetry(count: number, error: unknown): boolean {
  if (error instanceof ApiFailure) return false;
  return count < 1;
}
