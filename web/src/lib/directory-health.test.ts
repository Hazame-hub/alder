import { beforeEach, describe, expect, it } from "vitest";
import { ApiFailure } from "./api";
import { readHealth, recordFailure, recordSuccess, resetHealth, shouldRetry } from "./directory-health";

// Whether the header may claim the directory is answering.
//
// GET /session is an object in Alder's own memory, so it answers 200 in a
// millisecond while the directory it names is unreachable — measured, with
// the harness 389 DS paused. The header therefore cannot ask the session; it
// watches what every other request is doing, which is what this store is.

const failure = (status: number, body?: Partial<{ error: string; message: string }>) =>
  new ApiFailure(status, { error: (body?.error ?? "upstream") as never, message: body?.message ?? "no" });

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
