import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

export const modulesQuery = queryOptions({
  queryKey: ["modules"],
  queryFn: async () => (await data(api.GET("/api/modules"))) ?? [],
});

export const settingsQuery = (module: string) =>
  queryOptions({
    queryKey: ["modules", module, "settings"],
    queryFn: () => data(api.GET("/api/modules/{module}/settings", { params: { path: { module } } })),
  });

export const revisionsQuery = (module: string) =>
  infiniteQueryOptions({
    queryKey: ["modules", module, "revisions"],
    queryFn: ({ pageParam }) =>
      data(
        api.GET("/api/modules/{module}/settings/revisions", {
          params: { path: { module }, query: { before: pageParam, limit: 20 } },
        }),
      ),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (page) => (page.more ? page.revisions?.at(-1)?.revision : undefined),
  });

export const allowlistQuery = (module: string, list: string) =>
  queryOptions({
    queryKey: ["modules", module, "allowlists", list],
    queryFn: () => data(api.GET("/api/modules/{module}/allowlists/{list}", { params: { path: { module, list } } })),
  });

/** recordsQuery is a module's latest event log records. */
export const recordsQuery = (module: string) =>
  queryOptions({
    queryKey: ["events", { component: `module:${module}` }],
    queryFn: () => data(api.GET("/api/events", { params: { query: { component: `module:${module}`, limit: 6 } } })),
  });
