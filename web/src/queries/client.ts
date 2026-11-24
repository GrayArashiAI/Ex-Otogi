import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { ApiError } from "@/api/client";
import { toastManager } from "@/components/ui/toast";
import { restartKey } from "./system";

function report(error: Error, kind: "query" | "mutation", quiet?: unknown) {
  if (error instanceof ApiError && error.status === 401) {
    // Signed out meanwhile: the sign-in page takes over.
    window.location.assign(`/sign-in?next=${encodeURIComponent(window.location.pathname)}`);
    return;
  }
  if (quiet) return;
  if (kind === "query") {
    // While the bot restarts nothing loads, and the restart's dialog says so.
    if (queryClient.isMutating({ mutationKey: restartKey }) > 0) return;
    // What could not be loaded says so where it would have shown; this is the passing notice.
    toastManager.add({ type: "error", title: "Could not load", description: error.message, id: error.message });
    return;
  }
  // A failed change stays until it is dismissed: the toast is the only word of what failed.
  toastManager.add({
    type: "error",
    title: "The change was not made",
    description: error.message,
    id: error.message,
    timeout: 0,
    actionProps: { children: "Dismiss", onClick: () => toastManager.close(error.message) },
  });
}

/** Every failed query or mutation shows a toast, except a mutation with
 * meta.quiet, which shows its error where it happened. */
export const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: (error) => report(error, "query") }),
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => report(error, "mutation", mutation.meta?.quiet),
  }),
  defaultOptions: {
    queries: { staleTime: 10_000, retry: (count, error) => !(error instanceof ApiError) && count < 2 },
  },
});
