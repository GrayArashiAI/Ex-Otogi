import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api, data } from "@/api/client";

export const modulesQuery = queryOptions({
  queryKey: ["modules"],
  queryFn: async () => (await data(api.GET("/api/modules"))) ?? [],
});

/** Subject is what settings are of: a module, or a section of the settings outside modules. */
export type Subject = { module: string } | { section: string };

/** subjectKey starts the query keys of a subject's settings. */
export const subjectKey = (of: Subject) => ("module" in of ? ["modules", of.module] : ["system", of.section]);

export const settingsQuery = (of: Subject) =>
  queryOptions({
    queryKey: [...subjectKey(of), "settings"],
    queryFn: () =>
      "module" in of
        ? data(api.GET("/api/modules/{module}/settings", { params: { path: of } }))
        : data(api.GET("/api/system/{section}/settings", { params: { path: of } })),
  });

export const revisionsQuery = (of: Subject) =>
  infiniteQueryOptions({
    queryKey: [...subjectKey(of), "revisions"],
    queryFn: ({ pageParam }) => {
      const query = { before: pageParam, limit: 20 };
      return "module" in of
        ? data(api.GET("/api/modules/{module}/settings/revisions", { params: { path: of, query } }))
        : data(api.GET("/api/system/{section}/settings/revisions", { params: { path: of, query } }));
    },
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (page) => (page.more ? page.revisions?.at(-1)?.revision : undefined),
  });

/** saveSettings saves a subject's settings over the revision given, as a new one. */
export function saveSettings(of: Subject, over: number, settings: unknown) {
  const header = { "If-Match": `"${over}"` };
  return "module" in of
    ? data(api.PUT("/api/modules/{module}/settings", { params: { path: of, header }, body: settings as never }))
    : data(api.PUT("/api/system/{section}/settings", { params: { path: of, header }, body: settings as never }));
}

/** restoreSettings saves a revision's settings again, over the revision given. */
export function restoreSettings(of: Subject, over: number, revision: number) {
  const header = { "If-Match": `"${over}"` };
  return "module" in of
    ? data(
        api.POST("/api/modules/{module}/settings/revisions/{revision}/restore", {
          params: { path: { ...of, revision }, header },
        }),
      )
    : data(
        api.POST("/api/system/{section}/settings/revisions/{revision}/restore", {
          params: { path: { ...of, revision }, header },
        }),
      );
}

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
