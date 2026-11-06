import { MutationCache, QueryCache, QueryClient } from "@tanstack/react-query";
import { ApiError } from "@/api/client";
import { toastManager } from "@/components/ui/toast";

function report(error: Error) {
  if (error instanceof ApiError && error.status === 401) {
    // Signed out meanwhile: the sign-in page takes over.
    window.location.assign(`/sign-in?next=${encodeURIComponent(window.location.pathname)}`);
    return;
  }
  toastManager.add({ type: "error", title: "Something went wrong", description: error.message, id: error.message });
}

/** Every failed query or mutation shows a toast. */
export const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError: report }),
  mutationCache: new MutationCache({ onError: report }),
  defaultOptions: {
    queries: { staleTime: 10_000, retry: (count, error) => !(error instanceof ApiError) && count < 2 },
  },
});
