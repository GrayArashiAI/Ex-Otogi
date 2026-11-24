import { queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

/** secretsQuery lists the secrets settings can refer to; never their values. */
export const secretsQuery = queryOptions({
  queryKey: ["secrets"],
  queryFn: async () => (await data(api.GET("/api/secrets"))) ?? [],
});
