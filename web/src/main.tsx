import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { ApiFailure } from "@/lib/api";
import {
  noteFetchEnd,
  noteFetchStart,
  recordFailure,
  recordSuccess,
  shouldRetry,
} from "@/lib/directory-health";
import { validateAppSearch } from "@/lib/route";
import { App } from "@/app";
import "@/styles.css";

/**
 * Every failure is written down, once, in one place.
 *
 * Two things were wrong and both were invisible. Nothing logged a failed
 * request, so a browser console was empty while nine calls had failed —
 * the first place anybody looks said nothing at all. And nothing but the
 * failing view knew, so the header went on reporting a healthy bind to a
 * directory that had stopped answering.
 *
 * A cache-level handler fixes both without every view remembering to.
 */
function noteFailure(error: unknown, where: string) {
  recordFailure(error);
  // The shape, never the payload.
  //
  // `detail` carries the server's own text, and some of that text quotes the
  // input it was about: an LDIF parse error echoes the offending line, and a
  // line can be `userPassword: ...`. A console line is a log line, and rule 6
  // of the charter does not stop being true because the log is in a browser.
  // The dialog still shows `detail` to the person who needs it, on screen,
  // where it is not written down.
  const shape =
    error instanceof ApiFailure ? `${error.status} ${error.code}: ${error.message}` : String(error);
  // Unconditional, and not behind a debug flag: the next person to debug this
  // should find it in ten seconds rather than in the network tab.
  console.error(`alder: ${where} failed — ${shape}`);
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error, query) => noteFailure(error, String(query.queryKey[0] ?? "request")),
    onSuccess: (_data, query) => recordSuccess(query.queryKey[0]),
  }),
  mutationCache: new MutationCache({
    onError: (error, _vars, _ctx, mutation) =>
      noteFailure(error, String(mutation.options.mutationKey?.[0] ?? "request")),
    onSuccess: (_data, _vars, _ctx, mutation) => recordSuccess(mutation.options.mutationKey?.[0]),
  }),
  defaultOptions: {
    queries: {
      // A directory read is cheap and the data is live; refetching on focus is
      // how a second person's change shows up without a reload.
      staleTime: 15_000,
      // Never retry an answer the server gave. A 502 from an unreachable
      // directory costs a full thirty-second operation timeout, and the old
      // policy spent two more of them before any screen was allowed to say
      // something had gone wrong -- ninety seconds, which nobody waits.
      retry: shouldRetry,
    },
    mutations: { retry: false },
  },
});

/**
 * What is in flight, so the header can say "still waiting" rather than
 * waiting silently.
 *
 * A subscription to the cache rather than a hook: every request on every
 * screen counts, and the alternative -- each view reporting its own -- is the
 * arrangement that produced a header claiming a healthy bind while six calls
 * were failing underneath it.
 *
 * The cache reports a fetch as an `updated` event carrying the action that
 * caused it. `fetch` opens one; `success`, `error` and `failed` close it.
 * Anything else about the query -- a component mounting, data going stale --
 * arrives here too and is ignored.
 */
queryClient.getQueryCache().subscribe((event) => {
  if (event.type !== "updated") return;
  const hash = event.query.queryHash;
  switch (event.action.type) {
    case "fetch":
      noteFetchStart(hash, event.query.queryKey[0]);
      break;
    case "success":
    case "error":
    case "failed":
      // `failed` is a retry, not an end. The request is still outstanding and
      // the clock should keep running: a retried request is exactly the case
      // where somebody is waiting and nothing is happening.
      if (event.action.type !== "failed") noteFetchEnd(hash);
      break;
  }
});

queryClient.getMutationCache().subscribe((event) => {
  if (event.type !== "updated") return;
  const hash = `mutation:${event.mutation.mutationId}`;
  switch (event.mutation.state.status) {
    case "pending":
      noteFetchStart(hash, event.mutation.options.mutationKey?.[0]);
      break;
    default:
      noteFetchEnd(hash);
  }
});

/**
 * One route, and everything in its search parameters.
 *
 * Nested paths would mean encoding a DN into a path segment, which is the one
 * thing the API deliberately avoids — a DN carries commas, equals signs and
 * non-ASCII text, and proxies disagree about double-encoding them. So the route
 * tree is a single page and the query string says where you are.
 */
const rootRoute = createRootRoute();

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  validateSearch: validateAppSearch,
  component: App,
});

const router = createRouter({
  routeTree: rootRoute.addChildren([appRoute]),
  // A path nobody defined lands on the application rather than on an error
  // page: the SPA is served for every path, so a stray URL is a typo, not a
  // missing feature.
  defaultNotFoundComponent: App,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const root = document.getElementById("root");
if (!root) throw new Error("the #root element is missing from index.html");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
