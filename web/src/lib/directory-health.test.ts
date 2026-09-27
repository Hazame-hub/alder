import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiFailure } from "./api";
import {
  SLOW_AFTER_MS,
  noteInFlight,
  readHealth,
  recordFailure,
  recordSuccess,
  resetHealth,
  shouldRetry,
} from "./directory-health";

// Whether the header may claim the directory is answering.
//
// GET /session is an object in Alder's own memory, so it answers 200 in a
// millisecond while the directory it names is unreachable — measured, with
// the harness 389 DS paused. The header therefore cannot ask the session; it
// watches what every other request is doing, which is what this store is.

const failure = (
  status: number,
  body?: Partial<{ error: string; message: string; detail: string }>,
) =>
  new ApiFailure(status, {
    error: (body?.error ?? "upstream") as never,
    message: body?.message ?? "no",
    ...(body?.detail !== undefined ? { detail: body.detail } : {}),
  });

beforeEach(() => resetHealth());

describe("whether the directory is answering", () => {
  it("starts out saying nothing", () => {
    expect(readHealth().kind).toBe("ok");
  });

  it("notices a directory that did not answer", () => {
    recordFailure(failure(502, { message: "The directory returned an error." }));
    const health = readHealth();
    expect(health.kind).toBe("unreachable");
    if (health.kind === "unreachable") expect(health.status).toBe(502);
  });

  it("carries the server's own sentence, not the client library's text", () => {
    // The badge has room for one line and shows it in a tooltip. `detail` is
    // the LDAP library's wording wrapped in whatever call produced it --
    // "Network Error" inside "directory: search failed: ..." -- which is
    // protocol noise where a person is looking for what to do next.
    recordFailure(
      failure(502, {
        message: "The directory did not answer.",
        detail: "directory: search uid=x failed: Network Error",
      }),
    );
    const health = readHealth();
    expect(health.kind).toBe("unreachable");
    if (health.kind === "unreachable") {
      expect(health.message).toBe("The directory did not answer.");
      expect(health.message).not.toContain("Network Error");
    }
  });

  it("does not cry wolf over the ordinary business of the day", () => {
    // A 403 is the directory working correctly and refusing; a 404 is an
    // answer. Reporting those as "not answering" would make the indicator
    // noise, and an indicator that is noise gets ignored — which is worse
    // than not having one.
    for (const status of [400, 401, 403, 404, 409, 422]) {
      resetHealth();
      recordFailure(failure(status, { error: "forbidden" }));
      expect(readHealth().kind, `status ${status}`).toBe("ok");
    }
  });

  it("counts a failure that never reached Alder at all", () => {
    recordFailure(new TypeError("Failed to fetch"));
    expect(readHealth().kind).toBe("unreachable");
  });

  it("clears when something that reached the directory succeeds", () => {
    recordFailure(failure(502));
    expect(readHealth().kind).toBe("unreachable");
    recordSuccess("entry");
    expect(readHealth().kind).toBe("ok");
  });

  it("is not cleared by a request Alder answers from memory", () => {
    // The premise of the whole module: GET /session is an object in Alder's
    // own memory and answers 200 in under a millisecond while the directory
    // is unreachable. The schema parse is memoised per session and the
    // source offer is static. Letting any of them clear the state puts back
    // the false claim this exists to remove -- and /session refetches on
    // window focus, so looking away and back would have done it.
    for (const key of ["session", "schema", "source"]) {
      resetHealth();
      recordFailure(failure(502));
      recordSuccess(key);
      expect(readHealth().kind, `a successful ${key} query`).toBe("unreachable");
    }
  });
});

describe("while a request is still in flight", () => {
  // The other half of the same failure, and the half that was still there
  // after the badge was built. An LDAP operation gets thirty seconds, so a
  // directory that has stopped answering produces about forty seconds of a
  // screen showing nothing at all before anything is allowed to say so. A
  // badge that arrives then is a post-mortem.
  const entry = { hash: "entry-1", key: "entry" };
  const tree = { hash: "tree-1", key: "tree" };

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-27T12:00:00Z"));
    resetHealth();
  });
  afterEach(() => vi.useRealTimers());

  it("says nothing about a request that has only just started", () => {
    noteInFlight([entry]);
    expect(readHealth().kind).toBe("ok");
    vi.advanceTimersByTime(SLOW_AFTER_MS - 1);
    expect(readHealth().kind).toBe("ok");
  });

  it("says so once one has been outstanding too long, without being asked again", () => {
    // No further event arrives: the request is outstanding, which is exactly
    // why nothing is happening. The state has to change on a timer or it
    // never changes at all.
    noteInFlight([entry]);
    vi.advanceTimersByTime(SLOW_AFTER_MS);
    expect(readHealth().kind).toBe("waiting");
  });

  it("counts the seconds, because 'how long has this been?' is the next question", () => {
    noteInFlight([entry]);
    vi.advanceTimersByTime(SLOW_AFTER_MS + 3000);
    const health = readHealth();
    expect(health.kind).toBe("waiting");
    if (health.kind === "waiting") expect(health.seconds).toBe((SLOW_AFTER_MS + 3000) / 1000);
  });

  it("stops when the request comes back", () => {
    noteInFlight([entry]);
    vi.advanceTimersByTime(SLOW_AFTER_MS);
    expect(readHealth().kind).toBe("waiting");
    noteInFlight([]);
    expect(readHealth().kind).toBe("ok");
  });

  it("times the oldest request, not the newest", () => {
    // A screen that keeps issuing requests while one of them hangs would
    // otherwise reset the clock on every keystroke and never report it.
    noteInFlight([entry]);
    vi.advanceTimersByTime(SLOW_AFTER_MS - 500);
    noteInFlight([entry, tree]);
    vi.advanceTimersByTime(500);
    expect(readHealth().kind).toBe("waiting");
  });

  it("keeps counting forwards when the oldest of several returns", () => {
    // The badge counts the wait, not the request. Recomputing from whatever
    // is oldest right now makes the number jump backwards at the moment one
    // request succeeds, which reads as a broken clock rather than progress.
    noteInFlight([entry]);
    vi.advanceTimersByTime(3000);
    noteInFlight([entry, tree]);
    vi.advanceTimersByTime(3000);
    const before = readHealth();
    noteInFlight([tree]);
    const after = readHealth();
    expect(before.kind).toBe("waiting");
    expect(after.kind).toBe("waiting");
    if (before.kind === "waiting" && after.kind === "waiting") {
      expect(after.seconds).toBeGreaterThanOrEqual(before.seconds);
    }
  });

  it("forgets a request the cache stopped reporting, whatever became of it", () => {
    // A query removed while fetching, or paused because the browser went
    // offline, produces no completion event. Counting events up and down
    // leaked one entry and pinned the badge on for the rest of the session.
    noteInFlight([entry]);
    vi.advanceTimersByTime(SLOW_AFTER_MS);
    expect(readHealth().kind).toBe("waiting");
    // The cache simply stops mentioning it.
    noteInFlight([]);
    vi.advanceTimersByTime(SLOW_AFTER_MS * 5);
    expect(readHealth().kind).toBe("ok");
  });

  it("does not restart the clock when the same request refetches", () => {
    noteInFlight([entry]);
    vi.advanceTimersByTime(SLOW_AFTER_MS + 2000);
    noteInFlight([entry]);
    const health = readHealth();
    expect(health.kind).toBe("waiting");
    if (health.kind === "waiting") expect(health.seconds).toBe((SLOW_AFTER_MS + 2000) / 1000);
  });

  it("does not blame the directory for a request Alder answers from memory", () => {
    // /session is answered out of Alder's own memory. A slow one means the
    // browser or Alder is busy, not that the directory has stopped
    // answering, and saying otherwise is the false claim in reverse.
    noteInFlight([{ hash: "session-1", key: "session" }]);
    vi.advanceTimersByTime(SLOW_AFTER_MS * 3);
    expect(readHealth().kind).toBe("ok");
  });

  it("counts a mutation, which always reaches the directory", () => {
    // A write carries no route to exempt, so its key is undefined. That must
    // not be mistaken for something answered from memory.
    noteInFlight([{ hash: "mutation:1", key: undefined }]);
    vi.advanceTimersByTime(SLOW_AFTER_MS);
    expect(readHealth().kind).toBe("waiting");
  });

  it("keeps reporting a failure while the retry is in flight", () => {
    // Waiting is a fact still being established; not answering is one
    // already established. Downgrading the second to the first as soon as
    // somebody clicks again walks the indicator backwards while things are
    // getting worse.
    recordFailure(failure(502));
    noteInFlight([entry]);
    vi.advanceTimersByTime(SLOW_AFTER_MS);
    expect(readHealth().kind).toBe("unreachable");
  });
});

describe("whether to try again", () => {
  it("never retries an answer the server gave", () => {
    // The operation timeout is thirty seconds. Two retries meant ninety
    // seconds before a screen was allowed to say anything had gone wrong,
    // and nobody waits ninety seconds.
    expect(shouldRetry(0, failure(502))).toBe(false);
    expect(shouldRetry(0, failure(500))).toBe(false);
    expect(shouldRetry(0, failure(403))).toBe(false);
  });

  it("retries once when the request never reached Alder", () => {
    const network = new TypeError("Failed to fetch");
    expect(shouldRetry(0, network)).toBe(true);
    expect(shouldRetry(1, network)).toBe(false);
  });
});
