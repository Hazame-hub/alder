import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { ApiFailure } from "@/lib/api";
import { validateAppSearch } from "@/lib/route";
import { App } from "@/app";
import "@/styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // A directory read is cheap and the data is live; refetching on focus is
      // how a second person's change shows up without a reload.
      staleTime: 15_000,
      retry: (count, error) => {
        // Retrying a 401 or a 403 achieves nothing but noise in the directory's
        // log; the session is gone or the bind is not permitted.
        if (error instanceof ApiFailure && error.status < 500) return false;
        return count < 2;
      },
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
