import { queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

export const statusQuery = queryOptions({
  queryKey: ["status"],
  queryFn: () => data(api.GET("/api/status")),
  refetchInterval: 10_000,
});
