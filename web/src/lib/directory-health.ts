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
  /**
   * A request to the directory has been outstanding long enough to say so.
   *
   * The audit's second finding was not only that a failure was silent: it was
   * that the silence lasted about forty seconds, because the LDAP operation
   * timeout is thirty and nothing said anything until it expired. A badge
   * that appears after the fact is a post-mortem. This is the status.
   */
  | { kind: "waiting"; since: number; seconds: number }
  /** The directory answered badly, or did not answer. */
  | { kind: "unreachable"; status: number; message: string; at: number };

/** How long a directory request may take before the header says so. */
export const SLOW_AFTER_MS = 4000;

let health: Health = { kind: "ok" };
const listeners = new Set<() => void>();

/**
 * The failure, held separately from what is published.
 *
 * Because the two states are not a sequence: a request can be in flight while
 * the last one is still known to have failed, and a failure outranks a wait.
 * "Not answering" is a fact already established; "still waiting" is a fact
 * still being established, and replacing the first with the second would walk
 * the indicator backwards while things got worse.
 */
let failure: Extract<Health, { kind: "unreachable" }> | null = null;

/**
 * Directory requests currently in flight, by the cache's hash for them, and
 * when each started.
 *
 * Per request rather than a count, because a count that never reaches zero
 * during ordinary navigation would make every busy moment look like a stall.
 * What matters is whether any *one* request has been outstanding too long.
 *
 * Reconciled from the cache rather than accumulated from events. The first
 * version counted `fetch` events up and `success`/`error` events down, which
 * is only correct if every fetch ends in one of those two -- and it does not.
 * A query removed while fetching, or one paused because the browser went
 * offline, leaves nothing behind to subtract, and one leaked entry pins the
 * badge on for the rest of the session. Asking the cache what is fetching
 * *now* cannot leak: whatever is no longer in the answer is no longer in
 * flight.
 */
const inFlight = new Map<string, number>();
let timer: ReturnType<typeof setTimeout> | undefined;

/**
 * When the current wait began, held separately from the oldest request.
 *
 * The badge counts the wait, not the request. Recomputing the number from
 * the oldest in-flight request every second makes it jump *backwards* when
 * that request is the one that comes back and a younger one is still going,
 * which reads as the clock being wrong rather than as progress.
 */
let waitingSince: number | undefined;

function publish(next: Health) {
  if (same(next, health)) return;
  health = next;
  listeners.forEach((l) => l());
}

function same(a: Health, b: Health): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === "waiting" && b.kind === "waiting") {
    return a.since === b.since && a.seconds === b.seconds;
  }
  return a.kind === "ok";
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
    failure = { kind: "unreachable", status: 0, message: "Alder itself did not answer.", at: Date.now() };
    recompute();
    return;
  }
  if (error.status === 502 || error.status === 504 || error.code === "upstream") {
    failure = {
      kind: "unreachable",
      status: error.status,
      // The server's own sentence, not its detail. `detail` is the client
      // library's text, and on the failure this badge exists for it is
      // "Network Error" wrapped in whatever call produced it -- protocol
      // noise in a place with room for one line.
      message: error.message,
      at: Date.now(),
    };
    recompute();
  }
}

/**
 * Requests Alder answers without asking the directory anything.
 *
 * `/session` is an object in Alder's own memory and its capabilities were
 * cached at connect time; the schema parse is memoised per session; the
 * source offer is static. Measured with the harness 389 DS paused, all three
 * answered 200 in single-digit milliseconds while every real directory call
 * was timing out.
 *
 * A success from one of these says nothing about the directory, and letting
 * it clear the state would put back exactly the false claim this module
 * exists to remove -- worse, intermittently, because `/session` refetches on
 * window focus, so looking away and back would have cleared the badge while
 * the directory was still dead.
 *
 * This list is the weak point: it is a fact about the server kept on the
 * client, and a new memory-answered endpoint would have to be added to it.
 * The alternative is for the server to mark which responses involved
 * directory I/O, which is the honest version and a change to every handler.
 */
const answeredFromMemory = new Set(["session", "schema", "source"]);

/**
 * Record that a request succeeded, which clears the state -- if that request
 * actually reached the directory.
 */
export function recordSuccess(key?: unknown) {
  if (answersFromMemory(key)) return;
  failure = null;
  recompute();
}

/**
 * Whether a query key names something Alder answers without asking the
 * directory. Exported because the same question decides two things: whether a
 * success clears the failure, and whether a slow request is the directory's
 * fault.
 */
export function answersFromMemory(key: unknown): boolean {
  return typeof key === "string" && answeredFromMemory.has(key);
}

/** One request the cache says is running now. */
export type Running = {
  /** The cache's identifier for it. A refetch of the same query reuses it,
   *  which is what keeps a refetch from counting as a second request. */
  hash: string;
  /** The first element of the query or mutation key, which is the route. */
  key: unknown;
};

/**
 * Everything the caches say is running, as of now.
 *
 * Called on every cache event with the whole list, not with one request at a
 * time: see the note on `inFlight` for why a running total is the wrong
 * shape here. Requests Alder answers from memory are dropped, so a slow
 * `/session` never accuses the directory of anything.
 */
export function noteInFlight(running: Running[], now = Date.now()) {
  const next = new Map<string, number>();
  for (const r of running) {
    if (answersFromMemory(r.key)) continue;
    next.set(r.hash, inFlight.get(r.hash) ?? now);
  }
  inFlight.clear();
  for (const [hash, since] of next) inFlight.set(hash, since);
  recompute(now);
}

/**
 * Work out what the header should say, and schedule the next time it might
 * change.
 *
 * One timer, reset each time, rather than one per request: while nothing is
 * slow it fires once at the moment the oldest request would become slow, and
 * while something is slow it ticks once a second so the badge can count. A
 * waiting badge with no number is a spinner with a border, and the question
 * an operator has after five seconds is "how long has this been?".
 */
function recompute(now = Date.now()) {
  if (timer !== undefined) {
    clearTimeout(timer);
    timer = undefined;
  }

  let oldest = Infinity;
  for (const since of inFlight.values()) oldest = Math.min(oldest, since);

  if (oldest === Infinity) {
    // Nothing is running, so whatever wait there was is over.
    waitingSince = undefined;
  } else if (waitingSince === undefined || oldest < waitingSince) {
    // A wait begins at the oldest request running when it began, and only
    // moves earlier -- a request that started before the ones already
    // counted has been waiting longer, and the badge should say so.
    waitingSince = oldest;
  }

  if (failure) {
    publish(failure);
  } else if (waitingSince !== undefined && now - waitingSince >= SLOW_AFTER_MS) {
    publish({
      kind: "waiting",
      since: waitingSince,
      seconds: Math.floor((now - waitingSince) / 1000),
    });
  } else {
    publish({ kind: "ok" });
  }

  if (waitingSince === undefined) return;
  const untilSlow = waitingSince + SLOW_AFTER_MS - now;
  timer = setTimeout(() => recompute(), untilSlow > 0 ? untilSlow : 1000);
}

/** Reset, for a disconnect and for tests. */
export function resetHealth() {
  failure = null;
  inFlight.clear();
  waitingSince = undefined;
  if (timer !== undefined) {
    clearTimeout(timer);
    timer = undefined;
  }
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
