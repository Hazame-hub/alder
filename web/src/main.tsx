import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiFailure } from "@/lib/api";
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

const root = document.getElementById("root");
if (!root) throw new Error("the #root element is missing from index.html");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
