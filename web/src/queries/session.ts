import { queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

export const sessionQuery = queryOptions({
  queryKey: ["session"],
  queryFn: () => data(api.GET("/api/auth/session")),
  staleTime: 60_000,
});

export const passkeysQuery = queryOptions({
  queryKey: ["passkeys"],
  queryFn: async () => (await data(api.GET("/api/auth/passkeys"))) ?? [],
});
