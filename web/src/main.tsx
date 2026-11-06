import { QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AnchoredToastProvider, ToastProvider } from "@/components/ui/toast";
import { queryClient } from "@/queries/client";
import { routeTree } from "./routeTree.gen";
import "./styles.css";

if (import.meta.env.DEV) {
  const { scan } = await import("react-scan");
  scan({ enabled: true });
}

const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: "intent",
  defaultPreloadStaleTime: 0,
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// biome-ignore lint/style/noNonNullAssertion: index.html has #root
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider position="top-center">
        <AnchoredToastProvider>
          <RouterProvider router={router} />
        </AnchoredToastProvider>
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
