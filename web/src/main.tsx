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
import { recordFailure, recordSuccess, shouldRetry } from "@/lib/directory-health";
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
  const detail =
    error instanceof ApiFailure
      ? `${error.status} ${error.code}: ${error.message}${error.detail ? ` (${error.detail})` : ""}`
      : String(error);
  // Unconditional, and not behind a debug flag: the next person to debug this
  // should find it in ten seconds rather than in the network tab.
  console.error(`alder: ${where} failed — ${detail}`, error);
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error, query) => noteFailure(error, String(query.queryKey[0] ?? "request")),
    onSuccess: () => recordSuccess(),
  }),
  mutationCache: new MutationCache({
    onError: (error, _vars, _ctx, mutation) =>
      noteFailure(error, String(mutation.options.mutationKey?.[0] ?? "request")),
    onSuccess: () => recordSuccess(),
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
